from controllers.common.csi_logger import get_stdout_logger
from controllers.servers.utils import get_system_info_for_topologies
from controllers.servers.errors import ValidationException
import controllers.servers.host_definer.messages as messages
from controllers.servers.host_definer.globals import NODES
from controllers.servers.host_definer.k8s.api import K8SApi
from controllers.servers.host_definer.k8s.manager import K8SManager
from controllers.servers.host_definer import settings
from controllers.servers.host_definer.utils import utils
from controllers.servers.host_definer.resource_manager.host_definition import HostDefinitionManager
from controllers.servers.host_definer.resource_manager.secret import SecretManager
from controllers.servers.host_definer.types import ManagedNode
from controllers.servers.host_definer.definition_manager.definition import DefinitionManager

logger = get_stdout_logger()


class Watcher():
    def __init__(self):
        super().__init__()
        self.k8s_api = K8SApi()
        self.k8s_manager = K8SManager()
        self.host_definition_manager = HostDefinitionManager()
        self.secret_manager = SecretManager()
        self.definition_manager = DefinitionManager()

    def _is_node_can_be_defined(self, node_name):
        return utils.is_dynamic_node_labeling_allowed() or self._is_node_has_manage_node_label(node_name)

    def _is_node_can_be_undefined(self, node_name):
        return utils.is_host_definer_can_delete_hosts() and \
            self._is_node_has_manage_node_label(node_name) and \
            not self._is_node_has_forbid_deletion_label(node_name)

    def _is_node_has_manage_node_label(self, node_name):
        return self._is_node_has_label_in_true(node_name, settings.MANAGE_NODE_LABEL)

    def _is_node_has_forbid_deletion_label(self, node_name):
        return self._is_node_has_label_in_true(node_name, settings.FORBID_DELETION_LABEL)

    def _is_node_has_label_in_true(self, node_name, label):
        node_info = self.k8s_manager.get_node_info(node_name)
        return node_info.labels.get(label) == settings.TRUE_STRING

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
        host_definitions_info = self.host_definition_manager.get_all_host_definitions_info_of_the_node(node_name)
        return host_definitions_info != []

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
