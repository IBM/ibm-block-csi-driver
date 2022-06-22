import time
import os
from threading import Thread
from kubernetes.client.rest import ApiException

from controllers.host_definer.watcher.watcher_helper import WatcherHelper, NODES, SECRET_IDS
from controllers.host_definer.common import settings, utils

logger = utils.get_stdout_logger()


class CsiNodeWatcher(WatcherHelper):
    def __init__(self):
        super().__init__()

    def watch_csi_nodes_resources(self):
        for event in self.csi_nodes_api.watch():
            node_name = self._get_node_name_from_csi_node_event(event)
            if (event[settings.TYPE_KEY] == settings.DELETED_EVENT) and (
                    node_name in NODES):
                self._handle_deleted_csi_node_pod(event)
            elif event[settings.TYPE_KEY] == settings.MODIFIED_EVENT:
                self._handle_modified_csi_node(event)
            elif self._is_this_new_ibm_block_csi_node(event):
                self._add_node_with_node_id(node_name, event)

    def _handle_modified_csi_node(self, csi_node_event):
        if self._is_csi_node_has_ibm_csi_block_driver(csi_node_event):
            self._handle_modified_csi_node_with_ibm_block_csi(csi_node_event)
        elif self._get_node_name_from_csi_node_event(csi_node_event) in NODES:
            self._handle_deleted_csi_node_pod(csi_node_event)

    def _handle_modified_csi_node_with_ibm_block_csi(self, csi_node_event):
        node_name = self._get_node_name_from_csi_node_event(csi_node_event)
        if self._is_this_new_ibm_block_csi_node(csi_node_event) and (node_name not in NODES):
            logger.info('New Kubernetes node {}, has csi IBM block'.format(
                node_name))
            self._add_node_with_node_id(node_name, csi_node_event)
            self._verify_host_on_all_storages(csi_node_event)

    def _add_node_with_node_id(self, node_name, csi_node_event):
        NODES[node_name] = self._get_node_id_from_csi_node(
            csi_node_event[settings.OBJECT_KEY])

    def _get_node_id_from_csi_node(self, csi_node):
        for driver in csi_node.spec.drivers:
            if driver.name == settings.IBM_BLOCK_CSI_DRIVER_NAME:
                return driver.nodeID
        return None

    def _verify_host_on_all_storages(self, csi_node_event):
        node_name = self._get_node_name_from_csi_node_event(csi_node_event)
        for secret_id in SECRET_IDS:
            if SECRET_IDS[secret_id] == 0:
                continue
            self._verify_host_request(secret_id, node_name, self.verify_host_defined_and_has_host_definition)

    def _handle_deleted_csi_node_pod(self, csi_node_event):
        remove_host_thread = Thread(
            target=self._verify_host_removed_from_all_storages_when_node_is_not_updated,
            args=(csi_node_event,))
        remove_host_thread.start()

    def _verify_host_removed_from_all_storages_when_node_is_not_updated(
            self, csi_node_event):
        if self._is_host_part_of_deletion(
                csi_node_event[settings.OBJECT_KEY].metadata.name):
            self._verify_host_removed_from_all_storages(csi_node_event)

    def _is_host_part_of_deletion(self, worker):
        counter = 0
        pod_phase = settings.BINARY_VALUE_FOR_DELETED_POD
        while counter < settings.SECONDS_TO_CHECK_POD_PHASE:
            pod = self._get_csi_ibm_block_node_pod_object_on_specific_worker(worker)
            if (pod and pod_phase == settings.BINARY_VALUE_FOR_DELETED_POD) or (
                    not pod and pod_phase == settings.BINARY_VALUE_FOR_EXISTING_POD):
                counter = 0
                pod_phase = 1 - pod_phase
            else:
                counter += 1
            time.sleep(1)
        return pod_phase == settings.BINARY_VALUE_FOR_DELETED_POD

    def _get_csi_ibm_block_node_pod_object_on_specific_worker(self, worker):
        try:
            csi_ibm_block_pods = self.core_api.list_pod_for_all_namespaces(
                label_selector=settings.CSI_IBM_BLOCK_PRODUCT_LABEL)
        except ApiException as ex:
            logger.error(
                'Failed to get csi IBM block pods, got error: {}'.format(
                    ex.body))
        if csi_ibm_block_pods.items:
            for pod in csi_ibm_block_pods.items:
                if (pod.spec.node_name == worker) and (
                        settings.IBM_BLOCK_CSI_NODE_PREFIX in pod.metadata.name):
                    return pod
        return None

    def _verify_host_removed_from_all_storages(self, csi_node_event):
        node_name = self._get_node_name_from_csi_node_event(csi_node_event)
        logger.info(
            'Kubernetes node {}, is ont using csi IBM block anymore'.format(
                node_name))
        for secret_id in SECRET_IDS:
            self._verify_host_request(secret_id, node_name,
                                      self._verify_host_undefined_on_storage_and_handle_host_definition)
        NODES.pop(node_name)

    def _verify_host_request(self, secret_id, node_name, verify_function):
        host_request = self.get_host_request_from_secret_id(secret_id)
        if host_request:
            host_request.node_id = self.get_node_id_from_node_name(node_name)
            verify_function(host_request)

    def _verify_host_undefined_on_storage_and_handle_host_definition(self, host_request):
        node_name = self.get_node_name_from_node_id(host_request.node_id)
        logger.info('Verifying that host {} is not defined on storage {}'.format(
            node_name, host_request.system_info[settings.MANAGEMENT_ADDRESS_KEY]))
        host_definition_name = self.get_host_definition_name(
            host_request, node_name)
        response = self.verify_host_undefined_on_storage_and_on_cluster(host_request, host_definition_name)
        if response.error_message:
            self._set_host_definition_status_to_pending_deletion(host_definition_name)
            self.create_event_to_host_definition_from_host_request(
                host_request, response.error_message)

    def _set_host_definition_status_to_pending_deletion(
            self, host_definition_name):
        try:
            self.set_host_definition_status(
                host_definition_name, settings.PENDING_DELETION_PHASE)
        except Exception as ex:
            logger.error(
                'Failed to set hostdefinition {} phase to pending for deletion, got error: {}'.format(
                    host_definition_name, ex))

    def _is_this_new_ibm_block_csi_node(self, csi_node_event):
        if (self._is_csi_node_has_ibm_csi_block_driver(csi_node_event)):
            if self._get_node_name_from_csi_node_event(
                    csi_node_event) not in NODES:
                return True
        return False

    def _is_csi_node_has_ibm_csi_block_driver(self, csi_node_event):
        if csi_node_event[settings.OBJECT_KEY].spec.drivers:
            for driver in csi_node_event[settings.OBJECT_KEY].spec.drivers:
                if driver.name == settings.IBM_BLOCK_CSI_DRIVER_NAME:
                    return True
        return False

    def _get_prefix(self):
        return os.getenv('PREFIX')

    def _get_node_name_from_csi_node_event(self, csi_node_event):
        return csi_node_event[settings.OBJECT_KEY].metadata.name
