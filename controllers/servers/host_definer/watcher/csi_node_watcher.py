import time
from threading import Thread

from controllers.common.csi_logger import get_stdout_logger
from controllers.servers.host_definer.watcher.watcher_helper import Watcher, NODES, SECRET_IDS
from controllers.servers.host_definer import settings

logger = get_stdout_logger()


class CsiNodeWatcher(Watcher):

    def add_initial_nodes(self):
        csi_nodes = self._get_csi_nodes_with_driver()
        for csi_node in csi_nodes:
            if self._is_host_can_be_defined(csi_node.name):
                self._add_node_to_nodes(csi_node)

    def watch_csi_nodes_resources(self):
        for event in self.csi_nodes_api.watch():
            csi_node = self._get_csi_node_object(event[settings.OBJECT_KEY])
            if (event[settings.TYPE_KEY] == settings.DELETED_EVENT) and (csi_node.name in NODES):
                self._handle_deleted_csi_node_pod(csi_node.name)
            elif event[settings.TYPE_KEY] == settings.MODIFIED_EVENT:
                self._handle_modified_csi_node(csi_node)

    def _handle_modified_csi_node(self, csi_node):
        if csi_node.node_id and self._is_host_can_be_defined(csi_node.name) and csi_node.name not in NODES:
            self._add_node_to_nodes(csi_node)
            self._define_host_on_all_storages_from_secrets(csi_node.name)
        elif csi_node.name in NODES:
            self._handle_deleted_csi_node_pod(csi_node.name)

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
            return self._is_csi_ibm_block_node_pod_running_on_worker(worker, daemon_set_name)
        return False

    def _wait_until_all_daemon_set_pods_are_up_to_date(self):
        csi_ibm_block_daemon_set = self._get_csi_ibm_block_daemon_set()
        if not csi_ibm_block_daemon_set:
            return None
        status = csi_ibm_block_daemon_set.status
        while status.updated_number_scheduled != status.desired_number_scheduled:
            if status.desired_number_scheduled == 0:
                return None
            csi_ibm_block_daemon_set = self._get_csi_ibm_block_daemon_set()
            if not csi_ibm_block_daemon_set:
                return None
            status = csi_ibm_block_daemon_set.status
            time.sleep(0.5)
        return csi_ibm_block_daemon_set.metadata.name

    def _is_csi_ibm_block_node_pod_running_on_worker(self, worker, daemon_set_name):
        csi_ibm_block_pods = self._get_csi_ibm_block_pods()
        if not csi_ibm_block_pods:
            return False

        if csi_ibm_block_pods.items:
            for pod in csi_ibm_block_pods.items:
                if (pod.spec.node_name == worker) and (daemon_set_name in pod.metadata.name):
                    return True
        return False

    def _undefine_hosts(self, node_name):
        for secret_id in SECRET_IDS:
            host_definition = self._get_host_definition_from_secret_and_node_name(node_name, secret_id)
            if host_definition.management_address:
                self._delete_definition(host_definition)

        self._remove_managed_by_host_definer_label(node_name)
        NODES.pop(node_name)
