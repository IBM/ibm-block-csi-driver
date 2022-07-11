import datetime

from kubernetes import client, config, dynamic
from kubernetes.client import api_client
from kubernetes.client.rest import ApiException

from controllers.common.csi_logger import get_stdout_logger
import controllers.servers.messages as messages
from controllers.servers.host_definer.types import HostDefinition
from controllers.servers.host_definer import settings
from controllers.servers.host_definer.types import CsiNode

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

    def _get_csi_nodes_with_driver(self):
        csi_nodes_with_driver = []
        csi_nodes = self._get_csi_nodes()
        for csi_node in csi_nodes:
            if self._is_csi_node_has_driver(csi_node):
                csi_nodes_with_driver.append(self._get_csi_node_object(csi_node))
        return csi_nodes_with_driver

    def _get_csi_nodes(self):
        try:
            return self.csi_nodes_api.get().items
        except ApiException as ex:
            logger.error(messages.FAILED_TO_GET_CSI_NODES.format(ex.body))
            return []

    def _get_nodes(self):
        try:
            return self.core_api.list_node().items
        except ApiException as ex:
            logger.error(messages.FAILED_TO_GET_NODES.format(ex.body))
            return []

    def _get_storage_classes(self):
        try:
            return self.storage_api.list_storage_class().items
        except ApiException as ex:
            logger.error(messages.FAILED_TO_GET_STORAGE_CLASSES.format(ex.body))
            return []

    def _is_csi_node_has_driver(self, csi_node):
        if csi_node.spec.drivers:
            for driver in csi_node.spec.drivers:
                return driver.name == settings.IBM_BLOCK_CSI_PROVISIONER_NAME
        return False

    def _get_csi_node(self, node_name):
        try:
            csi_node = self.csi_nodes_api.get(name=node_name)
            return self._get_csi_node_object(csi_node)
        except ApiException as ex:
            if ex.status != 404:
                logger.error(messages.CSI_NODE_DOES_NOT_EXIST.format(node_name))
            else:
                logger.error(messages.FAILED_TO_GET_CSI_NODE.format(node_name, ex.body))
            return CsiNode()

    def _get_csi_node_object(self, csi_node):
        csi_node_obj = CsiNode()
        csi_node_obj.name = csi_node.metadata.name
        csi_node_obj.node_id = self._get_node_id_from_csi_node(csi_node)
        return csi_node_obj

    def _get_node_id_from_csi_node(self, csi_node):
        if csi_node.spec.drivers:
            for driver in csi_node.spec.drivers:
                if driver.name == settings.IBM_BLOCK_CSI_PROVISIONER_NAME:
                    return driver.nodeID
        return None

    def _get_host_definition(self, node_name, secret):
        try:
            host_definitions = self._get_host_definitions()
            for host_definition in host_definitions:
                host_definition_obj = self._get_host_definition_object(host_definition)
                if self._is_host_definition_matches(host_definition_obj, node_name, secret):
                    return (host_definition_obj, 200)
            return ('', 404)
        except ApiException as ex:
            logger.error(messages.FAILED_TO_GET_HOST_DEFINITION.format(
                node_name, secret.name, secret.namespace, ex.body))
            return '', ex.status

    def _get_host_definitions(self):
        try:
            return self.host_definitions_api.get().items

        except ApiException as ex:
            logger.error(messages.FAILED_TO_GET_LIST_OF_HOST_DEFINITIONS.format(ex.body))
            return []

    def _get_host_definition_object(self, host_definition):
        host_definition_obj = HostDefinition()
        host_definition_obj.name = host_definition.metadata.name
        host_definition_obj.resource_version = host_definition.metadata.resource_version
        host_definition_obj.uid = host_definition.metadata.uid
        host_definition_obj.phase = self._get_host_definition_phase(host_definition)
        host_definition_obj.secret.name = self._get_attr_from_host_definition(
            host_definition, settings.SECRET_NAME_FIELD)
        host_definition_obj.secret.namespace = self._get_attr_from_host_definition(
            host_definition, settings.SECRET_NAMESPACE_FIELD)
        host_definition_obj.node_name = self._get_attr_from_host_definition(host_definition, settings.NODE_NAME_FIELD)
        host_definition_obj.node_id = self._get_attr_from_host_definition(host_definition, settings.NODE_ID_FIELD)
        return host_definition_obj

    def _get_host_definition_phase(self, host_definition):
        if host_definition.status:
            return host_definition.status.phase
        return ''

    def _get_attr_from_host_definition(self, host_definition, attribute):
        if hasattr(host_definition.spec.hostDefinition, attribute):
            return getattr(host_definition.spec.hostDefinition, attribute)
        return ''

    def _is_host_definition_matches(self, host_definition, node_name, secret):
        return host_definition.node_name == node_name and \
            host_definition.secret.name == secret.name and host_definition.secret.namespace == secret.namespace

    def _patch_host_definition(self, host_definition_manifest):
        host_definition_name = host_definition_manifest[settings.METADATA][settings.NAME]
        logger.info(messages.PATCHING_HOST_DEFINITION.format(host_definition_name))
        try:
            self.host_definitions_api.patch(body=host_definition_manifest, name=host_definition_name,
                                            content_type='application/merge-patch+json')
        except ApiException as ex:
            if ex.status == 404:
                logger.error(messages.HOST_DEFINITION_DOES_NOT_EXIST.format(host_definition_name))
            else:
                logger.error(messages.FAILED_TO_PATCH_HOST_DEFINITION.format(host_definition_name, ex.body))

    def _create_host_definition(self, host_definition_manifest):
        try:
            self.host_definitions_api.create(body=host_definition_manifest)
        except ApiException as ex:
            if ex != 404:
                logger.error(messages.FAILED_TO_CREATE_HOST_DEFINITION.format(
                    host_definition_manifest[settings.METADATA][settings.NAME], ex.body))

    def _set_host_definition_status(self, host_definition_name, host_definition_phase):
        logger.info(messages.SET_HOST_DEFINITION_STATUS.format(host_definition_name, host_definition_phase))
        status = self._get_status_manifest(host_definition_phase)
        try:
            self.custom_object_api.patch_cluster_custom_object_status(
                settings.CSI_IBM_GROUP, settings.VERSION, settings.HOST_DEFINITION_PLURAL, host_definition_name, status)
        except ApiException as ex:
            if ex.status == 404:
                logger.error(messages.HOST_DEFINITION_DOES_NOT_EXIST.format(host_definition_name))
            else:
                logger.error(messages.FAILED_TO_SET_HOST_DEFINITION_STATUS.format(host_definition_name, ex.body))

    def _get_status_manifest(self, host_definition_phase):
        status = {
            settings.STATUS: {
                settings.PHASE: host_definition_phase,
            }
        }

        return status

    def _get_event_for_host_definition(self, host_definition, message):
        return client.CoreV1Event(
            metadata=client.V1ObjectMeta(generate_name='{}.'.format(host_definition.name),),
            reporting_component=settings.HOST_DEFINER, reporting_instance=settings.HOST_DEFINER, action='Verifying',
            type='Error', reason=settings.FAILED_VERIFYING, message=str(message),
            event_time=datetime.datetime.utcnow().isoformat(timespec='microseconds') + 'Z',
            involved_object=client.V1ObjectReference(
                api_version=settings.CSI_IBM_API_VERSION, kind=settings.HOST_DEFINITION_KIND, name=host_definition.name,
                resource_version=host_definition.resource_version, uid=host_definition.uid,))

    def _create_event(self, namespace, event):
        try:
            self.core_api.create_namespaced_event(namespace, event)
        except ApiException as ex:
            logger.error(messages.FAILED_TO_CREATE_EVENT_FOR_HOST_DEFINITION.format(
                event.involved_object.name, ex.body))

    def _delete_host_definition(self, host_definition_name):
        try:
            self.host_definitions_api.delete(name=host_definition_name, body={})
        except ApiException as ex:
            if ex.status != 404:
                logger.error(messages.FAILED_TO_DELETE_HOST_DEFINITION.format(host_definition_name, ex.body))

    def _update_node_managed_by_host_definer_label(self, node_name, label_value):
        body = self._get_body_for_labels(label_value)
        try:
            self.core_api.patch_node(node_name, body)
        except ApiException as ex:
            logger.error(messages.FAILED_TO_UPDATE_NODE_LABEL.format(
                node_name, settings.MANAGED_BY_HOST_DEFINER_LABEL, ex.body))

    def _get_body_for_labels(self, label_value):
        body = {
            settings.METADATA: {
                settings.LABELS: {
                    settings.MANAGED_BY_HOST_DEFINER_LABEL: label_value}
            }
        }

        return body

    def _get_data_from_secret(self, secret):
        try:
            return self.core_api.read_namespaced_secret(name=secret.name, namespace=secret.namespace).data
        except ApiException as ex:
            if ex.status == 404:
                logger.error(messages.SECRET_DOES_NOT_EXIST.format(secret.name, secret.namespace))
            else:
                logger.error(messages.FAILED_TO_GET_SECRET.format(secret.name, secret.namespace, ex.body))
            return None

    def _read_node(self, node_name):
        try:
            return self.core_api.read_node(name=node_name)
        except ApiException as ex:
            logger.error(messages.FAILED_TO_GET_NODE.format(node_name, ex.body))
            return None

    def _get_csi_ibm_block_daemon_set(self):
        try:
            daemon_sets = self.apps_api.list_daemon_set_for_all_namespaces(label_selector=settings.DRIVER_PRODUCT_LABEL)
            if daemon_sets.items:
                return daemon_sets.items[0]
            return None
        except ApiException as ex:
            logger.error(messages.FAILED_TO_LIST_DAEMON_SETS.format(ex.body))
            return None

    def _get_csi_ibm_block_pods(self):
        try:
            return self.core_api.list_pod_for_all_namespaces(label_selector=settings.DRIVER_PRODUCT_LABEL)
        except ApiException as ex:
            logger.error(messages.FAILED_TO_LIST_PODS.format(ex.body))
            return None
