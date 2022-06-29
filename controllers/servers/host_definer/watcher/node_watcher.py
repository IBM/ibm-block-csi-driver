from queue import Empty
from kubernetes import watch
from kubernetes.client.rest import ApiException

from controllers.common.csi_logger import get_stdout_logger
from controllers.servers.host_definer.watcher.watcher_helper import NODES, Watcher
from controllers.servers.host_definer.common import settings

logger = get_stdout_logger()


class NodeWatcher(Watcher):

    def watch_nodes_resources(self):
        watcher = watch.Watch()
        for event in watcher.stream(self.core_api.list_node):
            node_name = event[settings.OBJECT_KEY].metadata.name
            csi_node = self._get_csi_node(node_name)
            if not csi_node:
                continue
            if event[settings.TYPE_KEY] == settings.ADDED_EVENT and \
                    self._is_csi_node_pod_deleted_while_host_definer_was_down(csi_node):
                if self.is_host_can_be_undefined(node_name):
                    self._undefine_host_definitions(node_name)
                if self.is_dynamic_node_labeling_allowed():
                    self.remove_managed_by_host_definer_label(node_name)

            if event[settings.TYPE_KEY] == settings.MODIFIED_EVENT and \
                    self._node_has_new_managed_by_host_definer_label(csi_node):
                self.add_node_to_nodes(node_name, csi_node)
                self.define_host_on_all_storages_from_secrets(node_name)

    def _get_csi_node(self, node_name):
        try:
            return self.csi_nodes_api.get(name=node_name)
        except ApiException as ex:
            if ex.status != 404:
                logger.error('Could not get csi node {}, got: {}'.format(
                    node_name, ex.body))
            return None

    def _is_csi_node_pod_deleted_while_host_definer_was_down(self, csi_node):
        node_name = self.get_node_name_from_csi_node(csi_node)
        return self.is_node_has_managed_by_host_definer_label(node_name) and \
            self._is_node_has_host_definitions(node_name) and \
            not self.is_ibm_csi_block_driver_in(csi_node)

    def _is_node_has_host_definitions(self, node_name):
        host_definitions = self._get_all_node_host_definitions(node_name)
        return host_definitions is not Empty

    def _get_all_node_host_definitions(self, node_name):
        node_host_definitions = []
        host_definitions = self._get_all_host_definitions()
        for host_definition in host_definitions:
            node_name_in_host_definition = self.get_node_name_from_node_id(
                host_definition.spec.hostDefinition.nodeId)
            if node_name_in_host_definition == node_name:
                node_host_definitions.append(host_definition)
        return node_host_definitions

    def _get_all_host_definitions(self):
        try:
            return self.host_definitions_api.get().items
        except ApiException as ex:
            logger.error("Could not get all hostDefinitions, got: {}".format(ex.body))
            return []

    def _undefine_host_definitions(self, node_name):
        host_definitions = self._get_all_node_host_definitions(node_name)
        for host_definition in host_definitions:
            host_request = self.get_host_request_from_host_definition(host_definition)
            if host_request:
                self.undefine_host_and_host_definition_with_events(
                    host_request)

    def _node_has_new_managed_by_host_definer_label(self, csi_node):
        node_name = self.get_node_name_from_csi_node(csi_node)
        return not self.is_dynamic_node_labeling_allowed() and \
            self.is_node_has_managed_by_host_definer_label(node_name) and \
            node_name not in NODES and \
            self.is_ibm_csi_block_driver_in(csi_node)
