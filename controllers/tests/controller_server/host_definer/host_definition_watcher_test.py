from unittest.mock import Mock

import controllers.tests.controller_server.host_definer.utils as utils
import controllers.tests.controller_server.host_definer.settings as settings
import controllers.servers.host_definer.messages as messages
from controllers.servers.host_definer.types import DefineHostResponse
from controllers.tests.controller_server.host_definer.common import BaseSetUp


class TestWatchHostDefinitionsResources(BaseSetUp):
    def setUp(self):
        super().setUp()
        self.host_definition_watcher._get_k8s_object_resource_version = Mock()
        self.host_definition_watcher._get_k8s_object_resource_version.return_value = settings.FAKE_RESOURCE_VERSION
        self.host_definition_watcher._remove_manage_node_label = Mock()
        self.host_definition_watcher._set_host_definition_status_to_ready = Mock()

    def test_events_on_host_definition_in_ready_state(self):
        self.host_definition_watcher._define_host_definition_after_pending_state = Mock()
        self.host_definition_watcher.host_definitions_api.watch.return_value = iter(
            [utils.get_fake_host_definition_watch_event(settings.MODIFIED_EVENT_TYPE, settings.READY_PHASE)])
        utils.run_function_with_timeout(self.host_definition_watcher.watch_host_definitions_resources, 1)
        self.host_definition_watcher._define_host_definition_after_pending_state.assert_not_called()

    def test_pending_deletion_that_managed_to_be_deleted_log_messages(self):
        self._default_pending_deletion()
        utils.run_function_with_timeout(self.host_definition_watcher.watch_host_definitions_resources, 1)
        self._assert_remove_finalizer_succeded_log_message()
        self.assertIn(messages.DELETE_HOST_DEFINITION.format(
            settings.FAKE_NODE_NAME), self._mock_logger.records)
        self.host_definition_watcher._remove_manage_node_label.assert_called_once()

    def test_pending_deletion_that_host_deleted_on_storage_but_failed_to_delete_host_definition_in_cluster(self):
        self._default_pending_deletion()
        self.host_definition_watcher.host_definitions_api.delete.side_effect = self.fake_api_exception
        utils.run_function_with_timeout(self.host_definition_watcher.watch_host_definitions_resources, 1)
        self._assert_remove_finalizer_succeded_log_message()
        self._assert_fail_to_delete_host_definition_log_message(self.http_resp.data)

    def _assert_remove_finalizer_succeded_log_message(self):
        self.assertIn(messages.REMOVE_FINALIZER_TO_HOST_DEFINITION.format(
            settings.FAKE_NODE_NAME), self._mock_logger.records)

    def test_pending_deletion_that_host_deleted_on_storage_but_failed_to_delete_finalizer(self):
        self._default_pending_deletion()
        self.host_definition_watcher.host_definitions_api.patch.side_effect = self.fake_api_exception
        utils.run_function_with_timeout(self.host_definition_watcher.watch_host_definitions_resources, 1)
        self.assertIn(messages.FAILED_TO_PATCH_HOST_DEFINITION.format(
            settings.FAKE_NODE_NAME, self.http_resp.data), self._mock_logger.records)
        self._assert_fail_to_delete_host_definition_log_message(messages.FAILED_TO_REMOVE_FINALIZER)

    def _assert_fail_to_delete_host_definition_log_message(self, error_message):
        self.assertIn(messages.FAILED_TO_DELETE_HOST_DEFINITION.format(
            settings.FAKE_NODE_NAME, error_message), self._mock_logger.records)

    def test_set_error_event_on_pending_deletion(self):
        self._default_pending_deletion()
        self.host_definition_watcher.storage_host_servicer.undefine_host.return_value = DefineHostResponse(
            error_message=settings.FAIL_MESSAGE_FROM_STORAGE)
        utils.patch_pending_variables()
        utils.run_function_with_timeout(self.host_definition_watcher.watch_host_definitions_resources, 1)
        self.assertIn(messages.SET_HOST_DEFINITION_PHASE_TO_ERROR.format(settings.FAKE_NODE_NAME),
                      self._mock_logger.records)

    def _default_pending_deletion(self):
        self.host_definition_watcher.host_definitions_api.watch.return_value = iter(
            [utils.get_fake_host_definition_watch_event(settings.MODIFIED_EVENT_TYPE, settings.PENDING_DELETION_PHASE)])
        self._default_pending_mocks()
        self.mock_os.getenv.return_value = settings.TRUE_STRING
        self.host_definition_watcher.core_api.read_node.return_value = self.fake_k8s_node_with_manage_node_label
        self.host_definition_watcher.storage_host_servicer.undefine_host.return_value = DefineHostResponse()
        self.host_definition_watcher.csi_nodes_api.get.return_value = self.fake_k8s_csi_node_with_ibm_block

    def test_handle_pending_host_definition_that_became_ready(self):
        self.host_definition_watcher._handle_pending_host_definition = Mock()
        self._default_pending_creation()
        self.host_definition_watcher.host_definitions_api.get.return_value = self.fake_ready_k8s_host_definitions
        utils.run_function_with_timeout(self.host_definition_watcher.watch_host_definitions_resources, 1)
        self.host_definition_watcher._handle_pending_host_definition.assert_called_once()

    def test_pending_creation_that_managed_to_be_created(self):
        self._default_pending_creation()
        utils.run_function_with_timeout(self.host_definition_watcher.watch_host_definitions_resources, 1)
        self.host_definition_watcher._set_host_definition_status_to_ready.assert_called_once()

    def test_set_error_event_on_pending_creation(self):
        self._default_pending_creation()
        self.host_definition_watcher.storage_host_servicer.undefine_host.return_value = DefineHostResponse(
            error_message=settings.FAIL_MESSAGE_FROM_STORAGE)
        utils.patch_pending_variables()
        utils.run_function_with_timeout(self.host_definition_watcher.watch_host_definitions_resources, 1)
        self.assertIn(messages.SET_HOST_DEFINITION_PHASE_TO_ERROR.format(settings.FAKE_NODE_NAME),
                      self._mock_logger.records)

    def _default_pending_creation(self):
        self.host_definition_watcher.host_definitions_api.watch.return_value = iter(
            [utils.get_fake_host_definition_watch_event(settings.MODIFIED_EVENT_TYPE, settings.PENDING_CREATION_PHASE)])
        self.host_definition_watcher.storage_host_servicer.define_host.return_value = DefineHostResponse()
        self._default_pending_mocks()

    def _default_pending_mocks(self):
        self.host_definition_watcher.core_api.read_namespaced_secret.return_value = self.fake_k8s_secret
        self.host_definition_watcher.host_definitions_api.get.return_value = \
            self.fake_pending_deletion_k8s_host_definitions
