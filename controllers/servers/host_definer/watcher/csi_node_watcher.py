import time
from threading import Thread

from controllers.common.csi_logger import get_stdout_logger
from controllers.servers.host_definer.globals import MANAGED_SECRETS, NODES
from controllers.servers.host_definer.watcher.watcher_helper import Watcher
import controllers.servers.host_definer.messages as messages
from controllers.servers.host_definer import settings
from controllers.servers.host_definer.utils import utils

logger = get_stdout_logger()


class CsiNodeWatcher(Watcher):

    def add_initial_csi_nodes(self):
        csi_nodes_info = self.k8s_manager.get_csi_nodes_info_with_driver()
        for csi_node_info in csi_nodes_info:
            if self._is_host_can_be_defined(csi_node_info.name):
                self._add_node_to_nodes(csi_node_info)

    def watch_csi_nodes_resources(self):
        while utils.loop_forever():
            stream = self.k8s_api.get_csi_node_stream()
            for watch_event in stream:
                watch_event = utils.munch(watch_event)
                csi_node_info = self.k8s_manager.generate_csi_node_info(watch_event.object)
                if (watch_event.type == settings.DELETED_EVENT) and (csi_node_info.name in NODES):
                    self._handle_deleted_csi_node_pod(csi_node_info)
                elif watch_event.type == settings.MODIFIED_EVENT:
                    self._handle_modified_csi_node(csi_node_info)

    def _handle_modified_csi_node(self, csi_node_info):
        if self._is_new_csi_node(csi_node_info):
            self._add_node_to_nodes(csi_node_info)
            self._define_host_on_all_storages(csi_node_info.name)
        elif csi_node_info.name in NODES:
            self._handle_deleted_csi_node_pod(csi_node_info)

    def _is_new_csi_node(self, csi_node_info):
        return csi_node_info.node_id and self._is_host_can_be_defined(csi_node_info.name) and \
            csi_node_info.name not in NODES

    def _handle_deleted_csi_node_pod(self, csi_node_info):
        if self._is_node_has_manage_node_label(csi_node_info.name):
            remove_host_thread = Thread(target=self._undefine_host_when_node_pod_is_deleted, args=(csi_node_info,))
            remove_host_thread.start()

    def _undefine_host_when_node_pod_is_deleted(self, csi_node_info):
        node_name = csi_node_info.name
        if self._is_host_part_of_update(node_name):
            self._create_definitions_when_csi_node_changed(csi_node_info)
        elif self._is_host_definer_can_delete_hosts() and \
                not self._is_node_has_forbid_deletion_label(node_name):
            self._undefine_hosts(csi_node_info.name)
        else:
            NODES.pop(node_name, None)

    def _is_host_part_of_update(self, worker):
        logger.info(messages.CHECK_IF_NODE_IS_PART_OF_UPDATE.format(worker))
        daemon_set_name = self._wait_until_all_daemon_set_pods_are_up_to_date()
        if daemon_set_name:
            return self._is_csi_node_pod_running_on_worker(worker, daemon_set_name)
        return False

    def _is_csi_node_pod_running_on_worker(self, worker, daemon_set_name):
        logger.info(messages.CHECK_IF_CSI_NODE_POD_IS_RUNNING.format(worker))
        csi_pods_info = self.k8s_manager.get_csi_pods_info()
        for pod_info in csi_pods_info:
            if (pod_info.node_name == worker) and (daemon_set_name in pod_info.name):
                return True
        return False

    def _wait_until_all_daemon_set_pods_are_up_to_date(self):
        csi_daemon_set = self.k8s_manager.get_csi_daemon_set()
        if not csi_daemon_set:
            return None
        status = csi_daemon_set.status
        while status.updated_number_scheduled != status.desired_number_scheduled:
            logger.info(messages.UPDATED_CSI_NODE_VS_DESIRED.format(
                status.updated_number_scheduled, status.desired_number_scheduled))
            if status.desired_number_scheduled == 0:
                return None
            csi_daemon_set = self.k8s_manager.get_csi_daemon_set()
            if not csi_daemon_set:
                return None
            status = csi_daemon_set.status
            time.sleep(0.5)
        return csi_daemon_set.metadata.name

    def _create_definitions_when_csi_node_changed(self, csi_node_info):
        for secret_info in MANAGED_SECRETS:
            secret_name, secret_namespace = secret_info.name, secret_info.namespace
            host_definition_info = self.k8s_manager.get_matching_host_definition_info(
                csi_node_info.name, secret_name, secret_namespace)
            if host_definition_info:
                if self._is_node_id_changed(host_definition_info.node_id, csi_node_info.node_id):
                    logger.info(messages.NODE_ID_WAS_CHANGED.format(csi_node_info.name,
                                host_definition_info.node_id, csi_node_info.node_id))
                    NODES[csi_node_info.name] = self._generate_managed_node(csi_node_info)
                    self._create_definition(host_definition_info)

    def _is_node_id_changed(self, host_definition_node_id, csi_node_node_id):
        return host_definition_node_id != csi_node_node_id \
            and host_definition_node_id and csi_node_node_id

    def _undefine_hosts(self, node_name):
        for secret_info in MANAGED_SECRETS:
            host_definition_info = self._get_host_definition_info_from_secret_and_node_name(node_name, secret_info)
            self._delete_definition(host_definition_info)
        self._remove_manage_node_label(node_name)
        NODES.pop(node_name, None)
