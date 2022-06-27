from kubernetes import watch

from controllers.common.csi_logger import get_stdout_logger
from controllers.servers.host_definer.watcher.watcher_helper import WatcherHelper, SECRET_IDS
from controllers.servers.host_definer.common import settings

logger = get_stdout_logger()


class StorageClassWatcher(WatcherHelper):

    def watch_storage_class_resources(self):
        watcher = watch.Watch()
        for event in watcher.stream(self.storage_api.list_storage_class):
            storage_class_name = event[settings.OBJECT_KEY].metadata.name
            secrets = self._get_secrets_from_storage_class_when_it_has_csi_ibm_block_as_a_provisioner(
                event[settings.OBJECT_KEY])
            if event[settings.TYPE_KEY] == settings.ADDED_EVENT:
                logger.info('New storageClass {}'.format(storage_class_name))
                self._handle_added_storage_class_event(
                    secrets)
            elif event[settings.TYPE_KEY] == settings.DELETED_EVENT:
                self._handle_deleted_storage_class_event(secrets)

    def _get_secrets_from_storage_class_when_it_has_csi_ibm_block_as_a_provisioner(
            self, storage_class):
        if self._is_storage_class_has_csi_ibm_block_as_a_provisioner(
                storage_class):
            return self._get_secrets_from_storage_class(storage_class)
        return []

    def _is_storage_class_has_csi_ibm_block_as_a_provisioner(
            self, storage_class):
        return storage_class.provisioner == settings.IBM_BLOCK_CSI_DRIVER_NAME

    def _get_secrets_from_storage_class(self, storage_class):
        secrets = set()
        for parameter in storage_class.parameters:
            secret = self._get_secret_if_parameter_is_valid(
                storage_class, parameter)
            secrets.add(secret)
        return list(filter(None, secrets))

    def _get_secret_if_parameter_is_valid(self, storage_class, parameter):
        if parameter.endswith(settings.SECRET_NAME_SUBSTRING):
            return self._get_secret(
                storage_class, parameter, settings.SECRET_NAME_SUBSTRING)
        if parameter.endswith(settings.DEPRECATED_SECRET_NAME_SUBSTRING):
            return self._get_secret(
                storage_class,
                parameter,
                settings.DEPRECATED_SECRET_NAME_SUBSTRING)
        return ''

    def _get_secret(self, storage_class, parameter, secret_name_substring):
        prefix = parameter.split(secret_name_substring)[0]
        return self.generate_secret_id_from_secret_and_namespace(
            storage_class.parameters[parameter], storage_class.parameters[
                prefix + secret_name_substring.replace('name', 'namespace')])

    def _handle_added_storage_class_event(
            self, secrets):
        for secret in secrets:
            self._verify_nodes_defined_when_new_secret(secret)
            if secret:
                self._add_secret_if_uniq_or_add_secret_counter(secret)

    def _verify_nodes_defined_when_new_secret(self, secret):
        if secret not in SECRET_IDS:
            self._verify_node_defined_on_storage_from_secret(secret)
        elif SECRET_IDS[secret] == 0:
            self._verify_node_defined_on_storage_from_secret(secret)

    def _verify_node_defined_on_storage_from_secret(self, secret):
        host_request = self.get_host_request_from_secret_id(secret)
        if host_request:
            self.verify_nodes_defined(host_request)

    def _add_secret_if_uniq_or_add_secret_counter(self, secret):
        if secret in SECRET_IDS:
            SECRET_IDS[secret] += 1
        else:
            SECRET_IDS[secret] = 1

    def _handle_deleted_storage_class_event(self, secrets):
        for secret in secrets:
            SECRET_IDS[secret] -= 1
