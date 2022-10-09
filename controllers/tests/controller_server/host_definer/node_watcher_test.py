from unittest.mock import Mock, patch
from kubernetes.client.rest import ApiException

import controllers.servers.host_definer.messages as messages
import controllers.tests.controller_server.host_definer.utils as utils
import controllers.tests.controller_server.host_definer.settings as settings
from controllers.tests.controller_server.host_definer.common import BaseSetUp


class TestAddInitialNodes(BaseSetUp):
    def setUp(self):
        super().setUp()
        self.node_watcher._remove_manage_node_label = Mock()
        self.node_watcher._delete_definition = Mock()

    def test_host_definer_does_not_delete_host_definitions_on_node_with_csi_node(self):
        self._default_node_mocks()
        self.node_watcher.add_initial_nodes()
        self.node_watcher._delete_definition.assert_not_called()

    def test_host_definer_deletes_host_definitions_on_node_with_csi_node(self):
        self._default_node_mocks()
        self.node_watcher.csi_nodes_api.get.return_value = self.fake_k8s_csi_node_without_ibm_block
        self.node_watcher.add_initial_nodes()
        self.node_watcher._delete_definition.assert_called()

    def test_if_detect_unmanaged_node_with_csi_node(self):
        self._default_node_mocks()
        self.mock_os.getenv.return_value = ''
        self.node_watcher.core_api.read_node.return_value = self.fake_k8s_node_with_fake_label
        self.node_watcher.add_initial_nodes()
        self.node_watcher.unmanaged_csi_nodes_with_driver.add.assert_called()

    def test_fail_to_read_nodes(self):
        self._default_node_mocks()
        self.node_watcher.core_api.read_node.side_effect = self.fake_api_exception
        self.node_watcher.add_initial_nodes()
        self.assertIn(messages.FAILED_TO_GET_NODE.format(settings.FAKE_NODE_NAME, self.http_resp.data), self._mock_logger.records)

    def test_fail_to_get_nodes(self):
        self.node_watcher.core_api.list_node.side_effect = self.fake_api_exception
        self.node_watcher.add_initial_nodes()
        self.assertIn(messages.FAILED_TO_GET_NODES.format(self.http_resp.data), self._mock_logger.records)

    def test_fail_to_get_csi_node_info(self):
        self._mock_fail_to_get_csi_node_info(self.http_resp)
        self.assertIn(messages.FAILED_TO_GET_CSI_NODE.format(settings.FAKE_NODE_NAME,
                      self.http_resp.data), self._mock_logger.records)

    def test_fail_to_get_csi_node_info_because_csi_node_does_not_exist(self):
        http_resp = self.http_resp
        http_resp.status = 404
        self._mock_fail_to_get_csi_node_info(http_resp)
        self.assertIn(messages.CSI_NODE_DOES_NOT_EXIST.format(settings.FAKE_NODE_NAME), self._mock_logger.records)

    def _default_node_mocks(self):
        self.node_watcher.core_api.list_node.return_value = self.fake_k8s_nodes
        self.node_watcher.csi_nodes_api.get.return_value = self.fake_k8s_csi_node_with_ibm_block
        self.node_watcher.core_api.read_node.return_value = self.fake_k8s_node_with_manage_node_label
        self.node_watcher.host_definitions_api.get.return_value = self.fake_ready_k8s_host_definitions
        self.mock_os.getenv.return_value = settings.TRUE_STRING

    def _mock_fail_to_get_csi_node_info(self, http_resp):
        self._default_node_mocks()
        self.node_watcher.core_api.read_node.return_value = self.fake_k8s_node_with_fake_label
        self.node_watcher.csi_nodes_api.get.side_effect = ApiException(http_resp=http_resp)
        self.node_watcher.add_initial_nodes()
        self.node_watcher.unmanaged_csi_nodes_with_driver.add.assert_not_called()


class TestWatchNodesResources(BaseSetUp):
    def setUp(self):
        super().setUp()
        self.node_watcher._get_k8s_object_resource_version = Mock()
        self.node_watcher._get_k8s_object_resource_version.return_value = settings.FAKE_RESOURCE_VERSION
        self.nodes_stream = patch('{}.watch.Watch.stream'.format(settings.NODES_WATCHER_PATH)).start()
        self.node_watcher._is_node_with_csi_ibm_csi_node_and_is_not_managed = Mock()
        self.node_watcher._create_definition = Mock()
        self.node_watcher._loop_forever = Mock()
        self.node_watcher._loop_forever.side_effect = [True, False]

    def test_no_call_for_unmanaged_nodes_list_when_node_is_managed_already(self):
        self._default_modified_event_mocks()
        self.node_watcher._is_host_can_be_defined = Mock()
        self.node_watcher._is_host_can_be_defined.return_value = True
        self.node_watcher.watch_nodes_resources()
        self.node_watcher.unmanaged_csi_nodes_with_driver.add.assert_not_called()

    def test_catch_node_with_new_manage_node_label(self):
        self._default_modified_event_mocks()
        self.node_watcher.watch_nodes_resources()
        self.assertEqual(1, len(self.mock_nodes_on_watcher_helper))
        self.node_watcher._create_definition.assert_called()
        self.node_watcher.unmanaged_csi_nodes_with_driver.remove.assert_called()

    def test_do_not_create_host_definitions_on_modified_node_without_csi_node(self):
        self._default_modified_event_mocks()
        self.node_watcher._is_node_with_csi_ibm_csi_node_and_is_not_managed.return_value = False
        self.node_watcher.watch_nodes_resources()
        self.node_watcher._create_definition.assert_not_called()
        self.node_watcher.unmanaged_csi_nodes_with_driver.remove.assert_not_called()

    def test_do_not_create_host_definitions_on_modified_node_when_dynamic_node_labeling_enabled(self):
        self._default_modified_event_mocks()
        self.mock_os.getenv.return_value = settings.TRUE_STRING
        self.node_watcher.watch_nodes_resources()
        self.node_watcher._create_definition.assert_not_called()
        self.node_watcher.unmanaged_csi_nodes_with_driver.remove.assert_not_called()

    def test_do_not_create_host_definitions_on_modified_node_with_no_manage_node_label(self):
        self._default_modified_event_mocks()
        self.node_watcher.core_api.read_node.return_value = self.fake_k8s_node_with_fake_label
        self.node_watcher.watch_nodes_resources()
        self.node_watcher._create_definition.assert_not_called()
        self.node_watcher.unmanaged_csi_nodes_with_driver.remove.assert_not_called()

    def _default_modified_event_mocks(self):
        self.nodes_stream.return_value = iter([utils.get_fake_node_watch_event(
            settings.MODIFIED_EVENT_TYPE)])
        self.node_watcher.csi_nodes_api.get.return_value = self.fake_k8s_csi_node_with_ibm_block
        self.mock_os.getenv.return_value = ''
        self.node_watcher.core_api.read_node.return_value = self.fake_k8s_node_with_manage_node_label
        self.node_watcher._is_node_with_csi_ibm_csi_node_and_is_not_managed.return_value = True
