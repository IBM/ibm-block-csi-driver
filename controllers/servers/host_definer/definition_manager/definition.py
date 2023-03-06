from controllers.common.csi_logger import get_stdout_logger
from controllers.servers.host_definer.globals import NODES, MANAGED_SECRETS
import controllers.servers.host_definer.messages as messages
from controllers.servers.host_definer.utils import manifest_utils
from controllers.servers.host_definer.types import DefineHostResponse
from controllers.servers.host_definer.k8s.api import K8SApi
from controllers.servers.host_definer.resource_manager.secret import SecretManager
from controllers.servers.host_definer.resource_manager.host_definition import HostDefinitionManager
from controllers.servers.host_definer.definition_manager.request import RequestManager
from controllers.servers.host_definer.storage_manager.host_definer_server import HostDefinerServicer

logger = get_stdout_logger()


class DefinitionManager:
    def __init__(self):
        self.k8s_api = K8SApi()
        self.secret_manager = SecretManager()
        self.request_manager = RequestManager()
        self.host_definition_manager = HostDefinitionManager()
        self.storage_host_servicer = HostDefinerServicer()

    def define_node_on_all_storages(self, node_name):
        logger.info(messages.DEFINE_NODE_ON_ALL_MANAGED_SECRETS.format(node_name))
        for secret_info in MANAGED_SECRETS:
            if secret_info.managed_storage_classes == 0:
                continue
            host_definition_info = self.host_definition_manager.get_host_definition_info_from_secret_and_node_name(
                node_name, secret_info)
            self.create_definition(host_definition_info)

    def delete_definition(self, host_definition_info):
        response = DefineHostResponse()
        if self.secret_manager.is_node_should_be_managed_on_secret(
                host_definition_info.node_name, host_definition_info.secret_name,
                host_definition_info.secret_namespace):
            response = self.undefine_host(host_definition_info)
        self.host_definition_manager.handle_k8s_host_definition_after_undefine_action(host_definition_info,
                                                                                      response)

    def undefine_node_definitions(self, node_name):
        for secret_info in MANAGED_SECRETS:
            host_definition_info = self.host_definition_manager.get_host_definition_info_from_secret_and_node_name(
                node_name, secret_info)
            self.delete_definition(host_definition_info)

    def undefine_host_after_pending(self, host_definition_info):
        response = DefineHostResponse()
        if self.secret_manager.is_node_should_be_managed_on_secret(
                host_definition_info.node_name, host_definition_info.secret_name,
                host_definition_info.secret_namespace):
            response = self.undefine_host(host_definition_info)
        return response

    def undefine_host(self, host_definition_info):
        logger.info(messages.UNDEFINED_HOST.format(host_definition_info.node_name,
                    host_definition_info.secret_name, host_definition_info.secret_namespace))
        return self._ensure_definition_state(host_definition_info, self.storage_host_servicer.undefine_host)

    def define_host_after_pending(self, host_definition_info):
        response = DefineHostResponse()
        if self.secret_manager.is_node_should_be_managed_on_secret(
                host_definition_info.node_name, host_definition_info.secret_name,
                host_definition_info.secret_namespace):
            response = self.define_host(host_definition_info)
            self._update_host_definition_from_storage_response(host_definition_info.name, response)
        else:
            self.host_definition_manager.delete_host_definition(host_definition_info.name)
        return response

    def _update_host_definition_from_storage_response(self, host_definition_name, response):
        logger.info(messages.UPDATE_HOST_DEFINITION_FIELDS_FROM_STORAGE.format(host_definition_name, response))
        host_definition_manifest = manifest_utils.generate_host_definition_response_fields_manifest(
            host_definition_name, response)
        self.k8s_api.patch_host_definition(host_definition_manifest)

    def define_nodes_when_new_secret(self, secret_info):
        managed_secret_info, index = self.secret_manager.get_matching_managed_secret_info(secret_info)
        secret_info.managed_storage_classes = 1
        if index == -1:
            MANAGED_SECRETS.append(secret_info)
            self._define_nodes_from_secret_info(secret_info)
        elif managed_secret_info.managed_storage_classes == 0:
            MANAGED_SECRETS[index] = secret_info
            self._define_nodes_from_secret_info(secret_info)
        else:
            secret_info.managed_storage_classes = managed_secret_info.managed_storage_classes + 1
            MANAGED_SECRETS[index] = secret_info

    def _define_nodes_from_secret_info(self, secret_info):
        logger.info(messages.NEW_MANAGED_SECRET.format(secret_info.name, secret_info.namespace))
        host_definition_info = self.host_definition_manager.get_host_definition_info_from_secret(secret_info)
        self.define_nodes(host_definition_info)

    def define_nodes(self, host_definition_info):
        for node_name, _ in NODES.items():
            host_definition_info = self.host_definition_manager.add_name_to_host_definition_info(
                node_name, host_definition_info)
            self.create_definition(host_definition_info)

    def create_definition(self, host_definition_info):
        if not self.secret_manager.is_node_should_be_managed_on_secret(
                host_definition_info.node_name, host_definition_info.secret_name,
                host_definition_info.secret_namespace):
            return
        host_definition_info = self.host_definition_manager.update_host_definition_info(host_definition_info)
        response = self.define_host(host_definition_info)
        current_host_definition_info_on_cluster = self.host_definition_manager.create_host_definition_if_not_exist(
            host_definition_info, response)
        self.host_definition_manager.set_status_to_host_definition_after_definition(
            response.error_message, current_host_definition_info_on_cluster)

    def define_host(self, host_definition_info):
        logger.info(messages.DEFINE_NODE_ON_SECRET.format(host_definition_info.node_name,
                    host_definition_info.secret_name, host_definition_info.secret_namespace))
        return self._ensure_definition_state(host_definition_info, self.storage_host_servicer.define_host)

    def _ensure_definition_state(self, host_definition_info, define_function):
        request = self.request_manager.generate_request(host_definition_info)
        if not request:
            response = DefineHostResponse()
            response.error_message = messages.FAILED_TO_GET_SECRET_EVENT.format(
                host_definition_info.secret_name, host_definition_info.secret_namespace)
            return response
        return define_function(request)
