from kubernetes.client.rest import ApiException

import controllers.tests.controller_server.host_definer.utils.test_utils as test_utils
import controllers.tests.controller_server.host_definer.utils.k8s_manifests_utils as k8s_manifests_utils
import controllers.tests.controller_server.host_definer.settings as test_settings
import controllers.common.settings as common_settings
from controllers.tests.controller_server.host_definer.common import BaseSetUp
from controllers.servers.host_definer.types import DefineHostResponse
from controllers.servers.host_definer.watcher.csi_node_watcher import CsiNodeWatcher


class CsiNodeWatcherBase(BaseSetUp):
    def setUp(self):
        super().setUp()
        self.csi_node_watcher = test_utils.get_class_mock(CsiNodeWatcher)
        self.nodes_on_csi_node_watcher = test_utils.patch_nodes_global_variable(test_settings.CSI_NODE_WATCHER_PATH)
        self.updated_daemon_set = test_utils.get_fake_k8s_daemon_set_items(1, 1)
        self.not_updated_daemon_set = test_utils.get_fake_k8s_daemon_set_items(0, 1)
        self.deleted_daemon_set = test_utils.get_fake_k8s_daemon_set_items(0, 0)
        self.secret_ids_on_csi_node_watcher = test_utils.patch_secret_ids_global_variable(
            test_settings.CSI_NODE_WATCHER_PATH)


class TestAddInitialCsiNodes(CsiNodeWatcherBase):
    def test_host_not_defined_for_csi_node_without_ibm_block_provider(self):
        self.csi_node_watcher.csi_nodes_api.get.return_value = test_utils.get_fake_k8s_csi_nodes(
            test_settings.FAKE_CSI_PROVISIONER, 1)
        self.csi_node_watcher.add_initial_csi_nodes()
        self.assertEqual(0, len(self.nodes_on_watcher_helper))

    def test_host_not_defined_for_node_without_labels_and_no_dynamic_labeling(self):
        self._prepare_default_mocks_for_add_node()
        self.os.getenv.return_value = ''
        self.csi_node_watcher.core_api.read_node.return_value = self.k8s_node_with_fake_label
        self.csi_node_watcher.add_initial_csi_nodes()
        self.assertEqual(0, len(self.nodes_on_watcher_helper))

    def test_host_defined_for_node_with_manage_label(self):
        self._prepare_default_mocks_for_add_node()
        self.os.getenv.return_value = ''
        self.csi_node_watcher.add_initial_csi_nodes()
        self.assertEqual(1, len(self.nodes_on_watcher_helper))

    def test_host_defined_for_multiple_nodes_with_dynamic_labeling(self):
        self._prepare_default_mocks_for_add_node()
        self.csi_node_watcher.csi_nodes_api.get.return_value = test_utils.get_fake_k8s_csi_nodes(
            test_settings.CSI_PROVISIONER_NAME, 2)
        self.csi_node_watcher.add_initial_csi_nodes()
        self.assertEqual(2, len(self.nodes_on_watcher_helper))

    def test_add_node_not_update_labels(self):
        self._prepare_default_mocks_for_add_node()
        self.csi_node_watcher.add_initial_csi_nodes()
        self.csi_node_watcher.core_api.patch_node.assert_not_called()
        self.assertEqual(1, len(self.nodes_on_watcher_helper))

    def test_add_node_update_labels(self):
        self._prepare_default_mocks_for_add_node()
        self.csi_node_watcher.core_api.read_node.return_value = self.k8s_node_with_fake_label
        self.csi_node_watcher.add_initial_csi_nodes()
        self.csi_node_watcher.core_api.patch_node.assert_called_once_with(
            test_settings.FAKE_NODE_NAME + '-0',
            k8s_manifests_utils.get_metadata_with_manage_node_labels_manifest(test_settings.TRUE_STRING))
        self.assertEqual(1, len(self.nodes_on_watcher_helper))

    def test_update_node_label_fail(self):
        self._prepare_default_mocks_for_add_node()
        self.csi_node_watcher.core_api.read_node.return_value = self.k8s_node_with_fake_label
        self.csi_node_watcher.core_api.patch_node.side_effect = self.fake_api_exception
        self.csi_node_watcher.add_initial_csi_nodes()
        self.assertEqual(1, len(self.nodes_on_watcher_helper))

    def _prepare_default_mocks_for_add_node(self):
        self.csi_node_watcher.csi_nodes_api.get.return_value = test_utils.get_fake_k8s_csi_nodes(
            test_settings.CSI_PROVISIONER_NAME, 1)
        self.os.getenv.return_value = test_settings.TRUE_STRING
        self.csi_node_watcher.core_api.read_node.return_value = self.k8s_node_with_manage_node_label

    def test_get_csi_nodes_fail(self):
        self.csi_node_watcher.csi_nodes_api.get.side_effect = self.fake_api_exception
        self.csi_node_watcher.add_initial_csi_nodes()
        self.assertEqual(0, len(self.nodes_on_watcher_helper))


class TestWatchCsiNodesResources(CsiNodeWatcherBase):
    def test_updated_csi_node_not_removed(self):
        self.nodes_on_watcher_helper[test_settings.FAKE_NODE_NAME] = test_settings.FAKE_NODE_ID
        self.nodes_on_csi_node_watcher[test_settings.FAKE_NODE_NAME] = test_settings.FAKE_NODE_ID
        self.csi_node_watcher.csi_nodes_api.watch.return_value = iter(
            [test_utils.get_fake_csi_node_watch_event(test_settings.DELETED_EVENT_TYPE)])
        self.csi_node_watcher.core_api.read_node.return_value = self.k8s_node_with_manage_node_label
        self.csi_node_watcher.apps_api.list_daemon_set_for_all_namespaces.side_effect = [
            self.not_updated_daemon_set, self.updated_daemon_set]
        self.csi_node_watcher.core_api.list_pod_for_all_namespaces.return_value = test_utils.get_fake_k8s_pods_items()
        test_utils.run_function_with_timeout(self.csi_node_watcher.watch_csi_nodes_resources, 0.5)
        self.assertEqual(1, len(self.nodes_on_csi_node_watcher))

    def test_delete_host_definition(self):
        self._prepare_default_mocks_for_deletion()
        test_utils.run_function_with_timeout(self.csi_node_watcher.watch_csi_nodes_resources, 0.5)
        self.csi_node_watcher.host_definitions_api.delete.assert_called_once_with(
            name=test_settings.FAKE_NODE_NAME, body={})
        self.assertEqual(0, len(self.nodes_on_csi_node_watcher))

    def test_delete_host_from_storage_failed(self):
        self._prepare_default_mocks_for_deletion()
        self.csi_node_watcher.storage_host_servicer.undefine_host.return_value = DefineHostResponse(
            error_message=test_settings.FAIL_MESSAGE_FROM_STORAGE)
        self.csi_node_watcher.custom_object_api.patch_cluster_custom_object_status.return_value = None
        test_utils.run_function_with_timeout(self.csi_node_watcher.watch_csi_nodes_resources, 0.5)
        self.csi_node_watcher.core_api.create_namespaced_event.assert_called()

    def test_fail_to_get_host_definitions_delete_host_definition_not_called(self):
        self._prepare_default_mocks_for_deletion()
        self.csi_node_watcher.host_definitions_api.get.side_effect = self.fake_api_exception
        test_utils.run_function_with_timeout(self.csi_node_watcher.watch_csi_nodes_resources, 0.5)
        self.assertEqual(0, len(self.nodes_on_csi_node_watcher))
        self.csi_node_watcher.host_definitions_api.delete.assert_not_called()

    def test_remove_manage_node_label(self):
        self._prepare_default_mocks_for_deletion()
        self.csi_node_watcher.csi_nodes_api.get.return_value = test_utils.get_fake_k8s_csi_node(
            test_settings.FAKE_CSI_PROVISIONER)
        self.csi_node_watcher.host_definitions_api.get.return_value = test_utils.get_empty_k8s_host_definitions()
        test_utils.run_function_with_timeout(self.csi_node_watcher.watch_csi_nodes_resources, 0.5)
        self.assertEqual(0, len(self.nodes_on_csi_node_watcher))
        self.csi_node_watcher.core_api.patch_node.assert_called_once_with(
            test_settings.FAKE_NODE_NAME, k8s_manifests_utils.get_metadata_with_manage_node_labels_manifest(None))

    def test_nodes_global_variable_reduced_on_csi_node_deletion_and_definer_cannot_delete(self):
        self._prepare_default_mocks_for_deletion()
        self.os.getenv.return_value = ''
        test_utils.run_function_with_timeout(self.csi_node_watcher.watch_csi_nodes_resources, 0.5)
        self.assertEqual(0, len(self.nodes_on_csi_node_watcher))
        self.csi_node_watcher.storage_host_servicer.undefine_host.assert_not_called()

    def test_nodes_global_variable_reduced_on_failed_daemon_set_list(self):
        self._prepare_default_mocks_for_deletion()
        self.csi_node_watcher.apps_api.list_daemon_set_for_all_namespaces.side_effect = ApiException(
            http_resp=self.http_resp)
        test_utils.run_function_with_timeout(self.csi_node_watcher.watch_csi_nodes_resources, 0.5)
        self.assertEqual(0, len(self.nodes_on_csi_node_watcher))

    def test_failed_pods_list_log_message(self):
        self._prepare_default_mocks_for_deletion()
        self.csi_node_watcher.core_api.list_pod_for_all_namespaces.side_effect = ApiException(
            http_resp=self.http_resp)
        test_utils.run_function_with_timeout(self.csi_node_watcher.watch_csi_nodes_resources, 0.5)
        self.assertEqual(0, len(self.nodes_on_csi_node_watcher))

    def test_csi_node_deleted_with_modify_event(self):
        self._prepare_default_mocks_for_deletion()
        self.csi_node_watcher.csi_nodes_api.watch.return_value = iter(
            [test_utils.get_fake_csi_node_watch_event(test_settings.MODIFIED_EVENT_TYPE)])
        test_utils.run_function_with_timeout(self.csi_node_watcher.watch_csi_nodes_resources, 0.5)
        self.csi_node_watcher.host_definitions_api.delete.assert_called_once_with(
            name=test_settings.FAKE_NODE_NAME, body={})

    def _prepare_default_mocks_for_deletion(self):
        self.nodes_on_watcher_helper[test_settings.FAKE_NODE_NAME] = test_settings.FAKE_NODE_ID
        self.nodes_on_csi_node_watcher[test_settings.FAKE_NODE_NAME] = test_settings.FAKE_NODE_ID
        self.csi_node_watcher.csi_nodes_api.watch.return_value = iter(
            [test_utils.get_fake_csi_node_watch_event(test_settings.DELETED_EVENT_TYPE)])
        self.csi_node_watcher.core_api.read_node.side_effect = [
            self.k8s_node_with_manage_node_label, self.k8s_node_with_fake_label]
        self.csi_node_watcher.apps_api.list_daemon_set_for_all_namespaces.return_value = self.deleted_daemon_set
        self.csi_node_watcher.core_api.list_pod_for_all_namespaces.return_value = test_utils.get_empty_k8s_pods()
        self.os.getenv.return_value = test_settings.TRUE_STRING
        self.csi_node_watcher.storage_host_servicer.undefine_host.return_value = DefineHostResponse()
        self.csi_node_watcher.host_definitions_api.get.return_value = self.ready_k8s_host_definitions
        self.csi_node_watcher.core_api.read_namespaced_secret.return_value = test_utils.get_fake_k8s_secret()
        self.csi_node_watcher.csi_nodes_api.get.return_value = test_utils.get_fake_k8s_csi_node(
            test_settings.CSI_PROVISIONER_NAME)
        self.secret_ids_on_csi_node_watcher[test_settings.FAKE_SECRET_ID] = 1

    def test_do_nothing_new_csi_node_with_already_host_definition_on_cluster(self):
        self._prepare_default_mocks_for_modified_event()
        self.csi_node_watcher.host_definitions_api.get.return_value = self.ready_k8s_host_definitions
        test_utils.run_function_with_timeout(self.csi_node_watcher.watch_csi_nodes_resources, 0.5)
        self.csi_node_watcher.storage_host_servicer.define_host.assert_not_called()

    def test_define_host_called_on_new_csi_node(self):
        self._prepare_default_mocks_for_modified_event()
        self.csi_node_watcher.host_definitions_api.get.side_effect = [
            test_utils.get_empty_k8s_host_definitions(), self.ready_k8s_host_definitions]
        test_utils.run_function_with_timeout(self.csi_node_watcher.watch_csi_nodes_resources, 0.5)
        self.assertEqual(1, len(self.nodes_on_watcher_helper))
        self.csi_node_watcher.storage_host_servicer.define_host.assert_called_once_with(
            test_utils.get_define_request(test_settings.TRUE_STRING))

    def test_define_host_not_called_on_new_csi_node_when_failed_to_get_secret(self):
        self._prepare_default_mocks_for_modified_event()
        self.csi_node_watcher.host_definitions_api.get.side_effect = [
            test_utils.get_empty_k8s_host_definitions(), self.ready_k8s_host_definitions]
        self.csi_node_watcher.core_api.read_namespaced_secret.side_effect = self.fake_api_exception
        test_utils.run_function_with_timeout(self.csi_node_watcher.watch_csi_nodes_resources, 0.5)
        self.csi_node_watcher.storage_host_servicer.define_host.assert_not_called()

    def test_fail_define_host_on_storage(self):
        self._prepare_default_mocks_for_modified_event()
        self.csi_node_watcher.host_definitions_api.get.side_effect = [
            test_utils.get_empty_k8s_host_definitions(), self.ready_k8s_host_definitions]
        self.csi_node_watcher.storage_host_servicer.define_host.return_value = DefineHostResponse(
            error_message=test_settings.FAIL_MESSAGE_FROM_STORAGE)
        test_utils.run_function_with_timeout(self.csi_node_watcher.watch_csi_nodes_resources, 0.5)
        self.csi_node_watcher.custom_object_api.patch_cluster_custom_object_status.assert_called_with(
            common_settings.CSI_IBM_GROUP, common_settings.VERSION,
            common_settings.HOST_DEFINITION_PLURAL, test_settings.FAKE_NODE_NAME,
            test_utils.get_pending_creation_status_manifest())

    def _prepare_default_mocks_for_modified_event(self):
        self.nodes_on_watcher_helper[test_settings.FAKE_NODE_NAME] = test_settings.FAKE_NODE_ID
        self.secret_ids_on_watcher_helper[test_settings.FAKE_SECRET_ID] = 1
        self.csi_node_watcher.csi_nodes_api.watch.return_value = iter(
            [test_utils.get_fake_csi_node_watch_event(test_settings.MODIFIED_EVENT_TYPE)])
        self.os.getenv.return_value = test_settings.TRUE_STRING
        self.csi_node_watcher.core_api.read_namespaced_secret.return_value = test_utils.get_fake_k8s_secret()
        self.csi_node_watcher.storage_host_servicer.define_host.return_value = DefineHostResponse()
