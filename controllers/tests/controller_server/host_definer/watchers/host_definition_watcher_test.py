from copy import deepcopy
from unittest.mock import patch, MagicMock

import controllers.tests.controller_server.host_definer.utils.test_utils as test_utils
import controllers.tests.controller_server.host_definer.settings as test_settings
from controllers.tests.controller_server.host_definer.watchers.watcher_base import WatcherBaseSetUp
from controllers.servers.host_definer.watcher.host_definition_watcher import HostDefinitionWatcher


class TestWatchHostDefinitionsResources(WatcherBaseSetUp):
    def setUp(self):
        super().setUp()
        self.watcher = HostDefinitionWatcher()
        self.watcher.k8s_api = MagicMock()
        self.watcher.resource_info_manager = MagicMock()
        self.watcher.host_definition_manager = MagicMock()
        self.watcher.definition_manager = MagicMock()
        self.watcher.node_manager = MagicMock()
        test_utils.patch_pending_variables()
        self.fake_define_response = test_utils.get_fake_define_host_response()
        self.fake_define_response.error_message = ''
        self.fake_action = test_settings.DEFINE_ACTION
        self.fake_host_definition_info = test_utils.get_fake_host_definition_info()
        self.fake_pending_deletion_host_definition_info = deepcopy(self.fake_host_definition_info)
        self.fake_pending_deletion_host_definition_info.phase = test_settings.PENDING_DELETION_PHASE
        self.fake_pending_creation_host_definition_info = deepcopy(self.fake_host_definition_info)
        self.fake_pending_creation_host_definition_info.phase = test_settings.PENDING_CREATION_PHASE
        self.host_definition_deleted_watch_manifest = test_utils.get_fake_host_definition_watch_event(
            test_settings.DELETED_EVENT_TYPE)
        self.host_definition_deleted_watch_munch = test_utils.convert_manifest_to_munch(
            self.host_definition_deleted_watch_manifest)

    @patch('{}.utils'.format(test_settings.HOST_DEFINITION_WATCHER_PATH))
    def test_define_pending_host_definition(self, mock_utils):
        host_definition_info = self.fake_pending_creation_host_definition_info
        self._prepare_watch_host_definition_resources(mock_utils, host_definition_info, True, False)
        self._prepare_define_host_using_exponential_backoff(mock_utils, host_definition_info, [False, True])
        test_utils.run_function_with_timeout(self.watcher.watch_host_definitions_resources, 0.5)
        self._assert_watch_host_definition_resources(mock_utils, host_definition_info, True)
        self._assert_define_host_using_exponential_backoff_called(mock_utils, host_definition_info, 2, False)

    @patch('{}.utils'.format(test_settings.HOST_DEFINITION_WATCHER_PATH))
    def test_undefine_pending_host_definition(self, mock_utils):
        host_definition_info = self.fake_pending_deletion_host_definition_info
        self._prepare_watch_host_definition_resources(mock_utils, host_definition_info, True, False)
        self._prepare_define_host_using_exponential_backoff(mock_utils, host_definition_info, [False, True], True)
        test_utils.run_function_with_timeout(self.watcher.watch_host_definitions_resources, 0.5)
        self._assert_watch_host_definition_resources(mock_utils, host_definition_info, True)
        self._assert_define_host_using_exponential_backoff_called(mock_utils, host_definition_info, 2, False, True)

    @patch('{}.utils'.format(test_settings.HOST_DEFINITION_WATCHER_PATH))
    def test_do_not_handle_pending_deletion_host_definition_that_cannot_be_defined(self, mock_utils):
        host_definition_info = self.fake_pending_deletion_host_definition_info
        self._prepare_watch_host_definition_resources(mock_utils, host_definition_info, True, False)
        self._prepare_define_host_using_exponential_backoff(mock_utils, host_definition_info, [False, True], False)
        test_utils.run_function_with_timeout(self.watcher.watch_host_definitions_resources, 0.5)
        self._assert_watch_host_definition_resources(mock_utils, host_definition_info, True)
        self._assert_define_host_using_exponential_backoff_called(mock_utils, host_definition_info, 2, False, False)

    @patch('{}.utils'.format(test_settings.HOST_DEFINITION_WATCHER_PATH))
    def test_pending_host_definition_phase_to_error(self, mock_utils):
        host_definition_info = self.fake_pending_deletion_host_definition_info
        self._prepare_watch_host_definition_resources(mock_utils, host_definition_info, True, False)
        self._prepare_define_host_using_exponential_backoff(mock_utils, host_definition_info, [False, False, False])
        test_utils.run_function_with_timeout(self.watcher.watch_host_definitions_resources, 0.5)
        self._assert_watch_host_definition_resources(mock_utils, host_definition_info, True)
        self._assert_define_host_using_exponential_backoff_called(mock_utils, host_definition_info, 3, True)

    @patch('{}.utils'.format(test_settings.HOST_DEFINITION_WATCHER_PATH))
    def test_create_event_when_failing_to_undefine_pending_deletion_host_definition(self, mock_utils):
        host_definition_info = self.fake_pending_deletion_host_definition_info
        self.fake_define_response.error_message = test_settings.MESSAGE
        self._prepare_watch_host_definition_resources(mock_utils, host_definition_info, True, False)
        self._prepare_define_host_using_exponential_backoff(mock_utils, host_definition_info, [False, True], True)
        test_utils.run_function_with_timeout(self.watcher.watch_host_definitions_resources, 0.5)
        self._assert_watch_host_definition_resources(mock_utils, host_definition_info, True)
        self._assert_define_host_using_exponential_backoff_called(
            mock_utils, host_definition_info, 2, False, True, True)

    @patch('{}.utils'.format(test_settings.HOST_DEFINITION_WATCHER_PATH))
    def test_do_not_handle_not_pending_host_definition(self, mock_utils):
        host_definition_info = self.fake_host_definition_info
        self._prepare_watch_host_definition_resources(mock_utils, host_definition_info, False)
        test_utils.run_function_with_timeout(self.watcher.watch_host_definitions_resources, 0.5)
        self._assert_watch_host_definition_resources(mock_utils, host_definition_info, False)
        self._assert_define_host_using_exponential_backoff_not_called(mock_utils)

    @patch('{}.utils'.format(test_settings.HOST_DEFINITION_WATCHER_PATH))
    def test_do_not_handle_pending_host_definition_when_event_type_is_deletion(self, mock_utils):
        host_definition_info = self.fake_host_definition_info
        self._prepare_watch_host_definition_resources(mock_utils, host_definition_info, True, True)
        test_utils.run_function_with_timeout(self.watcher.watch_host_definitions_resources, 0.5)
        self._assert_watch_host_definition_resources(mock_utils, host_definition_info, True)
        self._assert_define_host_using_exponential_backoff_not_called(mock_utils)

    def _prepare_watch_host_definition_resources(
            self, mock_utils, host_definition_info, is_host_definition_in_pending_phase,
            is_watch_object_type_is_delete=False):
        self.watcher.k8s_api.list_host_definition.return_value = [host_definition_info]
        mock_utils.get_k8s_object_resource_version.return_value = test_settings.FAKE_RESOURCE_VERSION
        self.watcher.k8s_api.get_host_definition_stream.return_value = iter(
            [self.host_definition_deleted_watch_manifest])
        mock_utils.munch.return_value = self.host_definition_deleted_watch_munch
        self.watcher.resource_info_manager.generate_host_definition_info.return_value = host_definition_info
        self.watcher.host_definition_manager.is_host_definition_in_pending_phase.return_value = \
            is_host_definition_in_pending_phase
        mock_utils.is_watch_object_type_is_delete.return_value = is_watch_object_type_is_delete

    def _prepare_define_host_using_exponential_backoff(
            self, mock_utils, host_definition_info, is_host_definition_not_pending, is_node_can_be_undefined=False):
        self.watcher.host_definition_manager.is_host_definition_not_pending.side_effect = \
            is_host_definition_not_pending
        self._prepare_handle_pending_host_definition(mock_utils, host_definition_info, is_node_can_be_undefined)

    def _prepare_handle_pending_host_definition(self, mock_utils, host_definition_info, is_node_can_be_undefined):
        mock_utils.get_action.return_value = self.fake_action
        if host_definition_info.phase == test_settings.PENDING_CREATION_PHASE:
            self.watcher.definition_manager.define_host_after_pending.return_value = self.fake_define_response
        else:
            self.watcher.node_manager.is_node_can_be_undefined.return_value = is_node_can_be_undefined
            if is_node_can_be_undefined:
                self.watcher.definition_manager.undefine_host_after_pending.return_value = self.fake_define_response

    def _assert_watch_host_definition_resources(
            self, mock_utils, host_definition_info, is_host_definition_in_pending_phase):
        self.watcher.k8s_api.list_host_definition.assert_called_with()
        mock_utils.get_k8s_object_resource_version.assert_called_with([host_definition_info])
        mock_utils.loop_forever.assert_called_with()
        self.watcher.k8s_api.get_host_definition_stream.assert_called_with(test_settings.FAKE_RESOURCE_VERSION, 5)
        mock_utils.munch.assert_called_once_with(self.host_definition_deleted_watch_manifest)
        self.watcher.resource_info_manager.generate_host_definition_info.assert_called_once_with(
            self.host_definition_deleted_watch_munch.object)
        self.watcher.host_definition_manager.is_host_definition_in_pending_phase.assert_called_once_with(
            host_definition_info.phase)
        if is_host_definition_in_pending_phase:
            mock_utils.is_watch_object_type_is_delete.assert_called_once_with(
                self.host_definition_deleted_watch_munch.type)

    def _assert_define_host_using_exponential_backoff_called(
            self, mock_utils, host_definition_info, host_definition_not_pending_call_count, set_phase_to_error=False,
            is_node_can_be_undefined=False, is_error_message=False):
        self.watcher.host_definition_manager.is_host_definition_not_pending.assert_called_with(
            host_definition_info)
        self.assertEqual(self.watcher.host_definition_manager.is_host_definition_not_pending.call_count,
                         host_definition_not_pending_call_count)
        if set_phase_to_error:
            self.watcher.host_definition_manager.set_host_definition_phase_to_error.assert_called_once_with(
                host_definition_info)
        else:
            self._assert_called_handle_pending_host_definition(
                mock_utils, host_definition_info, is_node_can_be_undefined, is_error_message)

    def _assert_called_handle_pending_host_definition(
            self, mock_utils, host_definition_info, is_node_can_be_undefined, is_error_message):
        mock_utils.get_action.assert_called_once_with(host_definition_info.phase)
        if host_definition_info.phase == test_settings.PENDING_CREATION_PHASE:
            self.watcher.definition_manager.define_host_after_pending.assert_called_once_with(host_definition_info)
            self.watcher.node_manager.is_node_can_be_undefined.assert_not_called()
            self.watcher.definition_manager.undefine_host_after_pending.assert_not_called()
        else:
            if is_error_message:
                self.watcher.node_manager.is_node_can_be_undefined.assert_called_once_with(
                    host_definition_info.node_name)
            else:
                self.watcher.node_manager.is_node_can_be_undefined.assert_called_with(host_definition_info.node_name)
                self.assertEqual(self.watcher.node_manager.is_node_can_be_undefined.call_count, 2)

            self.watcher.definition_manager.define_host_after_pending.assert_not_called()
            if is_node_can_be_undefined:
                self.watcher.definition_manager.undefine_host_after_pending.assert_called_once_with(
                    host_definition_info)
            else:
                self.watcher.definition_manager.undefine_host_after_pending.assert_not_called()

        self._assert_handle_message_from_storage(host_definition_info, is_node_can_be_undefined, is_error_message)

    def _assert_handle_message_from_storage(self, host_definition_info, is_node_can_be_undefined, is_error_message):
        if is_error_message:
            self.watcher.host_definition_manager.create_k8s_event_for_host_definition.assert_called_once_with(
                host_definition_info, self.fake_define_response.error_message, self.fake_action,
                test_settings.FAILED_MESSAGE_TYPE)
        elif host_definition_info.phase == test_settings.PENDING_CREATION_PHASE:
            self.watcher.host_definition_manager.set_host_definition_status_to_ready.assert_called_once_with(
                host_definition_info)
        elif is_node_can_be_undefined:
            self.watcher.host_definition_manager.delete_host_definition.assert_called_once_with(
                host_definition_info.name)
            self.watcher.node_manager.remove_manage_node_label.assert_called_once_with(host_definition_info.name)

    def _assert_define_host_using_exponential_backoff_not_called(self, mock_utils):
        self.watcher.host_definition_manager.is_host_definition_not_pending.assert_not_called()
        self.watcher.host_definition_manager.set_host_definition_phase_to_error.assert_not_called()
        self.watcher.host_definition_manager.create_k8s_event_for_host_definition.assert_not_called()
        self.watcher.host_definition_manager.set_host_definition_status_to_ready.assert_not_called()
        self.watcher.host_definition_manager.delete_host_definition.assert_not_called()
        mock_utils.get_action.assert_not_called()
        self.watcher.definition_manager.define_host_after_pending.assert_not_called()
        self.watcher.definition_manager.undefine_host_after_pending.assert_not_called()
        self.watcher.node_manager.remove_manage_node_label.assert_not_called()
        self.watcher.node_manager.is_node_can_be_undefined.assert_not_called()
