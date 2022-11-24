from unittest.mock import Mock

import controllers.tests.controller_server.host_definer.utils.test_utils as test_utils
import controllers.tests.controller_server.host_definer.settings as test_settings
import controllers.common.settings as common_settings
from controllers.tests.controller_server.host_definer.common import BaseSetUp
from controllers.servers.host_definer.types import DefineHostResponse
from controllers.servers.host_definer.watcher.host_definition_watcher import HostDefinitionWatcher


class HostDefinitionWatcherBase(BaseSetUp):
    def setUp(self):
        super().setUp()
        self.host_definition_watcher = test_utils.get_class_mock(HostDefinitionWatcher)


class TestWatchHostDefinitionsResources(HostDefinitionWatcherBase):
    def setUp(self):
        super().setUp()
        self.host_definition_watcher._get_k8s_object_resource_version = Mock()
        self.host_definition_watcher._get_k8s_object_resource_version.return_value = test_settings.FAKE_RESOURCE_VERSION

    def test_events_on_host_definition_in_ready_state(self):
        self.host_definition_watcher._define_host_definition_after_pending_state = Mock()
        self.host_definition_watcher.host_definitions_api.watch.return_value = iter(
            [test_utils.get_fake_host_definition_watch_event(test_settings.MODIFIED_EVENT_TYPE,
                                                             test_settings.READY_PHASE)])
        test_utils.run_function_with_timeout(self.host_definition_watcher.watch_host_definitions_resources, 0.5)
        self.host_definition_watcher._define_host_definition_after_pending_state.assert_not_called()

    def test_pending_deletion_that_managed_to_be_deleted_log_messages(self):
        self._prepare_default_mocks_for_pending_deletion()
        test_utils.run_function_with_timeout(self.host_definition_watcher.watch_host_definitions_resources, 0.5)
        self.host_definition_watcher.csi_nodes_api.get.assert_called_with(name=test_settings.FAKE_NODE_NAME)

    def test_set_error_event_on_pending_deletion(self):
        self._prepare_default_mocks_for_pending_deletion()
        self.host_definition_watcher.storage_host_servicer.undefine_host.return_value = DefineHostResponse(
            error_message=test_settings.FAIL_MESSAGE_FROM_STORAGE)
        test_utils.patch_pending_variables()
        test_utils.run_function_with_timeout(self.host_definition_watcher.watch_host_definitions_resources, 0.5)
        self.assertEqual(self.host_definition_watcher.storage_host_servicer.undefine_host.call_count,
                         test_settings.HOST_DEFINITION_PENDING_VARS['HOST_DEFINITION_PENDING_RETRIES'])

    def _prepare_default_mocks_for_pending_deletion(self):
        self.host_definition_watcher.host_definitions_api.watch.return_value = iter(
            [test_utils.get_fake_host_definition_watch_event(test_settings.MODIFIED_EVENT_TYPE,
                                                             test_settings.PENDING_DELETION_PHASE)])
        self._prepare_default_mocks_for_pending()
        self.os.getenv.return_value = test_settings.TRUE_STRING
        self.host_definition_watcher.core_api.read_node.return_value = self.k8s_node_with_manage_node_label
        self.host_definition_watcher.storage_host_servicer.undefine_host.return_value = DefineHostResponse()
        self.host_definition_watcher.csi_nodes_api.get.return_value = test_utils.get_fake_k8s_csi_node(
            test_settings.CSI_PROVISIONER_NAME)

    def test_handle_pending_host_definition_that_became_ready(self):
        self._prepare_default_mocks_for_pending_creation()
        self.host_definition_watcher.host_definitions_api.get.return_value = self.ready_k8s_host_definitions
        test_utils.patch_pending_variables()
        test_utils.run_function_with_timeout(self.host_definition_watcher.watch_host_definitions_resources, 0.5)
        self.host_definition_watcher.storage_host_servicer.define_host.assert_called_once_with(
            test_utils.get_define_request())

    def test_pending_creation_that_managed_to_be_created(self):
        self._prepare_default_mocks_for_pending_creation()
        self.host_definition_watcher._loop_forever = Mock()
        self.host_definition_watcher._loop_forever.side_effect = [True, False]
        test_utils.run_function_with_timeout(self.host_definition_watcher.watch_host_definitions_resources, 0.5)
        self.host_definition_watcher.custom_object_api.patch_cluster_custom_object_status.assert_called_once_with(
            common_settings.CSI_IBM_GROUP, common_settings.VERSION,
            common_settings.HOST_DEFINITION_PLURAL, test_settings.FAKE_NODE_NAME,
            test_utils.get_ready_status_manifest())

    def _prepare_default_mocks_for_pending_creation(self):
        self.host_definition_watcher.host_definitions_api.watch.return_value = iter(
            [test_utils.get_fake_host_definition_watch_event(test_settings.MODIFIED_EVENT_TYPE,
                                                             test_settings.PENDING_CREATION_PHASE)])
        self.host_definition_watcher.storage_host_servicer.define_host.return_value = DefineHostResponse()
        self.os.getenv.return_value = ''
        self._prepare_default_mocks_for_pending()

    def _prepare_default_mocks_for_pending(self):
        self.host_definition_watcher.core_api.read_namespaced_secret.return_value = test_utils.get_fake_k8s_secret()
        self.host_definition_watcher.host_definitions_api.get.return_value = \
            test_utils.get_fake_k8s_host_definitions_items(test_settings.PENDING_DELETION_PHASE)
