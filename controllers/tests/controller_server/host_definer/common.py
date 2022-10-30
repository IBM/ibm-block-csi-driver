import unittest
from mock import patch
from kubernetes.client.rest import ApiException

import controllers.tests.controller_server.host_definer.utils.test_utils as test_utils
import controllers.tests.controller_server.host_definer.settings as settings


class BaseSetUp(unittest.TestCase):
    def setUp(self):
        test_utils.patch_kubernetes_manager_init()
        self.mock_os = patch('{}.os'.format(settings.WATCHER_HELPER_PATH)).start()
        self.mock_nodes_on_watcher_helper = test_utils.patch_nodes_global_variable(settings.WATCHER_HELPER_PATH)
        self.mock_secret_ids_on_watcher_helper = test_utils.patch_secret_ids_global_variable(
            settings.WATCHER_HELPER_PATH)
        self.fake_k8s_node_with_manage_node_label = test_utils.get_fake_k8s_node(settings.MANAGE_NODE_LABEL)
        self.fake_k8s_node_with_fake_label = test_utils.get_fake_k8s_node(settings.FAKE_LABEL)
        self.fake_ready_k8s_host_definitions = test_utils.get_fake_k8s_host_definitions_items(settings.READY_PHASE)
        self.http_resp = test_utils.get_error_http_resp()
        self.fake_api_exception = ApiException(http_resp=self.http_resp)
