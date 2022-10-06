from unittest.mock import Mock, patch

import controllers.tests.controller_server.host_definer.utils as utils
import controllers.tests.controller_server.host_definer.settings as settings
from controllers.tests.controller_server.host_definer.common import BaseSetUp
import controllers.servers.host_definer.messages as messages


class TestAddInitialStorageClasses(BaseSetUp):
    def setUp(self):
        super().setUp()
        self.storage_class_watcher._get_k8s_object_resource_version = Mock()
        self.storage_class_watcher._get_k8s_object_resource_version.return_value = settings.FAKE_RESOURCE_VERSION
        self.storage_class_watcher._create_definition = Mock()

    def test_add_new_storage_class_with_new_secret(self):
        self.storage_class_watcher.storage_api.list_storage_class.return_value = self.fake_k8s_storage_classes_with_ibm_block
        self.mock_secret_ids_on_storage_class_watcher.pop(settings.FAKE_SECRET_ID)
        self.storage_class_watcher.add_initial_storage_classes()
        self.storage_class_watcher._create_definition.assert_called()
        self.assertEqual(1, len(self.mock_secret_ids_on_storage_class_watcher))

    def test_add_new_storage_class_with_existing_secret(self):
        self.storage_class_watcher.storage_api.list_storage_class.return_value = self.fake_k8s_storage_classes_with_ibm_block
        self.storage_class_watcher.add_initial_storage_classes()
        self.storage_class_watcher._create_definition.assert_not_called()
        self.assertEqual(2, self.mock_secret_ids_on_storage_class_watcher[settings.FAKE_SECRET_ID])

    def test_new_storage_class_log_messages(self):
        self.storage_class_watcher.storage_api.list_storage_class.return_value = self.fake_k8s_storage_classes_with_ibm_block
        self.storage_class_watcher.add_initial_storage_classes()
        self.assertIn(messages.NEW_STORAGE_CLASS.format(settings.FAKE_STORAGE_CLASS), self._mock_logger.records)

    def test_add_new_storage_class_without_ibm_csi_provisioner(self):
        self.storage_class_watcher.storage_api.list_storage_class.return_value = self.fake_k8s_storage_classes_without_ibm_block
        self.mock_secret_ids_on_storage_class_watcher.pop(settings.FAKE_SECRET_ID)
        self.storage_class_watcher.add_initial_storage_classes()
        self.storage_class_watcher._create_definition.assert_not_called()

    def test_fail_to_list_storage_classes(self):
        self.storage_class_watcher.storage_api.list_storage_class.side_effect = self.fake_api_exception
        self.storage_class_watcher.add_initial_storage_classes()
        self.storage_class_watcher._create_definition.assert_not_called()
        self.assertIn(messages.FAILED_TO_GET_STORAGE_CLASSES.format(self.http_resp.data), self._mock_logger.records)


class TestWatchStorageClassResources(BaseSetUp):
    def setUp(self):
        super().setUp()
        self.storage_class_watcher._get_k8s_object_resource_version = Mock()
        self.storage_class_watcher._get_k8s_object_resource_version.return_value = settings.FAKE_RESOURCE_VERSION
        self.storage_class_stream = patch('{}.watch.Watch.stream'.format(settings.SECRET_WATCHER_PATH)).start()
        self.storage_class_watcher._create_definition = Mock()
        self.storage_class_watcher._loop_forever = Mock()
        self.storage_class_watcher._loop_forever.side_effect = [True, False]

    def test_add_new_storage_class_with_new_secret(self):
        self.storage_class_stream.return_value = iter([utils.get_fake_secret_storage_event(
            settings.ADDED_EVENT, settings.CSI_PROVISIONER_NAME)])
        self.mock_secret_ids_on_storage_class_watcher.pop(settings.FAKE_SECRET_ID)
        self.storage_class_watcher.watch_storage_class_resources()
        self.storage_class_watcher._create_definition.assert_called()
        self.assertEqual(1, len(self.mock_secret_ids_on_storage_class_watcher))

    def test_add_new_storage_class_with_existing_secret(self):
        self.storage_class_stream.return_value = iter([utils.get_fake_secret_storage_event(
            settings.ADDED_EVENT, settings.CSI_PROVISIONER_NAME)])
        self.storage_class_watcher.watch_storage_class_resources()
        self.storage_class_watcher._create_definition.assert_not_called()
        self.assertEqual(2, self.mock_secret_ids_on_storage_class_watcher[settings.FAKE_SECRET_ID])

    def test_add_new_storage_class_without_ibm_csi_provisioner(self):
        self.storage_class_stream.return_value = iter([utils.get_fake_secret_storage_event(
            settings.ADDED_EVENT, settings.FAKE_CSI_PROVISIONER)])
        self.mock_secret_ids_on_storage_class_watcher.pop(settings.FAKE_SECRET_ID)
        self.storage_class_watcher.watch_storage_class_resources()
        self.storage_class_watcher._create_definition.assert_not_called()

    def test_deleted_managed_storage_class(self):
        self.storage_class_stream.return_value = iter([utils.get_fake_secret_storage_event(
            settings.DELETED_EVENT_TYPE, settings.CSI_PROVISIONER_NAME)])
        self.storage_class_watcher.watch_storage_class_resources()
        self.assertEqual(0, self.mock_secret_ids_on_storage_class_watcher[settings.FAKE_SECRET_ID])
