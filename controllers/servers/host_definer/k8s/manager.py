import datetime

from kubernetes import client

from controllers.common.csi_logger import get_stdout_logger
import controllers.servers.host_definer.messages as messages
from controllers.servers.host_definer import settings
from controllers.servers.host_definer.utils import utils
from controllers.servers.host_definer.utils import manifest_utils
from controllers.servers.host_definer.k8s.api import K8SApi
import controllers.common.settings as common_settings
from controllers.servers.host_definer.types import (
    CsiNodeInfo, PodInfo, NodeInfo, StorageClassInfo, HostDefinitionInfo)

logger = get_stdout_logger()


class K8SManager():
    def __init__(self):
        self.k8s_api = K8SApi()

    def get_csi_nodes_info_with_driver(self):
        csi_nodes_info_with_driver = []
        k8s_csi_nodes = self.k8s_api.list_csi_node().items
        for k8s_csi_node in k8s_csi_nodes:
            if self._is_k8s_csi_node_has_driver(k8s_csi_node):
                csi_nodes_info_with_driver.append(self.generate_csi_node_info(k8s_csi_node))
        logger.info(messages.CSI_NODES_WITH_IBM_BLOCK_CSI_DRIVER.format(csi_nodes_info_with_driver))
        return csi_nodes_info_with_driver

    def _is_k8s_csi_node_has_driver(self, k8s_csi_node):
        if k8s_csi_node.spec.drivers:
            for driver in k8s_csi_node.spec.drivers:
                if driver.name == settings.CSI_PROVISIONER_NAME:
                    return True
        return False

    def get_nodes_info(self):
        nodes_info = []
        for k8s_node in self.k8s_api.list_node().items:
            k8s_node = self.generate_node_info(k8s_node)
            nodes_info.append(k8s_node)
        return nodes_info

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

    def get_matching_host_definition_info(self, node_name, secret_name, secret_namespace):
        k8s_host_definitions = self.k8s_api.list_host_definition().items
        for k8s_host_definition in k8s_host_definitions:
            host_definition_info = self.generate_host_definition_info(k8s_host_definition)
            if self._is_host_definition_matches(host_definition_info, node_name, secret_name, secret_namespace):
                return host_definition_info
        return None

    def _is_host_definition_matches(self, host_definition_info, node_name, secret_name, secret_namespace):
        return host_definition_info.node_name == node_name and \
            host_definition_info.secret_name == secret_name and \
            host_definition_info.secret_namespace == secret_namespace

    def create_host_definition(self, host_definition_manifest):
        k8s_host_definition = self.k8s_api.create_host_definition(host_definition_manifest)
        if k8s_host_definition:
            logger.info(messages.CREATED_HOST_DEFINITION.format(k8s_host_definition.metadata.name))
            self._add_finalizer(k8s_host_definition.metadata.name)
            return self.generate_host_definition_info(k8s_host_definition)
        return HostDefinitionInfo()

    def _add_finalizer(self, host_definition_name):
        logger.info(messages.ADD_FINALIZER_TO_HOST_DEFINITION.format(host_definition_name))
        self._update_finalizer(host_definition_name, [settings.CSI_IBM_FINALIZER, ])

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

    def set_host_definition_status(self, host_definition_name, host_definition_phase):
        logger.info(messages.SET_HOST_DEFINITION_STATUS.format(host_definition_name, host_definition_phase))
        status = manifest_utils.get_host_definition_status_manifest(host_definition_phase)
        self.k8s_api.patch_cluster_custom_object_status(
            common_settings.CSI_IBM_GROUP, common_settings.VERSION, common_settings.HOST_DEFINITION_PLURAL,
            host_definition_name, status)

    def generate_k8s_event(self, host_definition_info, message, action, message_type):
        return client.CoreV1Event(
            metadata=client.V1ObjectMeta(generate_name='{}.'.format(host_definition_info.name), ),
            reporting_component=settings.HOST_DEFINER, reporting_instance=settings.HOST_DEFINER, action=action,
            type=self._get_event_type(message_type), reason=message_type + action, message=str(message),
            event_time=datetime.datetime.utcnow().isoformat(timespec='microseconds') + 'Z',
            involved_object=client.V1ObjectReference(
                api_version=settings.CSI_IBM_API_VERSION, kind=settings.HOST_DEFINITION_KIND,
                name=host_definition_info.name, resource_version=host_definition_info.resource_version,
                uid=host_definition_info.uid, ))

    def _get_event_type(self, message_type):
        if message_type != settings.SUCCESSFUL_MESSAGE_TYPE:
            return settings.WARNING_EVENT_TYPE
        return settings.NORMAL_EVENT_TYPE

    def delete_host_definition(self, host_definition_name):
        logger.info(messages.DELETE_HOST_DEFINITION.format(host_definition_name))
        remove_finalizer_status_code = self._remove_finalizer(host_definition_name)
        if remove_finalizer_status_code == 200:
            self.k8s_api.delete_host_definition(host_definition_name)
        else:
            logger.error(messages.FAILED_TO_DELETE_HOST_DEFINITION.format(
                host_definition_name, messages.FAILED_TO_REMOVE_FINALIZER))

    def _remove_finalizer(self, host_definition_name):
        logger.info(messages.REMOVE_FINALIZER_TO_HOST_DEFINITION.format(host_definition_name))
        return self._update_finalizer(host_definition_name, [])

    def _update_finalizer(self, host_definition_name, finalizers):
        finalizer_manifest = manifest_utils.get_finalizer_manifest(host_definition_name, finalizers)
        return self.k8s_api.patch_host_definition(finalizer_manifest)

    def update_manage_node_label(self, node_name, label_value):
        body = manifest_utils.get_body_manifest_for_labels(label_value)
        self.k8s_api.patch_node(node_name, body)

    def get_secret_data(self, secret_name, secret_namespace):
        logger.info(messages.READ_SECRET.format(secret_name, secret_namespace))
        secret_data = self.k8s_api.get_secret_data(secret_name, secret_namespace)
        if secret_data:
            return utils.change_decode_base64_secret_config(secret_data)
        return {}

    def get_node_info(self, node_name):
        k8s_node = self.k8s_api.read_node(node_name)
        if k8s_node:
            return self.generate_node_info(k8s_node)
        return NodeInfo('', {})

    def generate_node_info(self, k8s_node):
        return NodeInfo(k8s_node.metadata.name, k8s_node.metadata.labels)

    def get_csi_daemon_set(self):
        daemon_sets = self.k8s_api.list_daemon_set_for_all_namespaces(settings.DRIVER_PRODUCT_LABEL)
        if daemon_sets and daemon_sets.items:
            return daemon_sets.items[0]
        return None

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
