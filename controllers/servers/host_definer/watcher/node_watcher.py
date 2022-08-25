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
        nodes_info = self._get_nodes_info()
        for node_info in nodes_info:
            node_name = node_info.name
            csi_node_info = self._get_csi_node_info(node_name)
            if self._is_csi_node_pod_deleted_while_host_definer_was_down(csi_node_info):
                self._delete_host_definitions(node_name)
                self._remove_manage_node_label(node_name)

            if self._is_unmanaged_csi_node_has_driver(csi_node_info):
                self.unmanaged_csi_nodes_with_driver.add(csi_node_info.name)

    def _is_csi_node_pod_deleted_while_host_definer_was_down(self, csi_node_info):
        return self._is_node_has_manage_node_label(csi_node_info.name) and \
            self._is_node_has_host_definitions(csi_node_info.name) and not csi_node_info.node_id

    def watch_nodes_resources(self):
        while True:
            resource_version = self.core_api.list_node().metadata.resource_version
            stream = watch.Watch().stream(self.core_api.list_node, resource_version=resource_version, timeout_seconds=5)
            for watch_event in stream:
                watch_event = self._munch_watch_event(watch_event)
                node_name = watch_event.object.metadata.name
                csi_node_info = self._get_csi_node_info(node_name)
                if watch_event.type in settings.MODIFIED_EVENT and \
                        self._is_unmanaged_csi_node_has_driver(csi_node_info):
                    self.unmanaged_csi_nodes_with_driver.add(csi_node_info.name)

                if watch_event.type == settings.MODIFIED_EVENT and \
                        self._is_node_has_new_manage_node_label(csi_node_info):
                    self._add_node_to_nodes(csi_node_info)
                    self._define_host_on_all_storages(node_name)
                    self.unmanaged_csi_nodes_with_driver.remove(csi_node_info.name)

    def _is_unmanaged_csi_node_has_driver(self, csi_node_info):
        return csi_node_info.node_id and not self._is_host_can_be_defined(csi_node_info.name)

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
            csi_node_info.name not in NODES and csi_node_info.node_id and \
            csi_node_info.name in self.unmanaged_csi_nodes_with_driver
