import os
import random
import string

from controllers.common.csi_logger import get_stdout_logger
from controllers.servers.settings import SECRET_SUPPORTED_TOPOLOGIES_PARAMETER
from controllers.servers.utils import get_system_info_for_topologies
from controllers.servers.errors import ValidationException
import controllers.servers.host_definer.messages as messages
from controllers.servers.host_definer.k8s.api import K8SApi
from controllers.servers.host_definer.k8s.manager import K8SManager
from controllers.servers.host_definer import settings
from controllers.servers.host_definer.utils import utils
from controllers.servers.host_definer.utils import manifest_utils
import controllers.common.settings as common_settings
from controllers.servers.host_definer.types import (
    DefineHostRequest, DefineHostResponse, HostDefinitionInfo, SecretInfo, ManagedNode)
from controllers.servers.host_definer.storage_manager.host_definer_server import HostDefinerServicer

MANAGED_SECRETS = []
NODES = {}
logger = get_stdout_logger()


class Watcher():
    def __init__(self):
        super().__init__()
        self.storage_host_servicer = HostDefinerServicer()
        self.k8s_api = K8SApi()
        self.k8s_manager = K8SManager()

    def _define_host_on_all_storages(self, node_name):
        logger.info(messages.DEFINE_NODE_ON_ALL_MANAGED_SECRETS.format(node_name))
        for secret_info in MANAGED_SECRETS:
            if secret_info.managed_storage_classes == 0:
                continue
            host_definition_info = self._get_host_definition_info_from_secret_and_node_name(node_name, secret_info)
            self._create_definition(host_definition_info)

    def _get_host_definition_info_from_secret_and_node_name(self, node_name, secret_info):
        host_definition_info = self._get_host_definition_info_from_secret(secret_info)
        host_definition_info = self._add_name_to_host_definition_info(node_name, host_definition_info)
        return host_definition_info

    def _get_host_definition_info_from_secret(self, secret_info):
        host_definition_info = HostDefinitionInfo()
        host_definition_info.secret_name = secret_info.name
        host_definition_info.secret_namespace = secret_info.namespace
        return host_definition_info

    def _define_nodes(self, host_definition_info):
        for node_name, _ in NODES.items():
            host_definition_info = self._add_name_to_host_definition_info(node_name, host_definition_info)
            self._create_definition(host_definition_info)

    def _add_name_to_host_definition_info(self, node_name, host_definition_info):
        host_definition_info.node_name = node_name
        host_definition_info.node_id = NODES[node_name].node_id
        host_definition_info.name = self._get_host_definition_name(node_name)
        return host_definition_info

    def _create_definition(self, host_definition_info):
        if not self._is_node_should_be_managed_on_secret(
                host_definition_info.node_name, host_definition_info.secret_name,
                host_definition_info.secret_namespace):
            return
        host_definition_info = self._update_host_definition_info(host_definition_info)
        response = self._define_host(host_definition_info)
        current_host_definition_info_on_cluster = self._create_host_definition_if_not_exist(
            host_definition_info, response)
        self._set_status_to_host_definition_after_definition(
            response.error_message, current_host_definition_info_on_cluster)

    def _update_host_definition_info(self, host_definition_info):
        host_definition_info_on_cluster = self.k8s_manager.get_matching_host_definition_info(
            host_definition_info.node_name, host_definition_info.secret_name, host_definition_info.secret_namespace)
        if host_definition_info_on_cluster:
            host_definition_info.connectivity_type = host_definition_info_on_cluster.connectivity_type
            host_definition_info.node_id = host_definition_info_on_cluster.node_id
        return host_definition_info

    def _define_host(self, host_definition_info):
        logger.info(messages.DEFINE_NODE_ON_SECRET.format(host_definition_info.node_name,
                    host_definition_info.secret_name, host_definition_info.secret_namespace))
        return self._ensure_definition_state(host_definition_info, self.storage_host_servicer.define_host)

    def _create_host_definition_if_not_exist(self, host_definition_info, response):
        node_id = NODES[host_definition_info.node_name].node_id
        host_definition_manifest = manifest_utils.get_host_definition_manifest(host_definition_info, response, node_id)
        current_host_definition_info_on_cluster = self.k8s_manager.get_matching_host_definition_info(
            host_definition_info.node_name, host_definition_info.secret_name, host_definition_info.secret_namespace)
        if current_host_definition_info_on_cluster:
            host_definition_manifest[settings.METADATA][
                common_settings.NAME_FIELD] = current_host_definition_info_on_cluster.name
            self.k8s_api.patch_host_definition(host_definition_manifest)
            return current_host_definition_info_on_cluster
        else:
            logger.info(messages.CREATING_NEW_HOST_DEFINITION.format(host_definition_info.name))
            return self.k8s_manager.create_host_definition(host_definition_manifest)

    def _set_status_to_host_definition_after_definition(self, message_from_storage, host_definition_info):
        if message_from_storage and host_definition_info:
            self.k8s_manager.set_host_definition_status(host_definition_info.name,
                                                        settings.PENDING_CREATION_PHASE)
            self._create_k8s_event_for_host_definition(
                host_definition_info, message_from_storage, settings.DEFINE_ACTION, settings.FAILED_MESSAGE_TYPE)
        elif host_definition_info:
            self._set_host_definition_status_to_ready(host_definition_info)

    def _delete_definition(self, host_definition_info):
        response = DefineHostResponse()
        if self._is_node_should_be_managed_on_secret(host_definition_info.node_name, host_definition_info.secret_name,
                                                     host_definition_info.secret_namespace):
            response = self._undefine_host(host_definition_info)
        self._handle_k8s_host_definition_after_undefine_action_if_exist(host_definition_info, response)

    def _is_node_should_be_managed_on_secret(self, node_name, secret_name, secret_namespace):
        logger.info(messages.CHECK_NODE_SHOULD_BE_MANAGED_BY_SECRET.format(node_name, secret_name, secret_namespace))
        secret_data = self.k8s_manager.get_secret_data(secret_name, secret_namespace)
        utils.validate_secret(secret_data)
        managed_secret_info, _ = self._get_managed_secret_by_name_and_namespace(secret_name, secret_namespace)
        if self._is_node_should_managed_on_secret_info(node_name, managed_secret_info):
            logger.info(messages.NODE_SHOULD_BE_MANAGED_ON_SECRET.format(node_name, secret_name, secret_namespace))
            return True
        logger.info(messages.NODE_SHOULD_NOT_BE_MANAGED_ON_SECRET.format(node_name, secret_name, secret_namespace))
        return False

    def _get_managed_secret_by_name_and_namespace(self, secret_name, secret_namespace):
        secret_info = self._generate_secret_info(secret_name, secret_namespace)
        managed_secret_info, index = self._get_matching_managed_secret_info(secret_info)
        return managed_secret_info, index

    def _is_node_should_managed_on_secret_info(self, node_name, secret_info):
        if secret_info:
            nodes_with_system_id = secret_info.nodes_with_system_id
            if nodes_with_system_id and nodes_with_system_id.get(node_name):
                return True
            if nodes_with_system_id:
                return False
            return True
        return False

    def _is_topology_secret(self, secret_data):
        utils.validate_secret(secret_data)
        if utils.get_secret_config(secret_data):
            return True
        return False

    def _undefine_host(self, host_definition_info):
        logger.info(messages.UNDEFINED_HOST.format(host_definition_info.node_name,
                    host_definition_info.secret_name, host_definition_info.secret_namespace))
        return self._ensure_definition_state(host_definition_info, self.storage_host_servicer.undefine_host)

    def _handle_k8s_host_definition_after_undefine_action_if_exist(self, host_definition_info, response):
        current_host_definition_info_on_cluster = self.k8s_manager.get_matching_host_definition_info(
            host_definition_info.node_name, host_definition_info.secret_name, host_definition_info.secret_namespace)
        if current_host_definition_info_on_cluster:
            self._handle_k8s_host_definition_after_undefine_action(
                response.error_message, current_host_definition_info_on_cluster)

    def _handle_k8s_host_definition_after_undefine_action(self, message_from_storage, host_definition_info):
        if message_from_storage and host_definition_info:
            self.k8s_manager.set_host_definition_status(host_definition_info.name,
                                                        settings.PENDING_DELETION_PHASE)
            self._create_k8s_event_for_host_definition(
                host_definition_info, message_from_storage,
                settings.UNDEFINE_ACTION, settings.FAILED_MESSAGE_TYPE)
        elif host_definition_info:
            self.k8s_manager.delete_host_definition(host_definition_info.name)

    def _set_host_definition_status_to_ready(self, host_definition):
        self.k8s_manager.set_host_definition_status(host_definition.name, settings.READY_PHASE)
        self._create_k8s_event_for_host_definition(
            host_definition, settings.SUCCESS_MESSAGE, settings.DEFINE_ACTION, settings.SUCCESSFUL_MESSAGE_TYPE)

    def _create_k8s_event_for_host_definition(self, host_definition_info, message, action, message_type):
        logger.info(messages.CREATE_EVENT_FOR_HOST_DEFINITION.format(message, host_definition_info.name))
        k8s_event = self.k8s_manager.generate_k8s_event(host_definition_info, message, action, message_type)
        self.k8s_api.create_event(settings.DEFAULT_NAMESPACE, k8s_event)

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
        node_info = self.k8s_manager.get_node_info(node_name)
        return self._get_label_value(node_info.labels, label) == settings.TRUE_STRING

    def _ensure_definition_state(self, host_definition_info, define_function):
        request = self._get_request_from_host_definition(host_definition_info)
        if not request:
            response = DefineHostResponse()
            response.error_message = messages.FAILED_TO_GET_SECRET_EVENT.format(
                host_definition_info.secret_name, host_definition_info.secret_namespace)
            return response
        return define_function(request)

    def _get_request_from_host_definition(self, host_definition_info):
        node_name = host_definition_info.node_name
        logger.info(messages.GENERATE_REQUEST_FOR_NODE.format(node_name))
        node_info = self.k8s_manager.get_node_info(node_name)
        request = self._get_new_request(node_info.labels)
        request = self._add_array_connectivity_info_to_request(
            request, host_definition_info.secret_name, host_definition_info.secret_namespace, node_info.labels)
        if request:
            request.node_id_from_host_definition = host_definition_info.node_id
            request.node_id_from_csi_node = self._get_node_id_by_node(host_definition_info)
            request.io_group = self._get_io_group_by_node(host_definition_info.node_name)
        return request

    def _get_new_request(self, labels):
        request = DefineHostRequest()
        connectivity_type_label_on_node = self._get_label_value(labels, settings.CONNECTIVITY_TYPE_LABEL)
        request.prefix = utils.get_prefix()
        request.connectivity_type_from_user = utils.get_connectivity_type_from_user(connectivity_type_label_on_node)
        return request

    def _get_label_value(self, labels, label):
        return labels.get(label)

    def _add_array_connectivity_info_to_request(self, request, secret_name, secret_namespace, labels):
        request.array_connection_info = self._get_array_connection_info_from_secret(
            secret_name, secret_namespace, labels)
        if request.array_connection_info:
            return request
        return None

    def _get_array_connection_info_from_secret(self, secret_name, secret_namespace, labels):
        secret_data = self.k8s_manager.get_secret_data(secret_name, secret_namespace)
        if secret_data:
            node_topology_labels = self._get_topology_labels(labels)
            return utils.get_array_connection_info_from_secret_data(secret_data, node_topology_labels)
        return {}

    def _get_node_id_by_node(self, host_definition_info):
        try:
            return NODES[host_definition_info.node_name].node_id
        except Exception:
            return host_definition_info.node_id

    def _get_io_group_by_node(self, node_name):
        try:
            return NODES[node_name].io_group
        except Exception:
            return ''

    def _get_host_definition_name(self, node_name):
        return '{0}-{1}'.format(node_name, self._get_random_string()).replace('_', '.')

    def _get_random_string(self):
        return ''.join(random.choices(string.ascii_lowercase + string.digits, k=20))

    def _add_node_to_nodes(self, csi_node_info):
        logger.info(messages.NEW_KUBERNETES_NODE.format(csi_node_info.name))
        self._add_manage_node_label_to_node(csi_node_info.name)
        NODES[csi_node_info.name] = self._generate_managed_node(csi_node_info)

    def _add_manage_node_label_to_node(self, node_name):
        if self._is_node_has_manage_node_label(node_name):
            return
        logger.info(messages.ADD_LABEL_TO_NODE.format(settings.MANAGE_NODE_LABEL, node_name))
        self.k8s_manager.update_manage_node_label(node_name, settings.TRUE_STRING)

    def _generate_managed_node(self, csi_node_info):
        node_info = self.k8s_manager.get_node_info(csi_node_info.name)
        return ManagedNode(csi_node_info, node_info.labels)

    def _remove_manage_node_label(self, node_name):
        if self._is_managed_by_host_definer_label_should_be_removed(node_name):
            logger.info(messages.REMOVE_LABEL_FROM_NODE.format(settings.MANAGE_NODE_LABEL, node_name))
            self.k8s_manager.update_manage_node_label(node_name, None)

    def _is_managed_by_host_definer_label_should_be_removed(self, node_name):
        return self._is_dynamic_node_labeling_allowed() and \
            not self._is_node_has_ibm_block_csi(node_name) and \
            not self._is_node_has_host_definitions(node_name)

    def _is_node_has_ibm_block_csi(self, node_name):
        csi_node_info = self.k8s_manager.get_csi_node_info(node_name)
        return csi_node_info.node_id != ''

    def _is_node_has_host_definitions(self, node_name):
        host_definitions_info = self._get_all_node_host_definitions_info(node_name)
        return host_definitions_info != []

    def _get_all_node_host_definitions_info(self, node_name):
        node_host_definitions_info = []
        k8s_host_definitions = self.k8s_api.list_host_definition().items
        for k8s_host_definition in k8s_host_definitions:
            host_definition_info = self.k8s_manager.generate_host_definition_info(k8s_host_definition)
            if host_definition_info.node_name == node_name:
                node_host_definitions_info.append(host_definition_info)
        return node_host_definitions_info

    def _generate_secret_info(self, secret_name, secret_namespace, nodes_with_system_id={}, system_ids_topologies={}):
        return SecretInfo(secret_name, secret_namespace, nodes_with_system_id, system_ids_topologies)

    def _is_secret_managed(self, secret_info):
        _, index = self._get_matching_managed_secret_info(secret_info)
        if index != -1:
            return True
        return False

    def _get_matching_managed_secret_info(self, secret_info):
        for index, managed_secret_info in enumerate(MANAGED_SECRETS):
            if managed_secret_info.name == secret_info.name and managed_secret_info.namespace == secret_info.namespace:
                return managed_secret_info, index
        return secret_info, -1

    def _generate_nodes_with_system_id(self, secret_data):
        nodes_with_system_id = {}
        secret_config = utils.get_secret_config(secret_data)
        nodes_info = self.k8s_manager.get_nodes_info()
        for node_info in nodes_info:
            nodes_with_system_id[node_info.name] = self._get_system_id_for_node(node_info, secret_config)
        return nodes_with_system_id

    def _get_system_id_for_node(self, node_info, secret_config):
        node_topology_labels = self._get_topology_labels(node_info.labels)
        try:
            _, system_id = get_system_info_for_topologies(secret_config, node_topology_labels)
        except ValidationException:
            return ''
        return system_id

    def _get_topology_labels(self, labels):
        topology_labels = {}
        for label in labels:
            if utils.is_topology_label(label):
                topology_labels[label] = labels[label]
        return topology_labels

    def _generate_secret_system_ids_topologies(self, secret_data):
        system_ids_topologies = {}
        secret_config = utils.get_secret_config(secret_data)
        for system_id, system_info in secret_config.items():
            system_ids_topologies[system_id] = (system_info.get(SECRET_SUPPORTED_TOPOLOGIES_PARAMETER))
        return system_ids_topologies
