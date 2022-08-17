from kubernetes import watch
from munch import Munch

import controllers.servers.host_definer.messages as messages
from controllers.common.csi_logger import get_stdout_logger
from controllers.servers.host_definer.watcher.watcher_helper import Watcher, SECRET_IDS
from controllers.servers.host_definer import settings

logger = get_stdout_logger()


class StorageClassWatcher(Watcher):

    def add_initial_storage_classes(self):
        storage_classes = self._get_storage_classes()
        for storage_class in storage_classes:
            secrets = self._get_secrets_from_storage_class_with_driver_provisioner(storage_class)
            self._handle_added_watch_event(secrets, storage_class.name)

    def watch_storage_class_resources(self):
        while True:
            resource_version = self.storage_api.list_storage_class().metadata.resource_version
            stream = watch.Watch().stream(self.storage_api.list_storage_class,
                                          resource_version=resource_version, timeout_seconds=5)
            for watch_event in stream:
                watch_event = Munch.fromDict(watch_event)
                storage_class = self._get_storage_class_object(watch_event.object)
                secrets = self._get_secrets_from_storage_class_with_driver_provisioner(storage_class)
                if watch_event.type == settings.ADDED_EVENT:
                    self._handle_added_watch_event(secrets, storage_class.name)

                if watch_event.type == settings.DELETED_EVENT:
                    self._handle_deleted_watch_event(secrets)

    def _get_secrets_from_storage_class_with_driver_provisioner(self, storage_class):
        if self._is_storage_class_has_csi_as_a_provisioner(storage_class):
            return self._get_secrets_from_storage_class(storage_class)
        return []

    def _is_storage_class_has_csi_as_a_provisioner(
            self, storage_class):
        return storage_class.provisioner == settings.CSI_PROVISIONER_NAME

    def _get_secrets_from_storage_class(self, storage_class):
        secrets = set()
        for parameter_name in storage_class.parameters:
            if self._is_secret(parameter_name):
                secret = self._get_secret(storage_class, parameter_name)
                secrets.add(secret)
        return list(filter(None, secrets))

    def _is_secret(self, parameter_name):
        return parameter_name.endswith(settings.SECRET_NAME_SUFFIX) and \
            parameter_name.startswith(settings.CSI_PARAMETER_PREFIX)

    def _get_secret(self, storage_class, parameter_name):
        secret_name_suffix = settings.SECRET_NAME_SUFFIX
        prefix = parameter_name.split(secret_name_suffix)[0]
        return self._generate_secret_id_from_secret_and_namespace(
            storage_class.parameters[parameter_name],
            storage_class.parameters[prefix + secret_name_suffix.replace(settings.NAME, settings.NAMESPACE)])

    def _handle_added_watch_event(self, secrets, storage_class_name):
        logger.info(messages.NEW_STORAGE_CLASS.format(storage_class_name))
        for secret in secrets:
            self._verify_nodes_defined_when_new_secret(secret)
            if secret:
                self._add_secret_if_uniq_or_add_secret_counter(secret)

    def _verify_nodes_defined_when_new_secret(self, secret):
        if secret not in SECRET_IDS:
            self._verify_node_defined_on_storage_from_secret(secret)
        elif SECRET_IDS[secret] == 0:
            self._verify_node_defined_on_storage_from_secret(secret)

    def _verify_node_defined_on_storage_from_secret(self, secret_id):
        secret = self._get_secret_object_from_id(secret_id)
        host_definition = self._get_host_definition_from_secret(secret)
        self._verify_nodes_defined(host_definition)

    def _add_secret_if_uniq_or_add_secret_counter(self, secret):
        if secret in SECRET_IDS:
            SECRET_IDS[secret] += 1
        else:
            SECRET_IDS[secret] = 1

    def _handle_deleted_watch_event(self, secrets):
        for secret in secrets:
            SECRET_IDS[secret] -= 1
