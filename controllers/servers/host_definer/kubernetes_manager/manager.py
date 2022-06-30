import datetime

from kubernetes import client, config, dynamic
from kubernetes.client import api_client
from kubernetes.client.rest import ApiException

from controllers.common.csi_logger import get_stdout_logger
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
        return self.csi_nodes_api.get().items

    def _is_csi_node_has_driver(self, csi_node):
        if csi_node.spec.drivers:
            for driver in csi_node.spec.drivers:
                if driver.name == settings.IBM_BLOCK_CSI_PROVISIONER_NAME:
                    return True
        return False

    def _get_csi_node(self, node_name):
        try:
            csi_node = self.csi_nodes_api.get(name=node_name)
            return self._get_csi_node_object(csi_node)
        except ApiException as ex:
            if ex.status != 404:
                logger.error('Could not get csi node {}, got: {}'.format(node_name, ex.body))
            else:
                logger.error('node {}, do not have csi node'.format(node_name))
            return CsiNode()

    def _get_csi_node_object(self, csi_node):
        csi_node_obj = CsiNode()
        csi_node_obj.name = csi_node.metadata.name
        csi_node_obj.node_id = self._get_node_id_from_csi_node(csi_node)

    def _get_node_id_from_csi_node(self, csi_node):
        if csi_node.spec.drivers:
            for driver in csi_node.spec.drivers:
                if driver.name == settings.IBM_BLOCK_CSI_PROVISIONER_NAME:
                    return driver.nodeID
        return None

    def _get_host_definition(self, host_definition_name):
        try:
            return (self.host_definitions_api.get(name=host_definition_name), 200)
        except ApiException as ex:
            if ex.status == 404:
                logger.error('Host definition {} does not exists'.format(host_definition_name))
            else:
                logger.error('Failed to get host definition {}, go this error: {}'.format(
                    host_definition_name, ex.body))
            return '', ex.status

    def _get_host_definitions(self):
        try:
            host_definitions_obj = []
            host_definitions = self.host_definitions_api.get().items
            for host_definition in host_definitions:
                host_definitions_obj.append(self._get_host_definition_object(host_definition))
            return host_definitions_obj

        except ApiException as ex:
            logger.error("Could not get all hostDefinitions, got: {}".format(ex.body))
            return []

    def _get_host_definition_object(self, host_definition):
        host_definition_obj = HostDefinition()
        host_definition_obj.name = host_definition.metadata.name
        host_definition_obj.phase = self._get_phase_of_host_definition(host_definition)
        host_definition_obj.secret_name = self._get_attr_from_host_definition(
            host_definition, settings.SECRET_NAME_FIELD)
        host_definition_obj.secret_namespace = self._get_attr_from_host_definition(
            host_definition, settings.SECRET_NAMESPACE_FIELD)
        host_definition_obj.node_name = self._get_attr_from_host_definition(host_definition, settings.NODE_NAME_FIELD)
        host_definition_obj.node_id = self._get_attr_from_host_definition(host_definition, settings.NODE_ID_FIELD)
        host_definition_obj.management_address = self._get_attr_from_host_definition(
            host_definition, settings.MANAGEMENT_ADDRESS_FIELD)
        return host_definition_obj

    def _get_attr_from_host_definition(self, host_definition, attribute):
        try:
            return getattr(host_definition.spec.hostDefinition, attribute)
        except:
            return ''

    def patch_host_definition(self, host_definition_manifest):
        host_definition_name = host_definition_manifest['metadata']['name']
        logger.info('Patching host definition: {}'.format(host_definition_name))
        try:
            self.host_definitions_api.patch(body=host_definition_manifest, name=host_definition_name,
                                            content_type='application/merge-patch+json')
        except ApiException as ex:
            if ex.status == 404:
                logger.error('host definition {}does not exist'.format(host_definition_name))
            else:
                logger.error('Failed to patch host definition {}, go this error: {}'.format(
                    host_definition_name, ex.body))

    def _create_host_definition(self, host_definition_manifest):
        try:
            self.host_definitions_api.create(body=host_definition_manifest)
        except ApiException as ex:
            logger.error('Failed to create host definition {}, go this error: {}'.format(
                host_definition_manifest['metadata']['name'], ex.body))

    def set_host_definition_status(self, host_definition_name, host_definition_phase):
        logger.info('Set host definition {} status to: {}'.format(host_definition_name, host_definition_phase))
        status = self._get_status_manifest(host_definition_phase)
        try:
            self.custom_object_api.patch_cluster_custom_object_status(
                settings.CSI_IBM_GROUP, settings.VERSION, settings.HOST_DEFINITION_PLURAL, host_definition_name, status)
        except ApiException as ex:
            if ex.status == 404:
                logger.error('host definition {}does not exist'.format(host_definition_name))
            else:
                logger.error('Failed to set host definition {} status, go this error: {}'.format(
                    host_definition_name, ex.body))

    def _get_status_manifest(self, host_definition_phase):
        status = {
            'status': {
                'phase': host_definition_phase,
            }
        }

        return status

    def _get_event_for_object(self, obj, message):
        return client.CoreV1Event(
            metadata=client.V1ObjectMeta(
                generate_name='{}.'.format(obj.metadata.name),
            ),
            reporting_component=settings.HOST_DEFINER,
            reporting_instance=settings.HOST_DEFINER,
            action='Verifying',
            type='Error',
            reason=settings.FAILED_VERIFYING,
            message=str(message),
            event_time=datetime.datetime.utcnow().isoformat(
                timespec='microseconds') + 'Z',
            involved_object=client.V1ObjectReference(api_version=obj.api_version, kind=obj.kind,
                                                     name=obj.metadata.name,
                                                     resource_version=obj.metadata.resource_version,
                                                     uid=obj.metadata.uid,
                                                     ))

    def create_event(self, namespace, event):
        try:
            self.core_api.create_namespaced_event(namespace, event)
        except ApiException as ex:
            logger.error('Failed to create event for host definition {}, go this error: {}'.format(
                event.involved_object.name, ex.body))

    def delete_host_definition(self, host_definition_name):
        try:
            self.host_definitions_api.delete(name=host_definition_name, body={})
        except ApiException as ex:
            if ex.status == 404:
                logger.error('Failed to delete hostDefinition {} because it does not exist'.format(
                    host_definition_name))
            else:
                logger.error('Failed to delete hostDefinition {}, got: {}'.format(host_definition_name, ex.body))

    def _update_node_managed_by_host_definer_label(self, node_name, label_value):
        body = self._get_body_for_labels(label_value)
        try:
            self.core_api.patch_node(node_name, body)
        except ApiException as ex:
            logger.error('Could not update node {} {} label, got: {}'.format(
                node_name, settings.MANAGED_BY_HOST_DEFINER_LABEL, ex.body))

    def _get_body_for_labels(self, label_value):
        body = {
            'metadata': {
                'labels': {
                    settings.MANAGED_BY_HOST_DEFINER_LABEL: label_value}
            }
        }

        return body

    def _get_data_from_secret(self, secret_name, secret_namespace):
        try:
            return self.core_api.read_namespaced_secret(name=secret_name, namespace=secret_namespace).data
        except ApiException as ex:
            if ex.status == 404:
                logger.info('Secret {} in namespace {} does not exist'.format(secret_name, secret_namespace))
            else:
                logger.info('Failed to get Secret {} in namespace {}, go this error: {}'.format(
                    secret_name, secret_namespace, ex.body))
            return None

    def _read_node(self, node_name):
        try:
            return self.core_api.read_node(name=node_name)
        except ApiException as ex:
            logger.error('Could not get node {}, got: {}'.format(node_name, ex.body))
            return None
