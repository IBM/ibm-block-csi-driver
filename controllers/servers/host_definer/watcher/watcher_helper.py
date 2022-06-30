import base64
import os

from controllers.servers.config import (SECRET_ARRAY_PARAMETER,
                                        SECRET_PASSWORD_PARAMETER,
                                        SECRET_USERNAME_PARAMETER)
from controllers.common import utils
from controllers.servers.host_definer.kubernetes_manager.manager import KubernetesManager
from controllers.common.csi_logger import get_stdout_logger
from controllers.servers.host_definer import settings
from controllers.servers.host_definer.types import DefineHostRequest, HostDefinition
from controllers.servers.host_definer.storage_manager.host_definer_server import HostDefinerServicer

SECRET_IDS = {}
NODES = {}
logger = get_stdout_logger()


class Watcher(KubernetesManager):
    def __init__(self):
        self.storage_host_servicer = HostDefinerServicer()

    def verify_nodes_defined(self, host_definition):
        for node_name, _ in NODES.items():
            host_definition = self._add_name_to_host_definition(node_name, host_definition)
            self._verify_host_defined_and_has_host_definition(host_definition)

    def _verify_host_defined_and_has_host_definition(self, host_definition):
        if self._is_host_already_defined(host_definition.node_name, host_definition.name):
            return
        response = self.verify_host_defined_on_storage_and_on_cluster(host_definition)
        if response.error_message:
            self._verify_host_definition_in_phase(host_definition, settings.PENDING_CREATION_PHASE)
            self._create_event_to_host_definition(host_definition.name, response.error_message)

    def _is_host_already_defined(self, node_name, host_definition_name):
        if self.is_host_definition_ready(host_definition_name):
            logger.info('Host {} is already on storage, detected hostdefinition {} in Ready phase'.format(
                node_name, host_definition_name))
            return True
        return False

    def is_host_definition_ready(self, host_definition_name):
        host_definition, _ = self._get_host_definition(host_definition_name)
        if host_definition:
            return self._get_host_definition_phase(host_definition) == settings.READY_PHASE
        return False

    def _get_host_definition_phase(self, host_definition):
        if host_definition.status:
            return host_definition.status.phase
        return ''

    def verify_host_defined_on_storage_and_on_cluster(self, host_definition):
        response = self._ensure_definition_state(host_definition, self.storage_host_servicer.define_host)
        if response.error_message:
            return response
        self._verify_host_definition_in_phase(host_definition, settings.READY_PHASE)
        return response

    def _verify_host_definition_in_phase(self, host_definition, phase):
        host_definition_manifest = self.get_host_definition_manifest(host_definition)
        logger.info('Verifying hostDefinition {} is in {} phase'.format(host_definition.name, phase))
        host_definition_instance, _ = self._get_host_definition(host_definition.name)
        if host_definition_instance:
            self.patch_host_definition(host_definition_manifest)
        else:
            logger.info('Creating host Definition: {}'.format(host_definition.name))
            self._create_host_definition(host_definition_manifest)
        self.set_host_definition_status(host_definition.name, phase)

    def get_host_definition_name(self, management_address, node_name):
        return '{0}.{1}'.format(management_address, node_name).replace('_', '.')

    def get_host_definition_manifest(self, host_definition):
        return {
            'apiVersion': settings.CSI_IBM_API_VERSION,
            'kind': settings.HOST_DEFINITION_KIND,
            'metadata': {
                'name': host_definition.name,
            },
            'spec': {
                'hostDefinition': {
                    settings.MANAGEMENT_ADDRESS_FIELD: host_definition.management_address,
                    settings.NODE_NAME_FIELD: host_definition.node_name,
                    settings.NODE_ID_FIELD: host_definition.node_id,
                    settings.SECRET_NAME_FIELD: host_definition.secret_name,
                    settings.SECRET_NAMESPACE_FIELD: host_definition.secret_namespace,
                },
            },
        }

    def _get_prefix(self):
        return os.getenv('PREFIX')

    def get_connectivity(self):
        return os.getenv('CONNECTIVITY')

    def undefine_host_and_host_definition_with_events(self, host_definition):
        node_name = host_definition.node_name
        logger.info('Verifying that host {} is undefined from storage {}'.format(node_name,
                                                                                 host_definition.management_address))
        response = self.undefine_host_and_host_definition(host_definition)
        if response.error_message:
            self.set_host_definition_status(host_definition.name, settings.PENDING_DELETION_PHASE)
            self._create_event_to_host_definition(host_definition.name, response.error_message)

    def _create_event_to_host_definition(self, host_definition_name, message):
        host_definition, _ = self._get_host_definition(host_definition_name)
        if host_definition:
            self.add_event_to_host_definition(host_definition, message)

    def get_node_name_from_node_id(self, node_id):
        node_name, _, _, _ = utils.get_node_id_info(node_id)
        return node_name

    def add_event_to_host_definition(self, host_definition, message):
        logger.info('Creating event for host definition: {} error event: {}'.format(host_definition.name, message))
        event = self._get_event_for_object(host_definition, message)
        self.create_event(settings.DEFAULT_NAMESPACE, event)

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
        return self._is_host_has_label_in_true(node_name, settings.HOST_DEFINER_FORBID_DELETION_LABEL)

    def _is_host_has_label_in_true(self, node_name, label):
        node = self._read_node(node_name)
        if not node:
            return False
        if label in node.metadata.labels:
            return node.metadata.labels[label] == 'true'
        return False

    def undefine_host_and_host_definition(self, host_definition):
        response = self._ensure_definition_state(host_definition, self.storage_host_servicer.undefine_host)
        if response.error_message:
            return response
        self.delete_host_definition(host_definition.name)
        return response

    def _ensure_definition_state(self, host_definition, define_function):
        host_request = self._get_host_request_from_host_definition(host_definition)
        if not host_request:
            return
        return define_function(host_request)

    def _get_host_request_from_host_definition(self, host_definition):
        host_request = self.get_host_request_from_secret_name_and_namespace(host_definition.secret_name,
                                                                            host_definition.secret_namespace)
        if host_request:
            host_request.node_id = host_definition.node_id
            host_request.node_name = host_definition.node_name
        return host_request

    def get_host_request_from_secret_name_and_namespace(self, secret_name, secret_namespace):
        host_request = self._get_new_host_request()
        host_request.system_info = self._get_system_info_from_secret(secret_name, secret_namespace)
        if host_request.system_info:
            return host_request
        return None

    def _get_new_host_request(self):
        host_request = DefineHostRequest()
        host_request.prefix = self._get_prefix()
        host_request.connectivity_type = self.get_connectivity()
        return host_request

    def _get_host_definition_from_secret_and_node_name(self, node_name, secret_id):
        secret = self._get_secret_name_and_namespace_from_id(secret_id)
        host_definition = self._get_host_definition_from_secret(secret)
        if host_definition.management_address:
            host_definition = self._add_name_to_host_definition(node_name, host_definition)

        return host_definition

    def _get_host_definition_from_secret(self, secret_name, secret_namespace):
        host_definition = HostDefinition()
        host_definition.secret_name = secret_name
        host_definition.secret_namespace = secret_namespace
        host_definition.management_address = self._get_managment_address(
            host_definition.secret_name, host_definition.secret_namespace)

    def _get_managment_address(self, secret_name, secret_namespace):
        system_info = self._get_system_info_from_secret(secret_name, secret_namespace)
        if system_info:
            return system_info[SECRET_ARRAY_PARAMETER]
        return ''

    def _get_system_info_from_secret(self, secret_name, secret_namespace):
        secret_data = self._get_data_from_secret(secret_name, secret_namespace)
        return self._get_system_info_from_secret_data(secret_data)

    def _get_system_info_from_secret_data(self, secret_data):
        if not secret_data:
            return ''
        return {
            SECRET_ARRAY_PARAMETER: self._decode_base64_to_string(secret_data[SECRET_ARRAY_PARAMETER]),
            SECRET_USERNAME_PARAMETER: self._decode_base64_to_string(secret_data[SECRET_USERNAME_PARAMETER]),
            SECRET_PASSWORD_PARAMETER: self._decode_base64_to_string(secret_data[SECRET_PASSWORD_PARAMETER])
        }

    def _decode_base64_to_string(self, content_with_base64):
        base64_bytes = content_with_base64.encode('ascii')
        decoded_string_in_bytes = base64.b64decode(base64_bytes)
        return decoded_string_in_bytes.decode('ascii')

    def _add_name_to_host_definition(self, node_name, host_definition):
        host_definition.node_name = node_name
        host_definition.node_id = NODES[node_name]
        host_definition.name = self.get_host_definition_name(host_definition.management_address, node_name)
        return host_definition

    def _get_secret_name_and_namespace_from_id(self, secret_id):
        return secret_id.split(',')

    def get_node_name_from_csi_node(self, csi_node):
        return csi_node.metadata.name

    def add_node_to_nodes(self, csi_node):
        logger.info('New Kubernetes node {}, has csi IBM block'.format(csi_node.name))
        self._add_managed_by_host_definer_label_to_node(csi_node.name)
        NODES[csi_node.name] = csi_node.node_id

    def _add_managed_by_host_definer_label_to_node(self, node_name):
        if self.is_node_has_managed_by_host_definer_label(node_name):
            return
        logger.info('Add {} label to node {}'.format(settings.MANAGED_BY_HOST_DEFINER_LABEL, node_name))
        self._update_node_managed_by_host_definer_label(node_name, 'true')

    def remove_managed_by_host_definer_label(self, node_name):
        if self.is_dynamic_node_labeling_allowed():
            logger.info('Remove {} label from node {}'.format(settings.MANAGED_BY_HOST_DEFINER_LABEL, node_name))
            self._update_node_managed_by_host_definer_label(node_name, None)

    def define_host_on_all_storages_from_secrets(self, node_name):
        for secret_id, storage_classes_using_this_secret in SECRET_IDS.items():
            if storage_classes_using_this_secret == 0:
                continue
            host_definition = self._get_host_definition_from_secret_and_node_name(node_name, secret_id)
            if host_definition.management_address:
                self._verify_host_defined_and_has_host_definition(host_definition)

    def generate_secret_id_from_secret_and_namespace(self, secret_name, secret_namespace):
        return secret_name + ',' + secret_namespace
