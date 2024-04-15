import controllers.servers.host_definer.messages as messages
from controllers.common.csi_logger import get_stdout_logger
from controllers.servers.host_definer.globals import MANAGED_SECRETS
from controllers.servers.host_definer.watcher.watcher_helper import Watcher
from controllers.servers.host_definer.utils import utils

logger = get_stdout_logger()


class SecretWatcher(Watcher):

    def watch_secret_resources(self):
        while utils.loop_forever():
            stream = self.k8s_api.get_secret_stream()
            for watch_event in stream:
                watch_event = utils.munch(watch_event)
                secret_info = self.resource_info_manager.generate_k8s_secret_to_secret_info(watch_event.object)
                if self.secret_manager.is_secret_can_be_changed(secret_info, watch_event.type):
                    secret_info = self._get_secret_info(watch_event.object)
                    self._handle_storage_class_secret(secret_info)

    def _get_secret_info(self, watch_event_object):
        secret_data = utils.change_decode_base64_secret_config(watch_event_object.data)
        if self.secret_manager.is_topology_secret(secret_data):
            nodes_with_system_id = self.node_manager.generate_nodes_with_system_id(secret_data)
            system_ids_topologies = self.secret_manager.generate_secret_system_ids_topologies(secret_data)
            secret_info = self.resource_info_manager.generate_k8s_secret_to_secret_info(
                watch_event_object, nodes_with_system_id, system_ids_topologies)
        else:
            secret_info = self.resource_info_manager.generate_k8s_secret_to_secret_info(watch_event_object)
        return secret_info

    def _handle_storage_class_secret(self, secret_info):
        managed_secret_info, index = self.secret_manager.get_matching_managed_secret_info(secret_info)
        if managed_secret_info.managed_storage_classes > 0:
            secret_info.managed_storage_classes = managed_secret_info.managed_storage_classes
            MANAGED_SECRETS[index] = secret_info
            self._define_host_after_watch_event(secret_info)

    def _define_host_after_watch_event(self, secret_info):
        logger.info(messages.SECRET_HAS_BEEN_MODIFIED.format(secret_info.name, secret_info.namespace))
        host_definition_info = self.host_definition_manager.get_host_definition_info_from_secret(secret_info)
        self.definition_manager.define_nodes(host_definition_info)
