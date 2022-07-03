from kubernetes import watch

import controllers.servers.messages as messages
from controllers.common.csi_logger import get_stdout_logger
from controllers.servers.host_definer.watcher.watcher_helper import Watcher, SECRET_IDS
from controllers.servers.host_definer import settings

logger = get_stdout_logger()


class SecretWatcher(Watcher):

    def watch_secret_resources(self):
        while True:
            resource_version = self.core_api.list_secret_for_all_namespaces().metadata.resource_version
            stream = watch.Watch().stream(self.core_api.list_secret_for_all_namespaces,
                                          resource_version=resource_version, timeout_seconds=5)
            for event in stream:
                secret = event[settings.OBJECT_KEY]
                secret_name = secret.metadata.name
                secret_namespace = secret.metadata.namespace
                if self._is_secret_used_by_storage_class(secret_name, secret_namespace):
                    event_type = event[settings.TYPE_KEY]
                    self._handle_storage_class_secret(secret, event_type)

    def _handle_storage_class_secret(self, secret, secret_event_type):
        secret_name = secret.metadata.name
        secret_namespace = secret.metadata.namespace
        if secret_event_type in (settings.ADDED_EVENT, settings.MODIFIED_EVENT):
            self._verify_host_defined_after_secret_event(secret_name, secret_namespace)

    def _is_secret_used_by_storage_class(self, secret_name, secret_namespace):
        return self._generate_secret_id_from_secret_and_namespace(secret_name, secret_namespace) in SECRET_IDS

    def _verify_host_defined_after_secret_event(self, secret_name, secret_namespace):
        logger.info(messages.SECRET_HAS_BEEN_MODIFIED.format(secret_name, secret_namespace))
        host_definition = self._get_host_definition_from_secret(secret_name, secret_namespace)
        if host_definition.management_address:
            logger.info(messages.VERIFY_HOSTS_ON_NEW_STORAGE.format(host_definition.management_address))
            self._verify_nodes_defined(host_definition)
