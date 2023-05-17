from unittest.mock import MagicMock
from copy import deepcopy

from controllers.tests.controller_server.host_definer.resource_manager.base_resource_manager import BaseResourceManager
from controllers.servers.host_definer.resource_manager.csi_node import CSINodeManager
import controllers.tests.controller_server.host_definer.utils.test_utils as test_utils
import controllers.tests.controller_server.host_definer.settings as test_settings
import controllers.common.settings as common_settings


class TestCSINodeManager(BaseResourceManager):
    def setUp(self):
        super().setUp()
        self.csi_node = CSINodeManager()
        self.csi_node.k8s_api = MagicMock()
        self.csi_node.daemon_set_manager = MagicMock()
        self.csi_node.resource_info_manager = MagicMock()
        self.fake_csi_node_info = test_utils.get_fake_csi_node_info()
        self.fake_pod_info = test_utils.get_fake_pod_info()
        self.fake_k8s_csi_nodes_with_ibm_driver = test_utils.get_fake_k8s_csi_nodes(
            common_settings.CSI_PROVISIONER_NAME, 1)
        self.fake_k8s_csi_nodes_with_non_ibm_driver = test_utils.get_fake_k8s_csi_nodes(
            test_settings.FAKE_CSI_PROVISIONER, 1)

    def test_get_csi_nodes_info_with_driver_success(self):
        self._test_get_k8s_resources_info_success(
            self.csi_node.get_csi_nodes_info_with_driver, self.csi_node.k8s_api.list_csi_node,
            self.csi_node.resource_info_manager.generate_csi_node_info, self.fake_csi_node_info,
            self.fake_k8s_csi_nodes_with_ibm_driver)

    def test_get_csi_nodes_info_with_driver_empty_list_success(self):
        self._test_get_k8s_resources_info_empty_list_success(
            self.csi_node.get_csi_nodes_info_with_driver, self.csi_node.k8s_api.list_csi_node,
            self.csi_node.resource_info_manager.generate_csi_node_info)

    def test_get_csi_nodes_info_with_driver_non_ibm_driver_success(self):
        self.csi_node.k8s_api.list_csi_node.return_value = self.fake_k8s_csi_nodes_with_non_ibm_driver
        self.csi_node.resource_info_manager.generate_csi_node_info.return_value = self.fake_csi_node_info
        result = self.csi_node.get_csi_nodes_info_with_driver()
        self.assertEqual(result, [])
        self.csi_node.resource_info_manager.generate_csi_node_info.assert_not_called()

    def test_host_is_part_of_update_when_the_node_has_matching_csi_node_pod(self):
        self._test_is_host_part_of_update(True, test_settings.FAKE_DAEMON_SET_NAME, [self.fake_pod_info])
        self.csi_node.resource_info_manager.get_csi_pods_info.assert_called_once_with()

    def test_host_is_not_part_of_update_when_the_node_do_not_have_csi_node_pod_on_it(self):
        pod_info = deepcopy(self.fake_pod_info)
        pod_info.node_name = 'bad_node_name'
        self._test_is_host_part_of_update(False, test_settings.FAKE_DAEMON_SET_NAME, [pod_info])
        self.csi_node.resource_info_manager.get_csi_pods_info.assert_called_once_with()

    def test_host_is_not_part_of_update_when_non_of_the_pods_has_the_daemon_set_name_in_their_name(self):
        self._test_is_host_part_of_update(False, 'bad_daemon_set_name', [self.fake_pod_info, self.fake_pod_info])
        self.csi_node.resource_info_manager.get_csi_pods_info.assert_called_once_with()

    def test_host_is_not_part_of_update_when_fail_to_get_daemon_set(self):
        self._test_is_host_part_of_update(False, None)
        self.csi_node.resource_info_manager.get_csi_pods_info.assert_not_called()

    def _test_is_host_part_of_update(self, expected_result, daemon_set_name, pods_info=None):
        self.csi_node.daemon_set_manager.wait_until_all_daemon_set_pods_are_up_to_date.return_value = daemon_set_name
        self.csi_node.resource_info_manager.get_csi_pods_info.return_value = pods_info
        result = self.csi_node.is_host_part_of_update(test_settings.FAKE_NODE_NAME)
        self.assertEqual(result, expected_result)
        self.csi_node.daemon_set_manager.wait_until_all_daemon_set_pods_are_up_to_date.assert_called_once_with()

    def test_return_true_when_node_id_changed(self):
        self._test_is_node_id_changed(True, test_settings.FAKE_NODE_ID, 'different_node_id')

    def test_return_false_when_node_id_did_not_change(self):
        self._test_is_node_id_changed(False, test_settings.FAKE_NODE_ID, test_settings.FAKE_NODE_ID)

    def test_return_false_when_host_definition_node_id_is_none(self):
        self._test_is_node_id_changed(False, None, test_settings.FAKE_NODE_ID)

    def test_return_false_when_csi_node_node_id_is_none(self):
        self._test_is_node_id_changed(False, test_settings.FAKE_NODE_ID, None)

    def _test_is_node_id_changed(self, expected_result, host_definition_node_id, csi_node_node_id):
        result = self.csi_node.is_node_id_changed(host_definition_node_id, csi_node_node_id)
        self.assertEqual(result, expected_result)
