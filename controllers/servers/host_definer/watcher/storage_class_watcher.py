from kubernetes import watch

import controllers.servers.messages as messages
from controllers.common.csi_logger import get_stdout_logger
from controllers.servers.host_definer.watcher.watcher_helper import Watcher, SECRET_IDS
from controllers.servers.host_definer import settings

logger = get_stdout_logger()


class StorageClassWatcher(Watcher):

    def watch_storage_class_resources(self):
        watcher = watch.Watch()
        stream = watcher.stream(self.storage_api.list_storage_class)
        for event in stream:
            storage_class = event[settings.OBJECT_KEY]
            storage_class_name = storage_class.metadata.name
            secrets = self._get_secrets_from_storage_class_with_driver_provisioner(storage_class)

            if event[settings.TYPE_KEY] == settings.ADDED_EVENT:
                logger.info(messages.NEW_STORAGE_CLASS.format(storage_class_name))
                self._handle_added_storage_class_event(secrets)

            elif event[settings.TYPE_KEY] == settings.DELETED_EVENT:
                self._handle_deleted_storage_class_event(secrets)

    def _get_secrets_from_storage_class_with_driver_provisioner(self, storage_class):
        if self._is_storage_class_has_csi_ibm_block_as_a_provisioner(storage_class):
            return self._get_secrets_from_storage_class(storage_class)
        return []

    def _is_storage_class_has_csi_ibm_block_as_a_provisioner(
            self, storage_class):
        return storage_class.provisioner == settings.IBM_BLOCK_CSI_PROVISIONER_NAME

    def _get_secrets_from_storage_class(self, storage_class):
        secrets = set()
        for parameter in storage_class.parameters:
            if self._is_secret(parameter):
                secret = self._get_secret_if_parameter_is_valid(storage_class, parameter)
                secrets.add(secret)
        return list(filter(None, secrets))

    def _is_secret(self, parameter):
        return parameter.endswith(settings.SECRET_NAME_SUBSTRING)

    def _get_secret_if_parameter_is_valid(self, storage_class, parameter):
        return self._get_secret(storage_class, parameter, settings.SECRET_NAME_SUBSTRING)

    def _get_secret(self, storage_class, parameter, secret_name_substring):
        prefix = parameter.split(secret_name_substring)[0]
        return self._generate_secret_id_from_secret_and_namespace(
            storage_class.parameters[parameter],
            storage_class.parameters[prefix + secret_name_substring.replace(settings.NAME, settings.NAMESPACE)])

    def _handle_added_storage_class_event(self, secrets):
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
        secret_name, secret_namespace = self._get_secret_name_and_namespace_from_id(secret_id)
        host_definition = self._get_host_definition_from_secret(secret_name, secret_namespace)
        if host_definition.management_address:
            self._verify_nodes_defined(host_definition)

    def _add_secret_if_uniq_or_add_secret_counter(self, secret):
        if secret in SECRET_IDS:
            SECRET_IDS[secret] += 1
        else:
            SECRET_IDS[secret] = 1

    def _handle_deleted_storage_class_event(self, secrets):
        for secret in secrets:
            SECRET_IDS[secret] -= 1
