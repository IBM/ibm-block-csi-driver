from unittest.mock import Mock, patch

import controllers.tests.controller_server.host_definer.utils.test_utils as test_utils
import controllers.tests.controller_server.host_definer.settings as settings
from controllers.tests.controller_server.host_definer.common import BaseSetUp
from controllers.servers.host_definer.watcher.storage_class_watcher import StorageClassWatcher


class StorageClassWatcherBase(BaseSetUp):
    def setUp(self):
        super().setUp()
        self.storage_class_watcher = test_utils.get_class_mock(StorageClassWatcher)
        self.managed_secrets_on_storage_class_watcher = test_utils.patch_managed_secrets_global_variable(
            settings.STORAGE_CLASS_WATCHER_PATH)


class TestAddInitialStorageClasses(StorageClassWatcherBase):
    def setUp(self):
        super().setUp()
        self.storage_class_watcher.storage_api.list_storage_class.return_value = \
            test_utils.get_fake_k8s_storage_class_items(settings.CSI_PROVISIONER_NAME)
        self.storage_class_watcher.host_definitions_api.get.return_value = \
            test_utils.get_fake_k8s_host_definitions_items(settings.READY_PHASE)
        self.storage_class_watcher.core_api.read_namespaced_secret.return_value = test_utils.get_fake_k8s_secret()

    def test_add_new_storage_class_with_new_secret(self):
        self.storage_class_watcher.host_definitions_api.get.return_value = \
            test_utils.get_fake_k8s_host_definitions_items('not_ready')
        self.nodes_on_watcher_helper[settings.FAKE_NODE_NAME] = settings.FAKE_NODE_ID
        self.storage_class_watcher.add_initial_storage_classes()
        self.storage_class_watcher.storage_host_servicer.define_host.assert_called()
        self.assertEqual(1, len(self.managed_secrets_on_storage_class_watcher))

    def test_add_new_storage_class_with_existing_secret(self):
        self.managed_secrets_on_storage_class_watcher.append(test_utils.get_fake_secret_info())
        self.managed_secrets_on_watcher_helper.append(test_utils.get_fake_secret_info())
        self.storage_class_watcher.add_initial_storage_classes()
        self.storage_class_watcher.storage_host_servicer.define_host.assert_not_called()
        self.assertEqual(2, self.managed_secrets_on_storage_class_watcher[0].managed_storage_classes)

    def test_add_new_storage_class_without_ibm_csi_provisioner(self):
        self.storage_class_watcher.storage_api.list_storage_class.return_value = \
            test_utils.get_fake_k8s_storage_class_items(settings.FAKE_CSI_PROVISIONER)
        self.storage_class_watcher.add_initial_storage_classes()
        self.assertEqual(0, len(self.managed_secrets_on_storage_class_watcher))
        self.storage_class_watcher.storage_host_servicer.define_host.assert_not_called()


class TestWatchStorageClassResources(StorageClassWatcherBase):
    def setUp(self):
        super().setUp()
        self.storage_class_stream = patch('{}.watch.Watch.stream'.format(settings.SECRET_WATCHER_PATH)).start()
        self.storage_class_stream.return_value = iter([test_utils.get_fake_secret_storage_event(
            settings.ADDED_EVENT, settings.CSI_PROVISIONER_NAME)])
        self.storage_class_watcher.host_definitions_api.get.return_value = \
            test_utils.get_fake_k8s_host_definitions_items(settings.READY_PHASE)
        self.storage_class_watcher.core_api.read_namespaced_secret.return_value = test_utils.get_fake_k8s_secret()
        self.storage_class_watcher._loop_forever = Mock()
        self.storage_class_watcher._loop_forever.side_effect = [True, False]

    def test_add_new_storage_class_with_new_secret(self):
        self.storage_class_watcher.host_definitions_api.get.return_value = \
            test_utils.get_fake_k8s_host_definitions_items('not_ready')
        self.nodes_on_watcher_helper[settings.FAKE_NODE_NAME] = settings.FAKE_NODE_ID
        self.storage_class_watcher.watch_storage_class_resources()
        self.storage_class_watcher.storage_host_servicer.define_host.assert_called()
        self.assertEqual(1, len(self.managed_secrets_on_storage_class_watcher))

    def test_add_new_storage_class_with_existing_secret(self):
        self.managed_secrets_on_storage_class_watcher.append(test_utils.get_fake_secret_info())
        self.managed_secrets_on_watcher_helper.append(test_utils.get_fake_secret_info())
        self.storage_class_watcher.watch_storage_class_resources()
        self.storage_class_watcher.storage_host_servicer.define_host.assert_not_called()
        self.assertEqual(2, self.managed_secrets_on_storage_class_watcher[0].managed_storage_classes)

    def test_add_new_storage_class_without_ibm_csi_provisioner(self):
        self.storage_class_stream.return_value = iter([test_utils.get_fake_secret_storage_event(
            settings.ADDED_EVENT, settings.FAKE_CSI_PROVISIONER)])
        self.storage_class_watcher.watch_storage_class_resources()
        self.assertEqual(0, len(self.managed_secrets_on_storage_class_watcher))
        self.storage_class_watcher.storage_host_servicer.define_host.assert_not_called()

    def test_deleted_managed_storage_class(self):
        self.storage_class_stream.return_value = iter([test_utils.get_fake_secret_storage_event(
            settings.DELETED_EVENT_TYPE, settings.CSI_PROVISIONER_NAME)])
        self.managed_secrets_on_storage_class_watcher.append(test_utils.get_fake_secret_info())
        self.managed_secrets_on_watcher_helper.append(test_utils.get_fake_secret_info())
        self.storage_class_watcher.watch_storage_class_resources()
        self.assertEqual(0, self.managed_secrets_on_storage_class_watcher[0].managed_storage_classes)
