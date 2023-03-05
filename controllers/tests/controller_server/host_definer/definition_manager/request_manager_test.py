import unittest
from copy import deepcopy
from unittest.mock import MagicMock, patch

from controllers.servers.host_definer.k8s.api import K8SApi
from controllers.servers.host_definer.utils import utils
import controllers.tests.controller_server.host_definer.utils.test_utils as test_utils
import controllers.tests.controller_server.host_definer.settings as test_settings
from controllers.servers.host_definer.definition_manager.request import RequestManager


class TestSecretManager(unittest.TestCase):
    def setUp(self):
        test_utils.patch_function(K8SApi, '_load_cluster_configuration')
        test_utils.patch_function(K8SApi, '_get_dynamic_client')
        self.request_manager = RequestManager()
        self.request_manager.secret_manager = MagicMock()
        self.request_manager.k8s_manager = MagicMock()
        self.mock_global_managed_nodes = test_utils.patch_nodes_global_variable(
            test_settings.HOST_DEFINITION_MANAGER_PATH)
        self.mock_get_prefix = patch.object(utils, 'get_prefix').start()
        self.mock_get_user_connectivity_type = patch.object(utils, 'get_connectivity_type_from_user').start()
        self.fake_host_definition_info = test_utils.get_fake_host_definition_info()
        self.fake_node_info = test_utils.get_fake_node_info()
        self.fake_node_info.labels[test_settings.CONNECTIVITY_TYPE_LABEL] = test_settings.ISCSI_CONNECTIVITY_TYPE
        self.array_connection_info = test_utils.get_array_connection_info()
        self.define_request = test_utils.get_define_request(
            test_settings.FAKE_PREFIX, test_settings.ISCSI_CONNECTIVITY_TYPE, test_settings.FAKE_NODE_ID)

    def test_generate_request_success(self):
        self.mock_global_managed_nodes[test_settings.FAKE_NODE_NAME] = test_utils.get_fake_managed_node()
        self._prepare_generate_request(self.array_connection_info)
        result = self.request_manager.generate_request(self.fake_host_definition_info)
        self._assert_called_generate_request()
        self.assertEqual(result, self.define_request)

    def test_get_none_when_array_connection_info_is_empty_success(self):
        self._prepare_generate_request(None)
        result = self.request_manager.generate_request(self.fake_host_definition_info)
        self._assert_called_generate_request()
        self.assertEqual(result, None)

    def test_generate_request_when_node_is_not_in_managed_nodes_success(self):
        host_definition_info = deepcopy(self.fake_host_definition_info)
        host_definition_info.node_id = 'fake_new_node_id'
        define_request = deepcopy(self.define_request)
        define_request.node_id_from_csi_node = 'fake_new_node_id'
        define_request.node_id_from_host_definition = 'fake_new_node_id'
        define_request.io_group = ''
        self._prepare_generate_request(self.array_connection_info)
        result = self.request_manager.generate_request(host_definition_info)
        self._assert_called_generate_request()
        self.assertEqual(result, define_request)

    def _prepare_generate_request(self, array_connection_info):
        self.request_manager.k8s_manager.get_node_info.return_value = self.fake_node_info
        self.mock_get_prefix.return_value = test_settings.FAKE_PREFIX
        self.mock_get_user_connectivity_type.return_value = test_settings.ISCSI_CONNECTIVITY_TYPE
        self.request_manager.secret_manager.get_array_connection_info.return_value = array_connection_info

    def _assert_called_generate_request(self):
        self.request_manager.k8s_manager.get_node_info.assert_called_once_with(test_settings.FAKE_NODE_NAME)
        self.mock_get_prefix.assert_called_once_with()
        self.mock_get_user_connectivity_type.assert_called_once_with(test_settings.ISCSI_CONNECTIVITY_TYPE)
        self.request_manager.secret_manager.get_array_connection_info.assert_called_once_with(
            test_settings.FAKE_SECRET, test_settings.FAKE_SECRET_NAMESPACE, self.fake_node_info.labels)
