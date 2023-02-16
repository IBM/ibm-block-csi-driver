import unittest
from unittest.mock import MagicMock, patch
from kubernetes.client.rest import ApiException
from kubernetes.watch import Watch

from controllers.servers.host_definer.k8s.api import K8SApi
import controllers.tests.controller_server.host_definer.utils.test_utils as test_utils
import controllers.tests.controller_server.host_definer.utils.k8s_manifests_utils as test_manifest_utils
import controllers.tests.controller_server.host_definer.settings as test_settings
import controllers.common.settings as common_settings


class TestKubernetesApi(unittest.TestCase):
    def setUp(self):
        test_utils.patch_k8s_api_init()
        self.k8s_api = K8SApi()
        self.not_found_api_exception = ApiException(http_resp=test_utils.get_error_http_resp(404))
        self.general_api_exception = ApiException(http_resp=test_utils.get_error_http_resp(405))
        self.k8s_api.csi_nodes_api = MagicMock()
        self.k8s_api.host_definitions_api = MagicMock()
        self.k8s_api.custom_object_api = MagicMock()
        self.k8s_api.core_api = MagicMock()
        self.k8s_api.apps_api = MagicMock()
        self.k8s_api.storage_api = MagicMock()
        self.mock_stream = patch.object(Watch, 'stream').start()

    def test_get_csi_node_success(self):
        self.k8s_api.csi_nodes_api.get.return_value = test_utils.get_fake_k8s_csi_node()
        result = self.k8s_api.get_csi_node(test_settings.FAKE_NODE_NAME)
        self.assertEqual(result, test_utils.get_fake_k8s_csi_node())
        self.k8s_api.csi_nodes_api.get.assert_called_once_with(name=test_settings.FAKE_NODE_NAME)

    def test_get_csi_node_not_found(self):
        self.k8s_api.csi_nodes_api.get.side_effect = self.not_found_api_exception
        result = self.k8s_api.get_csi_node(test_settings.FAKE_NODE_NAME)
        self.assertEqual(result, None)

    def test_get_csi_node_failure(self):
        self.k8s_api.csi_nodes_api.get.side_effect = self.general_api_exception
        result = self.k8s_api.get_csi_node(test_settings.FAKE_NODE_NAME)
        self.assertEqual(result, None)

    def test_list_host_definition_success(self):
        self.k8s_api.host_definitions_api.get.return_value = test_utils.get_fake_k8s_host_definitions_items()
        result = self.k8s_api.list_host_definition()
        self.assertEqual(result, test_utils.get_fake_k8s_host_definitions_items())
        self.k8s_api.host_definitions_api.get.assert_called_once_with()

    def test_list_host_definition_failure(self):
        self._test_list_k8s_resource_failure(self.k8s_api.list_host_definition, self.k8s_api.host_definitions_api.get)

    def test_create_host_definition_success(self):
        self.k8s_api.host_definitions_api.create.return_value = test_utils.get_fake_empty_k8s_list()
        result = self.k8s_api.create_host_definition(test_manifest_utils.get_fake_k8s_host_definition_manifest())
        self.assertEqual(result, test_utils.get_fake_empty_k8s_list())
        self.k8s_api.host_definitions_api.create.assert_called_once_with(
            body=test_manifest_utils.get_fake_k8s_host_definition_manifest())

    def test_create_host_definition_failure(self):
        self.k8s_api.host_definitions_api.create.side_effect = self.general_api_exception
        result = self.k8s_api.create_host_definition(test_manifest_utils.get_fake_k8s_host_definition_manifest())
        self.assertEqual(result, None)

    def test_patch_cluster_custom_object_status_success(self):
        self.k8s_api.csi_nodes_api.get.return_value = None
        self.k8s_api.patch_cluster_custom_object_status(
            common_settings.CSI_IBM_GROUP, common_settings.VERSION, common_settings.HOST_DEFINITION_PLURAL,
            test_settings.FAKE_NODE_NAME, test_settings.READY_PHASE)
        self.k8s_api.custom_object_api.patch_cluster_custom_object_status.assert_called_with(
            common_settings.CSI_IBM_GROUP, common_settings.VERSION,
            common_settings.HOST_DEFINITION_PLURAL, test_settings.FAKE_NODE_NAME,
            test_settings.READY_PHASE)

    def test_create_event_success(self):
        self.k8s_api.core_api.create_namespaced_event.return_value = None
        self.k8s_api.create_event(test_settings.FAKE_SECRET_NAMESPACE, test_utils.get_fake_empty_k8s_list())
        self.k8s_api.core_api.create_namespaced_event.assert_called_with(
            test_settings.FAKE_SECRET_NAMESPACE, test_utils.get_fake_empty_k8s_list())

    def test_delete_host_definition_success(self):
        self.k8s_api.host_definitions_api.delete.return_value = test_utils.get_fake_k8s_host_definitions_items()
        result = self.k8s_api.delete_host_definition(test_settings.FAKE_NODE_NAME)
        self.assertEqual(result, test_utils.get_fake_k8s_host_definitions_items())
        self.k8s_api.host_definitions_api.delete.assert_called_once_with(name=test_settings.FAKE_NODE_NAME, body={})

    def test_delete_host_definition_failure(self):
        self.k8s_api.host_definitions_api.delete.side_effect = self.general_api_exception
        result = self.k8s_api.delete_host_definition(test_settings.FAKE_NODE_NAME)
        self.assertEqual(result, None)

    def test_patch_host_definition_success(self):
        self.k8s_api.host_definitions_api.patch.return_value = None
        result = self.k8s_api.patch_host_definition(test_manifest_utils.get_fake_k8s_host_definition_manifest())
        self.assertEqual(result, 200)
        self.k8s_api.host_definitions_api.patch.assert_called_once_with(
            name=test_settings.FAKE_NODE_NAME, body=test_manifest_utils.get_fake_k8s_host_definition_manifest(),
            content_type='application/merge-patch+json')

    def test_patch_host_definition_failure(self):
        self.k8s_api.host_definitions_api.patch.side_effect = self.not_found_api_exception
        result = self.k8s_api.patch_host_definition(test_manifest_utils.get_fake_k8s_host_definition_manifest())
        self.assertEqual(result, 404)

    def test_patch_node_success(self):
        self.k8s_api.core_api.patch_node.return_value = None
        self.k8s_api.patch_node(test_settings.FAKE_NODE_NAME, test_manifest_utils.get_fake_k8s_node_manifest(
            test_settings.MANAGE_NODE_LABEL))
        self.k8s_api.core_api.patch_node.assert_called_once_with(
            test_settings.FAKE_NODE_NAME, test_manifest_utils.get_fake_k8s_node_manifest(
                test_settings.MANAGE_NODE_LABEL))

    def test_get_secret_data_success(self):
        self.k8s_api.core_api.read_namespaced_secret.return_value = test_utils.get_fake_k8s_secret()
        result = self.k8s_api.get_secret_data(test_settings.FAKE_SECRET, test_settings.FAKE_SECRET_NAMESPACE)
        self.assertEqual(result, test_utils.get_fake_k8s_secret().data)
        self.k8s_api.core_api.read_namespaced_secret.assert_called_once_with(
            name=test_settings.FAKE_SECRET, namespace=test_settings.FAKE_SECRET_NAMESPACE)

    def test_get_secret_data_failure(self):
        self.k8s_api.core_api.read_namespaced_secret.side_effect = self.not_found_api_exception
        result = self.k8s_api.get_secret_data(test_settings.FAKE_SECRET, test_settings.FAKE_SECRET_NAMESPACE)
        self.assertEqual(result, {})

    def test_read_node_success(self):
        self.k8s_api.core_api.read_node.return_value = test_utils.get_fake_k8s_node(test_settings.MANAGE_NODE_LABEL)
        result = self.k8s_api.read_node(test_settings.FAKE_NODE_NAME)
        self.assertEqual(result, test_utils.get_fake_k8s_node(test_settings.MANAGE_NODE_LABEL))
        self.k8s_api.core_api.read_node.assert_called_once_with(name=test_settings.FAKE_NODE_NAME)

    def test_read_node_failure(self):
        self.k8s_api.core_api.read_node.side_effect = self.not_found_api_exception
        result = self.k8s_api.read_node(test_settings.FAKE_NODE_NAME)
        self.assertEqual(result, None)

    def test_list_daemon_set_for_all_namespaces_success(self):
        self.k8s_api.apps_api.list_daemon_set_for_all_namespaces.return_value = \
            test_utils.get_fake_k8s_daemon_set_items(0, 0)
        result = self.k8s_api.list_daemon_set_for_all_namespaces(test_settings.MANAGE_NODE_LABEL)
        self.assertEqual(result, test_utils.get_fake_k8s_daemon_set_items(0, 0))
        self.k8s_api.apps_api.list_daemon_set_for_all_namespaces.assert_called_once_with(
            label_selector=test_settings.MANAGE_NODE_LABEL)

    def test_list_daemon_set_for_all_namespaces_failure(self):
        self.k8s_api.apps_api.list_daemon_set_for_all_namespaces.side_effect = self.general_api_exception
        result = self.k8s_api.list_daemon_set_for_all_namespaces(test_settings.FAKE_NODE_NAME)
        self.assertEqual(result, None)

    def test_list_pod_for_all_namespaces_success(self):
        self.k8s_api.core_api.list_pod_for_all_namespaces.return_value = \
            test_utils.get_fake_k8s_daemon_set_items(0, 0)
        result = self.k8s_api.list_pod_for_all_namespaces(test_settings.MANAGE_NODE_LABEL)
        self.assertEqual(result, test_utils.get_fake_k8s_daemon_set_items(0, 0))
        self.k8s_api.core_api.list_pod_for_all_namespaces.assert_called_once_with(
            label_selector=test_settings.MANAGE_NODE_LABEL)

    def test_list_pod_for_all_namespaces_failure(self):
        self.k8s_api.core_api.list_pod_for_all_namespaces.side_effect = self.general_api_exception
        result = self.k8s_api.list_pod_for_all_namespaces(test_settings.FAKE_NODE_NAME)
        self.assertEqual(result, None)

    @patch('{}.utils'.format(test_settings.K8S_API_PATH))
    def test_get_storage_class_stream_success(self, mock_utils):
        self._test_basic_resource_stream_success(
            self.k8s_api.get_storage_class_stream, self.k8s_api.storage_api.list_storage_class, mock_utils)

    @patch('{}.utils'.format(test_settings.K8S_API_PATH))
    def test_get_node_stream_success(self, mock_utils):
        self._test_basic_resource_stream_success(self.k8s_api.get_node_stream,
                                                 self.k8s_api.core_api.list_node, mock_utils)

    @patch('{}.utils'.format(test_settings.K8S_API_PATH))
    def test_get_secret_stream_success(self, mock_utils):
        self._test_basic_resource_stream_success(self.k8s_api.get_secret_stream,
                                                 self.k8s_api.core_api.list_secret_for_all_namespaces,
                                                 mock_utils)

    def _test_basic_resource_stream_success(self, function_to_test, k8s_function, mock_utils):
        mock_utils.get_k8s_object_resource_version.return_value = test_settings.FAKE_RESOURCE_VERSION
        result = function_to_test()
        k8s_function.assert_called_once()
        self.mock_stream.assert_called_once_with(k8s_function, resource_version=test_settings.FAKE_RESOURCE_VERSION,
                                                 timeout_seconds=5)
        self.assertEqual(result, self.mock_stream.return_value)

    def test_get_storage_class_stream_failure(self):
        self._test_basic_resource_stream_failure(self.k8s_api.get_storage_class_stream)

    def test_get_node_stream_failure(self):
        self._test_basic_resource_stream_failure(self.k8s_api.get_node_stream)

    def test_get_secret_stream_failure(self):
        self._test_basic_resource_stream_failure(self.k8s_api.get_secret_stream)

    def _test_basic_resource_stream_failure(self, function_to_test):
        self.mock_stream.side_effect = self.general_api_exception
        with self.assertRaises(ApiException):
            function_to_test()

    def test_list_storage_class_success(self):
        self.k8s_api.storage_api.list_storage_class.return_value = \
            test_utils.get_fake_k8s_storage_class_items(test_settings.CSI_PROVISIONER_NAME)
        result = self.k8s_api.list_storage_class()
        self.assertEqual(result, test_utils.get_fake_k8s_storage_class_items(test_settings.CSI_PROVISIONER_NAME))

    def test_list_storage_class_failure(self):
        self._test_list_k8s_resource_failure(self.k8s_api.list_storage_class,
                                             self.k8s_api.storage_api.list_storage_class)

    def test_list_node_success(self):
        self.k8s_api.core_api.list_node.return_value = test_utils.get_fake_k8s_nodes_items()
        result = self.k8s_api.list_node()
        self.assertEqual(result, test_utils.get_fake_k8s_nodes_items())

    def test_list_node_failure(self):
        self._test_list_k8s_resource_failure(self.k8s_api.list_node, self.k8s_api.core_api.list_node)

    def test_list_csi_node_success(self):
        self.k8s_api.csi_nodes_api.get.return_value = test_utils.get_fake_k8s_csi_node(
            test_settings.CSI_PROVISIONER_NAME)
        result = self.k8s_api.list_csi_node()
        self.assertEqual(result, test_utils.get_fake_k8s_csi_node(test_settings.CSI_PROVISIONER_NAME))

    def test_list_csi_node_failure(self):
        self._test_list_k8s_resource_failure(self.k8s_api.list_csi_node, self.k8s_api.csi_nodes_api.get)

    def _test_list_k8s_resource_failure(self, function_to_test, k8s_function):
        k8s_function.side_effect = self.general_api_exception
        result = function_to_test()
        self.assertEqual(result, test_utils.get_fake_empty_k8s_list())

    def test_get_host_definition_stream_success(self):
        expected_output = iter([])
        self.k8s_api.host_definitions_api.watch.return_value = expected_output
        result = self.k8s_api.get_host_definition_stream(test_settings.FAKE_RESOURCE_VERSION, 5)
        self.k8s_api.host_definitions_api.watch.assert_called_once_with(
            resource_version=test_settings.FAKE_RESOURCE_VERSION, timeout=5)
        self.assertEqual(result, expected_output)

    def test_get_host_definition_stream_failure(self):
        self.k8s_api.host_definitions_api.watch.side_effect = self.general_api_exception
        with self.assertRaises(ApiException):
            self.k8s_api.get_host_definition_stream(test_settings.FAKE_RESOURCE_VERSION, 5)

    @patch('{}.utils'.format(test_settings.K8S_API_PATH))
    def test_get_csi_node_stream_success(self, mock_utils):
        expected_output = iter([])
        mock_utils.get_k8s_object_resource_version.return_value = test_settings.FAKE_RESOURCE_VERSION
        self.k8s_api.csi_nodes_api.watch.return_value = expected_output
        result = self.k8s_api.get_csi_node_stream()
        self.k8s_api.csi_nodes_api.watch.assert_called_once_with(
            resource_version=test_settings.FAKE_RESOURCE_VERSION, timeout=5)
        self.assertEqual(result, expected_output)

    def test_get_csi_node_stream_failure(self):
        self.k8s_api.csi_nodes_api.watch.side_effect = self.general_api_exception
        with self.assertRaises(ApiException):
            self.k8s_api.get_csi_node_stream()
