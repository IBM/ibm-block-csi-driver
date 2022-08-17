from kubernetes import watch
from munch import Munch

import controllers.servers.host_definer.messages as messages
from controllers.common.csi_logger import get_stdout_logger
from controllers.servers.host_definer.watcher.watcher_helper import Watcher, SECRET_IDS
from controllers.servers.host_definer.types import Secret
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
                secret = Secret()
                secret.name = watch_event.object.metadata.name
                secret.namespace = watch_event.object.metadata.namespace
                if self._is_secret_used_by_storage_class(secret):
                    self._handle_storage_class_secret(secret, watch_event.type)

    def _handle_storage_class_secret(self, secret, watch_event_type):
        secret_id = self._generate_secret_id_from_secret_and_namespace(secret.name, secret.namespace)
        if watch_event_type in (settings.ADDED_EVENT, settings.MODIFIED_EVENT) and \
                SECRET_IDS[secret_id] > 0:
            self._verify_host_defined_after_watch_event(secret)

    def _is_secret_used_by_storage_class(self, secret):
        return self._generate_secret_id_from_secret_and_namespace(secret.name, secret.namespace) in SECRET_IDS

    def _verify_host_defined_after_watch_event(self, secret):
        logger.info(messages.SECRET_HAS_BEEN_MODIFIED.format(secret.name, secret.namespace))
        host_definition = self._get_host_definition_from_secret(secret)
        self._verify_nodes_defined(host_definition)
