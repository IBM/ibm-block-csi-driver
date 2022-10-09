from unittest.mock import Mock
from kubernetes.client.rest import ApiException

import controllers.tests.controller_server.host_definer.utils.utils as utils
import controllers.tests.controller_server.host_definer.settings as settings
from controllers.servers.host_definer.types import DefineHostResponse
import controllers.servers.host_definer.messages as messages
from controllers.tests.controller_server.host_definer.common import BaseSetUp


class TestAddInitialCsiNodes(BaseSetUp):
    def test_is_host_can_be_defined_not_called_when_there_is_csi_node_but_not_with_ibm_block_provider(self):
        self.csi_node_watcher._is_host_can_be_defined = Mock()
        self.csi_node_watcher.csi_nodes_api.get.return_value = self.fake_k8s_csi_nodes_without_ibm_block
        self.csi_node_watcher.add_initial_csi_nodes()
        self.csi_node_watcher._is_host_can_be_defined.assert_not_called()

    def test_add_node_not_called_when_node_do_not_have_labels_and_dynamic_node_labeling_is_disabled(self):
        self.csi_node_watcher._add_node_to_nodes = Mock()
        self.csi_node_watcher.csi_nodes_api.get.return_value = self.fake_k8s_csi_nodes_with_ibm_block
        self.mock_os.getenv.return_value = ''
        self.csi_node_watcher.core_api.read_node.return_value = self.fake_k8s_node_with_fake_label
        self.csi_node_watcher.add_initial_csi_nodes()
        self.csi_node_watcher._add_node_to_nodes.assert_not_called()

    def test_add_node_called_when_node_has_manage_label(self):
        self.csi_node_watcher._add_node_to_nodes = Mock()
        self.csi_node_watcher.csi_nodes_api.get.return_value = self.fake_k8s_csi_nodes_with_ibm_block
        self.mock_os.getenv.return_value = ''
        self.csi_node_watcher.core_api.read_node.return_value = self.fake_k8s_node_with_manage_node_label
        self.csi_node_watcher.add_initial_csi_nodes()
        self.csi_node_watcher._add_node_to_nodes.assert_called()

    def test_add_node_called_when_dynamic_node_labeling_is_enabled(self):
        self.csi_node_watcher._add_node_to_nodes = Mock()
        self.csi_node_watcher.csi_nodes_api.get.return_value = self.fake_k8s_csi_nodes_with_ibm_block
        self.mock_os.getenv.return_value = settings.TRUE_STRING
        self.csi_node_watcher.add_initial_csi_nodes()
        self.csi_node_watcher._add_node_to_nodes.assert_called()

    def test_add_node_log_message(self):
        self.csi_node_watcher.csi_nodes_api.get.return_value = self.fake_k8s_csi_nodes_with_ibm_block
        self.mock_os.getenv.return_value = settings.TRUE_STRING
        self.csi_node_watcher.add_initial_csi_nodes()
        self.assertIn(messages.NEW_KUBERNETES_NODE.format(self.fake_csi_node_info.name), self._mock_logger.records)

    def test_add_node_not_update_labels(self):
        self.csi_node_watcher.csi_nodes_api.get.return_value = self.fake_k8s_csi_nodes_with_ibm_block
        self.mock_os.getenv.return_value = settings.TRUE_STRING
        self.csi_node_watcher.core_api.read_node.return_value = self.fake_k8s_node_with_manage_node_label
        self.csi_node_watcher.add_initial_csi_nodes()
        self.csi_node_watcher.core_api.patch_node.assert_not_called()
        self.assertEqual(1, len(self.mock_nodes_on_watcher_helper))

    def test_add_node_update_labels(self):
        self.csi_node_watcher.csi_nodes_api.get.return_value = self.fake_k8s_csi_nodes_with_ibm_block
        self.mock_os.getenv.return_value = settings.TRUE_STRING
        self.csi_node_watcher.core_api.read_node.return_value = self.fake_k8s_node_with_fake_label
        self.csi_node_watcher.add_initial_csi_nodes()
        self.csi_node_watcher.core_api.patch_node.assert_called()
        self.assertEqual(1, len(self.mock_nodes_on_watcher_helper))

    def test_add_node_update_labels_log_message(self):
        self.csi_node_watcher.csi_nodes_api.get.return_value = self.fake_k8s_csi_nodes_with_ibm_block
        self.mock_os.getenv.return_value = settings.TRUE_STRING
        self.csi_node_watcher.core_api.read_node.return_value = self.fake_k8s_node_with_fake_label
        self.csi_node_watcher.add_initial_csi_nodes()
        self.assertIn(messages.ADD_LABEL_TO_NODE.format(settings.MANAGE_NODE_LABEL,
                      settings.FAKE_NODE_NAME), self._mock_logger.records)

    def test_failed_update_node_label(self):
        self.csi_node_watcher.csi_nodes_api.get.return_value = self.fake_k8s_csi_nodes_with_ibm_block
        self.mock_os.getenv.return_value = settings.TRUE_STRING
        self.csi_node_watcher.core_api.read_node.return_value = self.fake_k8s_node_with_fake_label
        self.csi_node_watcher.core_api.patch_node.side_effect = self.fake_api_exception
        self.csi_node_watcher.add_initial_csi_nodes()
        utils.assert_fail_to_update_label_log_message(self.http_resp.data, self._mock_logger.records)
        self.assertEqual(1, len(self.mock_nodes_on_watcher_helper))

    def test_failed_to_get_csi_nodes(self):
        self.csi_node_watcher.csi_nodes_api.get.side_effect = self.fake_api_exception
        self.csi_node_watcher.add_initial_csi_nodes()
        self.assertIn(messages.FAILED_TO_GET_CSI_NODES.format(self.http_resp.data), self._mock_logger.records)


class TestWatchCsiNodesResources(BaseSetUp):
    def setUp(self):
        super().setUp()
        self.csi_node_watcher._get_k8s_object_resource_version = Mock()
        self.csi_node_watcher._get_k8s_object_resource_version.return_value = settings.FAKE_RESOURCE_VERSION
        self.csi_node_watcher._delete_host_definition = Mock()

    def test_do_nothing_when_csi_node_is_updated(self):
        self.csi_node_watcher.csi_nodes_api.watch.return_value = iter(
            [utils.get_fake_csi_node_watch_event(settings.DELETED_EVENT_TYPE)])
        self.csi_node_watcher.core_api.read_node.return_value = self.fake_k8s_node_with_manage_node_label
        self.csi_node_watcher.apps_api.list_daemon_set_for_all_namespaces.side_effect = [
            self.fake_not_updated_daemon_set, self.fake_updated_daemon_set]
        self.csi_node_watcher.core_api.list_pod_for_all_namespaces.return_value = self.fake_k8s_pods
        utils.run_function_with_timeout(self.csi_node_watcher.watch_csi_nodes_resources, 0.5)
        self.assertEqual(1, len(self.mock_nodes_on_csi_node_watcher))

    def test_delete_host_definition(self):
        self._default_mock_csi_node_pod_deletion()
        utils.run_function_with_timeout(self.csi_node_watcher.watch_csi_nodes_resources, 0.5)
        self.csi_node_watcher._delete_host_definition.assert_called()

    def test_delete_host_definition_undefine_host_log_message(self):
        self._default_mock_csi_node_pod_deletion()
        utils.run_function_with_timeout(self.csi_node_watcher.watch_csi_nodes_resources, 0.5)
        self.assertIn(messages.UNDEFINED_HOST.format(settings.FAKE_NODE_NAME,
                      settings.FAKE_SECRET, settings.FAKE_SECRET_NAMESPACE), self._mock_logger.records)

    def test_nodes_global_variable_reduced_on_csi_node_deletion(self):
        self._default_mock_csi_node_pod_deletion()
        utils.run_function_with_timeout(self.csi_node_watcher.watch_csi_nodes_resources, 0.5)
        self.assertEqual(0, len(self.mock_nodes_on_csi_node_watcher))

    def test_fail_to_delete_host_from_storage(self):
        self._default_mock_csi_node_pod_deletion()
        self.csi_node_watcher.storage_host_servicer.undefine_host.return_value = DefineHostResponse(
            error_message=settings.FAIL_MESSAGE_FROM_STORAGE)
        self.csi_node_watcher.custom_object_api.patch_cluster_custom_object_status.return_value = None
        utils.run_function_with_timeout(self.csi_node_watcher.watch_csi_nodes_resources, 0.5)
        self.csi_node_watcher.core_api.create_namespaced_event.assert_called()

    def test_pending_deletion_log_message(self):
        self._default_mock_csi_node_pod_deletion()
        self.csi_node_watcher.storage_host_servicer.undefine_host.return_value = DefineHostResponse(
            error_message=settings.FAIL_MESSAGE_FROM_STORAGE)
        self.csi_node_watcher.custom_object_api.patch_cluster_custom_object_status.return_value = None
        utils.run_function_with_timeout(self.csi_node_watcher.watch_csi_nodes_resources, 0.5)
        self._assert_set_host_definition_status_log_message(settings.PENDING_DELETION_PHASE)

    def _assert_set_host_definition_status_log_message(self, host_definition_phase):
        self.assertIn(messages.SET_HOST_DEFINITION_STATUS.format(settings.FAKE_NODE_NAME,
                      host_definition_phase), self._mock_logger.records)

    def test_fail_set_host_definition_status_log_message(self):
        self._test_fail_to_set_host_definition(self.http_resp)
        self.assertIn(messages.FAILED_TO_SET_HOST_DEFINITION_STATUS.format(
            settings.FAKE_NODE_NAME, self.http_resp.data), self._mock_logger.records)

    def test_fail_set_not_existing_host_definition_status_log_message(self):
        http_resp = self.http_resp
        http_resp.status = 404
        self._test_fail_to_set_host_definition(http_resp)
        self.assertIn(messages.HOST_DEFINITION_DOES_NOT_EXIST.format(
            settings.FAKE_NODE_NAME), self._mock_logger.records)

    def _test_fail_to_set_host_definition(self, http_resp):
        self._default_mock_csi_node_pod_deletion()
        self.csi_node_watcher.storage_host_servicer.undefine_host.return_value = DefineHostResponse(
            error_message=settings.FAIL_MESSAGE_FROM_STORAGE)
        self.csi_node_watcher.custom_object_api.patch_cluster_custom_object_status.side_effect = ApiException(
            http_resp=http_resp)
        utils.run_function_with_timeout(self.csi_node_watcher.watch_csi_nodes_resources, 0.5)

    def test_fail_create_event_log_message(self):
        self._default_mock_csi_node_pod_deletion()
        self.csi_node_watcher.storage_host_servicer.undefine_host.return_value = DefineHostResponse(
            error_message=settings.FAIL_MESSAGE_FROM_STORAGE)
        self.csi_node_watcher.core_api.create_namespaced_event.side_effect = self.fake_api_exception
        utils.run_function_with_timeout(self.csi_node_watcher.watch_csi_nodes_resources, 0.5)
        self.assertIn(messages.FAILED_TO_CREATE_EVENT_FOR_HOST_DEFINITION.format(
            settings.FAKE_NODE_NAME, self.http_resp.data), self._mock_logger.records)

    def test_fail_to_get_host_definitions_delete_host_definition_not_called(self):
        self._default_mock_csi_node_pod_deletion()
        self.csi_node_watcher.host_definitions_api.get.side_effect = self.fake_api_exception
        utils.run_function_with_timeout(self.csi_node_watcher.watch_csi_nodes_resources, 0.5)
        self.csi_node_watcher._delete_host_definition.assert_not_called()

    def test_fail_to_get_host_definitions_log_message(self):
        self._default_mock_csi_node_pod_deletion()
        self.csi_node_watcher.host_definitions_api.get.side_effect = self.fake_api_exception
        utils.run_function_with_timeout(self.csi_node_watcher.watch_csi_nodes_resources, 0.5)
        self.assertIn(messages.FAILED_TO_GET_LIST_OF_HOST_DEFINITIONS.format(
            self.http_resp.data), self._mock_logger.records)

    def test_remove_manage_node_label(self):
        self._default_mock_csi_node_pod_deletion()
        self.csi_node_watcher.csi_nodes_api.get.return_value = self.fake_k8s_csi_node_without_ibm_block
        self.csi_node_watcher.host_definitions_api.get.return_value = self.fake_empty_k8s_host_definitions
        utils.run_function_with_timeout(self.csi_node_watcher.watch_csi_nodes_resources, 0.5)
        self.csi_node_watcher.core_api.patch_node.assert_called()

    def test_fail_remove_manage_node_label(self):
        self._default_mock_csi_node_pod_deletion()
        self.csi_node_watcher.csi_nodes_api.get.return_value = self.fake_k8s_csi_node_without_ibm_block
        self.csi_node_watcher.host_definitions_api.get.return_value = self.fake_empty_k8s_host_definitions
        self.csi_node_watcher.core_api.patch_node.side_effect = self.fake_api_exception
        utils.run_function_with_timeout(self.csi_node_watcher.watch_csi_nodes_resources, 0.5)
        utils.assert_fail_to_update_label_log_message(self.http_resp.data, self._mock_logger.records)
        self.assertIn(messages.REMOVE_LABEL_FROM_NODE.format(
            settings.MANAGE_NODE_LABEL, settings.FAKE_NODE_NAME), self._mock_logger.records)

    def test_nodes_global_variable_reduced_on_csi_node_deletion_and_definer_cannot_delete(self):
        self._default_mock_csi_node_pod_deletion()
        self.mock_os.getenv.return_value = ''
        utils.run_function_with_timeout(self.csi_node_watcher.watch_csi_nodes_resources, 0.5)
        self.assertEqual(0, len(self.mock_nodes_on_csi_node_watcher))
        self.csi_node_watcher.storage_host_servicer.undefine_host.assert_not_called()

    def test_nodes_global_variable_reduced_on_failed_daemon_set_list(self):
        self._default_mock_csi_node_pod_deletion()
        self.csi_node_watcher.apps_api.list_daemon_set_for_all_namespaces.side_effect = ApiException(
            http_resp=self.http_resp)
        utils.run_function_with_timeout(self.csi_node_watcher.watch_csi_nodes_resources, 0.5)
        self.assertIn(messages.FAILED_TO_LIST_DAEMON_SETS.format(self.http_resp.data), self._mock_logger.records)
        self.assertEqual(0, len(self.mock_nodes_on_csi_node_watcher))

    def test_failed_pods_list_log_message(self):
        self._default_mock_csi_node_pod_deletion()
        self.csi_node_watcher.core_api.list_pod_for_all_namespaces.side_effect = ApiException(
            http_resp=self.http_resp)
        utils.run_function_with_timeout(self.csi_node_watcher.watch_csi_nodes_resources, 0.5)
        self.assertIn(messages.FAILED_TO_LIST_PODS.format(self.http_resp.data), self._mock_logger.records)
        self.assertEqual(0, len(self.mock_nodes_on_csi_node_watcher))

    def test_csi_node_deleted_with_modify_event(self):
        self._default_mock_csi_node_pod_deletion()
        self.csi_node_watcher.csi_nodes_api.watch.return_value = iter(
            [utils.get_fake_csi_node_watch_event(settings.MODIFIED_EVENT_TYPE)])
        utils.run_function_with_timeout(self.csi_node_watcher.watch_csi_nodes_resources, 0.5)
        self.csi_node_watcher._delete_host_definition.assert_called()

    def _default_mock_csi_node_pod_deletion(self):
        self.csi_node_watcher.csi_nodes_api.watch.return_value = iter(
            [utils.get_fake_csi_node_watch_event(settings.DELETED_EVENT_TYPE)])
        self.csi_node_watcher.core_api.read_node.side_effect = [
            self.fake_k8s_node_with_manage_node_label, self.fake_k8s_node_with_fake_label]
        self.csi_node_watcher.apps_api.list_daemon_set_for_all_namespaces.return_value = self.fake_deleted_daemon_set
        self.csi_node_watcher.core_api.list_pod_for_all_namespaces.return_value = self.no_k8s_pods
        self.mock_os.getenv.return_value = settings.TRUE_STRING
        self.csi_node_watcher.storage_host_servicer.undefine_host.return_value = DefineHostResponse()
        self.csi_node_watcher.host_definitions_api.get.return_value = self.fake_ready_k8s_host_definitions
        self.csi_node_watcher.core_api.read_namespaced_secret.return_value = self.fake_k8s_secret
        self.csi_node_watcher.csi_nodes_api.get.return_value = self.fake_k8s_csi_node_with_ibm_block

    def test_do_nothing_new_csi_node_with_already_host_definition_on_cluster(self):
        self._default_mock_modified_event()
        self.csi_node_watcher.host_definitions_api.get.return_value = self.fake_ready_k8s_host_definitions
        utils.run_function_with_timeout(self.csi_node_watcher.watch_csi_nodes_resources, 0.5)
        self.csi_node_watcher.storage_host_servicer.define_host.assert_not_called()

    def test_define_host_called_on_new_csi_node(self):
        self._default_mock_modified_event()
        self.csi_node_watcher.host_definitions_api.get.side_effect = [
            self.fake_empty_k8s_host_definitions, self.fake_ready_k8s_host_definitions]
        utils.run_function_with_timeout(self.csi_node_watcher.watch_csi_nodes_resources, 0.5)
        self.csi_node_watcher.storage_host_servicer.define_host.assert_called()

    def test_define_host_not_called_on_new_csi_node_when_failed_to_get_secret(self):
        self._default_mock_modified_event()
        self.csi_node_watcher.host_definitions_api.get.side_effect = [
            self.fake_empty_k8s_host_definitions, self.fake_ready_k8s_host_definitions]
        self.csi_node_watcher.core_api.read_namespaced_secret.side_effect = self.fake_api_exception
        utils.run_function_with_timeout(self.csi_node_watcher.watch_csi_nodes_resources, 0.5)
        self.csi_node_watcher.storage_host_servicer.define_host.assert_not_called()

    def test_log_message_for_general_fail_while_trying_to_get_secret(self):
        self._test_fail_to_get_secret_secret(self.http_resp)
        self.assertIn(messages.FAILED_TO_GET_SECRET.format(
            settings.FAKE_SECRET, settings.FAKE_SECRET_NAMESPACE, self.http_resp.data), self._mock_logger.records)

    def test_log_message_for_general_fail_while_for_not_exists_secret(self):
        http_resp = self.http_resp
        http_resp.status = 404
        self._test_fail_to_get_secret_secret(http_resp)
        self.assertIn(messages.SECRET_DOES_NOT_EXIST.format(
            settings.FAKE_SECRET, settings.FAKE_SECRET_NAMESPACE), self._mock_logger.records)

    def _test_fail_to_get_secret_secret(self, http_resp):
        self._default_mock_modified_event()
        self.csi_node_watcher.host_definitions_api.get.side_effect = [
            self.fake_empty_k8s_host_definitions, self.fake_ready_k8s_host_definitions]
        self.csi_node_watcher.core_api.read_namespaced_secret.side_effect = ApiException(http_resp=http_resp)
        utils.run_function_with_timeout(self.csi_node_watcher.watch_csi_nodes_resources, 0.5)

    def test_fail_define_host_on_storage_log_message(self):
        self.csi_node_watcher._set_host_definition_status_to_ready = Mock()
        self._default_mock_modified_event()
        self.csi_node_watcher.host_definitions_api.get.side_effect = [
            self.fake_empty_k8s_host_definitions, self.fake_ready_k8s_host_definitions]
        self.csi_node_watcher.storage_host_servicer.define_host.return_value = DefineHostResponse(
            error_message=settings.FAIL_MESSAGE_FROM_STORAGE)
        utils.run_function_with_timeout(self.csi_node_watcher.watch_csi_nodes_resources, 0.5)
        self._assert_set_host_definition_status_log_message(settings.PENDING_CREATION_PHASE)
        self._assert_create_new_event_log_message(settings.FAIL_MESSAGE_FROM_STORAGE)
        self.csi_node_watcher._set_host_definition_status_to_ready.assert_not_called()

    def test_successful_host_creation_on_storage(self):
        self._default_mock_modified_event()
        self.csi_node_watcher.host_definitions_api.get.side_effect = [
            self.fake_empty_k8s_host_definitions, self.fake_ready_k8s_host_definitions]
        utils.run_function_with_timeout(self.csi_node_watcher.watch_csi_nodes_resources, 0.5)
        self._assert_set_host_definition_status_log_message(settings.READY_PHASE)
        self._assert_create_new_event_log_message(settings.SUCCESS_MESSAGE)

    def test_fail_to_create_host_definition_after_successful_host_creation_on_storage(self):
        self.csi_node_watcher._get_host_definition_name = Mock()
        self._default_mock_modified_event()
        self.csi_node_watcher.host_definitions_api.get.return_value = self.fake_empty_k8s_host_definitions
        self.csi_node_watcher._get_host_definition_name.return_value = settings.FAKE_NODE_NAME
        self.csi_node_watcher.host_definitions_api.create.side_effect = self.fake_api_exception
        utils.run_function_with_timeout(self.csi_node_watcher.watch_csi_nodes_resources, 0.5)
        self.assertIn(messages.CREATING_NEW_HOST_DEFINITION.format(settings.FAKE_NODE_NAME), self._mock_logger.records)
        self.assertIn(messages.FAILED_TO_CREATE_HOST_DEFINITION.format(
            settings.FAKE_NODE_NAME, self.http_resp.data), self._mock_logger.records)

    def _assert_create_new_event_log_message(self, event_message):
        self.assertIn(messages.CREATE_EVENT_FOR_HOST_DEFINITION.format(
            event_message, settings.FAKE_NODE_NAME), self._mock_logger.records)

    def _default_mock_modified_event(self):
        self.mock_nodes_on_csi_node_watcher.pop(settings.FAKE_NODE_NAME)
        self.csi_node_watcher.csi_nodes_api.watch.return_value = iter(
            [utils.get_fake_csi_node_watch_event(settings.MODIFIED_EVENT_TYPE)])
        self.mock_os.getenv.return_value = settings.TRUE_STRING
        self.csi_node_watcher.core_api.read_namespaced_secret.return_value = self.fake_k8s_secret
        self.csi_node_watcher.storage_host_servicer.define_host.return_value = DefineHostResponse()
