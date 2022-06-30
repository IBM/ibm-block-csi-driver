import time
from threading import Thread
from kubernetes.client.rest import ApiException

from controllers.common.csi_logger import get_stdout_logger
from controllers.servers.host_definer.watcher.watcher_helper import Watcher, NODES, SECRET_IDS
from controllers.servers.host_definer import settings

logger = get_stdout_logger()


class CsiNodeWatcher(Watcher):

    def add_initial_nodes(self):
        csi_nodes = self._get_csi_nodes_with_driver()
        for csi_node in csi_nodes:
            if self.is_host_can_be_defined(csi_node.name):
                self.add_node_to_nodes(csi_node)

    def watch_csi_nodes_resources(self):
        for event in self.csi_nodes_api.watch():
            csi_node = self._get_csi_node_object(event[settings.OBJECT_KEY])
            if (event[settings.TYPE_KEY] == settings.DELETED_EVENT) and (csi_node.name in NODES):
                self._handle_deleted_csi_node_pod(csi_node.name)
            elif event[settings.TYPE_KEY] == settings.MODIFIED_EVENT:
                self._handle_modified_csi_node(csi_node)

    def _handle_modified_csi_node(self, csi_node):
        if csi_node.node_id and self.is_host_can_be_defined(csi_node.name) and csi_node.name not in NODES:
            self.add_node_to_nodes(csi_node)
            self.define_host_on_all_storages_from_secrets(csi_node.name)
        elif csi_node.name in NODES:
            self._handle_deleted_csi_node_pod(csi_node.name)

    def _handle_deleted_csi_node_pod(self, node_name):
        if self.is_host_can_be_undefined(node_name):
            remove_host_thread = Thread(target=self._undefine_host_when_node_pod_is_deleted, args=(node_name,))
            remove_host_thread.start()
        else:
            NODES.pop(node_name)

    def _undefine_host_when_node_pod_is_deleted(self, node_name):
        if self._is_host_part_of_deletion(node_name):
            self._undefine_host_from_all_storages_from_secrets(node_name)

    def _is_host_part_of_deletion(self, worker):
        counter = 0
        pod_phase = settings.BINARY_VALUE_FOR_DELETED_POD
        while counter < settings.SECONDS_TO_CHECK_POD_PHASE:
            pod = self._get_csi_ibm_block_node_pod_on_specific_worker(worker)
            if (pod and pod_phase == settings.BINARY_VALUE_FOR_DELETED_POD) or (
                    not pod and pod_phase == settings.BINARY_VALUE_FOR_EXISTING_POD):
                counter = 0
                pod_phase = 1 - pod_phase
            else:
                counter += 1
            time.sleep(1)
        return pod_phase == settings.BINARY_VALUE_FOR_DELETED_POD

    def _get_csi_ibm_block_node_pod_on_specific_worker(self, worker):
        try:
            csi_ibm_block_pods = self.core_api.list_pod_for_all_namespaces(
                label_selector=settings.CSI_IBM_BLOCK_PRODUCT_LABEL)
        except ApiException as ex:
            logger.error('Failed to get csi IBM block pods, got: {}'.format(ex.body))
        if csi_ibm_block_pods.items:
            for pod in csi_ibm_block_pods.items:
                if (pod.spec.node_name == worker) and (settings.IBM_BLOCK_CSI_NODE_PREFIX in pod.metadata.name):
                    return pod
        return None

    def _undefine_host_from_all_storages_from_secrets(self, node_name):
        for secret_id in SECRET_IDS:
            host_definition = self._get_host_definition_from_secret_and_node_name(node_name, secret_id)
            if host_definition.management_address:
                self.undefine_host_and_host_definition_with_events(host_definition)

        self.remove_managed_by_host_definer_label(node_name)
        NODES.pop(node_name)
