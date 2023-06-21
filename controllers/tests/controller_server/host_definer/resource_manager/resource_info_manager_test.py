from unittest.mock import MagicMock, Mock, patch

from controllers.servers.host_definer.types import SecretInfo
from controllers.tests.controller_server.host_definer.resource_manager.base_resource_manager import BaseResourceManager
from controllers.servers.host_definer.resource_manager.resource_info import ResourceInfoManager
import controllers.tests.controller_server.host_definer.utils.test_utils as test_utils
import controllers.tests.controller_server.host_definer.settings as test_settings
import controllers.common.settings as common_settings


class TestCSINodeManager(BaseResourceManager):
    def setUp(self):
        super().setUp()
        self.resource_info_manager = ResourceInfoManager()
        self.resource_info_manager.k8s_api = MagicMock()
        self.fake_node_info = test_utils.get_fake_node_info()
        self.fake_csi_node_info = test_utils.get_fake_csi_node_info()
        self.fake_storage_class_info = test_utils.get_fake_storage_class_info()
        self.fake_host_definition_info = test_utils.get_fake_host_definition_info()
        self.fake_k8s_secret = test_utils.get_fake_k8s_secret()

    def test_get_node_info_seccess(self):
        self.resource_info_manager.k8s_api.read_node.return_value = test_utils.get_fake_k8s_node(
            common_settings.MANAGE_NODE_LABEL)
        self.resource_info_manager.generate_node_info = Mock()
        self.resource_info_manager.generate_node_info.return_value = self.fake_node_info
        result = self.resource_info_manager.get_node_info(common_settings.MANAGE_NODE_LABEL)
        self.assertEqual(result.name, self.fake_node_info.name)
        self.assertEqual(result.labels, self.fake_node_info.labels)
        self.resource_info_manager.k8s_api.read_node.assert_called_once_with(common_settings.MANAGE_NODE_LABEL)
        self.resource_info_manager.generate_node_info.assert_called_once_with(test_utils.get_fake_k8s_node(
            common_settings.MANAGE_NODE_LABEL))

    def test_fail_to_get_node_info(self):
        self.resource_info_manager.k8s_api.read_node.return_value = None
        self.resource_info_manager.generate_node_info = Mock()
        result = self.resource_info_manager.get_node_info(common_settings.MANAGE_NODE_LABEL)
        self.assertEqual(result.name, '')
        self.assertEqual(result.labels, {})
        self.resource_info_manager.k8s_api.read_node.assert_called_once_with(common_settings.MANAGE_NODE_LABEL)
        self.resource_info_manager.generate_node_info.assert_not_called()

    def test_generate_node_info_success(self):
        result = self.resource_info_manager.generate_node_info(test_utils.get_fake_k8s_node(
            common_settings.MANAGE_NODE_LABEL))
        self.assertEqual(result.name, self.fake_node_info.name)
        self.assertEqual(result.labels, test_utils.get_fake_k8s_node(common_settings.MANAGE_NODE_LABEL).metadata.labels)

    def test_get_csi_node_info_success(self):
        self.resource_info_manager.k8s_api.get_csi_node.return_value = test_utils.get_fake_k8s_csi_node()
        self.resource_info_manager.generate_csi_node_info = Mock()
        self.resource_info_manager.generate_csi_node_info.return_value = self.fake_csi_node_info
        result = self.resource_info_manager.get_csi_node_info(test_settings.FAKE_NODE_NAME)
        self.assertEqual(result.name, self.fake_csi_node_info.name)
        self.assertEqual(result.node_id, self.fake_csi_node_info.node_id)
        self.resource_info_manager.k8s_api.get_csi_node.assert_called_once_with(test_settings.FAKE_NODE_NAME)
        self.resource_info_manager.generate_csi_node_info.assert_called_once_with(test_utils.get_fake_k8s_csi_node())

    def test_get_non_exist_csi_node_info_success(self):
        self.resource_info_manager.k8s_api.get_csi_node.return_value = None
        self.resource_info_manager.generate_csi_node_info = Mock()
        result = self.resource_info_manager.get_csi_node_info(test_settings.FAKE_NODE_NAME)
        self.assertEqual(result.name, "")
        self.assertEqual(result.node_id, "")
        self.resource_info_manager.k8s_api.get_csi_node.assert_called_once_with(test_settings.FAKE_NODE_NAME)
        self.resource_info_manager.generate_csi_node_info.assert_not_called()

    def test_generate_csi_node_info_with_ibm_driver_success(self):
        result = self.resource_info_manager.generate_csi_node_info(
            test_utils.get_fake_k8s_csi_node(common_settings.CSI_PROVISIONER_NAME))
        self.assertEqual(result.name, self.fake_csi_node_info.name)
        self.assertEqual(result.node_id, self.fake_csi_node_info.node_id)

    def test_generate_csi_node_info_with_non_ibm_driver_success(self):
        result = self.resource_info_manager.generate_csi_node_info(
            test_utils.get_fake_k8s_csi_node(test_settings.FAKE_CSI_PROVISIONER))
        self.assertEqual(result.name, self.fake_csi_node_info.name)
        self.assertEqual(result.node_id, '')

    def test_get_storage_classes_info_success(self):
        self.resource_info_manager.generate_storage_class_info = Mock()
        self._test_get_k8s_resources_info_success(
            self.resource_info_manager.get_storage_classes_info, self.resource_info_manager.k8s_api.list_storage_class,
            self.resource_info_manager.generate_storage_class_info, self.fake_storage_class_info,
            test_utils.get_fake_k8s_storage_class_items(common_settings.CSI_PROVISIONER_NAME))

    def test_get_storage_classes_info_empty_list_success(self):
        self.resource_info_manager.generate_storage_class_info = Mock()
        self._test_get_k8s_resources_info_empty_list_success(self.resource_info_manager.get_storage_classes_info,
                                                             self.resource_info_manager.k8s_api.list_storage_class,
                                                             self.resource_info_manager.generate_storage_class_info)

    def test_generate_storage_class_info_success(self):
        k8s_storage_class = test_utils.get_fake_k8s_storage_class(common_settings.CSI_PROVISIONER_NAME)
        result = self.resource_info_manager.generate_storage_class_info(k8s_storage_class)
        self.assertEqual(result.name, self.fake_storage_class_info.name)
        self.assertEqual(result.provisioner, self.fake_storage_class_info.provisioner)
        self.assertEqual(result.parameters, self.fake_storage_class_info.parameters)

    def test_get_csi_pods_info_success(self):
        self._test_get_pods_info(1)

    def test_get_multiple_csi_pods_info_success(self):
        self._test_get_pods_info(2)

    def _test_get_pods_info(self, number_of_pods):
        self.resource_info_manager.k8s_api.list_pod_for_all_namespaces.return_value = \
            test_utils.get_fake_k8s_pods_items(number_of_pods)
        result = self.resource_info_manager.get_csi_pods_info()
        self.assertEqual(result[0].name, test_utils.get_fake_k8s_pods_items().items[0].metadata.name)
        self.assertEqual(result[0].node_name, test_utils.get_fake_k8s_pods_items().items[0].spec.node_name)
        self.assertEqual(len(result), number_of_pods)
        self.resource_info_manager.k8s_api.list_pod_for_all_namespaces.assert_called_once_with(
            common_settings.DRIVER_PRODUCT_LABEL)

    def test_get_none_when_fail_to_get_k8s_pods(self):
        self.resource_info_manager.k8s_api.list_pod_for_all_namespaces.return_value = None
        result = self.resource_info_manager.get_csi_pods_info()
        self.assertEqual(result, [])
        self.resource_info_manager.k8s_api.list_pod_for_all_namespaces.assert_called_once_with(
            common_settings.DRIVER_PRODUCT_LABEL)

    @patch('{}.utils'.format(test_settings.RESOURCE_INFO_MANAGER_PATH))
    def test_generate_host_definition_info_success(self, mock_utils):
        k8s_host_definition = test_utils.get_fake_k8s_host_definition(common_settings.READY_PHASE)
        mock_utils.get_k8s_object_resource_version.return_value = self.fake_host_definition_info.resource_version
        result = self.resource_info_manager.generate_host_definition_info(k8s_host_definition)
        self.assertEqual(result.name, self.fake_host_definition_info.name)
        self.assertEqual(result.resource_version, self.fake_host_definition_info.resource_version)
        self.assertEqual(result.uid, self.fake_host_definition_info.uid)
        self.assertEqual(result.phase, self.fake_host_definition_info.phase)
        self.assertEqual(result.secret_name, self.fake_host_definition_info.secret_name)
        self.assertEqual(result.secret_namespace, self.fake_host_definition_info.secret_namespace)
        self.assertEqual(result.node_name, self.fake_host_definition_info.node_name)
        self.assertEqual(result.node_id, self.fake_host_definition_info.node_id)
        self.assertEqual(result.connectivity_type, self.fake_host_definition_info.connectivity_type)
        mock_utils.get_k8s_object_resource_version.assert_called_once_with(k8s_host_definition)

    def test_generate_k8s_secret_to_secret_info_success(self):
        result = self.resource_info_manager.generate_k8s_secret_to_secret_info(self.fake_k8s_secret, 'input1', 'input2')
        self.assertEqual(result.name, test_settings.FAKE_SECRET)
        self.assertEqual(result.namespace, test_settings.FAKE_SECRET_NAMESPACE)
        self.assertEqual(result.nodes_with_system_id, 'input1')
        self.assertEqual(result.system_ids_topologies, 'input2')
        self.assertEqual(type(result), SecretInfo)

    def test_generate_k8s_secret_to_secret_info_defaults_success(self):
        result = self.resource_info_manager.generate_k8s_secret_to_secret_info(self.fake_k8s_secret)
        self.assertEqual(result.name, test_settings.FAKE_SECRET)
        self.assertEqual(result.namespace, test_settings.FAKE_SECRET_NAMESPACE)
        self.assertEqual(result.nodes_with_system_id, {})
        self.assertEqual(result.system_ids_topologies, {})
        self.assertEqual(type(result), SecretInfo)

    def test_generate_secret_info_success(self):
        result = self.resource_info_manager.generate_secret_info(
            test_settings.FAKE_SECRET, test_settings.FAKE_SECRET_NAMESPACE, 'input1', 'input2')
        self.assertEqual(result.name, test_settings.FAKE_SECRET)
        self.assertEqual(result.namespace, test_settings.FAKE_SECRET_NAMESPACE)
        self.assertEqual(result.nodes_with_system_id, 'input1')
        self.assertEqual(result.system_ids_topologies, 'input2')
        self.assertEqual(type(result), SecretInfo)

    def test_generate_secret_info_defaults_success(self):
        result = self.resource_info_manager.generate_secret_info(test_settings.FAKE_SECRET,
                                                                 test_settings.FAKE_SECRET_NAMESPACE)
        self.assertEqual(result.name, test_settings.FAKE_SECRET)
        self.assertEqual(result.namespace, test_settings.FAKE_SECRET_NAMESPACE)
        self.assertEqual(result.nodes_with_system_id, {})
        self.assertEqual(result.system_ids_topologies, {})
        self.assertEqual(type(result), SecretInfo)
