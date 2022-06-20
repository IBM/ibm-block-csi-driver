from kubernetes import watch

from host_definer.watcher.watcher_helper import WatcherHelper, SECRET_IDS
from host_definer.watcher.exceptions import WatcherException
from host_definer.common import settings, utils

logger = utils.get_stdout_logger()


class SecretWatcher(WatcherHelper):
    def __init__(self):
        super().__init__()

    def watch_secret_resources(self):
        while True:
            resource_version = self.core_api.list_secret_for_all_namespaces().metadata.resource_version
            stream = watch.Watch().stream(self.core_api.list_secret_for_all_namespaces,
                                          resource_version=resource_version, timeout_seconds=5)
            for event in stream:
                if event[settings.TYPE_KEY] == settings.ADDED_EVENT and self._is_secret_used_by_storage_class(
                        event):
                    self._verify_host_on_storage_from_secret_event(event)
                if event[settings.TYPE_KEY] == settings.MODIFIED_EVENT:
                    if self._is_secret_used_by_storage_class(event):
                        self._handle_modified_secrets(event)

    def _is_secret_used_by_storage_class(self, event):
        secret_name = event[settings.OBJECT_KEY].metadata.name
        secret_namespace = event[settings.OBJECT_KEY].metadata.namespace
        return self._generate_secret_id_From_secret_and_namespace(
            secret_name, secret_namespace) in SECRET_IDS

    def _handle_modified_secrets(self, secret_event):
        secret_name = secret_event[settings.OBJECT_KEY].metadata.name
        secret_namespace = secret_event[settings.OBJECT_KEY].metadata.namespace
        logger.info('Secret {} in namespace {}, has been modified'.format(
            secret_name, secret_namespace))
        self._verify_host_on_storage_from_secret_event(secret_event)

    def _verify_host_on_storage_from_secret_event(self, secret_event):
        secret_name = secret_event[settings.OBJECT_KEY].metadata.name
        secret_namespace = secret_event[settings.OBJECT_KEY].metadata.namespace
        try:
            host_request = self.get_host_request_from_secret_name_and_namespace(
                secret_name, secret_namespace)
            logger.info(
                'Verifying hosts on new storage {}'.format(
                    host_request.system_info[settings.MANAGEMENT_ADDRESS_KEY]))
            self.verify_csi_nodes_on_storage(host_request)
        except WatcherException as ex:
            logger.error(
                'Failed to find secret {} in namespace {}, got: {}'.format(
                    secret_name, secret_namespace, ex))
