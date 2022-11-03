from unittest.mock import Mock, patch

import controllers.tests.controller_server.host_definer.utils.test_utils as test_utils
import controllers.tests.controller_server.host_definer.settings as settings
from controllers.tests.controller_server.host_definer.common import BaseSetUp
from controllers.servers.host_definer.watcher.secret_watcher import SecretWatcher


class SecretWatcherBase(BaseSetUp):
    def setUp(self):
        super().setUp()
        self.secret_watcher = test_utils.get_class_mock(SecretWatcher)
        self.secret_ids_on_secret_watcher = test_utils.patch_secret_ids_global_variable(
            settings.SECRET_WATCHER_PATH)


class TestWatchSecretResources(SecretWatcherBase):
    def setUp(self):
        super().setUp()
        self.secret_stream = patch('{}.watch.Watch.stream'.format(settings.SECRET_WATCHER_PATH)).start()
        self.secret_watcher._loop_forever = Mock()
        self.secret_watcher._loop_forever.side_effect = [True, False]

    def test_create_definitions_managed_secret_was_modified(self):
        self._prepare_default_mocks_for_secret()
        self.nodes_on_watcher_helper[settings.FAKE_NODE_NAME] = settings.FAKE_NODE_ID
        self.secret_ids_on_secret_watcher[settings.FAKE_SECRET_ID] = 1
        self.secret_watcher.host_definitions_api.get.return_value = \
            test_utils.get_fake_k8s_host_definitions_items('not_ready')
        self.secret_watcher.watch_secret_resources()
        self.secret_watcher.storage_host_servicer.define_host.assert_called()

    def test_ignore_deleted_events(self):
        self._prepare_default_mocks_for_secret()
        self.secret_stream.return_value = iter([test_utils.get_fake_secret_watch_event(
            settings.DELETED_EVENT_TYPE)])
        self.secret_watcher.watch_secret_resources()
        self.secret_watcher.storage_host_servicer.define_host.assert_not_called()

    def test_do_not_create_definitions_when_managed_secret_modified_but_no_managed_nodes(self):
        self._prepare_default_mocks_for_secret()
        self.secret_watcher.watch_secret_resources()
        self.secret_watcher.storage_host_servicer.define_host.assert_not_called()

    def test_modified_secret_that_is_not_in_managed_secrets(self):
        self._prepare_default_mocks_for_secret()
        self.secret_watcher.watch_secret_resources()
        self.secret_watcher.storage_host_servicer.define_host.assert_not_called()

    def _prepare_default_mocks_for_secret(self):
        self.secret_stream.return_value = iter([test_utils.get_fake_secret_watch_event(
            settings.MODIFIED_EVENT_TYPE)])
        self.secret_watcher.host_definitions_api.get.return_value = \
            test_utils.get_fake_k8s_host_definitions_items(settings.READY_PHASE)
        self.secret_watcher.core_api.read_namespaced_secret.return_value = test_utils.get_fake_k8s_secret()