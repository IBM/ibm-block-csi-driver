from kubernetes import watch

from host_definer.watcher.watcher_helper import WatcherHelper, SECRET_IDS
from host_definer.common import settings, utils

logger = utils.get_stdout_logger()


class StorageClassWatcher(WatcherHelper):
    def __init__(self):
        super().__init__()

    def watch_storage_class_resources(self):
        watcher = watch.Watch()
        for event in watcher.stream(self.storage_api.list_storage_class):
            storage_class_name = event[settings.OBJECT_KEY].metadata.name
            secrets = self._get_secrets_from_storage_class_when_it_has_csi_ibm_block_as_a_provisioner(
                event[settings.OBJECT_KEY])
            if event[settings.TYPE_KEY] == settings.ADDED_EVENT:
                logger.info('New storageClass {}'.format(storage_class_name))
                self._handle_added_event_on_storage_class(
                    storage_class_name, secrets)
            elif event[settings.TYPE_KEY] == settings.DELETED_EVENT:
                self._handle_deleted_event_on_storage_class(secrets)

    def _get_secrets_from_storage_class_when_it_has_csi_ibm_block_as_a_provisioner(
            self, storage_class):
        if self._is_storage_class_has_csi_ibm_block_as_a_provisioner(
                storage_class):
            return self._get_secrets_from_storage_class(storage_class)

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
        elif parameter.endswith(settings.DEPRECATED_SECRET_NAME_SUBSTRING):
            return self._get_secret(
                storage_class,
                parameter,
                settings.DEPRECATED_SECRET_NAME_SUBSTRING)
        return ''

    def _get_secret(self, storage_class, parameter, secret_name_substring):
        prefix = parameter.split(secret_name_substring)[0]
        return self._generate_secret_id_From_secret_and_namespace(
            storage_class.parameters[parameter], storage_class.parameters[
                prefix + secret_name_substring.replace('name', 'namespace')])

    def _handle_added_event_on_storage_class(
            self, storage_class_name, secrets):
        for secret in secrets:
            try:
                self._verify_csi_nodes_on_storage_when_new_secret(secret)
            except Exception as ex:
                logger.error(
                    'Failed to get secret data for storageclass {}, got: {}'.format(
                        storage_class_name, ex))
            if secret:
                self._add_secret_if_uniq_or_add_secret_counter(secret)

    def _verify_csi_nodes_on_storage_when_new_secret(self, secret):
        if secret not in SECRET_IDS:
            self._verify_csi_node_on_storage_from_secret(secret)
        elif SECRET_IDS[secret] == 0:
            self._verify_csi_node_on_storage_from_secret(secret)

    def _verify_csi_node_on_storage_from_secret(self, secret):
        host_object = self.get_host_object_from_secret_id(secret)
        self.verify_csi_nodes_on_storage(host_object)

    def _add_secret_if_uniq_or_add_secret_counter(self, secret):
        if secret in SECRET_IDS:
            SECRET_IDS[secret] += 1
        else:
            SECRET_IDS[secret] = 1

    def _handle_deleted_event_on_storage_class(self, secrets):
        for secret in secrets:
            SECRET_IDS[secret] -= 1
