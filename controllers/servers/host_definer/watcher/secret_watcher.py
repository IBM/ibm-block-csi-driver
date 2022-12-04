from kubernetes import watch

import controllers.servers.host_definer.messages as messages
from controllers.common.csi_logger import get_stdout_logger
from controllers.servers.host_definer.watcher.watcher_helper import Watcher, MANAGED_SECRETS
from controllers.servers.host_definer.types import SecretInfo
from controllers.servers.host_definer import settings

logger = get_stdout_logger()


class SecretWatcher(Watcher):

    def watch_secret_resources(self):
        while self._loop_forever():
            resource_version = self._get_k8s_object_resource_version(self.core_api.list_secret_for_all_namespaces())
            stream = watch.Watch().stream(self.core_api.list_secret_for_all_namespaces,
                                          resource_version=resource_version, timeout_seconds=5)
            for watch_event in stream:
                watch_event = self._munch(watch_event)
                secret_info = self._generate_k8s_secret_to_secret_info(watch_event.object, {})
                if self._is_secret_managed(secret_info):
                    secret_data = self._change_decode_base64_secret_config(watch_event.object.data)
                    nodes_with_system_id = self._generate_nodes_with_system_id(secret_data)
                    secret_info = self._generate_k8s_secret_to_secret_info(watch_event.object, nodes_with_system_id)
                    self._handle_storage_class_secret(secret_info, watch_event.type)

    def _generate_k8s_secret_to_secret_info(self, k8s_secret, nodes_with_system_id):
        return SecretInfo(k8s_secret.metadata.name, k8s_secret.metadata.namespace, nodes_with_system_id)

    def _handle_storage_class_secret(self, secret_info, watch_event_type):
        managed_secret_info, index = self._get_matching_managed_secret_info(secret_info)
        if watch_event_type in (settings.ADDED_EVENT, settings.MODIFIED_EVENT) and \
                managed_secret_info.managed_storage_classes > 0:
            secret_info.managed_storage_classes = managed_secret_info.managed_storage_classes
            MANAGED_SECRETS[index] = secret_info
            self._define_host_after_watch_event(secret_info)

    def _define_host_after_watch_event(self, secret_info):
        logger.info(messages.SECRET_HAS_BEEN_MODIFIED.format(secret_info.name, secret_info.namespace))
        host_definition_info = self._get_host_definition_info_from_secret(secret_info)
        self._define_nodes(host_definition_info)
