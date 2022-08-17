import base64
import os
import random
import string

from controllers.servers.config import (SECRET_ARRAY_PARAMETER,
                                        SECRET_PASSWORD_PARAMETER,
                                        SECRET_USERNAME_PARAMETER)
import controllers.servers.host_definer.messages as messages
import controllers.servers.messages as common_messages
from controllers.servers.host_definer.kubernetes_manager.manager import KubernetesManager
from controllers.common.csi_logger import get_stdout_logger
from controllers.servers.host_definer import settings
from controllers.servers.host_definer.types import DefineHostRequest, DefineHostResponse, HostDefinition, Secret
from controllers.servers.host_definer.storage_manager.host_definer_server import HostDefinerServicer

SECRET_IDS = {}
NODES = {}
logger = get_stdout_logger()


class Watcher(KubernetesManager):
    def __init__(self):
        super().__init__()
        self.storage_host_servicer = HostDefinerServicer()

    def _verify_nodes_defined(self, host_definition):
        for node_name, _ in NODES.items():
            host_definition = self._add_name_to_host_definition(node_name, host_definition)
            self._create_definition(host_definition)

    def _create_definition(self, host_definition):
        if self._is_host_defined(host_definition.node_name, host_definition.secret):
            logger.info(messages.HOST_ALREADY_ON_STORAGE_HOST_DEFINITION_READY.format(
                host_definition.node_name, host_definition.secret.name,
                host_definition.secret.namespace, host_definition.name))
            return
        response = self._define_host(host_definition)
        host_definition.name = self._create_host_definition_if_not_exist(host_definition)
        if response.error_message:
            self._set_host_definition_status(host_definition.name, settings.PENDING_CREATION_PHASE)
            self._create_event_to_host_definition(host_definition, response.error_message)
        else:
            self._set_host_definition_status(host_definition.name, settings.READY_PHASE)

    def _is_host_defined(self, node_name, secret):
        host_definition, _ = self._get_host_definition(node_name, secret)
        return self._is_host_definition_in_ready_state(host_definition)

    def _is_host_definition_in_ready_state(self, host_definition):
        if host_definition:
            return host_definition.phase == settings.READY_PHASE
        return False

    def _define_host(self, host_definition):
        return self._ensure_definition_state(host_definition, self.storage_host_servicer.define_host)

    def _create_host_definition_if_not_exist(self, host_definition):
        host_definition_manifest = self._get_host_definition_manifest(host_definition)
        host_definition_instance, _ = self._get_host_definition(host_definition.node_name, host_definition.secret)
        if host_definition_instance:
            host_definition_manifest[settings.METADATA][settings.NAME] = host_definition_instance.name
            self._patch_host_definition(host_definition_manifest)
        else:
            logger.info(messages.CREATING_NEW_HOST_DEFINITION.format(host_definition.name))
            self._create_host_definition(host_definition_manifest)
        return host_definition_manifest[settings.METADATA][settings.NAME]

    def _get_host_definition_manifest(self, host_definition):
        return {
            settings.API_VERSION: settings.CSI_IBM_API_VERSION,
            settings.KIND: settings.HOST_DEFINITION_KIND,
            settings.METADATA: {
                settings.NAME: host_definition.name,
            },
            settings.SPEC: {
                settings.HOST_DEFINITION_FIELD: {
                    settings.NODE_NAME_FIELD: host_definition.node_name,
                    settings.NODE_ID_FIELD: host_definition.node_id,
                    settings.SECRET_NAME_FIELD: host_definition.secret.name,
                    settings.SECRET_NAMESPACE_FIELD: host_definition.secret.namespace,
                },
            },
        }

    def _get_prefix(self):
        return os.getenv(settings.PREFIX_ENV_VAR)

    def _get_connectivity(self):
        return os.getenv(settings.CONNECTIVITY_ENV_VAR)

    def _delete_definition(self, host_definition):
        node_name = host_definition.node_name
        secret = host_definition.secret
        logger.info(messages.VERIFY_HOST_IS_UNDEFINED.format(node_name, secret.name, secret.namespace))
        response = self._undefine_host(host_definition)
        host_definition_instance, _ = self._get_host_definition(host_definition.node_name, host_definition.secret)
        if response.error_message and host_definition_instance:
            self._set_host_definition_status(host_definition_instance.name, settings.PENDING_DELETION_PHASE)
            self._create_event_to_host_definition(host_definition_instance, response.error_message)
        elif not response.error_message and host_definition_instance:
            self._delete_host_definition(host_definition_instance.name)
            self._remove_manage_node_label(node_name)

    def _undefine_host(self, host_definition):
        return self._ensure_definition_state(host_definition, self.storage_host_servicer.undefine_host)

    def _create_event_to_host_definition(self, host_definition, message):
        self._add_event_to_host_definition(host_definition, message)
        if host_definition:
            self._add_event_to_host_definition(host_definition, message)

    def _add_event_to_host_definition(self, host_definition, message):
        logger.info(messages.CREATE_EVENT_FOR_HOST_DEFINITION.format(host_definition.name, message))
        event = self._get_event_for_host_definition(host_definition, message)
        self._create_event(settings.DEFAULT_NAMESPACE, event)

    def _is_host_can_be_defined(self, node_name):
        if self._is_dynamic_node_labeling_allowed():
            return True
        return self._is_node_has_manage_node_label(node_name)

    def _is_dynamic_node_labeling_allowed(self):
        return os.getenv(settings.DYNAMIC_NODE_LABELING_ENV_VAR) == settings.TRUE_STRING

    def _is_host_can_be_undefined(self, node_name):
        if self._is_host_definer_can_delete_hosts():
            return self._is_node_has_manage_node_label(node_name) and \
                (not self._is_node_has_forbid_deletion_label(node_name))
        return False

    def _is_host_definer_can_delete_hosts(self):
        return os.getenv(settings.ALLOW_DELETE_ENV_VAR) == settings.TRUE_STRING

    def _is_node_has_manage_node_label(self, node_name):
        return self._is_host_has_label_in_true(node_name, settings.MANAGE_NODE_LABEL)

    def _is_node_has_forbid_deletion_label(self, node_name):
        return self._is_host_has_label_in_true(node_name, settings.FORBID_DELETION_LABEL)

    def _is_host_has_label_in_true(self, node_name, label):
        node = self._read_node(node_name)
        if not node:
            return False
        return node.metadata.labels.get(label) == settings.TRUE_STRING

    def _ensure_definition_state(self, host_definition, define_function):
        request = self._get_request_from_host_definition(host_definition)
        if not request:
            response = DefineHostResponse()
            response.error_message = 'Failed to get Secret'
            return response
        return define_function(request)

    def _get_request_from_host_definition(self, host_definition):
        request = self._get_request_from_secret(host_definition.secret)
        if request:
            request.node_id = host_definition.node_id
            request.node_name = host_definition.node_name
        return request

    def _get_request_from_secret(self, secret):
        request = self._get_new_request()
        request.system_info = self._get_system_info_from_secret(secret)
        if request.system_info:
            return request
        return None

    def _get_new_request(self):
        request = DefineHostRequest()
        request.prefix = self._get_prefix()
        request.connectivity_type = self._get_connectivity()
        return request

    def _get_host_definition_from_secret_and_node_name(self, node_name, secret_id):
        secret = self._get_secret_object_from_id(secret_id)
        host_definition = self._get_host_definition_from_secret(secret)
        host_definition = self._add_name_to_host_definition(node_name, host_definition)
        return host_definition

    def _get_host_definition_from_secret(self, secret):
        host_definition = HostDefinition()
        host_definition.secret = secret
        return host_definition

    def _get_system_info_from_secret(self, secret):
        secret_data = self._get_data_from_secret(secret)
        return self._get_system_info_from_secret_data(secret_data)

    def _get_system_info_from_secret_data(self, secret_data):
        try:
            return self._get_system_info(secret_data)
        except KeyError:
            logger.error(common_messages.INVALID_SECRET_CONFIG_MESSAGE)
            return ''

    def _get_system_info(self, secret_data):
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
        host_definition.name = self._get_host_definition_name(node_name)
        return host_definition

    def _get_host_definition_name(self, node_name):
        return '{0}.{1}'.format(node_name, self._get_random_string()).replace('_', '.')

    def _get_random_string(self):
        return ''.join(random.choices(string.ascii_lowercase + string.digits, k=20))

    def _get_secret_object_from_id(self, secret_id):
        secret = Secret()
        secret.name, secret.namespace = secret_id.split(',')
        return secret

    def _add_node_to_nodes(self, csi_node):
        logger.info(messages.NEW_KUBERNETES_NODE.format(csi_node.name))
        self._add_manage_node_label_to_node(csi_node.name)
        NODES[csi_node.name] = csi_node.node_id

    def _add_manage_node_label_to_node(self, node_name):
        if self._is_node_has_manage_node_label(node_name):
            return
        logger.info(messages.ADD_LABEL_TO_NODE.format(settings.MANAGE_NODE_LABEL, node_name))
        self._update_manage_node_label(node_name, settings.TRUE_STRING)

    def _remove_manage_node_label(self, node_name):
        if self._is_dynamic_node_labeling_allowed():
            logger.info(messages.REMOVE_LABEL_FROM_NODE.format(settings.MANAGE_NODE_LABEL, node_name))
            self._update_manage_node_label(node_name, None)

    def _define_host_on_all_storages_from_secrets(self, node_name):
        for secret_id, storage_classes_using_this_secret in SECRET_IDS.items():
            if storage_classes_using_this_secret == 0:
                continue
            host_definition = self._get_host_definition_from_secret_and_node_name(node_name, secret_id)
            self._create_definition(host_definition)

    def _generate_secret_id_from_secret_and_namespace(self, secret_name, secret_namespace):
        return secret_name + settings.SECRET_SEPARATOR + secret_namespace
