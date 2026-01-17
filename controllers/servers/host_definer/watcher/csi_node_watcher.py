import time
from threading import Thread

from controllers.common.csi_logger import get_stdout_logger
from controllers.servers.host_definer.watcher.watcher_helper import Watcher, NODES, MANAGED_SECRETS
import controllers.servers.host_definer.messages as messages
from controllers.servers.host_definer import settings

logger = get_stdout_logger()


class CsiNodeWatcher(Watcher):

    def add_initial_csi_nodes(self):
        logger.info("DEBUG - uriziv - 77")
        csi_nodes_info = self._get_csi_nodes_info_with_driver()
        for csi_node_info in csi_nodes_info:
            if self._is_host_can_be_defined(csi_node_info.name):
                self._add_node_to_nodes(csi_node_info)
        logger.info("DEBUG - uriziv - 78")

    def watch_csi_nodes_resources(self):
        while self._loop_forever():
            resource_version = self._get_k8s_object_resource_version(self.csi_nodes_api.get())
            stream = self.csi_nodes_api.watch(resource_version=resource_version, timeout=5)
            for watch_event in stream:
                logger.info("debug - uriziv - 87")
                logger.info(watch_event)
                watch_event = self._munch(watch_event)
                logger.info(watch_event)
                logger.info(watch_event.type)
                csi_node_info = self._generate_csi_node_info(watch_event.object)
                node_initiators = self._get_node_initiators(node_name=csi_node_info.name)
                logger.info(node_initiators)
                logger.info("debug - uriziv - 88")
                if (watch_event.type == settings.DELETED_EVENT) and (csi_node_info.name in NODES):
                    logger.info("debug - uriziv - 89")
                    self._handle_deleted_csi_node_pod(csi_node_info, node_initiators)
                elif watch_event.type == settings.MODIFIED_EVENT:
                    logger.info("debug - uriziv - 90")
                    self._handle_modified_csi_node(csi_node_info, node_initiators)

    def _handle_modified_csi_node(self, csi_node_info, node_initiators):
        if self._is_new_csi_node(csi_node_info):
            self._add_node_to_nodes(csi_node_info)
            self._define_host_on_all_storages(csi_node_info.name)
        elif csi_node_info.name in NODES:
            self._handle_deleted_csi_node_pod(csi_node_info, node_initiators)

    def _is_new_csi_node(self, csi_node_info):
        return csi_node_info.node_id and self._is_host_can_be_defined(csi_node_info.name) and \
            csi_node_info.name not in NODES

    def _handle_deleted_csi_node_pod(self, csi_node_info, node_initiators):
        if self._is_node_has_manage_node_label(csi_node_info.name):
            remove_host_thread = Thread(target=self._undefine_host_when_node_pod_is_deleted,
                                        args=(csi_node_info, node_initiators))
            remove_host_thread.start()

    def _undefine_host_when_node_pod_is_deleted(self, csi_node_info, node_initiators):
        node_name = csi_node_info.name
        if self._is_host_part_of_update(node_name):
            self._create_definitions_when_csi_node_changed(csi_node_info, node_initiators)
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
            logger.info(messages.UPDATED_CSI_NODE_VS_DESIRED.format(
                status.updated_number_scheduled, status.desired_number_scheduled))
            if status.desired_number_scheduled == 0:
                return None
            csi_daemon_set = self._get_csi_daemon_set()
            if not csi_daemon_set:
                return None
            status = csi_daemon_set.status
            time.sleep(0.5)
        return csi_daemon_set.metadata.name

    def _create_definitions_when_csi_node_changed(self, csi_node_info, node_initiators):
        logger.info("DEBUG - uriziv - 85")
        for secret_info in MANAGED_SECRETS:
            secret_name, secret_namespace = secret_info.name, secret_info.namespace
            host_definition_info = self._get_matching_host_definition_info(
                csi_node_info.name, secret_name, secret_namespace)
            if host_definition_info:
                if self._is_node_initiators_changed(host_definition_info,
                                                    csi_node_info,
                                                    node_initiators):
                    logger.info(messages.UPDATE_HOST_DEFINITION_PORTS)
                    NODES[csi_node_info.name] = self._generate_managed_node(csi_node_info)
                    for node_i in NODES.values():
                        logger.info(node_i.__dict__)
                    logger.info("DEBUG - uriziv - 86")
                    self._create_definition(host_definition_info)

    def _is_node_initiators_changed(self, host_definition_info, csi_node_info, node_initiators):
        logger.info("DEBUG - uriziv - 91")
        logger.info(host_definition_info)
        logger.info(csi_node_info)
        logger.info(node_initiators)
        if not host_definition_info.node_id or not csi_node_info.node_id:
            return False

        node_initiators_changed = False
        node_initiators_from_host_definition = \
            self._get_node_initiators_from_host_definition(host_definition_info)
        if not node_initiators.compare_by_connectivity_type(node_initiators_from_host_definition,
                                                            host_definition_info.connectivity_type):
            logger.info(messages.NODE_INITIATORS_WAS_CHANGED.format(
                csi_node_info.name,
                node_initiators_from_host_definition,
                node_initiators))
            node_initiators_changed = True
        logger.info("DEBUG - uriziv - 92")
        return node_initiators_changed

    def _undefine_hosts(self, node_name):
        for secret_info in MANAGED_SECRETS:
            host_definition_info = self._get_host_definition_info_from_secret_and_node_name(node_name, secret_info)
            self._delete_definition(host_definition_info)
        self._remove_manage_node_label(node_name)
        NODES.pop(node_name, None)
