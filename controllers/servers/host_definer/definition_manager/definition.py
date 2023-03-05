from controllers.common.csi_logger import get_stdout_logger
from controllers.servers.host_definer.globals import NODES, MANAGED_SECRETS
from controllers.servers.host_definer import settings
from controllers.servers.host_definer.utils import utils
import controllers.servers.host_definer.messages as messages
from controllers.servers.host_definer.types import DefineHostResponse
from controllers.servers.host_definer.resource_manager.secret import SecretManager
from controllers.servers.host_definer.k8s.manager import K8SManager
from controllers.servers.host_definer.definition_manager.request import RequestManager

logger = get_stdout_logger()


class DefinitionManager:
    def __init__(self):
        self.secret_manager = SecretManager()
        self.k8s_manager = K8SManager()
        self.request_manager = RequestManager()

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

    def _ensure_definition_state(self, host_definition_info, define_function):
        request = self.request_manager.generate_request(host_definition_info)
        if not request:
            response = DefineHostResponse()
            response.error_message = messages.FAILED_TO_GET_SECRET_EVENT.format(
                host_definition_info.secret_name, host_definition_info.secret_namespace)
            return response
        return define_function(request)
