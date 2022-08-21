import time
from threading import Thread
from munch import Munch

from controllers.common.csi_logger import get_stdout_logger
from controllers.servers.host_definer.watcher.watcher_helper import Watcher, NODES, SECRET_IDS
from controllers.servers.host_definer import settings

logger = get_stdout_logger()


class CsiNodeWatcher(Watcher):

    def add_initial_csi_nodes(self):
        csi_nodes_info = self._get_csi_nodes_info_with_driver()
        for csi_node_info in csi_nodes_info:
            if self._is_host_can_be_defined(csi_node_info.name):
                self._add_node_to_nodes(csi_node_info)

    def watch_csi_nodes_resources(self):
        while True:
            resource_version = self.csi_nodes_api.get().metadata.resourceVersion
            stream = self.csi_nodes_api.watch(resource_version=resource_version, timeout=5)
            for watch_event in stream:
                watch_event = Munch.fromDict(watch_event)
                csi_node_info = self._generate_csi_node_info(watch_event.object)
                if (watch_event.type == settings.DELETED_EVENT) and (csi_node_info.name in NODES):
                    self._handle_deleted_csi_node_pod(csi_node_info.name)
                elif watch_event.type == settings.MODIFIED_EVENT:
                    self._handle_modified_csi_node(csi_node_info)

    def _handle_modified_csi_node(self, csi_node_info):
        if self._is_new_csi_node(csi_node_info):
            self._add_node_to_nodes(csi_node_info)
            self._define_host_on_all_storages(csi_node_info.name)
        elif csi_node_info.name in NODES:
            self._handle_deleted_csi_node_pod(csi_node_info.name)

    def _is_new_csi_node(self, csi_node_info):
        return csi_node_info.node_id and self._is_host_can_be_defined(csi_node_info.name) and \
            csi_node_info.name not in NODES

    def _handle_deleted_csi_node_pod(self, node_name):
        if self._is_host_can_be_undefined(node_name):
            remove_host_thread = Thread(target=self._undefine_host_when_node_pod_is_deleted, args=(node_name,))
            remove_host_thread.start()
        else:
            NODES.pop(node_name)

    def _undefine_host_when_node_pod_is_deleted(self, node_name):
        if self._is_host_part_of_update(node_name):
            pass
        else:
            self._undefine_hosts(node_name)

    def _is_host_part_of_update(self, worker):
        daemon_set_name = self._wait_until_all_daemon_set_pods_are_up_to_date()
        if daemon_set_name:
            return self._is_csi_node_pod_running_on_worker(worker, daemon_set_name)
        return False

    def _is_csi_node_pod_running_on_worker(self, worker, daemon_set_name):
        csi_pods_info = self._get_csi_pods_info()
        for pod_info in csi_pods_info:
            if (pod_info.node_name == worker) and (daemon_set_name in pod_info.name):
                return True
        return False

    def _wait_until_all_daemon_set_pods_are_up_to_date(self):
        csi_daemon_set = self._get_csi_daemon_set()
        if not csi_daemon_set:
            return None
        status = csi_daemon_set.status
        while status.updated_number_scheduled != status.desired_number_scheduled:
            if status.desired_number_scheduled == 0:
                return None
            csi_daemon_set = self._get_csi_daemon_set()
            if not csi_daemon_set:
                return None
            status = csi_daemon_set.status
            time.sleep(0.5)
        return csi_daemon_set.metadata.name

    def _undefine_hosts(self, node_name):
        for secret_id in SECRET_IDS:
            host_definition_info = self._get_host_definition_info_from_secret_and_node_name(node_name, secret_id)
            self._delete_definition(host_definition_info)
        NODES.pop(node_name)
