import unittest
from mock import patch, Mock
from kubernetes.client.rest import ApiException

import controllers.tests.controller_server.host_definer.utils.test_utils as test_utils
import controllers.tests.controller_server.host_definer.settings as test_settings


class BaseSetUp(unittest.TestCase):
    def setUp(self):
        test_utils.patch_kubernetes_manager_init()

        self._mock_utils_k8s_mgr = patch("controllers.servers.utils.KubernetesManager")
        self.mock_k8s_mgr_cls = self._mock_utils_k8s_mgr.start()

        mock_k8s_mgr = Mock()
        mock_core_api = Mock()
        mock_core_api.read_node.return_value = Mock(metadata=Mock(annotations={}))
        mock_k8s_mgr.core_api = mock_core_api
        self.mock_k8s_mgr_cls.return_value = mock_k8s_mgr

        self.os = patch('{}.os'.format(test_settings.WATCHER_HELPER_PATH)).start()
        self.nodes_on_watcher_helper = test_utils.patch_nodes_global_variable(test_settings.WATCHER_HELPER_PATH)
        self.managed_secrets_on_watcher_helper = test_utils.patch_managed_secrets_global_variable(test_settings.WATCHER_HELPER_PATH)
        self.k8s_node_with_manage_node_label = test_utils.get_fake_k8s_node(test_settings.MANAGE_NODE_LABEL)
        self.k8s_node_with_fake_label = test_utils.get_fake_k8s_node(test_settings.FAKE_LABEL)
        self.ready_k8s_host_definitions = test_utils.get_fake_k8s_host_definitions_items(test_settings.READY_PHASE)

    def tearDown(self):
        patch.stopall()
