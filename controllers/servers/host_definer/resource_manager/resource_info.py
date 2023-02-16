from controllers.common.csi_logger import get_stdout_logger
from controllers.servers.host_definer import settings
import controllers.common.settings as common_settings
from controllers.servers.host_definer.k8s.api import K8SApi
from controllers.servers.host_definer.utils import utils
from controllers.servers.host_definer.types import (
    NodeInfo, CsiNodeInfo, StorageClassInfo, PodInfo, HostDefinitionInfo, SecretInfo)


logger = get_stdout_logger()


class ResourceInfoManager:
    def __init__(self):
        self.k8s_api = K8SApi()

    def get_node_info(self, node_name):
        k8s_node = self.k8s_api.read_node(node_name)
        if k8s_node:
            return self.generate_node_info(k8s_node)
        return NodeInfo('', {})

    def generate_node_info(self, k8s_node):
        return NodeInfo(k8s_node.metadata.name, k8s_node.metadata.labels)

    def get_csi_node_info(self, node_name):
        k8s_csi_node = self.k8s_api.get_csi_node(node_name)
        if k8s_csi_node:
            return self.generate_csi_node_info(k8s_csi_node)
        return CsiNodeInfo()

    def generate_csi_node_info(self, k8s_csi_node):
        csi_node_info = CsiNodeInfo()
        csi_node_info.name = k8s_csi_node.metadata.name
        csi_node_info.node_id = self._get_node_id_from_k8s_csi_node(k8s_csi_node)
        return csi_node_info

    def _get_node_id_from_k8s_csi_node(self, k8s_csi_node):
        if k8s_csi_node.spec.drivers:
            for driver in k8s_csi_node.spec.drivers:
                if driver.name == settings.CSI_PROVISIONER_NAME:
                    return driver.nodeID
        return ''

    def get_storage_classes_info(self):
        storage_classes_info = []
        for k8s_storage_class in self.k8s_api.list_storage_class().items:
            storage_class_info = self.generate_storage_class_info(k8s_storage_class)
            storage_classes_info.append(storage_class_info)
        return storage_classes_info

    def generate_storage_class_info(self, k8s_storage_class):
        storage_class_info = StorageClassInfo()
        storage_class_info.name = k8s_storage_class.metadata.name
        storage_class_info.provisioner = k8s_storage_class.provisioner
        storage_class_info.parameters = k8s_storage_class.parameters
        return storage_class_info

    def get_csi_pods_info(self):
        pods_info = []
        k8s_pods = self.k8s_api.list_pod_for_all_namespaces(settings.DRIVER_PRODUCT_LABEL)
        if not k8s_pods:
            return pods_info
        for k8s_pod in k8s_pods.items:
            pod_info = self._generate_pod_info(k8s_pod)
            pods_info.append(pod_info)
        return pods_info

    def _generate_pod_info(self, k8s_pod):
        pod_info = PodInfo()
        pod_info.name = k8s_pod.metadata.name
        pod_info.node_name = k8s_pod.spec.node_name
        return pod_info

    def generate_host_definition_info(self, k8s_host_definition):
        host_definition_info = HostDefinitionInfo()
        host_definition_info.name = k8s_host_definition.metadata.name
        host_definition_info.resource_version = utils.get_k8s_object_resource_version(k8s_host_definition)
        host_definition_info.uid = k8s_host_definition.metadata.uid
        host_definition_info.phase = self._get_host_definition_phase(k8s_host_definition)
        host_definition_info.secret_name = self._get_attr_from_host_definition(
            k8s_host_definition, settings.SECRET_NAME_FIELD)
        host_definition_info.secret_namespace = self._get_attr_from_host_definition(
            k8s_host_definition, settings.SECRET_NAMESPACE_FIELD)
        host_definition_info.node_name = self._get_attr_from_host_definition(
            k8s_host_definition, settings.NODE_NAME_FIELD)
        host_definition_info.node_id = self._get_attr_from_host_definition(
            k8s_host_definition, common_settings.HOST_DEFINITION_NODE_ID_FIELD)
        host_definition_info.connectivity_type = self._get_attr_from_host_definition(
            k8s_host_definition, settings.CONNECTIVITY_TYPE_FIELD)
        return host_definition_info

    def _get_host_definition_phase(self, k8s_host_definition):
        if k8s_host_definition.status:
            return k8s_host_definition.status.phase
        return ''

    def _get_attr_from_host_definition(self, k8s_host_definition, attribute):
        if hasattr(k8s_host_definition.spec.hostDefinition, attribute):
            return getattr(k8s_host_definition.spec.hostDefinition, attribute)
        return ''

    def generate_k8s_secret_to_secret_info(self, k8s_secret, nodes_with_system_id={}, system_ids_topologies={}):
        return SecretInfo(
            k8s_secret.metadata.name, k8s_secret.metadata.namespace, nodes_with_system_id, system_ids_topologies)

    def generate_secret_info(self, secret_name, secret_namespace, nodes_with_system_id={}, system_ids_topologies={}):
        return SecretInfo(secret_name, secret_namespace, nodes_with_system_id, system_ids_topologies)
