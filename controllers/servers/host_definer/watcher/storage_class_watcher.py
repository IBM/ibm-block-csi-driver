import json
from kubernetes import watch

import controllers.servers.host_definer.messages as messages
from controllers.common.csi_logger import get_stdout_logger
from controllers.servers.host_definer.watcher.watcher_helper import Watcher, MANAGED_SECRETS
from controllers.servers.host_definer import settings
import controllers.common.settings as common_settings

logger = get_stdout_logger()


class StorageClassWatcher(Watcher):

    def add_initial_storage_classes(self):
        storage_classes_info = self._get_storage_classes_info()
        for storage_class_info in storage_classes_info:
            secrets_info = self._get_secrets_info_from_storage_class_with_driver_provisioner(storage_class_info)
            self._handle_added_watch_event(secrets_info, storage_class_info.name)

    def watch_storage_class_resources(self):
        while self._loop_forever():
            resource_version = self._get_k8s_object_resource_version(self.storage_api.list_storage_class())
            stream = watch.Watch().stream(self.storage_api.list_storage_class,
                                          resource_version=resource_version, timeout_seconds=5)
            for watch_event in stream:
                watch_event = self._munch(watch_event)
                storage_class_info = self._generate_storage_class_info(watch_event.object)
                secrets_info = self._get_secrets_info_from_storage_class_with_driver_provisioner(storage_class_info)
                if watch_event.type == settings.ADDED_EVENT:
                    self._handle_added_watch_event(secrets_info, storage_class_info.name)

                if watch_event.type == settings.DELETED_EVENT:
                    self._handle_deleted_watch_event(secrets_info)

    def _get_secrets_info_from_storage_class_with_driver_provisioner(self, storage_class_info):
        if self._is_storage_class_has_csi_as_a_provisioner(storage_class_info):
            return self._get_secrets_info_from_storage_class(storage_class_info)
        return []

    def _is_storage_class_has_csi_as_a_provisioner(self, storage_class_info):
        return storage_class_info.provisioner == settings.CSI_PROVISIONER_NAME

    def _get_secrets_info_from_storage_class(self, storage_class_info):
        secrets_info = []
        for parameter_name in storage_class_info.parameters:
            if self._is_secret(parameter_name):
                secret_name, secret_namespace = self._get_secret_name_and_namespace(storage_class_info, parameter_name)
                secret_data = self._get_secret_data(secret_name, secret_namespace)
                if self._is_topology_secret(secret_data):
                    nodes_with_system_id = self._generate_nodes_with_system_id(secret_data)
                    secret_info = self._generate_secret_info(secret_name, secret_namespace, nodes_with_system_id)
                    secrets_info = self._add_secret_info_to_list(secret_info, secrets_info)
                else:
                    secret_info = self._generate_secret_info(secret_name, secret_namespace)
                    secrets_info = self._add_secret_info_to_list(secret_info, secrets_info)
        return list(filter(None, secrets_info))

    def _is_secret(self, parameter_name):
        return parameter_name.endswith(settings.SECRET_NAME_SUFFIX) and \
            parameter_name.startswith(settings.CSI_PARAMETER_PREFIX)

    def _get_secret_name_and_namespace(self, storage_class_info, parameter_name):
        secret_name_suffix = settings.SECRET_NAME_SUFFIX
        prefix = parameter_name.split(secret_name_suffix)[0]
        return (storage_class_info.parameters[parameter_name],
                storage_class_info.parameters[prefix + secret_name_suffix.replace(
                    common_settings.NAME_FIELD, common_settings.NAMESPACE_FIELD)])

    def _is_topology_secret(self, secret_data):
        self._validate_secret(secret_data)
        if self._get_secret_secret_config(secret_data):
            return True
        return False

    def _add_secret_info_to_list(self, secret_info, list_with_secrets_info):
        for secret_info_in_list in list_with_secrets_info:
            if secret_info_in_list.name == secret_info.name and \
                    secret_info_in_list.namespace == secret_info.namespace:
                return list_with_secrets_info
        list_with_secrets_info.append(secret_info)
        return list_with_secrets_info

    def _handle_added_watch_event(self, secrets_info, storage_class_name):
        logger.info(messages.NEW_STORAGE_CLASS.format(storage_class_name))
        for secret_info in secrets_info:
            if secret_info:
                self._define_nodes_when_new_secret(secret_info)

    def _define_nodes_when_new_secret(self, secret_info):
        managed_secret_info, index = self._get_matching_managed_secret_info(secret_info)
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
        host_definition_info = self._get_host_definition_info_from_secret(secret_info)
        self._define_nodes(host_definition_info)

    def _handle_deleted_watch_event(self, secrets_info):
        for secret_info in secrets_info:
            _, index = self._get_matching_managed_secret_info(secret_info)
            MANAGED_SECRETS[index].managed_storage_classes -= 1
