from unittest.mock import Mock, patch

import controllers.tests.controller_server.host_definer.utils.test_utils as test_utils
import controllers.tests.controller_server.host_definer.settings as test_settings
from controllers.tests.controller_server.host_definer.common import BaseSetUp
from controllers.servers.host_definer.watcher.secret_watcher import SecretWatcher


class SecretWatcherBase(BaseSetUp):
    def setUp(self):
        super().setUp()
        self.secret_watcher = test_utils.get_class_mock(SecretWatcher)


class TestWatchSecretResources(SecretWatcherBase):
    def setUp(self):
        super().setUp()
        self.secret_stream = patch('{}.watch.Watch.stream'.format(test_settings.SECRET_WATCHER_PATH)).start()
        self.secret_watcher._loop_forever = Mock()
        self.secret_watcher._loop_forever.side_effect = [True, False]
        self.secret_watcher.core_api.read_node.return_value = self.k8s_node_with_fake_label

    def test_create_definitions_managed_secret_was_modified(self):
        self._prepare_default_mocks_for_secret()
        self.nodes_on_watcher_helper[test_settings.FAKE_NODE_NAME] = test_settings.FAKE_NODE_ID
        self.managed_secrets_on_watcher_helper.append(test_utils.get_fake_secret_info())
        self.secret_watcher.host_definitions_api.get.return_value = \
            test_utils.get_fake_k8s_host_definitions_items('not_ready')
        self.secret_watcher.watch_secret_resources()
        self.secret_watcher.storage_host_servicer.define_host.assert_called_once_with(
            test_utils.get_define_request(node_id_from_host_definition=test_settings.FAKE_NODE_ID))

    def test_ignore_deleted_events(self):
        self._prepare_default_mocks_for_secret()
        self.secret_stream.return_value = iter([test_utils.get_fake_secret_watch_event(
            test_settings.DELETED_EVENT_TYPE)])
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
            test_settings.MODIFIED_EVENT_TYPE)])
        self.secret_watcher.host_definitions_api.get.return_value = \
            test_utils.get_fake_k8s_host_definitions_items(test_settings.READY_PHASE)
        self.secret_watcher.core_api.read_namespaced_secret.return_value = test_utils.get_fake_k8s_secret()
        self.os.getenv.return_value = ''
