from kubernetes import watch

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
                if event[settings.TYPE_KEY] == settings.ADDED_EVENT and \
                        self._is_secret_used_by_storage_class(secret_name, secret_namespace):
                    self._verify_host_defined_from_secret_event(secret_name, secret_namespace)
                if event[settings.TYPE_KEY] == settings.MODIFIED_EVENT and \
                        self._is_secret_used_by_storage_class(secret_name, secret_namespace):
                    self._handle_modified_secrets(secret_name, secret_namespace)

    def _is_secret_used_by_storage_class(self, secret_name, secret_namespace):
        return self.generate_secret_id_from_secret_and_namespace(secret_name, secret_namespace) in SECRET_IDS

    def _handle_modified_secrets(self, secret_name, secret_namespace):
        logger.info('Secret {} in namespace {}, has been modified'.format(secret_name, secret_namespace))
        self._verify_host_defined_from_secret_event(secret_name, secret_namespace)

    def _verify_host_defined_from_secret_event(self, secret_name, secret_namespace):
        host_definition = self._get_host_definition_from_secret(secret_name, secret_namespace)
        if host_definition.management_address:
            logger.info('Verifying hosts on new storage {}'.format(host_definition.management_address))
            self.verify_nodes_defined(host_definition)
