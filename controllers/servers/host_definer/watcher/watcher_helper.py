import base64
import os
import random
import string
from munch import Munch

from controllers.common.csi_logger import get_stdout_logger
from controllers.servers.settings import (SECRET_ARRAY_PARAMETER,
                                          SECRET_PASSWORD_PARAMETER,
                                          SECRET_USERNAME_PARAMETER)
import controllers.servers.messages as common_messages
from controllers.servers.utils import get_array_connection_info_from_secrets
from controllers.servers.errors import ValidationException
import controllers.servers.host_definer.messages as messages
from controllers.servers.host_definer.kubernetes_manager.manager import KubernetesManager
from controllers.servers.host_definer import settings
from controllers.servers.host_definer.types import DefineHostRequest, DefineHostResponse, HostDefinitionInfo, SecretInfo
from controllers.servers.host_definer.storage_manager.host_definer_server import HostDefinerServicer

SECRET_IDS = {}
NODES = {}
logger = get_stdout_logger()


class Watcher(KubernetesManager):
    def __init__(self):
        super().__init__()
        self.storage_host_servicer = HostDefinerServicer()

    def _define_nodes(self, host_definition_info):
        for node_name, _ in NODES.items():
            host_definition_info = self._add_name_to_host_definition_info(node_name, host_definition_info)
            self._create_definition(host_definition_info)

    def _create_definition(self, host_definition_info):
        secret_name = host_definition_info.secret_name
        secret_namespace = host_definition_info.secret_namespace
        if self._is_host_defined(host_definition_info.node_name, secret_name, secret_namespace):
            logger.info(messages.HOST_ALREADY_ON_STORAGE_HOST_DEFINITION_READY.format(
                host_definition_info.node_name, secret_name, secret_namespace, host_definition_info.name))
            return
        response = self._define_host(host_definition_info)
        current_host_definition_info_on_cluster = self._create_host_definition_if_not_exist(host_definition_info)
        self._set_status_to_host_definition_after_definition(
            response.error_message, current_host_definition_info_on_cluster)

    def _is_host_defined(self, node_name, secret_name, secret_namespace):
        host_definition_info = self._get_matching_host_definition_info(node_name, secret_name, secret_namespace)
        return self._is_host_definition_in_ready_state(host_definition_info)

    def _is_host_definition_in_ready_state(self, host_definition_info):
        if host_definition_info:
            return host_definition_info.phase == settings.READY_PHASE
        return False

    def _define_host(self, host_definition_info):
        return self._ensure_definition_state(host_definition_info, self.storage_host_servicer.define_host)

    def _create_host_definition_if_not_exist(self, host_definition_info):
        host_definition_manifest = self._get_host_definition_manifest(host_definition_info)
        current_host_definition_info_on_cluster = self._get_matching_host_definition_info(
            host_definition_info.node_name, host_definition_info.secret_name, host_definition_info.secret_namespace)
        if current_host_definition_info_on_cluster:
            host_definition_manifest[settings.METADATA][settings.NAME] = current_host_definition_info_on_cluster.name
            _ = self._patch_host_definition(host_definition_manifest)
            return current_host_definition_info_on_cluster
        else:
            logger.info(messages.CREATING_NEW_HOST_DEFINITION.format(host_definition_info.name))
            return self._create_host_definition(host_definition_manifest)

    def _get_host_definition_manifest(self, host_definition_info):
        return {
            settings.API_VERSION: settings.CSI_IBM_API_VERSION,
            settings.KIND: settings.HOST_DEFINITION_KIND,
            settings.METADATA: {
                settings.NAME: host_definition_info.name,
            },
            settings.SPEC: {
                settings.HOST_DEFINITION_FIELD: {
                    settings.NODE_NAME_FIELD: host_definition_info.node_name,
                    settings.NODE_ID_FIELD: host_definition_info.node_id,
                    settings.SECRET_NAME_FIELD: host_definition_info.secret_name,
                    settings.SECRET_NAMESPACE_FIELD: host_definition_info.secret_namespace,
                },
            },
        }

    def _set_status_to_host_definition_after_definition(self, message_from_storage, host_definition_info):
        if message_from_storage and host_definition_info:
            self._set_host_definition_status(host_definition_info.name,
                                             settings.PENDING_CREATION_PHASE)
            self._create_k8s_event_for_host_definition(
                host_definition_info, message_from_storage, settings.DEFINE_ACTION, settings.FAILED_MESSAGE_TYPE)
        elif host_definition_info:
            self._set_host_definition_status_to_ready(host_definition_info)

    def _get_prefix(self):
        return os.getenv(settings.PREFIX_ENV_VAR)

    def _get_connectivity(self):
        return os.getenv(settings.CONNECTIVITY_ENV_VAR)

    def _delete_definition(self, host_definition_info):
        node_name = host_definition_info.node_name
        logger.info(messages.UNDEFINED_HOST.format(
            node_name, host_definition_info.secret_name, host_definition_info.secret_namespace))
        response = self._undefine_host(host_definition_info)
        current_host_definition_info_on_cluster = self._get_matching_host_definition_info(
            host_definition_info.node_name, host_definition_info.secret_name, host_definition_info.secret_namespace)
        self._handle_k8s_host_definition_after_undefine_action(
            response.error_message, current_host_definition_info_on_cluster)

    def _undefine_host(self, host_definition_info):
        return self._ensure_definition_state(host_definition_info, self.storage_host_servicer.undefine_host)

    def _handle_k8s_host_definition_after_undefine_action(self, message_from_storage, host_definition_info):
        if message_from_storage and host_definition_info:
            self._set_host_definition_status(host_definition_info.name,
                                             settings.PENDING_DELETION_PHASE)
            self._create_k8s_event_for_host_definition(
                host_definition_info, message_from_storage,
                settings.UNDEFINE_ACTION, settings.FAILED_MESSAGE_TYPE)
        elif host_definition_info:
            self._delete_host_definition(host_definition_info.name)

    def _set_host_definition_status_to_ready(self, host_definition):
        self._set_host_definition_status(host_definition.name, settings.READY_PHASE)
        self._create_k8s_event_for_host_definition(
            host_definition, settings.SUCCESS_MESSAGE, settings.DEFINE_ACTION, settings.SUCCESSFUL_MESSAGE_TYPE)

    def _create_k8s_event_for_host_definition(self, host_definition_info, message, action, message_type):
        logger.info(messages.CREATE_EVENT_FOR_HOST_DEFINITION.format(message, host_definition_info.name))
        k8s_event = self._generate_k8s_event(host_definition_info, message, action, message_type)
        self._create_k8s_event(settings.DEFAULT_NAMESPACE, k8s_event)

    def _is_host_can_be_defined(self, node_name):
        return self._is_dynamic_node_labeling_allowed() or self._is_node_has_manage_node_label(node_name)

    def _is_dynamic_node_labeling_allowed(self):
        return os.getenv(settings.DYNAMIC_NODE_LABELING_ENV_VAR) == settings.TRUE_STRING

    def _is_host_can_be_undefined(self, node_name):
        return self._is_host_definer_can_delete_hosts() and \
            self._is_node_has_manage_node_label(node_name) and \
            not self._is_node_has_forbid_deletion_label(node_name)

    def _is_host_definer_can_delete_hosts(self):
        return os.getenv(settings.ALLOW_DELETE_ENV_VAR) == settings.TRUE_STRING

    def _is_node_has_manage_node_label(self, node_name):
        return self._is_host_has_label_in_true(node_name, settings.MANAGE_NODE_LABEL)

    def _is_node_has_forbid_deletion_label(self, node_name):
        return self._is_host_has_label_in_true(node_name, settings.FORBID_DELETION_LABEL)

    def _is_host_has_label_in_true(self, node_name, label):
        k8s_node = self._read_node(node_name)
        if not k8s_node:
            return False
        return k8s_node.metadata.labels.get(label) == settings.TRUE_STRING

    def _ensure_definition_state(self, host_definition_info, define_function):
        request = self._get_request_from_host_definition(host_definition_info)
        if not request:
            response = DefineHostResponse()
            response.error_message = messages.FAILED_TO_GET_SECRET_EVENT.format(
                host_definition_info.secret_name, host_definition_info.secret_namespace)
            return response
        return define_function(request)

    def _get_request_from_host_definition(self, host_definition_info):
        request = self._get_request_from_secret(host_definition_info.secret_name, host_definition_info.secret_namespace)
        if request:
            request.node_id = host_definition_info.node_id
        return request

    def _get_request_from_secret(self, secret_name, secret_namespace):
        request = self._get_new_request()
        request.array_connection_info = self._get_array_connection_info_from_secret(secret_name, secret_namespace)
        if request.array_connection_info:
            return request
        return None

    def _get_new_request(self):
        request = DefineHostRequest()
        request.prefix = self._get_prefix()
        request.connectivity_type = self._get_connectivity()
        return request

    def _get_host_definition_info_from_secret_and_node_name(self, node_name, secret_id):
        secret_info = self._generate_secret_info_from_id(secret_id)
        host_definition_info = self._get_host_definition_info_from_secret(secret_info)
        host_definition_info = self._add_name_to_host_definition_info(node_name, host_definition_info)
        return host_definition_info

    def _get_host_definition_info_from_secret(self, secret_info):
        host_definition_info = HostDefinitionInfo()
        host_definition_info.secret_name = secret_info.name
        host_definition_info.secret_namespace = secret_info.namespace
        return host_definition_info

    def _get_array_connection_info_from_secret(self, secret_name, secret_namespace):
        secret_data = self._get_data_from_secret(secret_name, secret_namespace)
        return self._get_array_connection_info_from_secret_data(secret_data)

    def _get_array_connection_info_from_secret_data(self, secret_data):
        try:
            system_info = self._get_system_info(secret_data)
            if system_info:
                return get_array_connection_info_from_secrets(system_info)
        except KeyError:
            logger.error(common_messages.INVALID_SECRET_CONFIG_MESSAGE)
        except ValidationException as ex:
            logger.error(str(ex))
        except TypeError as ex:
            logger.error(str(ex))
        return None

    def _get_system_info(self, secret_data):
        if not secret_data:
            return None

        return {
            SECRET_ARRAY_PARAMETER: self._decode_base64_to_string(secret_data[SECRET_ARRAY_PARAMETER]),
            SECRET_USERNAME_PARAMETER: self._decode_base64_to_string(secret_data[SECRET_USERNAME_PARAMETER]),
            SECRET_PASSWORD_PARAMETER: self._decode_base64_to_string(secret_data[SECRET_PASSWORD_PARAMETER])
        }

    def _decode_base64_to_string(self, content_with_base64):
        if not self._is_base64(content_with_base64):
            return content_with_base64
        base64_bytes = content_with_base64.encode('ascii')
        decoded_string_in_bytes = base64.b64decode(base64_bytes)
        return decoded_string_in_bytes.decode('ascii')

    def _is_base64(self, content_with_base64):
        try:
            if isinstance(content_with_base64, str):
                string_in_bytes = bytes(content_with_base64, 'ascii')
            else:
                raise TypeError(messages.INVALID_SECRET_CONTENT_TYPE.format(
                    content_with_base64, type(content_with_base64)))
            return base64.b64encode(base64.b64decode(string_in_bytes)) == string_in_bytes
        except Exception:
            return False

    def _add_name_to_host_definition_info(self, node_name, host_definition_info):
        host_definition_info.node_name = node_name
        host_definition_info.node_id = NODES[node_name]
        host_definition_info.name = self._get_host_definition_name(node_name)
        return host_definition_info

    def _get_host_definition_name(self, node_name):
        return '{0}-{1}'.format(node_name, self._get_random_string()).replace('_', '.')

    def _get_random_string(self):
        return ''.join(random.choices(string.ascii_lowercase + string.digits, k=20))

    def _generate_secret_info_from_id(self, secret_id):
        secret_info = SecretInfo()
        secret_info.name, secret_info.namespace = secret_id
        return secret_info

    def _add_node_to_nodes(self, csi_node_info):
        logger.info(messages.NEW_KUBERNETES_NODE.format(csi_node_info.name))
        self._add_manage_node_label_to_node(csi_node_info.name)
        NODES[csi_node_info.name] = csi_node_info.node_id

    def _add_manage_node_label_to_node(self, node_name):
        if self._is_node_has_manage_node_label(node_name):
            return
        logger.info(messages.ADD_LABEL_TO_NODE.format(settings.MANAGE_NODE_LABEL, node_name))
        self._update_manage_node_label(node_name, settings.TRUE_STRING)

    def _remove_manage_node_label(self, node_name):
        if self._is_managed_by_host_definer_label_should_be_removed(node_name):
            logger.info(messages.REMOVE_LABEL_FROM_NODE.format(settings.MANAGE_NODE_LABEL, node_name))
            self._update_manage_node_label(node_name, None)

    def _is_managed_by_host_definer_label_should_be_removed(self, node_name):
        return self._is_dynamic_node_labeling_allowed() and \
            not self._is_node_has_ibm_block_csi(node_name) and \
            not self._is_node_has_host_definitions(node_name)

    def _is_node_has_ibm_block_csi(self, node_name):
        csi_node_info = self._get_csi_node_info(node_name)
        return csi_node_info.node_id != ''

    def _is_node_has_host_definitions(self, node_name):
        host_definitions_info = self._get_all_node_host_definitions_info(node_name)
        return host_definitions_info != []

    def _get_all_node_host_definitions_info(self, node_name):
        node_host_definitions_info = []
        k8s_host_definitions = self._get_k8s_host_definitions()
        for k8s_host_definition in k8s_host_definitions:
            host_definition_info = self._generate_host_definition_info(k8s_host_definition)
            if host_definition_info.node_name == node_name:
                node_host_definitions_info.append(host_definition_info)
        return node_host_definitions_info

    def _define_host_on_all_storages(self, node_name):
        for secret_id, storage_classes_using_this_secret in SECRET_IDS.items():
            if storage_classes_using_this_secret == 0:
                continue
            host_definition_info = self._get_host_definition_info_from_secret_and_node_name(node_name, secret_id)
            self._create_definition(host_definition_info)

    def _generate_secret_id(self, secret_name, secret_namespace):
        return (secret_name, secret_namespace)

    def _munch(self, watch_event):
        return Munch.fromDict(watch_event)

    def _loop_forever(self):
        return True