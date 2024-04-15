from copy import deepcopy
from unittest.mock import patch, MagicMock

import controllers.common.settings as common_settings
from controllers.servers.host_definer.watcher.secret_watcher import SecretWatcher
import controllers.tests.controller_server.host_definer.utils.test_utils as test_utils
import controllers.tests.controller_server.host_definer.settings as test_settings
from controllers.tests.controller_server.host_definer.watchers.watcher_base import WatcherBaseSetUp


class TestWatchSecretResources(WatcherBaseSetUp):
    def setUp(self):
        super().setUp()
        self.watcher = SecretWatcher()
        self.watcher.k8s_api = MagicMock()
        self.watcher.secret_manager = MagicMock()
        self.watcher.host_definition_manager = MagicMock()
        self.watcher.definition_manager = MagicMock()
        self.watcher.node_manager = MagicMock()
        self.watcher.resource_info_manager = MagicMock()
        self.fake_secret_info = test_utils.get_fake_secret_info()
        self.fake_secret_data = test_utils.get_fake_k8s_secret().data
        self.fake_node_info = test_utils.get_fake_node_info()
        self.fake_host_definition_info = test_utils.get_fake_host_definition_info()
        self.global_managed_secrets = test_utils.patch_managed_secrets_global_variable(
            test_settings.SECRET_WATCHER_PATH)
        self.secret_modified_watch_manifest = test_utils.get_fake_secret_watch_event(
            common_settings.MODIFIED_EVENT_TYPE)
        self.secret_modified_watch_munch = test_utils.convert_manifest_to_munch(self.secret_modified_watch_manifest)
        self.fake_nodes_with_system_id = {self.fake_node_info.name: test_settings.FAKE_SYSTEM_ID}

    @patch('{}.utils'.format(test_settings.SECRET_WATCHER_PATH))
    def test_watch_and_define_changed_topology_secret_topology(self, mock_utils):
        self._prepare_watch_secret_resource(True, mock_utils)
        self._prepare_get_secret_info(True, mock_utils)
        self._prepare_handle_storage_class_secret(2)
        self._test_watch_secret_resources(2, mock_utils)
        self._assert_get_secret_info_called(True, mock_utils)
        self._assert_handle_storage_class_secret_called()
        self.watcher.host_definition_manager.get_host_definition_info_from_secret.assert_called_once_with(
            self.fake_secret_info)
        self.watcher.definition_manager.define_nodes.assert_called_once_with(self.fake_host_definition_info)
        self.assertEqual(self.global_managed_secrets[0], self.fake_secret_info)

    @patch('{}.utils'.format(test_settings.SECRET_WATCHER_PATH))
    def test_watch_and_define_changed_non_topology_secret_topology(self, mock_utils):
        self._prepare_watch_secret_resource(True, mock_utils)
        self._prepare_get_secret_info(False, mock_utils)
        self._prepare_handle_storage_class_secret(2)
        self._test_watch_secret_resources(2, mock_utils)
        self._assert_get_secret_info_called(False, mock_utils)
        self._assert_handle_storage_class_secret_called()
        self.watcher.host_definition_manager.get_host_definition_info_from_secret.assert_called_once_with(
            self.fake_secret_info)
        self.watcher.definition_manager.define_nodes.assert_called_once_with(self.fake_host_definition_info)
        self.assertEqual(self.global_managed_secrets[0], self.fake_secret_info)

    @patch('{}.utils'.format(test_settings.SECRET_WATCHER_PATH))
    def test_watch_and_do_not_define_changed_secret_that_is_not_used_by_storage_class(self, mock_utils):
        self._prepare_watch_secret_resource(True, mock_utils)
        self._prepare_get_secret_info(True, mock_utils)
        self._prepare_handle_storage_class_secret(0)
        self._test_watch_secret_resources(2, mock_utils)
        self._assert_get_secret_info_called(True, mock_utils)
        self._assert_handle_storage_class_secret_called()
        self.watcher.host_definition_manager.get_host_definition_info_from_secret.assert_not_called()
        self.watcher.definition_manager.define_nodes.assert_not_called()

    @patch('{}.utils'.format(test_settings.SECRET_WATCHER_PATH))
    def test_watch_and_do_not_define_unchanged_secret(self, mock_utils):
        self._prepare_watch_secret_resource(False, mock_utils)
        self._test_watch_secret_resources(1, mock_utils)
        self._assert_get_secret_info_not_called(mock_utils)
        self._assert_handle_storage_class_secret_not_called()
        self.watcher.resource_info_manager.generate_k8s_secret_to_secret_info.assert_called_once_with(
            self.secret_modified_watch_munch.object)

    def _prepare_watch_secret_resource(self, is_secret_can_be_changed, mock_utils):
        mock_utils.loop_forever.side_effect = [True, False]
        self.watcher.k8s_api.get_secret_stream.return_value = iter([self.secret_modified_watch_manifest])
        mock_utils.munch.return_value = self.secret_modified_watch_munch
        self.watcher.resource_info_manager.generate_k8s_secret_to_secret_info.return_value = self.fake_secret_info
        self.watcher.secret_manager.is_secret_can_be_changed.return_value = is_secret_can_be_changed

    def _prepare_get_secret_info(self, is_topology_secret, mock_utils):
        mock_utils.change_decode_base64_secret_config.return_value = self.fake_secret_data
        self.watcher.secret_manager.is_topology_secret.return_value = is_topology_secret
        if is_topology_secret:
            self.watcher.node_manager.generate_nodes_with_system_id.return_value = self.fake_nodes_with_system_id
            self.watcher.secret_manager.generate_secret_system_ids_topologies.return_value = \
                test_settings.FAKE_SYSTEM_IDS_TOPOLOGIES
        self.watcher.resource_info_manager.generate_k8s_secret_to_secret_info.return_value = self.fake_secret_info

    def _prepare_handle_storage_class_secret(self, managed_storage_classes):
        secret_info_with_storage_classes = test_utils.get_fake_secret_info(managed_storage_classes)
        copy_secret_info = deepcopy(secret_info_with_storage_classes)
        self.global_managed_secrets.append(copy_secret_info)
        self.watcher.secret_manager.get_matching_managed_secret_info.return_value = (
            secret_info_with_storage_classes, 0)
        if managed_storage_classes > 0:
            self.watcher.host_definition_manager.get_host_definition_info_from_secret.return_value = \
                self.fake_host_definition_info

    def _test_watch_secret_resources(self, generate_secret_info_call_count, mock_utils):
        self.watcher.watch_secret_resources()
        mock_utils.loop_forever.assert_called()
        self.watcher.k8s_api.get_secret_stream.assert_called_once_with()
        mock_utils.munch.assert_called_once_with(self.secret_modified_watch_manifest)
        self.assertEqual(
            self.watcher.resource_info_manager.generate_k8s_secret_to_secret_info.call_count,
            generate_secret_info_call_count)
        self.watcher.secret_manager.is_secret_can_be_changed.assert_called_once_with(
            self.fake_secret_info, self.secret_modified_watch_munch.type)

    def _assert_get_secret_info_called(self, is_topology_secret, mock_utils):
        mock_utils.change_decode_base64_secret_config.assert_called_once_with(
            self.secret_modified_watch_munch.object.data)
        self.watcher.secret_manager.is_topology_secret.assert_called_once_with(self.fake_secret_data)
        if is_topology_secret:
            self.watcher.node_manager.generate_nodes_with_system_id.assert_called_once_with(self.fake_secret_data)
            self.watcher.secret_manager.generate_secret_system_ids_topologies.assert_called_once_with(
                self.fake_secret_data)

    def _assert_get_secret_info_not_called(self, mock_utils):
        mock_utils.change_decode_base64_secret_config.assert_not_called()
        self.watcher.secret_manager.is_topology_secret.assert_not_called()
        self.watcher.node_manager.generate_nodes_with_system_id.assert_not_called()
        self.watcher.secret_manager.generate_secret_system_ids_topologies.assert_not_called()

    def _assert_handle_storage_class_secret_called(self):
        self.watcher.secret_manager.get_matching_managed_secret_info.assert_called_once_with(self.fake_secret_info)

    def _assert_handle_storage_class_secret_not_called(self):
        self.watcher.secret_manager.get_matching_managed_secret_info.assert_not_called()
        self.watcher.host_definition_manager.get_host_definition_info_from_secret.assert_not_called()
        self.watcher.definition_manager.define_nodes.assert_not_called()
