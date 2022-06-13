import base64
from kubernetes import client, config, dynamic
from kubernetes.client import api_client
from kubernetes.client.rest import ApiException

from common import settings, utils
from storage_manager.exceptions import StorageException
from storage_manager.host import StorageHostManager
import watcher.exceptions as exceptions

SECRET_IDS = {}
CSI_NODES = []
logger = utils.get_stdout_logger()

class HostObject:
    pass

class WatcherHelper:
    def __init__(self):
        self.storage_host_manager = StorageHostManager()
        self._load_cluster_configuration()
        self.dynamic_client = self._get_dynamic_client()
        self.storage_api = client.StorageV1Api()
        self.core_api = client.CoreV1Api()
        self.csi_nodes_api = self._get_csi_nodes_api()
        self.csi_hostdefinitions_api = self._get_csi_hostdefinitions_api()
    
    def _get_dynamic_client(self):
        return dynamic.DynamicClient(api_client.ApiClient(configuration=self._load_cluster_configuration()))

    def _load_cluster_configuration(self):
        return config.load_incluster_config()

    def _get_csi_nodes_api(self):
        return self.dynamic_client.resources.get(
            api_version=settings.STORAGE_API_VERSION, kind=settings.CSINODE_KIND)

    def _get_csi_hostdefinitions_api(self):
        return self.dynamic_client.resources.get(
            api_version=settings.CSI_IBM_BLOCK_API_VERSION, kind=settings.HOSTDEFINITION_KIND)
        
    def verify_csi_nodes_on_storage(self, host_object):
        for csi_node in CSI_NODES:
            host_object.host_name = csi_node
            host_definition_name = self.get_host_definition_name_from_host_object(host_object)
            if self._is_host_already_on_storage(host_definition_name):
                logger.info('Host {} is already on storage, detected hostdefinition {} in Ready phase'.format(
                    csi_node, host_definition_name))
            else:
                self.verify_on_storage(host_object)
            
                
    def _is_host_already_on_storage(self, host_definition_name):
        try:
            return self.is_host_definition_ready(host_definition_name)
        except dynamic.exceptions.NotFoundError:
            return False
        except Exception as ex:
            logger.error(ex)

    def is_host_definition_ready(self, csi_host_definition_name):
        host_definition_object = self._get_host_definition_object(csi_host_definition_name)
        return host_definition_object.spec.hostDefinition.phase == settings.READY_PHASE

    def _get_host_definition_object(self, host_definition_name):
        try:
            return self.csi_hostdefinitions_api.get(name=host_definition_name)
        except dynamic.exceptions.NotFoundError as ex:
            raise ex
        except ApiException:
            raise exceptions.FailedToGetHostDefinitionObject
        
            
    def verify_on_storage(self, host_object):
        try:
            self.storage_host_manager.verify_host_on_storage(host_object)
            host_object.phase = settings.READY_PHASE
            self.verify_csi_host_definition_from_host_object(host_object)
        except StorageException:
            host_object.phase = settings.PENDING_PHASE
            self.verify_csi_host_definition_from_host_object(host_object)
        
    def verify_csi_host_definition_from_host_object(self, host_object):
        host_definition_name = self.get_host_definition_name_from_host_object(host_object)
        host_definition_manifest = self.get_host_definition_manifest_from_host_object(host_object, host_definition_name)
        try:
            self._verify_host_definition_manifest(host_definition_name, host_definition_manifest)
        except Exception as ex:
            logger.error('Failed to verify hostdefinition {} in {} phase, got error: {}'.format(
                host_definition_name, host_object.phase, ex))

    def get_host_definition_name_from_host_object(self, host_object):
        return '{0}.{1}'.format(host_object.storage_server, host_object.host_name).replace('_', '.')
    
    def get_host_definition_manifest_from_host_object(self, host_object, host_definition_name):
        host_object = self._set_defaults(host_object)

        manifest = {
            'apiVersion': settings.CSI_IBM_BLOCK_API_VERSION,
            'kind': settings.HOSTDEFINITION_KIND,
            'metadata': {
                'name': host_definition_name,
            },
            'spec': {
                'hostDefinition': {
                    'storageServer': host_object.storage_server,
                    'hostNameInStorage': host_object.host_name,
                    'secretName': host_object.secret_name,
                    'secretNamespace': host_object.secret_namespace,
                    'phase': host_object.phase,
                    'message': host_object.message,
                    'action' : host_object.action,
                    'retryVerifying' : host_object.retryVerifying,
                },
            },
        }
        return manifest

    def _set_defaults(self, host_object):
        if not hasattr(host_object, 'message'):
            host_object.message = ''  
        if not hasattr(host_object, 'action'):
            host_object.action = settings.CREATE_ACTION
        if not hasattr(host_object, 'phase'):
            host_object.phase = settings.PENDING_PHASE
        if not hasattr(host_object, 'retryVerifying'):
            host_object.retryVerifying = False
        return host_object
    
    def _verify_host_definition_manifest(self, host_definition_name, host_definition_manifest):
        logger.info('Verifying hostDefinition {} is in {} phase'.format(
            host_definition_name, host_definition_manifest['spec']['hostDefinition']['phase']))
        try:
            _ = self._get_host_definition_object(host_definition_name)
            self.patch_host_definition(host_definition_manifest)
        except dynamic.exceptions.NotFoundError:
            logger.info('Creating host Definition object: {}'.format(
                    host_definition_name))
            try:
                self.csi_hostdefinitions_api.create(body=host_definition_manifest)
            except ApiException as ex:
                raise exceptions.FailedToCreateHostDefinitionObject(host_definition_name, ex.body)
        
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
            raise exceptions.FailedToPatchHostDefinitionObject(host_definition_name, ex.body)

    def get_host_object_from_secret_id(self, secret):
        secret_name, secret_namespace = self._get_secret_id_properties(secret)
        return self.get_host_object_from_secret_name_and_namespace(secret_name, secret_namespace)
    
    def _get_secret_id_properties(self, secret_id):
        return secret_id.split(',')

    def get_host_object_from_secret_name_and_namespace(self, secret_name, secret_namespace):
        storage_data = self._get_storage_from_secret(secret_name, secret_namespace)
        host_object = self._get_host_object_from_storage_data(storage_data)
        host_object.secret_name = secret_name
        host_object.secret_namespace = secret_namespace
        return host_object
    
    def _get_host_object_from_storage_data(self, storage_data):
        host_object = HostObject()
        host_object.storage_server = storage_data[settings.STORAGE_SERVER_KEY]
        host_object.storage_username = storage_data[settings.STORAGE_SERVER_USERNAME_KEY]
        host_object.storage_password = storage_data[settings.STORAGE_SERVER_PASSWORD_KEY]
        return host_object
    
    def _get_storage_from_secret(self, secret_name, secret_namespace):
        try:
            secret_data = self.core_api.read_namespaced_secret(
                name=secret_name, namespace=secret_namespace).data
        except ApiException as ex:
            if ex.status == 400:
                raise exceptions.SecretDoesNotExist(secret_name, secret_namespace)
            else:
                raise exceptions.SecretDoesNotExistsFromUnknownReason(secret_name, secret_namespace, ex.body)
        
        return self._get_storage_from_secret_data(secret_data)
    
    def _get_storage_from_secret_data(self, secret_data):
        return {
            settings.STORAGE_SERVER_KEY: self._decode_base64_to_string(
                secret_data[settings.MANAGMENT_ADDRESS_KEY]),
            settings.STORAGE_SERVER_USERNAME_KEY: self._decode_base64_to_string(
                secret_data[settings.USERNAME_KEY]),
            settings.STORAGE_SERVER_PASSWORD_KEY: self._decode_base64_to_string(
                secret_data[settings.PASSWORD_KEY])
        }
        
    def _decode_base64_to_string(self, content_with_base64):
        base64_bytes = content_with_base64.encode('ascii')
        decoded_string_in_bytes = base64.b64decode(base64_bytes)
        return decoded_string_in_bytes.decode('ascii')
    
    def delete_host_definition_object(self, object_name):
        self.csi_hostdefinitions_api.delete(name=object_name, body={})

    def _generate_secret_id_From_secret_and_namespace(self, secret_name, secret_namespace):
        return secret_name + ',' + secret_namespace
