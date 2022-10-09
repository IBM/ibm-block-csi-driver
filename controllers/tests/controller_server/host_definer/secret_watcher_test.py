from unittest.mock import Mock, patch

import controllers.tests.controller_server.host_definer.utils.utils as utils
import controllers.tests.controller_server.host_definer.settings as settings
from controllers.tests.controller_server.host_definer.common import BaseSetUp
import controllers.servers.host_definer.messages as messages


class TestWatchSecretResources(BaseSetUp):
    def setUp(self):
        super().setUp()
        self.secret_watcher._get_k8s_object_resource_version = Mock()
        self.secret_watcher._get_k8s_object_resource_version.return_value = settings.FAKE_RESOURCE_VERSION
        self.secret_stream = patch('{}.watch.Watch.stream'.format(settings.SECRET_WATCHER_PATH)).start()
        self.secret_watcher._create_definition = Mock()
        self.secret_watcher._loop_forever = Mock()
        self.secret_watcher._loop_forever.side_effect = [True, False]

    def test_create_definitions_managed_secret_was_modified(self):
        self.secret_stream.return_value = iter([utils.get_fake_secret_watch_event(
            settings.MODIFIED_EVENT_TYPE)])
        self.secret_watcher.watch_secret_resources()
        self.secret_watcher._create_definition.assert_called()

    def test_log_message_managed_secret_was_modified(self):
        self.secret_stream.return_value = iter([utils.get_fake_secret_watch_event(
            settings.MODIFIED_EVENT_TYPE)])
        self.secret_watcher.watch_secret_resources()
        self.assertIn(messages.SECRET_HAS_BEEN_MODIFIED.format(settings.FAKE_SECRET,
                      settings.FAKE_SECRET_NAMESPACE), self._mock_logger.records)

    def test_do_nothing_on_deleted_secret_event(self):
        self.secret_stream.return_value = iter([utils.get_fake_secret_watch_event(
            settings.DELETED_EVENT_TYPE)])
        self.secret_watcher.watch_secret_resources()
        self.secret_watcher._create_definition.assert_not_called()

    def test_do_not_create_definitions_when_managed_secret_modified_but_no_managed_nodes(self):
        self.secret_stream.return_value = iter([utils.get_fake_secret_watch_event(
            settings.MODIFIED_EVENT_TYPE)])
        self.mock_nodes_on_watcher_helper.pop(settings.FAKE_NODE_NAME)
        self.secret_watcher.watch_secret_resources()
        self.secret_watcher._create_definition.assert_not_called()

    def test_modified_secret_that_is_not_in_managed_secrets(self):
        self.secret_watcher._handle_storage_class_secret = Mock()
        self.secret_stream.return_value = iter([utils.get_fake_secret_watch_event(
            settings.MODIFIED_EVENT_TYPE)])
        self.mock_secret_ids_on_secret_watcher.pop(settings.FAKE_SECRET_ID)
        self.secret_watcher.watch_secret_resources()
        self.secret_watcher._handle_storage_class_secret.assert_not_called()
