import unittest
from mock import patch
from kubernetes.client.rest import ApiException

import controllers.tests.controller_server.host_definer.utils.test_utils as test_utils
import controllers.tests.controller_server.host_definer.settings as test_settings


class BaseSetUp(unittest.TestCase):
    def setUp(self):
        test_utils.patch_kubernetes_manager_init()
        self.os = patch('{}.os'.format(test_settings.WATCHER_HELPER_PATH)).start()
        self.nodes_on_watcher_helper = test_utils.patch_nodes_global_variable(test_settings.WATCHER_HELPER_PATH)
        self.secret_ids_on_watcher_helper = test_utils.patch_secret_ids_global_variable(
            test_settings.WATCHER_HELPER_PATH)
        self.k8s_node_with_manage_node_label = test_utils.get_fake_k8s_node(test_settings.MANAGE_NODE_LABEL)
        self.k8s_node_with_fake_label = test_utils.get_fake_k8s_node(test_settings.FAKE_LABEL)
        self.ready_k8s_host_definitions = test_utils.get_fake_k8s_host_definitions_items(test_settings.READY_PHASE)
        self.http_resp = test_utils.get_error_http_resp()
        self.fake_api_exception = ApiException(http_resp=self.http_resp)
