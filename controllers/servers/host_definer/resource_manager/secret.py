from controllers.common.csi_logger import get_stdout_logger
from controllers.servers.settings import SECRET_SUPPORTED_TOPOLOGIES_PARAMETER
from controllers.servers.utils import is_topology_match
import controllers.common.settings as common_settings
from controllers.servers.host_definer.globals import MANAGED_SECRETS
from controllers.servers.host_definer import settings
from controllers.servers.host_definer.utils import utils
from controllers.servers.host_definer.types import SecretInfo
import controllers.servers.host_definer.messages as messages
from controllers.servers.host_definer.k8s.api import K8SApi
from controllers.servers.host_definer.resource_manager.resource_info import ResourceInfoManager

logger = get_stdout_logger()


class SecretManager:
    def __init__(self):
        self.k8s_api = K8SApi()
        self.resource_info_manager = ResourceInfoManager()

    def is_node_should_be_managed_on_secret(self, node_name, secret_name, secret_namespace):
        logger.info(messages.CHECK_NODE_SHOULD_BE_MANAGED_BY_SECRET.format(node_name, secret_name, secret_namespace))
        secret_data = self.get_secret_data(secret_name, secret_namespace)
        utils.validate_secret(secret_data)
        managed_secret_info, _ = self._get_managed_secret_by_name_and_namespace(secret_name, secret_namespace)
        if self.is_node_should_managed_on_secret_info(node_name, managed_secret_info):
            logger.info(messages.NODE_SHOULD_BE_MANAGED_ON_SECRET.format(node_name, secret_name, secret_namespace))
            return True
        logger.info(messages.NODE_SHOULD_NOT_BE_MANAGED_ON_SECRET.format(node_name, secret_name, secret_namespace))
        return False

    def _get_managed_secret_by_name_and_namespace(self, secret_name, secret_namespace):
        secret_info = self.resource_info_manager.generate_secret_info(secret_name, secret_namespace)
        managed_secret_info, index = self.get_matching_managed_secret_info(secret_info)
        return managed_secret_info, index

    def is_node_should_managed_on_secret_info(self, node_name, secret_info):
        if secret_info:
            nodes_with_system_id = secret_info.nodes_with_system_id
            if nodes_with_system_id and nodes_with_system_id.get(node_name):
                return True
            if nodes_with_system_id:
                return False
            return True
        return False

    def is_node_labels_in_system_ids_topologies(self, system_ids_topologies, node_labels):
        return self.get_system_id_for_node_labels(system_ids_topologies, node_labels) != ''

    def get_system_id_for_node_labels(self, system_ids_topologies, node_labels):
        node_topology_labels = self.get_topology_labels(node_labels)
        for system_id, system_topologies in system_ids_topologies.items():
            if is_topology_match(system_topologies, node_topology_labels):
                return system_id
        return ''

    def is_topology_secret(self, secret_data):
        utils.validate_secret(secret_data)
        if utils.get_secret_config(secret_data):
            return True
        return False

    def generate_secret_system_ids_topologies(self, secret_data):
        system_ids_topologies = {}
        secret_config = utils.get_secret_config(secret_data)
        for system_id, system_info in secret_config.items():
            try:
                system_ids_topologies[system_id] = (system_info.get(SECRET_SUPPORTED_TOPOLOGIES_PARAMETER))
            except AttributeError:
                system_ids_topologies[system_id] = None
        return system_ids_topologies

    def is_secret(self, parameter_name):
        return parameter_name.endswith(settings.SECRET_NAME_SUFFIX) and \
            parameter_name.startswith(settings.CSI_PARAMETER_PREFIX)

    def get_secret_name_and_namespace(self, storage_class_info, parameter_name):
        secret_name_suffix = settings.SECRET_NAME_SUFFIX
        prefix = parameter_name.split(secret_name_suffix)[0]
        return (storage_class_info.parameters[parameter_name],
                storage_class_info.parameters[prefix + secret_name_suffix.replace(
                    common_settings.NAME_FIELD, common_settings.NAMESPACE_FIELD)])

    def add_unique_secret_info_to_list(self, secret_info, secrets_info_list):
        for secret_info_in_list in secrets_info_list:
            if secret_info_in_list.name == secret_info.name and \
                    secret_info_in_list.namespace == secret_info.namespace:
                return secrets_info_list
        secrets_info_list.append(secret_info)
        return secrets_info_list

    def is_secret_can_be_changed(self, secret_info, watch_event_type):
        return self._is_secret_managed(secret_info) and \
            not utils.is_watch_object_type_is_delete(watch_event_type)

    def _is_secret_managed(self, secret_info):
        _, index = self.get_matching_managed_secret_info(secret_info)
        if index != -1:
            return True
        return False

    def get_matching_managed_secret_info(self, secret_info):
        for index, managed_secret_info in enumerate(MANAGED_SECRETS):
            if managed_secret_info.name == secret_info.name and managed_secret_info.namespace == secret_info.namespace:
                return managed_secret_info, index
        return secret_info, -1

    def get_array_connection_info(self, secret_name, secret_namespace, labels):
        secret_data = self.get_secret_data(secret_name, secret_namespace)
        if secret_data:
            node_topology_labels = self.get_topology_labels(labels)
            return utils.get_array_connection_info_from_secret_data(secret_data, node_topology_labels)
        return {}

    def get_secret_data(self, secret_name, secret_namespace):
        logger.info(messages.READ_SECRET.format(secret_name, secret_namespace))
        secret_data = self.k8s_api.get_secret_data(secret_name, secret_namespace)
        if secret_data:
            return utils.change_decode_base64_secret_config(secret_data)
        return {}

    def get_topology_labels(self, labels):
        topology_labels = {}
        for label in labels:
            if utils.is_topology_label(label):
                topology_labels[label] = labels[label]
        return topology_labels
