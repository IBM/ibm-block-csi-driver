from queue import Empty
from kubernetes import watch

from controllers.common.csi_logger import get_stdout_logger
from controllers.servers.host_definer.watcher.watcher_helper import NODES, Watcher
from controllers.servers.host_definer import settings

logger = get_stdout_logger()


class NodeWatcher(Watcher):
    def __init__(self):
        super().__init__()
        self.unmanaged_csi_nodes_with_driver = set()

    def add_initial_nodes(self):
        nodes = self._get_nodes()
        for node in nodes:
            node_name = node.metadata.name
            csi_node = self._get_csi_node(node_name)
            if self._is_csi_driver_node_deleted_while_host_definer_was_down(csi_node):
                self._delete_host_definitions(node_name)
                self._remove_managed_by_host_definer_label(node_name)

            if self._is_unmanaged_csi_node_has_driver(csi_node):
                self.unmanaged_csi_nodes_with_driver.add(csi_node.name)

    def _is_csi_driver_node_deleted_while_host_definer_was_down(self, csi_node):
        return self._is_node_has_managed_by_host_definer_label(csi_node.name) and \
            self._is_node_has_host_definitions(csi_node.name) and not csi_node.node_id

    def watch_nodes_resources(self):
        while True:
            resource_version = self.core_api.list_node().metadata.resource_version
            stream = watch.Watch().stream(self.core_api.list_node, resource_version=resource_version, timeout_seconds=5)
            for event in stream:
                node_name = event[settings.OBJECT_KEY].metadata.name
                csi_node = self._get_csi_node(node_name)
                if event[settings.TYPE_KEY] in settings.MODIFIED_EVENT and \
                        self._is_unmanaged_csi_node_has_driver(csi_node):
                    self.unmanaged_csi_nodes_with_driver.add(csi_node.name)

                if event[settings.TYPE_KEY] == settings.MODIFIED_EVENT and \
                        self._is_node_has_new_managed_by_host_definer_label(csi_node):
                    self._add_node_to_nodes(csi_node)
                    self._define_host_on_all_storages_from_secrets(node_name)
                    self.unmanaged_csi_nodes_with_driver.remove(csi_node.name)

    def _is_node_has_host_definitions(self, node_name):
        host_definitions = self._get_all_node_host_definitions(node_name)
        return host_definitions is not Empty

    def _is_unmanaged_csi_node_has_driver(self, csi_node):
        return csi_node.node_id and not self._is_host_can_be_defined(csi_node.name)

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
            host_definition_obj = self._get_host_definition_object(host_definition)
            if host_definition_obj.node_name == node_name:
                node_host_definitions.append(host_definition_obj)
        return node_host_definitions

    def _is_node_has_new_managed_by_host_definer_label(self, csi_node):
        return not self._is_dynamic_node_labeling_allowed() and \
            self._is_node_has_managed_by_host_definer_label(csi_node.name) and \
            csi_node.name not in NODES and csi_node.node_id and csi_node.name in self.unmanaged_csi_nodes_with_driver
