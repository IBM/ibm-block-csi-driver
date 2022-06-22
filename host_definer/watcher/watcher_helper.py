import base64
import datetime
import os
from kubernetes import client, config, dynamic
from kubernetes.client import api_client
from kubernetes.client.rest import ApiException

from host_definer.common import settings, utils
from host_definer.common.types import VerifyHostRequest
from host_definer.storage_manager.host import StorageHostManager
import host_definer.watcher.exceptions as exceptions

SECRET_IDS = {}
NODES = {}
logger = utils.get_stdout_logger()


class WatcherHelper:
    def __init__(self):
        self.storage_host_manager = StorageHostManager()
        self._load_cluster_configuration()
        self.dynamic_client = self._get_dynamic_client()
        self.storage_api = client.StorageV1Api()
        self.core_api = client.CoreV1Api()
        self.custom_object_api = client.CustomObjectsApi()
        self.csi_nodes_api = self._get_csi_nodes_api()
        self.csi_hostdefinitions_api = self._get_csi_hostdefinitions_api()

    def _get_dynamic_client(self):
        return dynamic.DynamicClient(api_client.ApiClient(
            configuration=self._load_cluster_configuration()))

    def _load_cluster_configuration(self):
        return config.load_incluster_config()

    def _get_csi_nodes_api(self):
        return self.dynamic_client.resources.get(
            api_version=settings.STORAGE_API_VERSION,
            kind=settings.CSINODE_KIND)

    def _get_csi_hostdefinitions_api(self):
        return self.dynamic_client.resources.get(
            api_version=settings.CSI_IBM_BLOCK_API_VERSION,
            kind=settings.HOSTDEFINITION_KIND)

    def verify_nodes_defined(self, host_request):
        for node in NODES:
            host_definition_name = self.get_host_definition_name(
                host_request, node)
            if self._is_host_already_defined(host_definition_name):
                logger.info(
                    'Host {} is already on storage, detected hostdefinition {} in Ready phase'.format(
                        node, host_definition_name))
            else:
                host_request.node_id = self.get_node_id_from_node_name(node)
                self.verify_host_defined_and_has_host_definition(host_request)

    def _is_host_already_defined(self, host_definition_name):
        try:
            return self.is_host_definition_ready(host_definition_name)
        except dynamic.exceptions.NotFoundError:
            return False
        except Exception as ex:
            logger.error(ex)

    def is_host_definition_ready(self, csi_host_definition_name):
        host_definition_object = self._get_host_definition_object(
            csi_host_definition_name)
        return self.get_phase_of_host_definition_object(
            host_definition_object) == settings.READY_PHASE

    def _get_host_definition_object(self, host_definition_name):
        try:
            return self.csi_hostdefinitions_api.get(name=host_definition_name)
        except dynamic.exceptions.NotFoundError as ex:
            raise ex
        except ApiException:
            raise exceptions.FailedToGetHostDefinitionObject

    def get_phase_of_host_definition_object(self, host_definition_object):
        if host_definition_object.status:
            return host_definition_object.status.phase
        return None

    def verify_host_defined_and_has_host_definition(self, host_request):
        response = self.verify_host_defined_on_storage_and_on_cluster(host_request)
        if response.error_message:
            self.verify_csi_host_definition_from_host_request(host_request, settings.PENDING_CREATION_PHASE)
            self.create_event_to_host_definition_from_host_request(
                host_request, response.error_message)

    def verify_host_defined_on_storage_and_on_cluster(self, host_request):
        response = self.storage_host_manager.verify_host_defined(host_request)
        if response.error_message:
            return response
        self.verify_csi_host_definition_from_host_request(host_request, settings.READY_PHASE)
        return response

    def verify_csi_host_definition_from_host_request(self, host_request, phase):
        node_name = self.get_node_name_from_node_id(host_request.node_id)
        host_definition_name = self.get_host_definition_name(
            host_request, node_name)
        try:
            self._verify_host_definition(host_definition_name, host_request, phase)
        except Exception as ex:
            logger.error(
                'Failed to verify hostdefinition {} in {} phase, got error: {}'.format(
                    host_definition_name, phase, ex))

    def get_host_definition_name(self, host_request, node_name):
        host_name_on_storage = self._get_host_name_on_storage(node_name)
        return '{0}.{1}'.format(
            host_request.system_info[settings.MANAGEMENT_ADDRESS_KEY], host_name_on_storage).replace('_', '.')

    def _get_host_name_on_storage(self, node_name):
        prefix = self._get_prefix()
        return prefix + node_name

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
                    'secretName': host_request.secret_name,
                    'secretNamespace': host_request.secret_namespace,
                },
            },
        }

    def _verify_host_definition(self, host_definition_name, host_request, phase):
        host_definition_manifest = self.get_host_definition_manifest_from_host_request(
            host_request, host_definition_name)
        logger.info('Verifying hostDefinition {} is in {} phase'.format(
            host_definition_name, phase))
        try:
            _ = self._get_host_definition_object(host_definition_name)
            self.patch_host_definition(host_definition_manifest)
        except dynamic.exceptions.NotFoundError:
            logger.info('Creating host Definition object: {}'.format(
                host_definition_name))
            self._create_host_definition(host_definition_manifest)
        self.set_host_definition_status(host_definition_name, phase)

    def patch_host_definition(self, host_definition_manifest):
        host_definition_name = host_definition_manifest['metadata']['name']
        logger.info('Patching host definition: {}'.format(
            host_definition_name))
        try:
            self.csi_hostdefinitions_api.patch(
                body=host_definition_manifest,
                name=host_definition_name,
                content_type='application/merge-patch+json')
        except dynamic.exceptions.NotFoundError as ex:
            raise ex
        except ApiException as ex:
            if ex.status == 404:
                raise exceptions.HostDefinitionDoesNotExist(
                    host_definition_name)
            else:
                raise exceptions.FailedToPatchHostDefinitionObject(
                    host_definition_name, ex.body)

    def _create_host_definition(self, host_definition_manifest):
        try:
            self.csi_hostdefinitions_api.create(body=host_definition_manifest)
        except ApiException as ex:
            raise exceptions.FailedToCreateHostDefinitionObject(
                host_definition_manifest['metadata']['name'], ex.body)

    def get_host_request_from_secret_id(self, secret):
        secret_name, secret_namespace = self._get_secret_name_and_namespace_from_id(secret)
        return self.get_host_request_from_secret_name_and_namespace(
            secret_name, secret_namespace)

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

    def _get_system_info_from_secret(self, secret_name, secret_namespace):
        try:
            secret_data = self.core_api.read_namespaced_secret(
                name=secret_name, namespace=secret_namespace).data
        except ApiException as ex:
            if ex.status == 404:
                raise exceptions.SecretDoesNotExist(
                    secret_name, secret_namespace)
            else:
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

    def verify_host_undefined_on_storage_and_on_cluster(self, host_request, host_definition_name):
        response = self.storage_host_manager.verify_host_undefined(host_request)
        if response.error_message:
            return response
        self.delete_host_definition(host_definition_name)
        return response

    def delete_host_definition(self, host_definition_name):
        try:
            self.csi_hostdefinitions_api.delete(name=host_definition_name, body={})
        except ApiException as ex:
            if ex.status == 404:
                logger.error('Failed to delete hostDefinition {} because it does not exist'.format(
                    host_definition_name))
            else:
                logger.error('Failed to delete hostDefinition {}, got: {}'.format(
                    host_definition_name. ex.body))

    def _generate_secret_id_From_secret_and_namespace(
            self, secret_name, secret_namespace):
        return secret_name + ',' + secret_namespace

    def set_host_definition_status(
            self,
            host_definition_name,
            host_definition_name_phase):
        logger.info("Set host definition {} status to: {}".format(
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
            else:
                raise exceptions.FailedToSetHostDefinitionStatus(
                    host_definition_name, ex.body)

    def create_event_to_host_definition_from_host_request(
            self, host_request, message):
        node_name = self.get_node_name_from_node_id(host_request.node_id)
        host_definition_name = self.get_host_definition_name(
            host_request, node_name)
        try:
            host_definition_object = self._get_host_definition_object(
                host_definition_name)
        except Exception as ex:
            logger.error(ex)
            return
        self.add_event_to_host_definition_object(host_definition_object, message)

    def add_event_to_host_definition_object(
            self, host_definition_object, message):
        logger.info('Creating event for host definition: {} error event: {}'.format(
            host_definition_object.metadata.name, message))
        event = self.get_event_for_object(host_definition_object, message)
        self.create_event(settings.DEFAULT_NAMESPACE, event)

    def get_event_for_object(self, obj, message):
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

    def get_node_name_from_node_id(self, node_id):
        return node_id.split(';')[0]

    def get_node_id_from_node_name(self, node_name):
        return NODES[node_name]

    def get_connectivity(self):
        return os.getenv('CONNECTIVITY')
