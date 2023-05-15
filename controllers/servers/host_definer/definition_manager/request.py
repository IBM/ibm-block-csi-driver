from controllers.common.csi_logger import get_stdout_logger
from controllers.servers.host_definer.globals import NODES
from controllers.servers.host_definer import settings
from controllers.servers.host_definer.utils import utils
import controllers.servers.host_definer.messages as messages
from controllers.servers.host_definer.types import DefineHostRequest
from controllers.servers.host_definer.resource_manager.secret import SecretManager
from controllers.servers.host_definer.resource_manager.resource_info import ResourceInfoManager

logger = get_stdout_logger()


class RequestManager:
    def __init__(self):
        self.secret_manager = SecretManager()
        self.resource_info_manager = ResourceInfoManager()

    def generate_request(self, host_definition_info):
        node_name = host_definition_info.node_name
        logger.info(messages.GENERATE_REQUEST_FOR_NODE.format(node_name))
        node_info = self.resource_info_manager.get_node_info(node_name)
        request = self._get_new_request(host_definition_info.secret_name,
                                        host_definition_info.secret_namespace, node_info.labels)
        if request:
            request.node_id_from_host_definition = host_definition_info.node_id
            request.node_id_from_csi_node = self._get_node_id_by_node(host_definition_info)
            request.io_group = self._get_io_group_by_node(host_definition_info.node_name)
        return request

    def _get_new_request(self, secret_name, secret_namespace, labels):
        connectivity_type_label_on_node = self._get_label_value(labels, settings.CONNECTIVITY_TYPE_LABEL)
        prefix = utils.get_prefix()
        connectivity_type_from_user = utils.get_connectivity_type_from_user(connectivity_type_label_on_node)
        array_connection_info = self.secret_manager.get_array_connection_info(secret_name, secret_namespace, labels)

        if array_connection_info:
            return DefineHostRequest(
                prefix=prefix, connectivity_type_from_user=connectivity_type_from_user,
                array_connection_info=array_connection_info, node_id_from_host_definition='', node_id_from_csi_node='',
                io_group='')
        return None

    def _get_label_value(self, labels, label):
        return labels.get(label)

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
