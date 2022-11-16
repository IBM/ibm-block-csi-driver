from unittest.mock import Mock, patch

import controllers.tests.controller_server.host_definer.utils.test_utils as test_utils
import controllers.tests.controller_server.host_definer.settings as test_settings
from controllers.tests.controller_server.host_definer.common import BaseSetUp
from controllers.servers.host_definer.watcher.node_watcher import NodeWatcher


class NodeWatcherBase(BaseSetUp):
    def setUp(self):
        super().setUp()
        self.node_watcher = test_utils.get_class_mock(NodeWatcher)
        self.unmanaged_csi_nodes_with_driver = patch(
            '{}.unmanaged_csi_nodes_with_driver'.format(test_settings.NODES_WATCHER_PATH), set()).start()
        self.expected_unmanaged_csi_nodes_with_driver = set()
        self.expected_unmanaged_csi_nodes_with_driver.add(test_settings.FAKE_NODE_NAME)
        self.nodes_on_node_watcher = test_utils.patch_nodes_global_variable(test_settings.NODES_WATCHER_PATH)


class TestAddInitialNodes(NodeWatcherBase):
    def test_host_definer_does_not_delete_host_definitions_on_node_with_csi_node(self):
        self._prepare_default_mocks_for_node()
        self.node_watcher.add_initial_nodes()
        self.node_watcher.storage_host_servicer.undefine_host.assert_not_called()

    def test_host_definer_deletes_host_definitions_on_node_with_csi_node(self):
        self._prepare_default_mocks_for_node()
        self.node_watcher.csi_nodes_api.get.return_value = test_utils.get_fake_k8s_csi_node(
            test_settings.FAKE_CSI_PROVISIONER)
        self.node_watcher.add_initial_nodes()
        self.node_watcher.storage_host_servicer.undefine_host.assert_not_called()

    def test_if_detect_unmanaged_node_with_csi_node(self):
        self._prepare_default_mocks_for_node()
        self.os.getenv.return_value = ''
        self.node_watcher.core_api.read_node.return_value = self.k8s_node_with_fake_label
        self.node_watcher.add_initial_nodes()
        self.assertEqual(self.expected_unmanaged_csi_nodes_with_driver, self.unmanaged_csi_nodes_with_driver)

    def _prepare_default_mocks_for_node(self):
        self.node_watcher.core_api.list_node.return_value = test_utils.get_fake_k8s_nodes_items()
        self.node_watcher.csi_nodes_api.get.return_value = test_utils.get_fake_k8s_csi_node(
            test_settings.CSI_PROVISIONER_NAME)
        self.node_watcher.core_api.read_node.return_value = self.k8s_node_with_manage_node_label
        self.node_watcher.host_definitions_api.get.return_value = self.ready_k8s_host_definitions
        self.os.getenv.return_value = test_settings.TRUE_STRING


class TestWatchNodesResources(NodeWatcherBase):
    def setUp(self):
        super().setUp()
        self.node_watcher._get_k8s_object_resource_version = Mock()
        self.node_watcher._get_k8s_object_resource_version.return_value = test_settings.FAKE_RESOURCE_VERSION
        self.nodes_stream = patch('{}.watch.Watch.stream'.format(test_settings.NODES_WATCHER_PATH)).start()
        self.node_watcher._loop_forever = Mock()
        self.node_watcher._loop_forever.side_effect = [True, False]

    def test_no_call_for_unmanaged_nodes_list_when_node_is_managed_already(self):
        self._prepare_default_mocks_for_modified_event()
        self.node_watcher.watch_nodes_resources()
        self.expected_unmanaged_csi_nodes_with_driver.clear()
        self.assertEqual(self.expected_unmanaged_csi_nodes_with_driver, self.unmanaged_csi_nodes_with_driver)

    def test_catch_node_with_new_manage_node_label(self):
        self._prepare_default_mocks_for_modified_event()
        self.secret_ids_on_watcher_helper[test_settings.FAKE_SECRET_ID] = 1
        self.node_watcher.watch_nodes_resources()
        self.assertEqual(1, len(self.nodes_on_watcher_helper))
        self.node_watcher.storage_host_servicer.define_host.assert_called_once_with(
            test_utils.get_define_request())
        self.expected_unmanaged_csi_nodes_with_driver.clear()
        self.assertEqual(self.expected_unmanaged_csi_nodes_with_driver, self.unmanaged_csi_nodes_with_driver)

    def test_do_not_create_host_definitions_on_modified_node_without_csi_node(self):
        self._prepare_default_mocks_for_modified_event()
        self.nodes_on_node_watcher[test_settings.FAKE_NODE_NAME] = test_settings.FAKE_NODE_ID
        self.node_watcher.watch_nodes_resources()
        self.node_watcher.storage_host_servicer.define_host.assert_not_called()
        self.assertEqual(self.expected_unmanaged_csi_nodes_with_driver, self.unmanaged_csi_nodes_with_driver)

    def test_do_not_create_host_definitions_on_modified_node_when_dynamic_node_labeling_enabled(self):
        self._prepare_default_mocks_for_modified_event()
        self.os.getenv.return_value = test_settings.TRUE_STRING
        self.node_watcher.watch_nodes_resources()
        self.node_watcher.storage_host_servicer.define_host.assert_not_called()
        self.assertEqual(self.expected_unmanaged_csi_nodes_with_driver, self.unmanaged_csi_nodes_with_driver)

    def test_do_not_create_host_definitions_on_modified_node_with_no_manage_node_label(self):
        self._prepare_default_mocks_for_modified_event()
        self.node_watcher.core_api.read_node.return_value = self.k8s_node_with_fake_label
        self.node_watcher.watch_nodes_resources()
        self.node_watcher.storage_host_servicer.define_host.assert_not_called()
        self.assertEqual(self.expected_unmanaged_csi_nodes_with_driver, self.unmanaged_csi_nodes_with_driver)

    def _prepare_default_mocks_for_modified_event(self):
        self.nodes_stream.return_value = iter([test_utils.get_fake_node_watch_event(
            test_settings.MODIFIED_EVENT_TYPE)])
        self.node_watcher.csi_nodes_api.get.return_value = test_utils.get_fake_k8s_csi_node(
            test_settings.CSI_PROVISIONER_NAME)
        self.os.getenv.return_value = ''
        self.node_watcher.core_api.read_node.return_value = self.k8s_node_with_manage_node_label
        self.node_watcher.host_definitions_api.get.return_value = test_utils.get_empty_k8s_host_definitions()
        self.node_watcher.core_api.read_namespaced_secret.return_value = test_utils.get_fake_k8s_secret()
        self.unmanaged_csi_nodes_with_driver.add(test_settings.FAKE_NODE_NAME)
