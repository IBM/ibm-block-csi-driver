from kubernetes import client, config, dynamic, watch
from kubernetes.client import api_client
from kubernetes.client.rest import ApiException
from munch import Munch

from controllers.common.csi_logger import get_stdout_logger
from controllers.servers.host_definer import settings
from controllers.servers.host_definer import utils
import controllers.common.settings as common_settings
import controllers.servers.host_definer.messages as messages

logger = get_stdout_logger()


class K8SApi():
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

    def get_csi_node(self, node_name):
        try:
            return self.csi_nodes_api.get(name=node_name)
        except ApiException as ex:
            if ex.status == 404:
                logger.error(messages.CSI_NODE_DOES_NOT_EXIST.format(node_name))
            else:
                logger.error(messages.FAILED_TO_GET_CSI_NODE.format(node_name, ex.body))
            return None

    def list_host_definition(self):
        try:
            return self.host_definitions_api.get()
        except ApiException as ex:
            logger.error(messages.FAILED_TO_GET_LIST_OF_HOST_DEFINITIONS.format(ex.body))
            return self._get_empty_k8s_list()

    def create_host_definition(self, host_definition_manifest):
        try:
            return self.host_definitions_api.create(body=host_definition_manifest)
        except ApiException as ex:
            if ex != 404:
                logger.error(messages.FAILED_TO_CREATE_HOST_DEFINITION.format(
                    host_definition_manifest[settings.METADATA][common_settings.NAME_FIELD], ex.body))
            return None

    def patch_cluster_custom_object_status(self, group, version, plural, name, status):
        try:
            self.custom_object_api.patch_cluster_custom_object_status(group, version, plural, name, status)
        except ApiException as ex:
            if ex.status == 404:
                logger.error(messages.HOST_DEFINITION_DOES_NOT_EXIST.format(name))
            else:
                logger.error(messages.FAILED_TO_SET_HOST_DEFINITION_STATUS.format(name, ex.body))

    def create_event(self, namespace, k8s_event):
        try:
            self.core_api.create_namespaced_event(namespace, k8s_event)
        except ApiException as ex:
            logger.error(messages.FAILED_TO_CREATE_EVENT_FOR_HOST_DEFINITION.format(
                k8s_event.involved_object.name, ex.body))

    def delete_host_definition(self, host_definition_name):
        try:
            return self.host_definitions_api.delete(name=host_definition_name, body={})
        except ApiException as ex:
            if ex.status != 404:
                logger.error(messages.FAILED_TO_DELETE_HOST_DEFINITION.format(host_definition_name, ex.body))
            return None

    def patch_host_definition(self, host_definition_manifest):
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

    def patch_node(self, node_name, body):
        try:
            self.core_api.patch_node(node_name, body)
        except ApiException as ex:
            logger.error(messages.FAILED_TO_UPDATE_NODE_LABEL.format(
                node_name, settings.MANAGE_NODE_LABEL, ex.body))

    def get_secret_data(self, secret_name, secret_namespace):
        try:
            return self.core_api.read_namespaced_secret(name=secret_name, namespace=secret_namespace).data
        except ApiException as ex:
            if ex.status == 404:
                logger.error(messages.SECRET_DOES_NOT_EXIST.format(secret_name, secret_namespace))
            else:
                logger.error(messages.FAILED_TO_GET_SECRET.format(secret_name, secret_namespace, ex.body))
            return {}

    def read_node(self, node_name):
        try:
            logger.info(messages.READ_NODE.format(node_name))
            return self.core_api.read_node(name=node_name)
        except ApiException as ex:
            logger.error(messages.FAILED_TO_GET_NODE.format(node_name, ex.body))
            return None

    def list_daemon_set_for_all_namespaces(self, label_selector):
        try:
            return self.apps_api.list_daemon_set_for_all_namespaces(label_selector=label_selector)
        except ApiException as ex:
            logger.error(messages.FAILED_TO_LIST_DAEMON_SETS.format(ex.body))
            return None

    def list_pod_for_all_namespaces(self, label_selector):
        try:
            return self.core_api.list_pod_for_all_namespaces(label_selector=label_selector)
        except ApiException as ex:
            logger.error(messages.FAILED_TO_LIST_PODS.format(ex.body))
            return None

    def get_storage_class_stream(self):
        return self._get_basic_resource_stream(self.storage_api.list_storage_class, 5)

    def list_storage_class(self):
        try:
            return self.storage_api.list_storage_class()
        except ApiException as ex:
            logger.error(messages.FAILED_TO_GET_STORAGE_CLASSES.format(ex.body))
            return self._get_empty_k8s_list()

    def get_node_stream(self):
        return self._get_basic_resource_stream(self.core_api.list_node, 5)

    def list_node(self):
        try:
            return self.core_api.list_node()
        except ApiException as ex:
            logger.error(messages.FAILED_TO_GET_NODES.format(ex.body))
            return self._get_empty_k8s_list()

    def get_secret_stream(self):
        return self._get_basic_resource_stream(self.core_api.list_secret_for_all_namespaces, 5)

    def _get_basic_resource_stream(self, list_function, timeout):
        resource_version = utils.get_k8s_object_resource_version(list_function())
        return watch.Watch().stream(list_function, resource_version=resource_version, timeout_seconds=timeout)

    def get_host_definition_stream(self, resource_version, timeout):
        return self.host_definitions_api.watch(resource_version=resource_version, timeout=timeout)

    def get_csi_node_stream(self):
        resource_version = utils.get_k8s_object_resource_version(self.list_csi_node())
        return self.csi_nodes_api.watch(resource_version=resource_version, timeout=5)

    def list_csi_node(self):
        try:
            return self.csi_nodes_api.get()
        except ApiException as ex:
            logger.error(messages.FAILED_TO_GET_CSI_NODES.format(ex.body))
            return self._get_empty_k8s_list()

    def _get_empty_k8s_list(self):
        much_object = Munch.fromDict({
            common_settings.ITEMS_FIELD: [],
            settings.METADATA: {
                common_settings.RESOURCE_VERSION_FIELD
            }
        })
        much_object.items = []
        return much_object
