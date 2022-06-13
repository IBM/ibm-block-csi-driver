import time
import os
from threading import Thread
from kubernetes.client.rest import ApiException

from watcher.watcher_helper import WatcherHelper, CSI_NODES, SECRET_IDS
from storage_manager.exceptions import StorageException
from common import settings, utils

logger = utils.get_stdout_logger()

class CsiNodeWatcher(WatcherHelper):
    def __init__(self):
        super().__init__()
        
    def watch_csi_nodes_resources(self):
        for event in self.csi_nodes_api.watch():
            if (event['type'] == settings.DELETED_EVENT) and (
                self._get_csi_node_name_with_prefix(event) in CSI_NODES):
                self._handle_removal_csi_ibm_block_from_node(event)
            elif event['type'] == settings.MODIFIED_EVENT:
                self._handle_modified_csi_node(event)
            elif self._is_this_new_ibm_block_csi_node(event):
                CSI_NODES.append(self._get_csi_node_name_with_prefix(event))

    def _handle_modified_csi_node(self, csi_node_event):
        if self._is_csi_node_has_ibm_csi_block_driver(csi_node_event):
            self._handle_modified_csi_node_with_ibm_block_csi(csi_node_event)
        elif self._get_csi_node_name_with_prefix(csi_node_event) in CSI_NODES:
            self._handle_removal_csi_ibm_block_from_node(csi_node_event)

    def _handle_modified_csi_node_with_ibm_block_csi(self, csi_node_event):
        if self._is_this_new_ibm_block_csi_node(csi_node_event) and (
            self._get_csi_node_name_with_prefix(csi_node_event) not in CSI_NODES):
            logger.info('New Kubernetes node {}, has csi IBM block'.format(
                csi_node_event['object'].metadata.name))
            CSI_NODES.append(self._get_csi_node_name_with_prefix(csi_node_event))
            self._verify_host_on_all_storages(csi_node_event)

    def _verify_host_on_all_storages(self, csi_node_event):
        for secret_id in SECRET_IDS:
            if SECRET_IDS[secret_id] == 0:
                continue 
            host_name = self._get_csi_node_name_with_prefix(csi_node_event)
            try:
                host_object = self.get_host_object_from_secret_id(secret_id)
                host_object.host_name = host_name
                self.verify_on_storage(host_object)
            except Exception as ex:
                logger.error('Failed to verify that host {} is on a storage, got: {}'.format(
                    host_name, ex))
            
    def _handle_removal_csi_ibm_block_from_node(self, csi_node_event):
        remove_host_thread = Thread(
            target=self._verify_host_removed_from_all_storages_when_node_is_not_updated,
            args=(csi_node_event,))
        remove_host_thread.start()

    def _verify_host_removed_from_all_storages_when_node_is_not_updated(self, csi_node_event):
        if self._is_host_part_of_deletion(csi_node_event['object'].metadata.name):
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
            csi_ibm_block_pods = self.core_api.list_pod_for_all_namespaces(label_selector=settings.CSI_IBM_BLOCK_PRODUCT_LABEL)
        except ApiException as ex:
            logger.error('Failed to get csi IBM block pods, got error: {}'.format(ex.body))
        if csi_ibm_block_pods.items:
            for pod in csi_ibm_block_pods.items:
                if (pod.spec.node_name == worker) and (
                    settings.IBM_BLOCK_CSI_NODE_PREFIX in pod.metadata.name):
                    return pod
        return None

    def _verify_host_removed_from_all_storages(self, csi_node_event):
        CSI_NODES.remove(self._get_csi_node_name_with_prefix(csi_node_event))
        logger.info('Kubernetes node {}, is ont using csi IBM block anymore'.format(
            csi_node_event['object'].metadata.name))
        for secret_id in SECRET_IDS:
            host_name = self._get_csi_node_name_with_prefix(csi_node_event)
            try:
                host_object = self.get_host_object_from_secret_id(secret_id)
                host_object.host_name = host_name
                self._verify_not_on_storage(host_object)
            except Exception as ex:
                logger.error('Failed to verify that host {} is not on a storage, got: {}'.format(
                    host_name, ex))
        
    def _verify_not_on_storage(self, host_object):
        logger.info('Verifying that host {} is not on storage {}'.format(
            host_object.host_name, host_object.storage_server))
        host_definition_name = self.get_host_definition_name_from_host_object(host_object)
        try:
            self.storage_host_manager.verify_host_removed_from_storage(host_object)
            self.delete_host_definition_object(host_definition_name)
        except StorageException:
            self._set_host_definition_action_to_delete(host_object)
        except Exception as ex:
            logger.error('Failed to delete hostdefinition {}, got error: {}'.format(
                host_definition_name, ex))

    def _set_host_definition_action_to_delete(self, host_object, host_definition_name):
        host_object.phase = settings.PENDING_PHASE
        host_object.action = settings.DELETE_ACTION
        host_definition_manifest = self.get_host_definition_manifest_from_host_object(host_object, host_definition_name)
        try:
            self.patch_host_definition(host_definition_manifest)
        except Exception as ex:
            logger.error('Failed to set hostdefinition {} action to delete, got error: {}'.format(
                host_definition_name, ex))

    def _is_this_new_ibm_block_csi_node(self, csi_node_event):
        if (self._is_csi_node_has_ibm_csi_block_driver(csi_node_event)):
            if self._get_csi_node_name_with_prefix(csi_node_event) not in CSI_NODES:
                return True
        return False

    def _is_csi_node_has_ibm_csi_block_driver(self, csi_node_event):
        if csi_node_event['object'].spec.drivers:
            for driver in csi_node_event['object'].spec.drivers:
                if driver.name == settings.IBM_BLOCK_CSI_DRIVER_NAME:
                    return True
        return False

    def _get_csi_node_name_with_prefix(self, csi_node_event):
        prefix = self._get_prefix()
        return prefix + csi_node_event['object'].metadata.name

    def _get_prefix(self):
        return os.getenv('PREFIX')
