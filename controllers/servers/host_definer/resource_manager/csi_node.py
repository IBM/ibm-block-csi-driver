from controllers.common.csi_logger import get_stdout_logger
import controllers.common.settings as common_settings
import controllers.servers.host_definer.messages as messages
from controllers.servers.host_definer.k8s.api import K8SApi
from controllers.servers.host_definer.resource_manager.resource_info import ResourceInfoManager
from controllers.servers.host_definer.resource_manager.daemon_set import DaemonSetManager

logger = get_stdout_logger()


class CSINodeManager:
    def __init__(self):
        self.k8s_api = K8SApi()
        self.resource_info_manager = ResourceInfoManager()
        self.daemon_set_manager = DaemonSetManager()

    def get_csi_nodes_info_with_driver(self):
        csi_nodes_info_with_driver = []
        k8s_csi_nodes = self.k8s_api.list_csi_node().items
        for k8s_csi_node in k8s_csi_nodes:
            if self._is_k8s_csi_node_has_driver(k8s_csi_node):
                csi_nodes_info_with_driver.append(self.resource_info_manager.generate_csi_node_info(k8s_csi_node))
        logger.info(messages.CSI_NODES_WITH_IBM_BLOCK_CSI_DRIVER.format(csi_nodes_info_with_driver))
        return csi_nodes_info_with_driver

    def _is_k8s_csi_node_has_driver(self, k8s_csi_node):
        if k8s_csi_node.spec.drivers:
            for driver in k8s_csi_node.spec.drivers:
                if driver.name == common_settings.CSI_PROVISIONER_NAME:
                    return True
        return False

    def is_host_part_of_update(self, worker):
        logger.info(messages.CHECK_IF_NODE_IS_PART_OF_UPDATE.format(worker))
        daemon_set_name = self.daemon_set_manager.wait_until_all_daemon_set_pods_are_up_to_date()
        if daemon_set_name:
            return self._is_csi_node_pod_running_on_worker(worker, daemon_set_name)
        return False

    def _is_csi_node_pod_running_on_worker(self, worker, daemon_set_name):
        logger.info(messages.CHECK_IF_CSI_NODE_POD_IS_RUNNING.format(worker))
        csi_pods_info = self.resource_info_manager.get_csi_pods_info()
        for pod_info in csi_pods_info:
            if (pod_info.node_name == worker) and (daemon_set_name in pod_info.name):
                return True
        return False

    def is_node_id_changed(self, host_definition_node_id, csi_node_node_id):
        if host_definition_node_id != csi_node_node_id and host_definition_node_id and csi_node_node_id:
            return True
        return False
