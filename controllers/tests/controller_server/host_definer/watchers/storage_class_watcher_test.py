from copy import deepcopy
from unittest.mock import MagicMock, patch

import controllers.common.settings as common_settings
import controllers.tests.controller_server.host_definer.utils.test_utils as test_utils
import controllers.tests.controller_server.host_definer.settings as test_settings
from controllers.tests.controller_server.host_definer.watchers.watcher_base import WatcherBaseSetUp
from controllers.servers.host_definer.watcher.storage_class_watcher import StorageClassWatcher


class StorageClassWatcherBase(WatcherBaseSetUp):
    def setUp(self):
        super().setUp()
        self.watcher = StorageClassWatcher()
        self.watcher.k8s_api = MagicMock()
        self.watcher.storage_class_manager = MagicMock()
        self.watcher.secret_manager = MagicMock()
        self.watcher.node_manager = MagicMock()
        self.watcher.resource_info_manager = MagicMock()
        self.watcher.definition_manager = MagicMock()
        self.fake_storage_class_info = test_utils.get_fake_storage_class_info()
        self.fake_storage_class_info.parameters = {test_settings.STORAGE_CLASS_SECRET_FIELD: test_settings.FAKE_SECRET}
        self.fake_secret_info = test_utils.get_fake_secret_info()
        self.fake_node_info = test_utils.get_fake_node_info()
        self.fake_secret_data = test_utils.get_fake_k8s_secret().data
        self.managed_secrets_on_storage_class_watcher = test_utils.patch_managed_secrets_global_variable(
            test_settings.STORAGE_CLASS_WATCHER_PATH)
        self.fake_nodes_with_system_id = {self.fake_node_info.name: test_settings.FAKE_SYSTEM_ID}

    def _prepare_get_secrets_info_from_storage_class_with_driver_provisioner(
            self, is_sc_has_csi_provisioner, is_secret, is_topology_secret=False):
        self.watcher.storage_class_manager.is_storage_class_has_csi_as_a_provisioner.return_value = \
            is_sc_has_csi_provisioner
        if is_sc_has_csi_provisioner:
            self._prepare_get_secrets_info_from_storage_class(is_secret, is_topology_secret)

    def _prepare_get_secrets_info_from_storage_class(self, is_secret, is_topology_secret):
        self.watcher.secret_manager.is_secret.return_value = is_secret
        if is_secret:
            self.watcher.secret_manager.get_secret_name_and_namespace.return_value = (
                test_settings.FAKE_SECRET, test_settings.FAKE_SECRET_NAMESPACE)
            self.watcher.secret_manager.get_secret_data.return_value = self.fake_secret_data
            self._prepare_get_secret_info(is_topology_secret)
            self.watcher.secret_manager.add_unique_secret_info_to_list.return_value = [self.fake_secret_info]

    def _prepare_get_secret_info(self, is_topology_secret):
        self.watcher.secret_manager.is_topology_secret.return_value = is_topology_secret
        self.watcher.resource_info_manager.generate_secret_info.return_value = self.fake_secret_info
        if is_topology_secret:
            self.watcher.node_manager.generate_nodes_with_system_id.return_value = self.fake_nodes_with_system_id
            self.watcher.secret_manager.generate_secret_system_ids_topologies.return_value = \
                test_settings.FAKE_SYSTEM_IDS_TOPOLOGIES

    def _assert_called_get_secrets_info_from_storage_class_called(self, is_secret, is_topology_secret=False):
        self.watcher.secret_manager.is_secret.assert_called_once_with(test_settings.STORAGE_CLASS_SECRET_FIELD)
        if is_secret:
            self.watcher.secret_manager.get_secret_name_and_namespace.assert_called_once_with(
                self.fake_storage_class_info, test_settings.STORAGE_CLASS_SECRET_FIELD)
            self.watcher.secret_manager.get_secret_data.assert_called_once_with(
                test_settings.FAKE_SECRET, test_settings.FAKE_SECRET_NAMESPACE)
            self.watcher.secret_manager.is_topology_secret.assert_called_once_with(self.fake_secret_data)
            self.watcher.secret_manager.add_unique_secret_info_to_list.assert_called_once_with(
                self.fake_secret_info, [])
            self._assert_get_secret_info(is_topology_secret)
        else:
            self._assert_get_secret_info_from_parameter_not_called()

    def _assert_get_secret_info(self, is_topology_secret):
        if is_topology_secret:
            self.watcher.node_manager.generate_nodes_with_system_id.assert_called_once_with(self.fake_secret_data)
            self.watcher.secret_manager.generate_secret_system_ids_topologies.assert_called_once_with(
                self.fake_secret_data)
            self.watcher.resource_info_manager.generate_secret_info.assert_called_once_with(
                test_settings.FAKE_SECRET, test_settings.FAKE_SECRET_NAMESPACE, self.fake_nodes_with_system_id,
                test_settings.FAKE_SYSTEM_IDS_TOPOLOGIES)
        else:
            self.watcher.node_manager.generate_nodes_with_system_id.assert_not_called()
            self.watcher.secret_manager.generate_secret_system_ids_topologies.assert_not_called()
            self.watcher.resource_info_manager.generate_secret_info.assert_called_once_with(
                test_settings.FAKE_SECRET, test_settings.FAKE_SECRET_NAMESPACE)

    def _assert_called_get_secrets_info_from_storage_class_not_called(self):
        self.watcher.secret_manager.is_secret.assert_not_called()
        self.watcher.secret_manager.get_secret_name_and_namespace.assert_not_called()
        self.watcher.secret_manager.get_secret_data.assert_not_called()
        self.watcher.secret_manager.is_topology_secret.assert_not_called()
        self.watcher.secret_manager.add_unique_secret_info_to_list.assert_not_called()
        self._assert_get_secret_info_from_parameter_not_called()

    def _assert_get_secret_info_from_parameter_not_called(self):
        self.watcher.secret_manager.get_secret_name_and_namespace.assert_not_called()
        self.watcher.secret_manager.get_secret_data.assert_not_called()
        self.watcher.secret_manager.is_topology_secret.assert_not_called()
        self.watcher.secret_manager.add_unique_secret_info_to_list.assert_not_called()
        self.watcher.node_manager.generate_nodes_with_system_id.assert_not_called()
        self.watcher.secret_manager.generate_secret_system_ids_topologies.assert_not_called()
        self.watcher.resource_info_manager.generate_secret_info.assert_not_called()


class TestAddInitialStorageClasses(StorageClassWatcherBase):

    def test_define_initial_storage_class_with_secret_parameter(self):
        self._prepare_add_initial_storage_classes([self.fake_storage_class_info], True, True, False)
        self.watcher.add_initial_storage_classes()
        self._assert_add_initial_storage_classes(True, True, False)
        self.watcher.storage_class_manager.is_storage_class_has_csi_as_a_provisioner.assert_called_once_with(
            self.fake_storage_class_info)
        self.watcher.definition_manager.define_nodes_when_new_secret.assert_called_once_with(self.fake_secret_info)

    def test_do_not_define_initial_storage_class_with_non_secret_parameter(self):
        self._prepare_add_initial_storage_classes([self.fake_storage_class_info], True, False)
        self.watcher.add_initial_storage_classes()
        self._assert_add_initial_storage_classes(True, False)
        self.watcher.storage_class_manager.is_storage_class_has_csi_as_a_provisioner.assert_called_once_with(
            self.fake_storage_class_info)
        self.watcher.definition_manager.define_nodes_when_new_secret.assert_not_called()

    def test_do_not_define_initial_storage_class_with_no_ibm_block_csi_provisioner(self):
        self._prepare_add_initial_storage_classes([self.fake_storage_class_info], False)
        self.watcher.add_initial_storage_classes()
        self._assert_add_initial_storage_classes(False)
        self.watcher.storage_class_manager.is_storage_class_has_csi_as_a_provisioner.assert_called_once_with(
            self.fake_storage_class_info)
        self.watcher.definition_manager.define_nodes_when_new_secret.assert_not_called()

    def test_define_initial_storage_class_with_secret_topology_parameter(self):
        self._prepare_add_initial_storage_classes([self.fake_storage_class_info], True, True, True)
        self.watcher.add_initial_storage_classes()
        self._assert_add_initial_storage_classes(True, True, True)
        self.watcher.storage_class_manager.is_storage_class_has_csi_as_a_provisioner.assert_called_once_with(
            self.fake_storage_class_info)
        self.watcher.definition_manager.define_nodes_when_new_secret.assert_called_once_with(self.fake_secret_info)

    def test_do_not_define_initial_empty_storage_classes(self):
        self._prepare_add_initial_storage_classes([])
        self.watcher.add_initial_storage_classes()
        self._assert_add_initial_storage_classes(False)
        self.watcher.storage_class_manager.is_storage_class_has_csi_as_a_provisioner.assert_not_called()
        self.watcher.definition_manager.define_nodes_when_new_secret.assert_not_called()

    def _prepare_add_initial_storage_classes(self, storage_classes_info, is_sc_has_csi_provisioner=False,
                                             is_secret=False, is_topology_secret=False):
        self.watcher.resource_info_manager.get_storage_classes_info.return_value = storage_classes_info
        self._prepare_get_secrets_info_from_storage_class_with_driver_provisioner(
            is_sc_has_csi_provisioner, is_secret, is_topology_secret)

    def _assert_add_initial_storage_classes(self, is_sc_has_csi_provisioner=False,
                                            is_secret=False, is_topology_secret=False):
        self.watcher.resource_info_manager.get_storage_classes_info.assert_called_once_with()
        if is_sc_has_csi_provisioner:
            self._assert_called_get_secrets_info_from_storage_class_called(is_secret, is_topology_secret)
        else:
            self._assert_called_get_secrets_info_from_storage_class_not_called()


class TestWatchStorageClassResources(StorageClassWatcherBase):
    def setUp(self):
        super().setUp()
        self.secret_info_with_storage_classes = test_utils.get_fake_secret_info(2)
        self.copy_secret_info_with_storage_classes = deepcopy(self.secret_info_with_storage_classes)
        self.storage_class_added_watch_manifest = test_utils.get_fake_storage_class_watch_event(
            common_settings.ADDED_EVENT_TYPE)
        self.storage_class_added_watch_munch = test_utils.convert_manifest_to_munch(
            self.storage_class_added_watch_manifest)
        self.storage_class_deleted_watch_manifest = test_utils.get_fake_storage_class_watch_event(
            common_settings.DELETED_EVENT_TYPE)
        self.storage_class_deleted_watch_munch = test_utils.convert_manifest_to_munch(
            self.storage_class_deleted_watch_manifest)
        self.global_managed_secrets = patch('{}.MANAGED_SECRETS'.format(test_settings.STORAGE_CLASS_WATCHER_PATH),
                                            [self.copy_secret_info_with_storage_classes]).start()

    @patch('{}.utils'.format(test_settings.STORAGE_CLASS_WATCHER_PATH))
    def test_define_new_storage_class_with_topology_secret_parameter(self, mock_utils):
        mock_utils.munch.return_value = self.storage_class_added_watch_munch
        self._prepare_get_secrets_info_from_storage_class_with_driver_provisioner(True, True, True)
        self._test_watch_storage_class_resources(self.storage_class_added_watch_manifest, mock_utils)
        self._assert_called_get_secrets_info_from_storage_class_called(True, True)
        self.watcher.definition_manager.define_nodes_when_new_secret.assert_called_once_with(self.fake_secret_info)
        self.watcher.secret_manager.get_matching_managed_secret_info.assert_not_called()

    @patch('{}.utils'.format(test_settings.STORAGE_CLASS_WATCHER_PATH))
    def test_define_new_storage_class_with_non_topology_secret_parameter(self, mock_utils):
        mock_utils.munch.return_value = self.storage_class_added_watch_munch
        self._prepare_get_secrets_info_from_storage_class_with_driver_provisioner(True, True, False)
        self._test_watch_storage_class_resources(self.storage_class_added_watch_manifest, mock_utils)
        self._assert_called_get_secrets_info_from_storage_class_called(True, False)
        self.watcher.definition_manager.define_nodes_when_new_secret.assert_called_once_with(self.fake_secret_info)
        self.watcher.secret_manager.get_matching_managed_secret_info.assert_not_called()

    @patch('{}.utils'.format(test_settings.STORAGE_CLASS_WATCHER_PATH))
    def test_do_not_define_new_storage_class_with_non_secret_parameter(self, mock_utils):
        mock_utils.munch.return_value = self.storage_class_added_watch_munch
        self._prepare_get_secrets_info_from_storage_class_with_driver_provisioner(True, False)
        self._test_watch_storage_class_resources(self.storage_class_added_watch_manifest, mock_utils)
        self._assert_called_get_secrets_info_from_storage_class_called(False)
        self.watcher.definition_manager.define_nodes_when_new_secret.assert_not_called()
        self.watcher.secret_manager.get_matching_managed_secret_info.assert_not_called()

    @patch('{}.utils'.format(test_settings.STORAGE_CLASS_WATCHER_PATH))
    def test_undefine_new_storage_class_with_topology_secret_parameter(self, mock_utils):
        self.global_managed_secrets.append(self.secret_info_with_storage_classes)
        mock_utils.munch.return_value = self.storage_class_deleted_watch_munch
        self._prepare_get_secrets_info_from_storage_class_with_driver_provisioner(True, True, True)
        self.watcher.secret_manager.get_matching_managed_secret_info.return_value = (None, 0)
        self._test_watch_storage_class_resources(self.storage_class_deleted_watch_manifest, mock_utils)
        self._assert_called_get_secrets_info_from_storage_class_called(True, True)
        self.watcher.secret_manager.get_matching_managed_secret_info.assert_called_once_with(self.fake_secret_info)
        self.watcher.definition_manager.define_nodes_when_new_secret.assert_not_called()
        self.assertEqual(self.global_managed_secrets[0].managed_storage_classes,
                         self.secret_info_with_storage_classes.managed_storage_classes - 1)

    @patch('{}.utils'.format(test_settings.STORAGE_CLASS_WATCHER_PATH))
    def test_undefine_new_storage_class_with_non_topology_secret_parameter(self, mock_utils):
        self.global_managed_secrets.append(self.secret_info_with_storage_classes)
        mock_utils.munch.return_value = self.storage_class_deleted_watch_munch
        self._prepare_get_secrets_info_from_storage_class_with_driver_provisioner(True, True, False)
        self.watcher.secret_manager.get_matching_managed_secret_info.return_value = (None, 0)
        self._test_watch_storage_class_resources(self.storage_class_deleted_watch_manifest, mock_utils)
        self._assert_called_get_secrets_info_from_storage_class_called(True, False)
        self.watcher.secret_manager.get_matching_managed_secret_info.assert_called_once_with(self.fake_secret_info)
        self.watcher.definition_manager.define_nodes_when_new_secret.assert_not_called()
        self.assertEqual(self.global_managed_secrets[0].managed_storage_classes,
                         self.secret_info_with_storage_classes.managed_storage_classes - 1)

    @patch('{}.utils'.format(test_settings.STORAGE_CLASS_WATCHER_PATH))
    def test_do_not_undefine_new_storage_class_with_non_secret_parameter(self, mock_utils):
        self.global_managed_secrets.append(self.secret_info_with_storage_classes)
        mock_utils.munch.return_value = self.storage_class_deleted_watch_munch
        self._prepare_get_secrets_info_from_storage_class_with_driver_provisioner(True, False)
        self.watcher.secret_manager.get_matching_managed_secret_info.return_value = (None, 0)
        self._test_watch_storage_class_resources(self.storage_class_deleted_watch_manifest, mock_utils)
        self._assert_called_get_secrets_info_from_storage_class_called(False)
        self.watcher.definition_manager.define_nodes_when_new_secret.assert_not_called()
        self.watcher.secret_manager.get_matching_managed_secret_info.assert_not_called()
        self.assertEqual(self.global_managed_secrets[0].managed_storage_classes,
                         self.secret_info_with_storage_classes.managed_storage_classes)

    def _test_watch_storage_class_resources(self, watch_manifest, mock_utils):
        mock_utils.loop_forever.side_effect = [True, False]
        self.watcher.k8s_api.get_storage_class_stream.return_value = iter([watch_manifest])
        self.watcher.resource_info_manager.generate_storage_class_info.return_value = self.fake_storage_class_info
        self.watcher.watch_storage_class_resources()
        mock_utils.munch.assert_called_once_with(watch_manifest)
        self.watcher.storage_class_manager.is_storage_class_has_csi_as_a_provisioner.assert_called_once_with(
            self.fake_storage_class_info)
