from kubernetes import watch

from controllers.common.csi_logger import get_stdout_logger
from controllers.servers.utils import is_topology_match
from controllers.servers.host_definer.watcher.watcher_helper import NODES, Watcher, MANAGED_SECRETS
from controllers.servers.host_definer import settings
from controllers.servers.host_definer import utils
from controllers.servers.host_definer import messages

logger = get_stdout_logger()
unmanaged_csi_nodes_with_driver = set()


class NodeWatcher(Watcher):
    def add_initial_nodes(self):
        nodes_info = self.k8s_manager.get_nodes_info()
        for node_info in nodes_info:
            node_name = node_info.name
            csi_node_info = self.k8s_manager.get_csi_node_info(node_name)
            if self._is_csi_node_pod_deleted_while_host_definer_was_down(csi_node_info):
                logger.info(messages.CSI_NODE_POD_DELETED_WHILE_HOST_DEFINER_WAS_DOWN.format(node_name))
                self._delete_host_definitions(node_name)
                self._remove_manage_node_label(node_name)

            if self._is_unmanaged_csi_node_has_driver(csi_node_info):
                logger.info(messages.DETECTED_UNMANAGED_CSI_NODE_WITH_IBM_BLOCK_CSI_DRIVER.format(csi_node_info.name))
                unmanaged_csi_nodes_with_driver.add(csi_node_info.name)

    def _is_csi_node_pod_deleted_while_host_definer_was_down(self, csi_node_info):
        return self._is_node_has_manage_node_label(csi_node_info.name) and \
            self._is_node_has_host_definitions(csi_node_info.name) and not csi_node_info.node_id

    def watch_nodes_resources(self):
        while self._loop_forever():
            stream = self.k8s_api.get_node_stream()
            for watch_event in stream:
                watch_event = self._munch(watch_event)
                node_name = watch_event.object.metadata.name
                csi_node_info = self.k8s_manager.get_csi_node_info(node_name)
                node_info = self.k8s_manager.generate_node_info(watch_event.object)
                self._add_new_unmanaged_nodes_with_ibm_csi_driver(watch_event, csi_node_info)
                self._define_new_managed_node(watch_event, node_name, csi_node_info)
                self._handle_node_topologies(node_info, watch_event)
                self._update_io_group(node_info)

    def _add_new_unmanaged_nodes_with_ibm_csi_driver(self, watch_event, csi_node_info):
        if watch_event.type in settings.MODIFIED_EVENT and \
                self._is_unmanaged_csi_node_has_driver(csi_node_info):
            logger.info(messages.DETECTED_UNMANAGED_CSI_NODE_WITH_IBM_BLOCK_CSI_DRIVER.format(csi_node_info.name))
            unmanaged_csi_nodes_with_driver.add(csi_node_info.name)

    def _is_unmanaged_csi_node_has_driver(self, csi_node_info):
        return csi_node_info.node_id and not self._is_host_can_be_defined(csi_node_info.name)

    def _define_new_managed_node(self, watch_event, node_name, csi_node_info):
        if watch_event.type == settings.MODIFIED_EVENT and \
                self._is_node_has_new_manage_node_label(csi_node_info):
            logger.info(messages.DETECTED_NEW_MANAGED_CSI_NODE.format(node_name))
            self._add_node_to_nodes(csi_node_info)
            self._define_host_on_all_storages(node_name)
            unmanaged_csi_nodes_with_driver.remove(csi_node_info.name)

    def _delete_host_definitions(self, node_name):
        if not self._is_host_can_be_undefined(node_name):
            return
        host_definitions_info = self._get_all_node_host_definitions_info(node_name)
        for host_definition_info in host_definitions_info:
            self._delete_definition(host_definition_info)
        self._remove_manage_node_label(node_name)

    def _is_node_has_new_manage_node_label(self, csi_node_info):
        return not self._is_dynamic_node_labeling_allowed() and \
            self._is_node_has_manage_node_label(csi_node_info.name) and \
            self._is_node_with_csi_ibm_csi_node_and_is_not_managed(csi_node_info)

    def _is_node_with_csi_ibm_csi_node_and_is_not_managed(self, csi_node_info):
        return csi_node_info.name not in NODES and csi_node_info.node_id and \
            csi_node_info.name in unmanaged_csi_nodes_with_driver

    def _handle_node_topologies(self, node_info, watch_event):
        if node_info.name not in NODES or watch_event.type != settings.MODIFIED_EVENT:
            return
        for index, managed_secret_info in enumerate(MANAGED_SECRETS):
            if not managed_secret_info.system_ids_topologies:
                continue
            if self._is_node_should_managed_on_secret_info(node_info.name, managed_secret_info):
                self._remove_node_if_topology_not_match(node_info, index, managed_secret_info)
            elif self._is_node_in_system_ids_topologies(managed_secret_info.system_ids_topologies, node_info.labels):
                self._define_host_with_new_topology(node_info, index, managed_secret_info)

    def _define_host_with_new_topology(self, node_info, index, managed_secret_info):
        node_name = node_info.name
        system_id = self._get_system_id_for_node_labels(
            managed_secret_info.system_ids_topologies, node_info.labels)
        managed_secret_info.nodes_with_system_id[node_name] = system_id
        MANAGED_SECRETS[index] = managed_secret_info
        self._define_host_on_all_storages(node_name)

    def _remove_node_if_topology_not_match(self, node_info, index, managed_secret_info):
        if not self._is_node_in_system_ids_topologies(managed_secret_info.system_ids_topologies, node_info.labels):
            managed_secret_info.nodes_with_system_id.pop(node_info.name, None)
            MANAGED_SECRETS[index] = managed_secret_info

    def _is_node_in_system_ids_topologies(self, system_ids_topologies, node_labels):
        return self._get_system_id_for_node_labels(system_ids_topologies, node_labels) != ''

    def _get_system_id_for_node_labels(self, system_ids_topologies, node_labels):
        topology_labels = self._get_topology_labels(node_labels)
        for system_id, system_topologies in system_ids_topologies.items():
            if is_topology_match(system_topologies, topology_labels):
                return system_id
        return ''

    def _update_io_group(self, node_info):
        io_group = utils.generate_io_group_from_labels(node_info.labels)
        node_name = node_info.name
        if node_name in NODES and io_group != NODES[node_name].io_group:
            logger.info(messages.IO_GROUP_CHANGED.format(node_name, io_group, NODES[node_name].io_group))
            NODES[node_name].io_group = io_group
            self._define_host_on_all_storages(node_name)
