from controllers.common.csi_logger import get_stdout_logger
from controllers.servers.utils import get_system_info_for_topologies
from controllers.servers.errors import ValidationException
from controllers.servers.host_definer import settings
from controllers.servers.host_definer.globals import NODES, MANAGED_SECRETS
from controllers.servers.host_definer.types import ManagedNode
import controllers.servers.host_definer.messages as messages
from controllers.servers.host_definer.utils import manifest_utils
from controllers.servers.host_definer.utils import utils
from controllers.servers.host_definer.k8s.api import K8SApi
from controllers.servers.host_definer.resource_manager.secret import SecretManager
from controllers.servers.host_definer.resource_manager.host_definition import HostDefinitionManager
from controllers.servers.host_definer.definition_manager.definition import DefinitionManager
from controllers.servers.host_definer.resource_manager.resource_info import ResourceInfoManager

logger = get_stdout_logger()


class NodeManager:
    def __init__(self):
        self.k8s_api = K8SApi()
        self.secret_manager = SecretManager()
        self.host_definition_manager = HostDefinitionManager()
        self.definition_manager = DefinitionManager()
        self.resource_info_manager = ResourceInfoManager()

    def is_node_can_be_defined(self, node_name):
        return utils.is_dynamic_node_labeling_allowed() or self.is_node_has_manage_node_label(node_name)

    def is_node_can_be_undefined(self, node_name):
        return utils.is_host_definer_can_delete_hosts() and \
            self.is_node_has_manage_node_label(node_name) and \
            not self.is_node_has_forbid_deletion_label(node_name)

    def is_node_has_forbid_deletion_label(self, node_name):
        return self._is_node_has_label_in_true(node_name, settings.FORBID_DELETION_LABEL)

    def add_node_to_nodes(self, csi_node_info):
        logger.info(messages.NEW_KUBERNETES_NODE.format(csi_node_info.name))
        self._add_manage_node_label_to_node(csi_node_info.name)
        NODES[csi_node_info.name] = self.generate_managed_node(csi_node_info)

    def _add_manage_node_label_to_node(self, node_name):
        if self.is_node_has_manage_node_label(node_name):
            return
        logger.info(messages.ADD_LABEL_TO_NODE.format(settings.MANAGE_NODE_LABEL, node_name))
        self._update_manage_node_label(node_name, settings.TRUE_STRING)

    def generate_managed_node(self, csi_node_info):
        node_info = self.resource_info_manager.get_node_info(csi_node_info.name)
        return ManagedNode(csi_node_info, node_info.labels)

    def remove_manage_node_label(self, node_name):
        if self._is_node_should_be_removed(node_name):
            logger.info(messages.REMOVE_LABEL_FROM_NODE.format(settings.MANAGE_NODE_LABEL, node_name))
            self._update_manage_node_label(node_name, None)

    def _is_node_should_be_removed(self, node_name):
        return utils.is_dynamic_node_labeling_allowed() and \
            not self._is_node_has_ibm_block_csi(node_name) and \
            not self.is_node_has_host_definitions(node_name)

    def _is_node_has_ibm_block_csi(self, node_name):
        csi_node_info = self.resource_info_manager.get_csi_node_info(node_name)
        return csi_node_info.node_id != ''

    def is_node_has_host_definitions(self, node_name):
        host_definitions_info = self.host_definition_manager.get_all_host_definitions_info_of_the_node(node_name)
        return host_definitions_info != []

    def _update_manage_node_label(self, node_name, label_value):
        body = manifest_utils.get_body_manifest_for_labels(label_value)
        self.k8s_api.patch_node(node_name, body)

    def generate_nodes_with_system_id(self, secret_data):
        nodes_with_system_id = {}
        secret_config = utils.get_secret_config(secret_data)
        nodes_info = self.get_nodes_info()
        for node_info in nodes_info:
            nodes_with_system_id[node_info.name] = self._get_system_id_for_node(node_info, secret_config)
        return nodes_with_system_id

    def get_nodes_info(self):
        nodes_info = []
        for k8s_node in self.k8s_api.list_node().items:
            k8s_node = self.resource_info_manager.generate_node_info(k8s_node)
            nodes_info.append(k8s_node)
        return nodes_info

    def _get_system_id_for_node(self, node_info, secret_config):
        node_topology_labels = self.secret_manager.get_topology_labels(node_info.labels)
        try:
            _, system_id = get_system_info_for_topologies(secret_config, node_topology_labels)
        except ValidationException:
            return ''
        return system_id

    def is_node_has_new_manage_node_label(self, csi_node_info, unmanaged_csi_nodes_with_driver):
        return not utils.is_dynamic_node_labeling_allowed() and \
            self.is_node_has_manage_node_label(csi_node_info.name) and \
            self._is_node_is_unmanaged_and_with_csi_node(csi_node_info, unmanaged_csi_nodes_with_driver)

    def is_node_has_manage_node_label(self, node_name):
        return self._is_node_has_label_in_true(node_name, settings.MANAGE_NODE_LABEL)

    def _is_node_has_label_in_true(self, node_name, label):
        node_info = self.resource_info_manager.get_node_info(node_name)
        return node_info.labels.get(label) == settings.TRUE_STRING

    def _is_node_is_unmanaged_and_with_csi_node(self, csi_node_info, unmanaged_csi_nodes_with_driver):
        if csi_node_info.name not in NODES and csi_node_info.node_id and \
                csi_node_info.name in unmanaged_csi_nodes_with_driver:
            return True
        return False

    def handle_node_topologies(self, node_info, watch_event_type):
        if node_info.name not in NODES or watch_event_type != settings.MODIFIED_EVENT:
            return
        for index, managed_secret_info in enumerate(MANAGED_SECRETS):
            if not managed_secret_info.system_ids_topologies:
                continue
            if self.secret_manager.is_node_should_managed_on_secret_info(node_info.name, managed_secret_info):
                self._remove_node_if_topology_not_match(node_info, index, managed_secret_info)
            elif self.secret_manager.is_node_labels_in_system_ids_topologies(managed_secret_info.system_ids_topologies,
                                                                             node_info.labels):
                self._define_host_with_new_topology(node_info, index, managed_secret_info)

    def _remove_node_if_topology_not_match(self, node_info, index, managed_secret_info):
        if not self.secret_manager.is_node_labels_in_system_ids_topologies(managed_secret_info.system_ids_topologies,
                                                                           node_info.labels):
            managed_secret_info.nodes_with_system_id.pop(node_info.name, None)
            MANAGED_SECRETS[index] = managed_secret_info

    def _define_host_with_new_topology(self, node_info, index, managed_secret_info):
        node_name = node_info.name
        system_id = self.secret_manager.get_system_id_for_node_labels(
            managed_secret_info.system_ids_topologies, node_info.labels)
        managed_secret_info.nodes_with_system_id[node_name] = system_id
        MANAGED_SECRETS[index] = managed_secret_info
        self.definition_manager.define_node_on_all_storages(node_name)

    def update_node_io_group(self, node_info):
        io_group = utils.generate_io_group_from_labels(node_info.labels)
        node_name = node_info.name
        try:
            if io_group != NODES[node_name].io_group:
                logger.info(messages.IO_GROUP_CHANGED.format(node_name, io_group, NODES[node_name].io_group))
                NODES[node_name].io_group = io_group
                self.definition_manager.define_node_on_all_storages(node_name)
        except KeyError:
            pass
