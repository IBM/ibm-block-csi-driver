from kubernetes import watch

import controllers.servers.host_definer.messages as messages
from controllers.common.csi_logger import get_stdout_logger
from controllers.servers.host_definer.watcher.watcher_helper import Watcher, SECRET_IDS
from controllers.servers.host_definer import settings

logger = get_stdout_logger()


class StorageClassWatcher(Watcher):

    def add_initial_storage_classes(self):
        storage_classes_info = self._get_storage_classes_info()
        for storage_class_info in storage_classes_info:
            secrets_id = self._get_secrets_id_from_storage_class_with_driver_provisioner(storage_class_info)
            self._handle_added_watch_event(secrets_id, storage_class_info.name)

    def watch_storage_class_resources(self):
        while True:
            resource_version = self.storage_api.list_storage_class().metadata.resource_version
            stream = watch.Watch().stream(self.storage_api.list_storage_class,
                                          resource_version=resource_version, timeout_seconds=5)
            for watch_event in stream:
                watch_event = self._munch(watch_event)
                storage_class_info = self._generate_storage_class_info(watch_event.object)
                secrets_id = self._get_secrets_id_from_storage_class_with_driver_provisioner(storage_class_info)
                if watch_event.type == settings.ADDED_EVENT:
                    self._handle_added_watch_event(secrets_id, storage_class_info.name)

                if watch_event.type == settings.DELETED_EVENT:
                    self._handle_deleted_watch_event(secrets_id)

    def _get_secrets_id_from_storage_class_with_driver_provisioner(self, storage_class_info):
        if self._is_storage_class_has_csi_as_a_provisioner(storage_class_info):
            return self._get_secrets_id_from_storage_class(storage_class_info)
        return []

    def _is_storage_class_has_csi_as_a_provisioner(self, storage_class_info):
        return storage_class_info.provisioner == settings.CSI_PROVISIONER_NAME

    def _get_secrets_id_from_storage_class(self, storage_class_info):
        secrets_id = set()
        for parameter_name in storage_class_info.parameters:
            if self._is_secret(parameter_name):
                secret_id = self._get_secret_id(storage_class_info, parameter_name)
                secrets_id.add(secret_id)
        return list(filter(None, secrets_id))

    def _is_secret(self, parameter_name):
        return parameter_name.endswith(settings.SECRET_NAME_SUFFIX) and \
            parameter_name.startswith(settings.CSI_PARAMETER_PREFIX)

    def _get_secret_id(self, storage_class_info, parameter_name):
        secret_name_suffix = settings.SECRET_NAME_SUFFIX
        prefix = parameter_name.split(secret_name_suffix)[0]
        return self._generate_secret_id(
            storage_class_info.parameters[parameter_name],
            storage_class_info.parameters[prefix + secret_name_suffix.replace(settings.NAME, settings.NAMESPACE)])

    def _handle_added_watch_event(self, secrets_id, storage_class_name):
        logger.info(messages.NEW_STORAGE_CLASS.format(storage_class_name))
        for secret_id in secrets_id:
            self.define_nodes_when_new_secret(secret_id)
            if secret_id:
                self._add_secret_if_uniq_or_add_secret_counter(secret_id)

    def define_nodes_when_new_secret(self, secret_id):
        secret_info = self._generate_secret_info_from_id(secret_id)
        if secret_id not in SECRET_IDS:
            self.define_nodes_from_secret_id(secret_info)
        elif SECRET_IDS[secret_id] == 0:
            self.define_nodes_from_secret_id(secret_info)

    def _add_secret_if_uniq_or_add_secret_counter(self, secret_id):
        if secret_id in SECRET_IDS:
            SECRET_IDS[secret_id] += 1
        else:
            SECRET_IDS[secret_id] = 1

    def define_nodes_from_secret_id(self, secret_info):
        host_definition_info = self._get_host_definition_info_from_secret(secret_info)
        self.define_nodes(host_definition_info)

    def _handle_deleted_watch_event(self, secrets_id):
        for secret_id in secrets_id:
            SECRET_IDS[secret_id] -= 1
