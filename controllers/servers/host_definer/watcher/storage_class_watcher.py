import controllers.servers.host_definer.messages as messages
from controllers.common.csi_logger import get_stdout_logger
from controllers.servers.host_definer.globals import MANAGED_SECRETS
from controllers.servers.host_definer.watcher.watcher_helper import Watcher
from controllers.servers.host_definer import settings
from controllers.servers.host_definer.utils import utils
import controllers.common.settings as common_settings

logger = get_stdout_logger()


class StorageClassWatcher(Watcher):

    def add_initial_storage_classes(self):
        storage_classes_info = self.k8s_manager.get_storage_classes_info()
        for storage_class_info in storage_classes_info:
            secrets_info = self._get_secrets_info_from_storage_class_with_driver_provisioner(storage_class_info)
            self._handle_added_watch_event(secrets_info, storage_class_info.name)

    def watch_storage_class_resources(self):
        while utils.loop_forever():
            stream = self.k8s_api.get_storage_class_stream()
            for watch_event in stream:
                watch_event = utils.munch(watch_event)
                storage_class_info = self.k8s_manager.generate_storage_class_info(watch_event.object)
                secrets_info = self._get_secrets_info_from_storage_class_with_driver_provisioner(storage_class_info)
                if watch_event.type == settings.ADDED_EVENT:
                    self._handle_added_watch_event(secrets_info, storage_class_info.name)

                if utils.is_watch_object_type_is_delete(watch_event.type):
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
            if self.secret_manager.is_secret(parameter_name):
                secret_name, secret_namespace = self.secret_manager.get_secret_name_and_namespace(
                    storage_class_info, parameter_name)
                secret_data = self.secret_manager.get_secret_data(secret_name, secret_namespace)
                logger.info(messages.SECRET_IS_BEING_USED_BY_STORAGE_CLASS.format(
                    secret_name, secret_namespace, storage_class_info.name))
                if self.secret_manager.is_topology_secret(secret_data):
                    logger.info(messages.SECRET_IS_FROM_TOPOLOGY_TYPE.format(secret_name, secret_namespace))
                    nodes_with_system_id = self._generate_nodes_with_system_id(secret_data)
                    system_ids_topologies = self.secret_manager.generate_secret_system_ids_topologies(secret_data)
                    secret_info = self.secret_manager.generate_secret_info(
                        secret_name, secret_namespace, nodes_with_system_id, system_ids_topologies)
                else:
                    secret_info = self.secret_manager.generate_secret_info(secret_name, secret_namespace)
                secrets_info = self.secret_manager.add_unique_secret_info_to_list(secret_info, secrets_info)
        return list(filter(None, secrets_info))

    def _handle_added_watch_event(self, secrets_info, storage_class_name):
        logger.info(messages.NEW_STORAGE_CLASS.format(storage_class_name))
        for secret_info in secrets_info:
            if secret_info:
                self._define_nodes_when_new_secret(secret_info)

    def _define_nodes_when_new_secret(self, secret_info):
        managed_secret_info, index = self.secret_manager.get_matching_managed_secret_info(secret_info)
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
        logger.info(messages.NEW_MANAGED_SECRET.format(secret_info.name, secret_info.namespace))
        host_definition_info = self.host_definition_manager.get_host_definition_info_from_secret(secret_info)
        self._define_nodes(host_definition_info)

    def _handle_deleted_watch_event(self, secrets_info):
        for secret_info in secrets_info:
            _, index = self.secret_manager.get_matching_managed_secret_info(secret_info)
            MANAGED_SECRETS[index].managed_storage_classes -= 1
