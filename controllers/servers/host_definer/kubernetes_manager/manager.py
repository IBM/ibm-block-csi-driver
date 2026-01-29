import ast
import datetime
import base64

from kubernetes import client, config, dynamic
from kubernetes.client import api_client
from kubernetes.client.rest import ApiException

from controllers.common.csi_logger import get_stdout_logger
import controllers.servers.host_definer.messages as messages
from controllers.servers.host_definer import settings
import controllers.common.settings as common_settings
from controllers.servers.host_definer.hd_types import (
    CsiNodeInfo, PodInfo, NodeInfo, StorageClassInfo, HostDefinitionInfo)

logger = get_stdout_logger()


class KubernetesManager():
    def __init__(self):
        self._load_cluster_configuration()
        self.dynamic_client = self._get_dynamic_client()
        self.storage_api = client.StorageV1Api()
        self.core_api = client.CoreV1Api()
        self.custom_object_api = client.CustomObjectsApi()
        self.apps_api = client.AppsV1Api()
        self.csi_nodes_api = self._get_csi_nodes_api()
        self.host_definitions_api = self._get_host_definitions_api()

    def _get_dynamic_client(self):
        return dynamic.DynamicClient(api_client.ApiClient(configuration=self._load_cluster_configuration()))

    def _load_cluster_configuration(self):
        return config.load_incluster_config()

    def _get_csi_nodes_api(self):
        return self.dynamic_client.resources.get(api_version=settings.STORAGE_API_VERSION,
                                                 kind=settings.CSINODE_KIND)

    def _get_host_definitions_api(self):
        return self.dynamic_client.resources.get(api_version=settings.CSI_IBM_API_VERSION,
                                                 kind=settings.HOST_DEFINITION_KIND)

    def _get_csi_nodes_info_with_driver(self):
        csi_nodes_info_with_driver = []
        k8s_csi_nodes = self._get_k8s_csi_nodes()
        for k8s_csi_node in k8s_csi_nodes:
            if self._is_k8s_csi_node_has_driver(k8s_csi_node):
                csi_nodes_info_with_driver.append(self._generate_csi_node_info(k8s_csi_node))
        logger.info(messages.CSI_NODES_WITH_IBM_BLOCK_CSI_DRIVER.format(csi_nodes_info_with_driver))
        return csi_nodes_info_with_driver

    def _get_k8s_csi_nodes(self):
        try:
            return self.csi_nodes_api.get().items
        except ApiException as ex:
            logger.error(messages.FAILED_TO_GET_CSI_NODES.format(ex.body))
            return []

    def _get_nodes_info(self):
        try:
            nodes_info = []
            for k8s_node in self.core_api.list_node().items:
                k8s_node = self._generate_node_info(k8s_node)
                nodes_info.append(k8s_node)
            return nodes_info
        except ApiException as ex:
            logger.error(messages.FAILED_TO_GET_NODES.format(ex.body))
            return []

    def _get_storage_classes_info(self):
        try:
            storage_classes_info = []
            for k8s_storage_class in self.storage_api.list_storage_class().items:
                storage_class_info = self._generate_storage_class_info(k8s_storage_class)
                storage_classes_info.append(storage_class_info)
            return storage_classes_info

        except ApiException as ex:
            logger.error(messages.FAILED_TO_GET_STORAGE_CLASSES.format(ex.body))
            return []

    def _generate_storage_class_info(self, k8s_storage_class):
        storage_class_info = StorageClassInfo()
        storage_class_info.name = k8s_storage_class.metadata.name
        storage_class_info.provisioner = k8s_storage_class.provisioner
        storage_class_info.parameters = k8s_storage_class.parameters
        return storage_class_info

    def _is_k8s_csi_node_has_driver(self, k8s_csi_node):
        if k8s_csi_node.spec.drivers:
            for driver in k8s_csi_node.spec.drivers:
                if driver.name == settings.CSI_PROVISIONER_NAME:
                    return True
        return False

    def _get_csi_node_info(self, node_name):
        try:
            k8s_csi_node = self.csi_nodes_api.get(name=node_name)
            return self._generate_csi_node_info(k8s_csi_node)
        except ApiException as ex:
            if ex.status == 404:
                logger.error(messages.CSI_NODE_DOES_NOT_EXIST.format(node_name))
            else:
                logger.error(messages.FAILED_TO_GET_CSI_NODE.format(node_name, ex.body))
            return CsiNodeInfo()

    def _generate_csi_node_info(self, k8s_csi_node):
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

    def _get_matching_host_definition_info(self, node_name, secret_name, secret_namespace):
        k8s_host_definitions = self._get_k8s_host_definitions()
        for k8s_host_definition in k8s_host_definitions:
            host_definition_info = self._generate_host_definition_info(k8s_host_definition)
            if self._is_host_definition_matches(host_definition_info, node_name, secret_name, secret_namespace):
                return host_definition_info
        return None

    def _get_k8s_host_definitions(self):
        try:
            return self.host_definitions_api.get().items

        except ApiException as ex:
            logger.error(messages.FAILED_TO_GET_LIST_OF_HOST_DEFINITIONS.format(ex.body))
            return []

    def _generate_host_definition_info(self, k8s_host_definition):
        host_definition_info = HostDefinitionInfo()
        host_definition_info.name = k8s_host_definition.metadata.name
        host_definition_info.resource_version = self._get_k8s_object_resource_version(k8s_host_definition)
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

    def _get_k8s_object_resource_version(self, k8s_object):
        if k8s_object.metadata.resource_version:
            return k8s_object.metadata.resource_version
        return k8s_object.metadata.resourceVersion

    def _get_host_definition_phase(self, k8s_host_definition):
        if k8s_host_definition.status:
            return k8s_host_definition.status.phase
        return ''

    def _get_attr_from_host_definition(self, k8s_host_definition, attribute):
        if hasattr(k8s_host_definition.spec.hostDefinition, attribute):
            return getattr(k8s_host_definition.spec.hostDefinition, attribute)
        return ''

    def _is_host_definition_matches(self, host_definition_info, node_name, secret_name, secret_namespace):
        return host_definition_info.node_name == node_name and \
               host_definition_info.secret_name == secret_name and \
               host_definition_info.secret_namespace == secret_namespace

    def _create_host_definition(self, host_definition_manifest):
        try:
            k8s_host_definition = self.host_definitions_api.create(body=host_definition_manifest)
            logger.info(messages.CREATED_HOST_DEFINITION.format(k8s_host_definition.metadata.name))
            self._add_finalizer(k8s_host_definition.metadata.name)
            return self._generate_host_definition_info(k8s_host_definition)
        except ApiException as ex:
            if ex != 404:
                logger.error(messages.FAILED_TO_CREATE_HOST_DEFINITION.format(
                    host_definition_manifest[settings.METADATA][common_settings.NAME_FIELD], ex.body))

    def _add_finalizer(self, host_definition_name):
        logger.info(messages.ADD_FINALIZER_TO_HOST_DEFINITION.format(host_definition_name))
        self._update_finalizer(host_definition_name, [settings.CSI_IBM_FINALIZER, ])

    def _set_host_definition_status(self, host_definition_name, host_definition_phase):
        logger.info(messages.SET_HOST_DEFINITION_STATUS.format(host_definition_name, host_definition_phase))
        status = self._get_status_manifest(host_definition_phase)
        try:
            self.custom_object_api.patch_cluster_custom_object_status(
                common_settings.CSI_IBM_GROUP, common_settings.VERSION, common_settings.HOST_DEFINITION_PLURAL,
                host_definition_name, status)
        except ApiException as ex:
            if ex.status == 404:
                logger.error(messages.HOST_DEFINITION_DOES_NOT_EXIST.format(host_definition_name))
            else:
                logger.error(messages.FAILED_TO_SET_HOST_DEFINITION_STATUS.format(host_definition_name, ex.body))

    def _get_status_manifest(self, host_definition_phase):
        return {
            settings.STATUS: {
                settings.PHASE: host_definition_phase,
            }
        }

    def _generate_k8s_event(self, host_definition_info, message, action, message_type):
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

    def _create_k8s_event(self, namespace, k8s_event):
        try:
            self.core_api.create_namespaced_event(namespace, k8s_event)
        except ApiException as ex:
            logger.error(messages.FAILED_TO_CREATE_EVENT_FOR_HOST_DEFINITION.format(
                k8s_event.involved_object.name, ex.body))

    def _delete_host_definition(self, host_definition_name):
        logger.info(messages.DELETE_HOST_DEFINITION.format(host_definition_name))
        try:
            remove_finalizer_status_code = self._remove_finalizer(host_definition_name)
            if remove_finalizer_status_code == 200:
                self.host_definitions_api.delete(name=host_definition_name, body={})
            else:
                logger.error(messages.FAILED_TO_DELETE_HOST_DEFINITION.format(
                    host_definition_name, messages.FAILED_TO_REMOVE_FINALIZER))
        except ApiException as ex:
            if ex.status != 404:
                logger.error(messages.FAILED_TO_DELETE_HOST_DEFINITION.format(host_definition_name, ex.body))

    def _remove_finalizer(self, host_definition_name):
        logger.info(messages.REMOVE_FINALIZER_TO_HOST_DEFINITION.format(host_definition_name))
        return self._update_finalizer(host_definition_name, [])

    def _update_finalizer(self, host_definition_name, finalizers):
        finalizer_manifest = {
            settings.METADATA: {
                common_settings.NAME_FIELD: host_definition_name,
                settings.FINALIZERS: finalizers,
            }
        }
        return self._patch_host_definition(finalizer_manifest)

    def _patch_host_definition(self, host_definition_manifest):
        host_definition_name = host_definition_manifest[settings.METADATA][common_settings.NAME_FIELD]
        logger.info(messages.PATCHING_HOST_DEFINITION.format(host_definition_name))
        try:
            self.host_definitions_api.patch(body=host_definition_manifest, name=host_definition_name,
                                            content_type='application/merge-patch+json')
            return 200
        except ApiException as ex:
            if ex.status == 404:
                logger.error(messages.HOST_DEFINITION_DOES_NOT_EXIST.format(host_definition_name))
            else:
                logger.error(messages.FAILED_TO_PATCH_HOST_DEFINITION.format(host_definition_name, ex.body))
            return ex.status

    def _update_manage_node_label(self, node_name, label_value):
        body = self._get_body_for_labels(label_value)
        try:
            self.core_api.patch_node(node_name, body)
        except ApiException as ex:
            logger.error(messages.FAILED_TO_UPDATE_NODE_LABEL.format(
                node_name, settings.MANAGE_NODE_LABEL, ex.body))

    def _get_body_for_labels(self, label_value):
        body = {
            settings.METADATA: {
                settings.LABELS: {
                    settings.MANAGE_NODE_LABEL: label_value}
            }
        }

        return body

    def _get_secret_data(self, secret_name, secret_namespace):
        try:
            logger.info(messages.READ_SECRET.format(secret_name, secret_namespace))
            secret_data = self.core_api.read_namespaced_secret(name=secret_name, namespace=secret_namespace).data
            return self._change_decode_base64_secret_config(secret_data)
        except ApiException as ex:
            if ex.status == 404:
                logger.error(messages.SECRET_DOES_NOT_EXIST.format(secret_name, secret_namespace))
            else:
                logger.error(messages.FAILED_TO_GET_SECRET.format(secret_name, secret_namespace, ex.body))
        return {}

    def _change_decode_base64_secret_config(self, secret_data):
        if settings.SECRET_CONFIG_FIELD in secret_data.keys():
            secret_data[settings.SECRET_CONFIG_FIELD] = self._decode_base64_to_dict(
                secret_data[settings.SECRET_CONFIG_FIELD])
        return secret_data

    def _decode_base64_to_dict(self, content_with_base64):
        decoded_string_content = self._decode_base64_to_string(content_with_base64)
        encoded_dict = str(decoded_string_content).encode('utf-8')
        base64_dict = base64.b64encode(encoded_dict)
        my_dict_again = ast.literal_eval(str(base64.b64decode(base64_dict)))
        return my_dict_again

    def _decode_base64_to_string(self, content_with_base64):
        try:
            base64_bytes = content_with_base64.encode('ascii')
            decoded_string_in_bytes = base64.b64decode(base64_bytes)
            return decoded_string_in_bytes.decode('ascii')
        except Exception:
            return content_with_base64

    def _get_node_info(self, node_name):
        k8s_node = self._read_node(node_name)
        if k8s_node:
            return self._generate_node_info(k8s_node)
        return NodeInfo('', {})

    def _read_node(self, node_name):
        try:
            logger.info(messages.READ_NODE.format(node_name))
            return self.core_api.read_node(name=node_name)
        except ApiException as ex:
            logger.error(messages.FAILED_TO_GET_NODE.format(node_name, ex.body))
            return None

    def _generate_node_info(self, k8s_node):
        return NodeInfo(k8s_node.metadata.name, k8s_node.metadata.labels)

    def _get_csi_daemon_set(self):
        try:
            daemon_sets = self.apps_api.list_daemon_set_for_all_namespaces(label_selector=settings.DRIVER_PRODUCT_LABEL)
            if daemon_sets.items:
                return daemon_sets.items[0]
            return None
        except ApiException as ex:
            logger.error(messages.FAILED_TO_LIST_DAEMON_SETS.format(ex.body))
            return None

    def _get_csi_pods_info(self):
        try:
            pods_info = []
            k8s_pods = self.core_api.list_pod_for_all_namespaces(label_selector=settings.DRIVER_PRODUCT_LABEL)
            for k8s_pod in k8s_pods.items:
                pod_info = self._generate_pod_info(k8s_pod)
                pods_info.append(pod_info)
            return pods_info

        except ApiException as ex:
            logger.error(messages.FAILED_TO_LIST_PODS.format(ex.body))
            return []

    def _generate_pod_info(self, k8s_pod):
        pod_info = PodInfo()
        pod_info.name = k8s_pod.metadata.name
        pod_info.node_name = k8s_pod.spec.node_name
        return pod_info
