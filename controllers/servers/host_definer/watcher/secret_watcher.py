from kubernetes import watch
from munch import Munch

import controllers.servers.host_definer.messages as messages
from controllers.common.csi_logger import get_stdout_logger
from controllers.servers.host_definer.watcher.watcher_helper import Watcher, SECRET_IDS
from controllers.servers.host_definer.types import SecretInfo
from controllers.servers.host_definer import settings

logger = get_stdout_logger()


class SecretWatcher(Watcher):

    def watch_secret_resources(self):
        while True:
            resource_version = self.core_api.list_secret_for_all_namespaces().metadata.resource_version
            stream = watch.Watch().stream(self.core_api.list_secret_for_all_namespaces,
                                          resource_version=resource_version, timeout_seconds=5)
            for watch_event in stream:
                watch_event = Munch.fromDict(watch_event)
                secret_info = self._generate_secret_info(watch_event.object)
                if self._is_secret_used_by_storage_class(secret_info):
                    self._handle_storage_class_secret(secret_info, watch_event.type)

    def _generate_secret_info(self, k8s_secret):
        secret_info = SecretInfo()
        secret_info.name = k8s_secret.metadata.name
        secret_info.namespace = k8s_secret.metadata.namespace
        return secret_info

    def _is_secret_used_by_storage_class(self, secret_info):
        return self._generate_secret_id(secret_info.name, secret_info.namespace) in SECRET_IDS

    def _handle_storage_class_secret(self, secret_info, watch_event_type):
        secret_id = self._generate_secret_id(secret_info.name, secret_info.namespace)
        if watch_event_type in (settings.ADDED_EVENT, settings.MODIFIED_EVENT) and \
                SECRET_IDS[secret_id] > 0:
            self.define_host_after_watch_event(secret_info)

    def define_host_after_watch_event(self, secret_info):
        logger.info(messages.SECRET_HAS_BEEN_MODIFIED.format(secret_info.name, secret_info.namespace))
        host_definition_info = self._get_host_definition_info_from_secret(secret_info)
        self.define_nodes(host_definition_info)
