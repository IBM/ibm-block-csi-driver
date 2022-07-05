from queue import Empty
from kubernetes import watch

from controllers.common.csi_logger import get_stdout_logger
from controllers.servers.host_definer.watcher.watcher_helper import NODES, Watcher
from controllers.servers.host_definer import settings

logger = get_stdout_logger()


class NodeWatcher(Watcher):

    def watch_nodes_resources(self):
        resource_version = ''
        while True:
            stream = watch.Watch().stream(self.core_api.list_node, resource_version=resource_version, timeout_seconds=5)
            resource_version = self.core_api.list_node().metadata.resource_version
            for event in stream:
                node_name = event[settings.OBJECT_KEY].metadata.name
                csi_node = self._get_csi_node(node_name)
                if event[settings.TYPE_KEY] == settings.ADDED_EVENT and \
                        self._is_csi_driver_node_deleted_while_host_definer_was_down(csi_node):
                    self._delete_host_definitions(node_name)
                    self._remove_managed_by_host_definer_label(node_name)

                elif event[settings.TYPE_KEY] == settings.MODIFIED_EVENT and \
                        self._is_node_has_new_managed_by_host_definer_label(csi_node):
                    self._add_node_to_nodes(csi_node)
                    self._define_host_on_all_storages_from_secrets(node_name)

    def _is_csi_driver_node_deleted_while_host_definer_was_down(self, csi_node):
        return self._is_node_has_managed_by_host_definer_label(csi_node.name) and \
            self._is_node_has_host_definitions(csi_node.name) and not csi_node.node_id

    def _is_node_has_host_definitions(self, node_name):
        host_definitions = self._get_all_node_host_definitions(node_name)
        return host_definitions is not Empty

    def _delete_host_definitions(self, node_name):
        if not self._is_host_can_be_undefined(node_name):
            return
        host_definitions = self._get_all_node_host_definitions(node_name)
        for host_definition in host_definitions:
            self._delete_definition(host_definition)

    def _get_all_node_host_definitions(self, node_name):
        node_host_definitions = []
        host_definitions = self._get_host_definitions()
        for host_definition in host_definitions:
            if host_definition.node_name == node_name:
                node_host_definitions.append(host_definition)
        return node_host_definitions

    def _is_node_has_new_managed_by_host_definer_label(self, csi_node):
        return not self._is_dynamic_node_labeling_allowed() and \
            self._is_node_has_managed_by_host_definer_label(csi_node.name) and \
            (csi_node.name not in NODES) and (csi_node.node_id)
