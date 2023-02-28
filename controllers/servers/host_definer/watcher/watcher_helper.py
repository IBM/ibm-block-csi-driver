from controllers.common.csi_logger import get_stdout_logger
from controllers.servers.settings import SECRET_SUPPORTED_TOPOLOGIES_PARAMETER
from controllers.servers.utils import get_system_info_for_topologies
from controllers.servers.errors import ValidationException
import controllers.servers.host_definer.messages as messages
from controllers.servers.host_definer.globals import MANAGED_SECRETS, NODES
from controllers.servers.host_definer.k8s.api import K8SApi
from controllers.servers.host_definer.k8s.manager import K8SManager
from controllers.servers.host_definer import settings
from controllers.servers.host_definer.utils import utils
from controllers.servers.host_definer.resource_manager.host_definition import HostDefinitionManager
from controllers.servers.host_definer.resource_manager.secret import SecretManager
from controllers.servers.host_definer.types import DefineHostRequest, DefineHostResponse, ManagedNode
from controllers.servers.host_definer.storage_manager.host_definer_server import HostDefinerServicer

logger = get_stdout_logger()


class Watcher():
    def __init__(self):
        super().__init__()
        self.storage_host_servicer = HostDefinerServicer()
        self.k8s_api = K8SApi()
        self.k8s_manager = K8SManager()
        self.host_definition_manager = HostDefinitionManager()
        self.secret_manager = SecretManager()

    def _define_host_on_all_storages(self, node_name):
        logger.info(messages.DEFINE_NODE_ON_ALL_MANAGED_SECRETS.format(node_name))
        for secret_info in MANAGED_SECRETS:
            if secret_info.managed_storage_classes == 0:
                continue
            host_definition_info = self.host_definition_manager.get_host_definition_info_from_secret_and_node_name(
                node_name, secret_info)
            self._create_definition(host_definition_info)

    def _define_nodes(self, host_definition_info):
        for node_name, _ in NODES.items():
            host_definition_info = self.host_definition_manager.add_name_to_host_definition_info(
                node_name, host_definition_info)
            self._create_definition(host_definition_info)

    def _create_definition(self, host_definition_info):
        if not self.secret_manager.is_node_should_be_managed_on_secret(
                host_definition_info.node_name, host_definition_info.secret_name,
                host_definition_info.secret_namespace):
            return
        host_definition_info = self.host_definition_manager.update_host_definition_info(host_definition_info)
        response = self._define_host(host_definition_info)
        current_host_definition_info_on_cluster = self.host_definition_manager.create_host_definition_if_not_exist(
            host_definition_info, response)
        self.host_definition_manager.set_status_to_host_definition_after_definition(
            response.error_message, current_host_definition_info_on_cluster)

    def _define_host(self, host_definition_info):
        logger.info(messages.DEFINE_NODE_ON_SECRET.format(host_definition_info.node_name,
                    host_definition_info.secret_name, host_definition_info.secret_namespace))
        return self._ensure_definition_state(host_definition_info, self.storage_host_servicer.define_host)

    def _delete_definition(self, host_definition_info):
        response = DefineHostResponse()
        if self.secret_manager.is_node_should_be_managed_on_secret(
                host_definition_info.node_name, host_definition_info.secret_name,
                host_definition_info.secret_namespace):
            response = self._undefine_host(host_definition_info)
        self.host_definition_manager.handle_k8s_host_definition_after_undefine_action_if_exist(host_definition_info,
                                                                                               response)

    def _undefine_host(self, host_definition_info):
        logger.info(messages.UNDEFINED_HOST.format(host_definition_info.node_name,
                    host_definition_info.secret_name, host_definition_info.secret_namespace))
        return self._ensure_definition_state(host_definition_info, self.storage_host_servicer.undefine_host)

    def _is_host_can_be_defined(self, node_name):
        return utils.is_dynamic_node_labeling_allowed() or self._is_node_has_manage_node_label(node_name)

    def _is_host_can_be_undefined(self, node_name):
        return utils.is_host_definer_can_delete_hosts() and \
            self._is_node_has_manage_node_label(node_name) and \
            not self._is_node_has_forbid_deletion_label(node_name)

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
        secret_data = self.secret_manager.get_secret_data(secret_name, secret_namespace)
        if secret_data:
            node_topology_labels = self.secret_manager.get_topology_labels(labels)
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
        return utils.is_dynamic_node_labeling_allowed() and \
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
            host_definition_info = self.host_definition_manager.generate_host_definition_info(k8s_host_definition)
            if host_definition_info.node_name == node_name:
                node_host_definitions_info.append(host_definition_info)
        return node_host_definitions_info

    def _generate_nodes_with_system_id(self, secret_data):
        nodes_with_system_id = {}
        secret_config = utils.get_secret_config(secret_data)
        nodes_info = self.k8s_manager.get_nodes_info()
        for node_info in nodes_info:
            nodes_with_system_id[node_info.name] = self._get_system_id_for_node(node_info, secret_config)
        return nodes_with_system_id

    def _get_system_id_for_node(self, node_info, secret_config):
        node_topology_labels = self.secret_manager.get_topology_labels(node_info.labels)
        try:
            _, system_id = get_system_info_for_topologies(secret_config, node_topology_labels)
        except ValidationException:
            return ''
        return system_id

    def _generate_secret_system_ids_topologies(self, secret_data):
        system_ids_topologies = {}
        secret_config = utils.get_secret_config(secret_data)
        for system_id, system_info in secret_config.items():
            system_ids_topologies[system_id] = (system_info.get(SECRET_SUPPORTED_TOPOLOGIES_PARAMETER))
        return system_ids_topologies
