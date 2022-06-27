import base64
import datetime
import os
from kubernetes import client, config, dynamic
from kubernetes.client import api_client
from kubernetes.client.rest import ApiException

from controllers.common.csi_logger import get_stdout_logger
from controllers.servers.host_definer.common import settings
from controllers.servers.host_definer.common.types import VerifyHostRequest
from controllers.servers.host_definer.storage_manager.host import StorageHostManager
import controllers.servers.host_definer.watcher.exceptions as exceptions

SECRET_IDS = {}
NODES = {}
logger = get_stdout_logger()


class WatcherHelper:
    def __init__(self):
        self.storage_host_manager = StorageHostManager()
        self._load_cluster_configuration()
        self.dynamic_client = self._get_dynamic_client()
        self.storage_api = client.StorageV1Api()
        self.core_api = client.CoreV1Api()
        self.custom_object_api = client.CustomObjectsApi()
        self.csi_nodes_api = self._get_csi_nodes_api()
        self.host_definitions_api = self._get_host_definitions_api()

    def _get_dynamic_client(self):
        return dynamic.DynamicClient(api_client.ApiClient(
            configuration=self._load_cluster_configuration()))

    def _load_cluster_configuration(self):
        return config.load_incluster_config()

    def _get_csi_nodes_api(self):
        return self.dynamic_client.resources.get(
            api_version=settings.STORAGE_API_VERSION,
            kind=settings.CSINODE_KIND)

    def _get_host_definitions_api(self):
        return self.dynamic_client.resources.get(
            api_version=settings.CSI_IBM_BLOCK_API_VERSION,
            kind=settings.HOSTDEFINITION_KIND)

    def verify_nodes_defined(self, host_request):
        for node in NODES:
            host_request.node_id = self.get_node_id_from_node_name(node)
            self._verify_host_defined_and_has_host_definition(host_request)

    def _verify_host_defined_and_has_host_definition(self, host_request):
        if self._is_host_already_defined(host_request):
            return
        response = self.verify_host_defined_on_storage_and_on_cluster(host_request)
        if response.error_message:
            self._verify_host_definition_in_phase(host_request, settings.PENDING_CREATION_PHASE)
            self._create_event_to_host_definition_from_host_request(
                host_request, response.error_message)

    def _is_host_already_defined(self, host_request):
        node = self.get_node_name_from_node_id(host_request.node_id)
        host_definition_name = self.get_host_definition_name(
            host_request, node)
        if self._is_host_definition_in_ready_state(host_definition_name):
            logger.info(
                'Host {} is already on storage, detected hostdefinition {} in Ready phase'.format(
                    node, host_definition_name))
            return True
        return False

    def _is_host_definition_in_ready_state(self, host_definition_name):
        try:
            return self.is_host_definition_ready(host_definition_name)
        except dynamic.exceptions.NotFoundError:
            return False
        except Exception as ex:
            logger.error(ex)
            return False

    def is_host_definition_ready(self, host_definition_name):
        host_definition = self._get_host_definition(
            host_definition_name)
        return self.get_phase_of_host_definition(
            host_definition) == settings.READY_PHASE

    def _get_host_definition(self, host_definition_name):
        try:
            return self.host_definitions_api.get(name=host_definition_name)
        except dynamic.exceptions.NotFoundError as ex:
            raise ex
        except ApiException:
            raise exceptions.FailedToGetHostDefinitionObject

    def get_phase_of_host_definition(self, host_definition):
        if host_definition.status:
            return host_definition.status.phase
        return None

    def verify_host_defined_on_storage_and_on_cluster(self, host_request):
        response = self.storage_host_manager.define_host(host_request)
        if response.error_message:
            return response
        self._verify_host_definition_in_phase(host_request, settings.READY_PHASE)
        return response

    def _verify_host_definition_in_phase(self, host_request, phase):
        node_name = self.get_node_name_from_node_id(host_request.node_id)
        host_definition_name = self.get_host_definition_name(
            host_request, node_name)
        try:
            self._verify_host_definition(host_definition_name, host_request, phase)
        except Exception as ex:
            logger.error(
                'Failed to verify hostdefinition {} in {} phase, got: {}'.format(
                    host_definition_name, phase, ex))

    def _verify_host_definition(self, host_definition_name, host_request, phase):
        host_definition_manifest = self.get_host_definition_manifest_from_host_request(
            host_request, host_definition_name)
        logger.info('Verifying hostDefinition {} is in {} phase'.format(
            host_definition_name, phase))
        try:
            _ = self._get_host_definition(host_definition_name)
            self.patch_host_definition(host_definition_manifest)
        except dynamic.exceptions.NotFoundError:
            logger.info('Creating host Definition: {}'.format(
                host_definition_name))
            self._create_host_definition(host_definition_manifest)
        self.set_host_definition_status(host_definition_name, phase)

    def get_host_definition_name(self, host_request, node_name):
        return '{0}.{1}'.format(
            host_request.system_info[settings.MANAGEMENT_ADDRESS_KEY], node_name).replace('_', '.')

    def get_host_definition_manifest_from_host_request(
            self, host_request, host_definition_name):
        node_name = self.get_node_name_from_node_id(host_request.node_id)
        return {
            'apiVersion': settings.CSI_IBM_BLOCK_API_VERSION,
            'kind': settings.HOSTDEFINITION_KIND,
            'metadata': {
                'name': host_definition_name,
            },
            'spec': {
                'hostDefinition': {
                    'managementAddress': host_request.system_info[settings.MANAGEMENT_ADDRESS_KEY],
                    'nodeName': node_name,
                    'nodeId': host_request.node_id,
                    'secretName': host_request.secret_name,
                    'secretNamespace': host_request.secret_namespace,
                },
            },
        }

    def patch_host_definition(self, host_definition_manifest):
        host_definition_name = host_definition_manifest['metadata']['name']
        logger.info('Patching host definition: {}'.format(
            host_definition_name))
        try:
            self.host_definitions_api.patch(
                body=host_definition_manifest,
                name=host_definition_name,
                content_type='application/merge-patch+json')
        except dynamic.exceptions.NotFoundError as ex:
            raise ex
        except ApiException as ex:
            if ex.status == 404:
                raise exceptions.HostDefinitionDoesNotExist(
                    host_definition_name)
            raise exceptions.FailedToPatchHostDefinitionObject(
                host_definition_name, ex.body)

    def _create_host_definition(self, host_definition_manifest):
        try:
            self.host_definitions_api.create(body=host_definition_manifest)
        except ApiException as ex:
            raise exceptions.FailedToCreateHostDefinitionObject(
                host_definition_manifest['metadata']['name'], ex.body)

    def get_host_request_from_secret_and_node_name(self, secret_id, node_name):
        host_request = self.get_host_request_from_secret_id(secret_id)
        if host_request:
            host_request.node_id = self.get_node_id_from_node_name(node_name)
        return host_request

    def get_node_id_from_node_name(self, node_name):
        return NODES[node_name]

    def get_host_request_from_secret_id(self, secret):
        secret_name, secret_namespace = self._get_secret_name_and_namespace_from_id(secret)
        return self.get_host_request_from_secret_name_and_namespace(
            secret_name, secret_namespace)

    def get_host_request_from_host_definition(
            self, host_definition):
        secret_name = host_definition.spec.hostDefinition.secretName
        secret_namespace = host_definition.spec.hostDefinition.secretNamespace
        host_request = self.get_host_request_from_secret_name_and_namespace(
            secret_name, secret_namespace)
        if host_request:
            host_request.node_id = host_definition.spec.hostDefinition.nodeId
        return host_request

    def _get_secret_name_and_namespace_from_id(self, secret_id):
        return secret_id.split(',')

    def get_host_request_from_secret_name_and_namespace(
            self, secret_name, secret_namespace):
        host_request = self._get_new_host_request()
        try:
            host_request.system_info = self._get_system_info_from_secret(
                secret_name, secret_namespace)
        except Exception as ex:
            logger.error(ex)
            return None
        host_request.secret_name = secret_name
        host_request.secret_namespace = secret_namespace
        return host_request

    def _get_new_host_request(self):
        host_request = VerifyHostRequest()
        host_request.prefix = self._get_prefix()
        host_request.connectivity_type = self.get_connectivity()
        return host_request

    def _get_prefix(self):
        return os.getenv('PREFIX')

    def get_connectivity(self):
        return os.getenv('CONNECTIVITY')

    def _get_system_info_from_secret(self, secret_name, secret_namespace):
        try:
            secret_data = self.core_api.read_namespaced_secret(
                name=secret_name, namespace=secret_namespace).data
        except ApiException as ex:
            if ex.status == 404:
                raise exceptions.SecretDoesNotExist(
                    secret_name, secret_namespace)
            raise exceptions.FailedToGetSecret(
                secret_name, secret_namespace, ex.body)

        return self._get_system_info_from_secret_data(secret_data)

    def _get_system_info_from_secret_data(self, secret_data):
        return {
            settings.MANAGEMENT_ADDRESS_KEY: self._decode_base64_to_string(
                secret_data[settings.MANAGEMENT_ADDRESS_KEY]),
            settings.USERNAME_KEY: self._decode_base64_to_string(
                secret_data[settings.USERNAME_KEY]),
            settings.PASSWORD_KEY: self._decode_base64_to_string(
                secret_data[settings.PASSWORD_KEY])
        }

    def _decode_base64_to_string(self, content_with_base64):
        base64_bytes = content_with_base64.encode('ascii')
        decoded_string_in_bytes = base64.b64decode(base64_bytes)
        return decoded_string_in_bytes.decode('ascii')

    def undefine_host_and_host_definition_with_events(self, host_request):
        node_name = self.get_node_name_from_node_id(host_request.node_id)
        logger.info('Verifying that host {} is undefined from storage {}'.format(
            node_name, host_request.system_info[settings.MANAGEMENT_ADDRESS_KEY]))
        host_definition_name = self.get_host_definition_name(
            host_request, node_name)
        response = self.define_host_and_host_definition(host_request, host_definition_name)
        if response.error_message:
            self.set_host_definition_status_to_pending_deletion(host_definition_name)
            self._create_event_to_host_definition_from_host_request(
                host_request, response.error_message)

    def set_host_definition_status_to_pending_deletion(
            self, host_definition_name):
        try:
            self.set_host_definition_status(
                host_definition_name, settings.PENDING_DELETION_PHASE)
        except Exception as ex:
            logger.error(
                'Failed to set hostdefinition {} phase to pending for deletion, got: {}'.format(
                    host_definition_name, ex))

    def set_host_definition_status(
            self,
            host_definition_name,
            host_definition_name_phase):
        logger.info('Set host definition {} status to: {}'.format(
            host_definition_name, host_definition_name_phase))
        status = {
            'status': {
                'phase': host_definition_name_phase,
            }
        }
        try:
            self.custom_object_api.patch_cluster_custom_object_status(
                settings.CSI_IBM_BLOCK_GROUP,
                settings.VERSION,
                settings.HOSTDEFINITION_PLURAL,
                host_definition_name,
                status)
        except ApiException as ex:
            if ex.status == 404:
                raise exceptions.HostDefinitionDoesNotExist(
                    host_definition_name)
            raise exceptions.FailedToSetHostDefinitionStatus(
                host_definition_name, ex.body)

    def _create_event_to_host_definition_from_host_request(
            self, host_request, message):
        node_name = self.get_node_name_from_node_id(host_request.node_id)
        host_definition_name = self.get_host_definition_name(
            host_request, node_name)
        try:
            host_definition = self._get_host_definition(
                host_definition_name)
        except Exception as ex:
            logger.error(ex)
            return
        self.add_event_to_host_definition(host_definition, message)

    def get_node_name_from_node_id(self, node_id):
        return node_id.split(';')[0]

    def add_event_to_host_definition(
            self, host_definition, message):
        logger.info('Creating event for host definition: {} error event: {}'.format(
            host_definition.metadata.name, message))
        event = self._get_event_for_object(host_definition, message)
        self.create_event(settings.DEFAULT_NAMESPACE, event)

    def _get_event_for_object(self, obj, message):
        return client.CoreV1Event(
            metadata=client.V1ObjectMeta(
                generate_name='{}.'.format(obj.metadata.name),
            ),
            reporting_component=settings.HOSTDEFINER,
            reporting_instance=settings.HOSTDEFINER,
            action='Verifying',
            type='Error',
            reason=settings.FAILED_VERIFYING,
            message=str(message),
            event_time=datetime.datetime.utcnow().isoformat(
                timespec='microseconds') + 'Z',
            involved_object=client.V1ObjectReference(
                api_version=obj.api_version,
                kind=obj.kind,
                name=obj.metadata.name,
                resource_version=obj.metadata.resource_version,
                uid=obj.metadata.uid,
            ))

    def create_event(self, namespace, event):
        try:
            self.core_api.create_namespaced_event(namespace, event)
        except ApiException as ex:
            logger.error(
                'Failed to create event for host definition {}, go this error: {}'.format(
                    event.involved_object.name, ex.body))

    def is_host_can_be_defined(self, node_name):
        if self.is_dynamic_node_labeling_allowed():
            return True
        return self.is_node_has_managed_by_host_definer_label(node_name)

    def is_dynamic_node_labeling_allowed(self):
        return os.getenv('DYNAMIC_NODE_LABELING') == 'true'

    def is_host_can_be_undefined(self, node_name):
        if self._is_host_definer_can_delete_hosts():
            return self.is_node_has_managed_by_host_definer_label(node_name) and \
                (not self._is_node_has_host_definer_avoid_deletion_label(node_name))
        return False

    def _is_host_definer_can_delete_hosts(self):
        return os.getenv('ALLOW_DELETE') == 'true'

    def is_node_has_managed_by_host_definer_label(self, node_name):
        return self._is_host_has_label_in_true(node_name, settings.MANAGED_BY_HOST_DEFINER_LABEL)

    def _is_node_has_host_definer_avoid_deletion_label(self, node_name):
        return self._is_host_has_label_in_true(node_name, settings.HOST_DEFINER_AVOID_DELETION_LABEL)

    def _is_host_has_label_in_true(self, node_name, label):
        try:
            node = self.core_api.read_node(name=node_name)
        except ApiException as ex:
            logger.error('Could not get node {}, got: {}'.format(
                node_name, ex.body))
            return False
        if label in node.metadata.labels:
            return node.metadata.labels[label] == 'true'
        return False

    def define_host_and_host_definition(self, host_request, host_definition_name):
        response = self.storage_host_manager.undefine_host(host_request)
        if response.error_message:
            return response
        self.delete_host_definition(host_definition_name)
        return response

    def delete_host_definition(self, host_definition_name):
        try:
            self.host_definitions_api.delete(name=host_definition_name, body={})
        except ApiException as ex:
            if ex.status == 404:
                logger.error('Failed to delete hostDefinition {} because it does not exist'.format(
                    host_definition_name))
            else:
                logger.error('Failed to delete hostDefinition {}, got: {}'.format(
                    host_definition_name, ex.body))

    def is_csi_node_has_ibm_csi_block_driver(self, csi_node):
        if csi_node.spec.drivers:
            for driver in csi_node.spec.drivers:
                if driver.name == settings.IBM_BLOCK_CSI_DRIVER_NAME:
                    return True
        return False

    def get_node_name_from_csi_node(self, csi_node):
        return csi_node.metadata.name

    def add_node_to_nodes(self, node_name, csi_node):
        self._add_managed_by_host_definer_label_to_node(node_name)
        NODES[node_name] = self._get_node_id_from_csi_node(
            csi_node)

    def _get_node_id_from_csi_node(self, csi_node):
        for driver in csi_node.spec.drivers:
            if driver.name == settings.IBM_BLOCK_CSI_DRIVER_NAME:
                return driver.nodeID
        return None

    def _add_managed_by_host_definer_label_to_node(self, node_name):
        if self.is_node_has_managed_by_host_definer_label(node_name):
            return
        logger.info('Add {} label to node {}'.format(
            settings.MANAGED_BY_HOST_DEFINER_LABEL, node_name))
        self._update_node_managed_by_host_definer_label(node_name, 'true')

    def remove_managed_by_host_definer_label(self, node_name):
        if self.is_dynamic_node_labeling_allowed():
            logger.info('Remove {} label from node {}'.format(
                settings.MANAGED_BY_HOST_DEFINER_LABEL, node_name))
            self._update_node_managed_by_host_definer_label(node_name, None)

    def _update_node_managed_by_host_definer_label(self, node_name, label_value):
        body = {
            'metadata': {
                'labels': {
                    settings.MANAGED_BY_HOST_DEFINER_LABEL: label_value}
            }
        }
        try:
            self.core_api.patch_node(node_name, body)
        except ApiException as ex:
            logger.error('Could not update node {} {} label, got: {}'.format(
                node_name, settings.MANAGED_BY_HOST_DEFINER_LABEL, ex.body))

    def define_host_on_all_storages_from_secrets(self, node_name):
        for secret_id, storage_classes_using_this_secret in SECRET_IDS.items():
            if storage_classes_using_this_secret == 0:
                continue
            host_request = self.get_host_request_from_secret_and_node_name(secret_id, node_name)
            if host_request:
                self._verify_host_defined_and_has_host_definition(host_request)

    def generate_secret_id_from_secret_and_namespace(
            self, secret_name, secret_namespace):
        return secret_name + ',' + secret_namespace
