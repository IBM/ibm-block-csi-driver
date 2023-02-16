from controllers.common.csi_logger import get_stdout_logger
from controllers.servers.host_definer.watcher.watcher_helper import Watcher
from controllers.servers.host_definer import settings
from controllers.servers.host_definer.utils import utils
from controllers.servers.host_definer import messages

logger = get_stdout_logger()
unmanaged_csi_nodes_with_driver = set()


class NodeWatcher(Watcher):
    def add_initial_nodes(self):
        nodes_info = self.node_manager.get_nodes_info()
        for node_info in nodes_info:
            node_name = node_info.name
            csi_node_info = self.resource_info_manager.get_csi_node_info(node_name)
            if self._is_csi_node_pod_deleted_while_host_definer_was_down(csi_node_info):
                logger.info(messages.CSI_NODE_POD_DELETED_WHILE_HOST_DEFINER_WAS_DOWN.format(node_name))
                self._delete_host_definitions(node_name)
                self.node_manager.remove_manage_node_label(node_name)

            if self._is_unmanaged_csi_node_has_driver(csi_node_info):
                logger.info(messages.DETECTED_UNMANAGED_CSI_NODE_WITH_IBM_BLOCK_CSI_DRIVER.format(csi_node_info.name))
                unmanaged_csi_nodes_with_driver.add(csi_node_info.name)

    def _is_csi_node_pod_deleted_while_host_definer_was_down(self, csi_node_info):
        if self.node_manager.is_node_has_manage_node_label(csi_node_info.name) and \
                self.node_manager.is_node_has_host_definitions(csi_node_info.name) and not csi_node_info.node_id:
            return True
        return False

    def _delete_host_definitions(self, node_name):
        if not self.node_manager.is_node_can_be_undefined(node_name):
            return
        host_definitions_info = self.host_definition_manager.get_all_host_definitions_info_of_the_node(node_name)
        for host_definition_info in host_definitions_info:
            self.definition_manager.delete_definition(host_definition_info)
        self.node_manager.remove_manage_node_label(node_name)

    def watch_nodes_resources(self):
        while utils.loop_forever():
            stream = self.k8s_api.get_node_stream()
            for watch_event in stream:
                watch_event = utils.munch(watch_event)
                node_name = watch_event.object.metadata.name
                csi_node_info = self.resource_info_manager.get_csi_node_info(node_name)
                node_info = self.resource_info_manager.generate_node_info(watch_event.object)
                self._add_new_unmanaged_nodes_with_ibm_csi_driver(watch_event, csi_node_info)
                self._define_new_managed_node(watch_event, node_name, csi_node_info)
                self.node_manager.handle_node_topologies(node_info, watch_event.type)
                self.node_manager.update_node_io_group(node_info)

    def _add_new_unmanaged_nodes_with_ibm_csi_driver(self, watch_event, csi_node_info):
        if watch_event.type in settings.MODIFIED_EVENT and \
                self._is_unmanaged_csi_node_has_driver(csi_node_info):
            logger.info(messages.DETECTED_UNMANAGED_CSI_NODE_WITH_IBM_BLOCK_CSI_DRIVER.format(csi_node_info.name))
            unmanaged_csi_nodes_with_driver.add(csi_node_info.name)

    def _is_unmanaged_csi_node_has_driver(self, csi_node_info):
        return csi_node_info.node_id and not self.node_manager.is_node_can_be_defined(csi_node_info.name)

    def _define_new_managed_node(self, watch_event, node_name, csi_node_info):
        if watch_event.type == settings.MODIFIED_EVENT and \
                self.node_manager.is_node_has_new_manage_node_label(csi_node_info, unmanaged_csi_nodes_with_driver):
            logger.info(messages.DETECTED_NEW_MANAGED_CSI_NODE.format(node_name))
            self.node_manager.add_node_to_nodes(csi_node_info)
            self.definition_manager.define_node_on_all_storages(node_name)
            unmanaged_csi_nodes_with_driver.remove(csi_node_info.name)
