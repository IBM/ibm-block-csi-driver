import unittest
from unittest.mock import MagicMock, Mock, patch

from controllers.servers.host_definer.k8s.manager import K8SManager
from controllers.servers.host_definer.k8s.api import K8SApi
from controllers.servers.host_definer.utils import utils
from controllers.servers.host_definer.utils import manifest_utils
import controllers.tests.controller_server.host_definer.utils.test_utils as test_utils
import controllers.tests.controller_server.host_definer.utils.k8s_manifests_utils as test_manifest_utils
import controllers.tests.controller_server.host_definer.settings as test_settings
import controllers.common.settings as common_settings


class TestKubernetesManager(unittest.TestCase):
    def setUp(self):
        test_utils.patch_function(K8SApi, '_load_cluster_configuration')
        test_utils.patch_function(K8SApi, '_get_dynamic_client')
        self.k8s_manager = K8SManager()
        self.k8s_manager.k8s_api = MagicMock()
        self.fake_csi_node_info = test_utils.get_fake_csi_node_info()
        self.fake_node_info = test_utils.get_fake_node_info()
        self.fake_storage_class_info = test_utils.get_fake_storage_class_info()
        self.fake_host_definition_info = test_utils.get_fake_host_definition_info()
        self.fake_k8s_csi_nodes_with_ibm_driver = test_utils.get_fake_k8s_csi_nodes(
            test_settings.CSI_PROVISIONER_NAME, 1)
        self.fake_k8s_csi_nodes_with_non_ibm_driver = test_utils.get_fake_k8s_csi_nodes(
            test_settings.FAKE_CSI_PROVISIONER, 1)
        self.mock_decode_base64_secret = patch.object(utils, 'change_decode_base64_secret_config').start()
        self.mock_get_status_manifest = patch.object(manifest_utils, 'get_host_definition_status_manifest').start()
        self.mock_get_body_manifest_for_labels = patch.object(manifest_utils, 'get_body_manifest_for_labels').start()
        self.mock_get_finalizer_manifest = patch.object(manifest_utils, 'get_finalizer_manifest').start()

    def test_get_csi_nodes_info_with_driver_success(self):
        self.k8s_manager.generate_csi_node_info = Mock()
        self._test_get_k8s_resources_info_success(
            self.k8s_manager.get_csi_nodes_info_with_driver, self.k8s_manager.k8s_api.list_csi_node,
            self.k8s_manager.generate_csi_node_info, self.fake_csi_node_info,
            self.fake_k8s_csi_nodes_with_ibm_driver)

    def test_get_csi_nodes_info_with_driver_empty_list_success(self):
        self.k8s_manager.generate_csi_node_info = Mock()
        self._test_get_k8s_resources_info_empty_list_success(self.k8s_manager.get_csi_nodes_info_with_driver,
                                                             self.k8s_manager.k8s_api.list_csi_node,
                                                             self.k8s_manager.generate_csi_node_info)

    def test_get_csi_nodes_info_with_driver_non_ibm_driver_success(self):
        self.k8s_manager.k8s_api.list_csi_node.return_value = self.fake_k8s_csi_nodes_with_non_ibm_driver
        self.k8s_manager.generate_csi_node_info = Mock()
        self.k8s_manager.generate_csi_node_info.return_value = self.fake_csi_node_info
        result = self.k8s_manager.get_csi_nodes_info_with_driver()
        self.assertEqual(result, [])
        self.k8s_manager.generate_csi_node_info.assert_not_called()

    def test_get_nodes_info_success(self):
        self.k8s_manager.generate_node_info = Mock()
        self._test_get_k8s_resources_info_success(
            self.k8s_manager.get_nodes_info, self.k8s_manager.k8s_api.list_node,
            self.k8s_manager.generate_node_info, self.fake_node_info,
            test_utils.get_fake_k8s_nodes_items())

    def test_get_nodes_info_empty_list_success(self):
        self.k8s_manager.generate_node_info = Mock()
        self._test_get_k8s_resources_info_empty_list_success(self.k8s_manager.get_nodes_info,
                                                             self.k8s_manager.k8s_api.list_node,
                                                             self.k8s_manager.generate_node_info)

    def test_get_storage_classes_info_success(self):
        self.k8s_manager.generate_storage_class_info = Mock()
        self._test_get_k8s_resources_info_success(
            self.k8s_manager.get_storage_classes_info, self.k8s_manager.k8s_api.list_storage_class,
            self.k8s_manager.generate_storage_class_info, self.fake_storage_class_info,
            test_utils.get_fake_k8s_storage_class_items(test_settings.CSI_PROVISIONER_NAME))

    def test_get_storage_classes_info_empty_list_success(self):
        self.k8s_manager.generate_storage_class_info = Mock()
        self._test_get_k8s_resources_info_empty_list_success(self.k8s_manager.get_storage_classes_info,
                                                             self.k8s_manager.k8s_api.list_storage_class,
                                                             self.k8s_manager.generate_storage_class_info)

    def _test_get_k8s_resources_info_success(self, function_to_test, k8s_function,
                                             get_info_function, fake_resource_info, fake_k8s_items):
        k8s_function.return_value = fake_k8s_items
        get_info_function.return_value = fake_resource_info
        result = function_to_test()
        self.assertEqual(result, [fake_resource_info])
        get_info_function.assert_called_once_with(fake_k8s_items.items[0])

    def _test_get_k8s_resources_info_empty_list_success(self, function_to_test, k8s_function, info_function):
        k8s_function.return_value = test_utils.get_fake_empty_k8s_list()
        result = function_to_test()
        self.assertEqual(result, [])
        info_function.assert_not_called()

    def test_generate_storage_class_info_success(self):
        k8s_storage_class = test_utils.get_fake_k8s_storage_class(test_settings.CSI_PROVISIONER_NAME)
        result = self.k8s_manager.generate_storage_class_info(k8s_storage_class)
        self.assertEqual(result.name, self.fake_storage_class_info.name)
        self.assertEqual(result.provisioner, self.fake_storage_class_info.provisioner)
        self.assertEqual(result.parameters, self.fake_storage_class_info.parameters)

    def test_get_csi_node_info_success(self):
        self.k8s_manager.k8s_api.get_csi_node.return_value = test_utils.get_fake_k8s_csi_node()
        self.k8s_manager.generate_csi_node_info = Mock()
        self.k8s_manager.generate_csi_node_info.return_value = self.fake_csi_node_info
        result = self.k8s_manager.get_csi_node_info(test_settings.FAKE_NODE_NAME)
        self.assertEqual(result.name, self.fake_csi_node_info.name)
        self.assertEqual(result.node_id, self.fake_csi_node_info.node_id)
        self.k8s_manager.k8s_api.get_csi_node.assert_called_once_with(test_settings.FAKE_NODE_NAME)
        self.k8s_manager.generate_csi_node_info.assert_called_once_with(test_utils.get_fake_k8s_csi_node())

    def test_get_non_exist_csi_node_info_success(self):
        self.k8s_manager.k8s_api.get_csi_node.return_value = None
        self.k8s_manager.generate_csi_node_info = Mock()
        result = self.k8s_manager.get_csi_node_info(test_settings.FAKE_NODE_NAME)
        self.assertEqual(result.name, "")
        self.assertEqual(result.node_id, "")
        self.k8s_manager.k8s_api.get_csi_node.assert_called_once_with(test_settings.FAKE_NODE_NAME)
        self.k8s_manager.generate_csi_node_info.assert_not_called()

    def test_generate_csi_node_info_with_ibm_driver_success(self):
        result = self.k8s_manager.generate_csi_node_info(
            test_utils.get_fake_k8s_csi_node(test_settings.CSI_PROVISIONER_NAME))
        self.assertEqual(result.name, self.fake_csi_node_info.name)
        self.assertEqual(result.node_id, self.fake_csi_node_info.node_id)

    def test_generate_csi_node_info_with_non_ibm_driver_success(self):
        result = self.k8s_manager.generate_csi_node_info(
            test_utils.get_fake_k8s_csi_node(test_settings.FAKE_CSI_PROVISIONER))
        self.assertEqual(result.name, self.fake_csi_node_info.name)
        self.assertEqual(result.node_id, '')

    def test_get_matching_host_definition_info_success(self):
        self._test_get_matching_host_definition_info_success(
            test_settings.FAKE_NODE_NAME, test_settings.FAKE_SECRET,
            test_settings.FAKE_SECRET_NAMESPACE, self.fake_host_definition_info)

    def test_get_none_when_host_definition_node_name_is_not_matched(self):
        self._test_get_matching_host_definition_info_success(
            'bad_node_name', test_settings.FAKE_SECRET, test_settings.FAKE_SECRET_NAMESPACE)

    def test_get_none_when_host_definition_secret_name_is_not_matched(self):
        self._test_get_matching_host_definition_info_success(
            test_settings.FAKE_NODE_NAME, 'bad_secret_name', test_settings.FAKE_SECRET_NAMESPACE)

    def test_get_none_when_host_definition_secret_namespace_is_not_matched(self):
        self._test_get_matching_host_definition_info_success(
            test_settings.FAKE_NODE_NAME, test_settings.FAKE_SECRET, 'bad_secret_namespace')

    def _test_get_matching_host_definition_info_success(
            self, node_name, secret_name, secret_namespace, expected_result=None):
        self.k8s_manager.k8s_api.list_host_definition.return_value = \
            test_utils.get_fake_k8s_host_definitions_items()
        self.k8s_manager.generate_host_definition_info = Mock()
        self.k8s_manager.generate_host_definition_info.return_value = self.fake_host_definition_info
        result = self.k8s_manager.get_matching_host_definition_info(node_name, secret_name, secret_namespace)
        self.assertEqual(result, expected_result)
        self.k8s_manager.generate_host_definition_info.assert_called_once_with(
            test_utils.get_fake_k8s_host_definitions_items().items[0])
        self.k8s_manager.k8s_api.list_host_definition.assert_called_once_with()

    def test_get_none_when_host_definition_list_empty_success(self):
        self.k8s_manager.k8s_api.list_host_definition.return_value = test_utils.get_fake_empty_k8s_list()
        self.k8s_manager.generate_host_definition_info = Mock()
        result = self.k8s_manager.get_matching_host_definition_info('', '', '')
        self.assertEqual(result, None)
        self.k8s_manager.generate_host_definition_info.assert_not_called()
        self.k8s_manager.k8s_api.list_host_definition.assert_called_once_with()

    def test_create_host_definition_success(self):
        finalizers_manifest = test_manifest_utils.get_finalizers_manifest([test_settings.CSI_IBM_FINALIZER, ])
        self.k8s_manager.k8s_api.create_host_definition.return_value = test_utils._get_fake_k8s_host_definitions(
            test_settings.READY_PHASE)
        self.mock_get_finalizer_manifest.return_value = finalizers_manifest
        self.k8s_manager.generate_host_definition_info = Mock()
        self.k8s_manager.generate_host_definition_info.return_value = self.fake_host_definition_info
        result = self.k8s_manager.create_host_definition(test_manifest_utils.get_fake_k8s_host_definition_manifest())
        self.assertEqual(result, self.fake_host_definition_info)
        self.k8s_manager.generate_host_definition_info.assert_called_once_with(
            test_utils._get_fake_k8s_host_definitions(test_settings.READY_PHASE))
        self.mock_get_finalizer_manifest.assert_called_once_with(
            test_settings.FAKE_NODE_NAME, [test_settings.CSI_IBM_FINALIZER, ])
        self.k8s_manager.k8s_api.patch_host_definition.assert_called_once_with(finalizers_manifest)
        self.k8s_manager.k8s_api.create_host_definition.assert_called_once_with(
            test_manifest_utils.get_fake_k8s_host_definition_manifest())

    def test_create_host_definition_failure(self):
        self.k8s_manager.k8s_api.create_host_definition.return_value = None
        self.k8s_manager.generate_host_definition_info = Mock()
        result = self.k8s_manager.create_host_definition(test_manifest_utils.get_fake_k8s_host_definition_manifest())
        self.assertEqual(result.name, "")
        self.assertEqual(result.node_id, "")
        self.mock_get_finalizer_manifest.assert_not_called()
        self.k8s_manager.generate_host_definition_info.assert_not_called()
        self.k8s_manager.k8s_api.patch_host_definition.assert_not_called()
        self.k8s_manager.k8s_api.create_host_definition.assert_called_once_with(
            test_manifest_utils.get_fake_k8s_host_definition_manifest())

    def test_generate_host_definition_info_success(self):
        k8s_host_definition = test_utils._get_fake_k8s_host_definitions(test_settings.READY_PHASE)
        result = self.k8s_manager.generate_host_definition_info(k8s_host_definition)
        self.assertEqual(result.name, self.fake_host_definition_info.name)
        self.assertEqual(result.resource_version, self.fake_host_definition_info.resource_version)
        self.assertEqual(result.uid, self.fake_host_definition_info.uid)
        self.assertEqual(result.phase, self.fake_host_definition_info.phase)
        self.assertEqual(result.secret_name, self.fake_host_definition_info.secret_name)
        self.assertEqual(result.secret_namespace, self.fake_host_definition_info.secret_namespace)
        self.assertEqual(result.node_name, self.fake_host_definition_info.node_name)
        self.assertEqual(result.node_id, self.fake_host_definition_info.node_id)
        self.assertEqual(result.connectivity_type, self.fake_host_definition_info.connectivity_type)

    def test_set_host_definition_status_success(self):
        status_phase_manifest = test_manifest_utils.get_status_phase_manifest(test_settings.READY_PHASE)
        self.mock_get_status_manifest.return_value = status_phase_manifest
        self.k8s_manager.set_host_definition_status(test_settings.FAKE_NODE_NAME, test_settings.READY_PHASE)
        self.mock_get_status_manifest.assert_called_once_with(test_settings.READY_PHASE)
        self.k8s_manager.k8s_api.patch_cluster_custom_object_status.assert_called_once_with(
            common_settings.CSI_IBM_GROUP, common_settings.VERSION, common_settings.HOST_DEFINITION_PLURAL,
            test_settings.FAKE_NODE_NAME, status_phase_manifest)

    def test_generate_k8s_normal_event_success(self):
        self._test_generate_k8s_event_success(test_settings.SUCCESSFUL_MESSAGE_TYPE, test_settings.NORMAL_EVENT_TYPE)

    def test_generate_k8s_warning_event_success(self):
        self._test_generate_k8s_event_success('unsuccessful message type', test_settings.WARNING_EVENT_TYPE)

    def _test_generate_k8s_event_success(self, message_type, expected_event_type):
        result = self.k8s_manager.generate_k8s_event(
            self.fake_host_definition_info, test_settings.MESSAGE,
            test_settings.DEFINE_ACTION, message_type)
        self.assertEqual(result.metadata, test_utils.get_event_object_metadata())
        self.assertEqual(result.reporting_component, test_settings.HOST_DEFINER)
        self.assertEqual(result.reporting_instance, test_settings.HOST_DEFINER)
        self.assertEqual(result.action, test_settings.DEFINE_ACTION)
        self.assertEqual(result.type, expected_event_type)
        self.assertEqual(result.reason, message_type + test_settings.DEFINE_ACTION)
        self.assertEqual(result.message, test_settings.MESSAGE)
        self.assertEqual(result.involved_object, test_utils.get_object_reference())

    def test_delete_host_definition_success(self):
        self._test_delete_host_definition(200)
        self.k8s_manager.k8s_api.delete_host_definition.assert_called_once_with(
            test_settings.FAKE_NODE_NAME)

    def test_fail_to_delete_host_definition_because_the_finalizers_fails_to_be_deleted(self):
        self._test_delete_host_definition(405)
        self.k8s_manager.k8s_api.delete_host_definition.assert_not_called()

    def _test_delete_host_definition(self, finalizers_status_code):
        self.mock_get_finalizer_manifest.return_value = test_manifest_utils.get_finalizers_manifest([])
        self.k8s_manager.k8s_api.patch_host_definition.return_value = finalizers_status_code
        self.k8s_manager.delete_host_definition(test_settings.FAKE_NODE_NAME)
        self.k8s_manager.k8s_api.patch_host_definition.assert_called_once_with(
            test_manifest_utils.get_finalizers_manifest([]))

    def test_update_manage_node_label_success(self):
        excepted_body = test_manifest_utils.get_metadata_with_manage_node_labels_manifest(
            test_settings.MANAGE_NODE_LABEL)
        self.mock_get_body_manifest_for_labels.return_value = excepted_body
        self.k8s_manager.update_manage_node_label(test_settings.FAKE_NODE_NAME, test_settings.MANAGE_NODE_LABEL)
        self.mock_get_body_manifest_for_labels.assert_called_once_with(test_settings.MANAGE_NODE_LABEL)
        self.k8s_manager.k8s_api.patch_node.assert_called_once_with(test_settings.FAKE_NODE_NAME, excepted_body)

    def test_get_secret_data_success(self):
        return_value = 'return value'
        self.k8s_manager.k8s_api.get_secret_data.return_value = return_value
        self.mock_decode_base64_secret.return_value = return_value
        result = self.k8s_manager.get_secret_data(test_settings.FAKE_SECRET, test_settings.FAKE_SECRET_NAMESPACE)
        self.assertEqual(result, return_value)
        self.k8s_manager.k8s_api.get_secret_data.assert_called_once_with(
            test_settings.FAKE_SECRET, test_settings.FAKE_SECRET_NAMESPACE)
        self.mock_decode_base64_secret.assert_called_once_with(return_value)

    def test_fail_to_get_secret_data(self):
        self.k8s_manager.k8s_api.get_secret_data.return_value = None
        result = self.k8s_manager.get_secret_data(test_settings.FAKE_SECRET, test_settings.FAKE_SECRET_NAMESPACE)
        self.assertEqual(result, {})
        self.k8s_manager.k8s_api.get_secret_data.assert_called_once_with(
            test_settings.FAKE_SECRET, test_settings.FAKE_SECRET_NAMESPACE)
        self.mock_decode_base64_secret.assert_not_called()

    def test_get_node_info_seccess(self):
        self.k8s_manager.k8s_api.read_node.return_value = test_utils.get_fake_k8s_node(
            test_settings.MANAGE_NODE_LABEL)
        self.k8s_manager.generate_node_info = Mock()
        self.k8s_manager.generate_node_info.return_value = self.fake_node_info
        result = self.k8s_manager.get_node_info(test_settings.MANAGE_NODE_LABEL)
        self.assertEqual(result.name, self.fake_node_info.name)
        self.assertEqual(result.labels, self.fake_node_info.labels)
        self.k8s_manager.k8s_api.read_node.assert_called_once_with(test_settings.MANAGE_NODE_LABEL)
        self.k8s_manager.generate_node_info.assert_called_once_with(test_utils.get_fake_k8s_node(
            test_settings.MANAGE_NODE_LABEL))

    def test_fail_to_get_node_info(self):
        self.k8s_manager.k8s_api.read_node.return_value = None
        self.k8s_manager.generate_node_info = Mock()
        result = self.k8s_manager.get_node_info(test_settings.MANAGE_NODE_LABEL)
        self.assertEqual(result.name, '')
        self.assertEqual(result.labels, {})
        self.k8s_manager.k8s_api.read_node.assert_called_once_with(test_settings.MANAGE_NODE_LABEL)
        self.k8s_manager.generate_node_info.assert_not_called()

    def test_generate_node_info_success(self):
        result = self.k8s_manager.generate_node_info(test_utils.get_fake_k8s_node(
            test_settings.MANAGE_NODE_LABEL))
        self.assertEqual(result.name, self.fake_node_info.name)
        self.assertEqual(result.labels, test_utils.get_fake_k8s_node(test_settings.MANAGE_NODE_LABEL).metadata.labels)

    def test_get_csi_daemon_set_success(self):
        self.k8s_manager.k8s_api.list_daemon_set_for_all_namespaces.return_value = \
            test_utils.get_fake_k8s_daemon_set_items(0, 0)
        result = self.k8s_manager.get_csi_daemon_set()
        self.assertEqual(result, test_utils.get_fake_k8s_daemon_set(0, 0))
        self.k8s_manager.k8s_api.list_daemon_set_for_all_namespaces.assert_called_once_with(
            test_settings.DRIVER_PRODUCT_LABEL)

    def test_get_none_when_fail_to_get_csi_daemon_set(self):
        self.k8s_manager.k8s_api.list_daemon_set_for_all_namespaces.return_value = None
        result = self.k8s_manager.get_csi_daemon_set()
        self.assertEqual(result, None)
        self.k8s_manager.k8s_api.list_daemon_set_for_all_namespaces.assert_called_once_with(
            test_settings.DRIVER_PRODUCT_LABEL)

    def test_get_csi_pods_info_success(self):
        self._test_get_pods_info(1)

    def test_get_multiple_csi_pods_info_success(self):
        self._test_get_pods_info(2)

    def _test_get_pods_info(self, number_of_pods):
        self.k8s_manager.k8s_api.list_pod_for_all_namespaces.return_value = test_utils.get_fake_k8s_pods_items(
            number_of_pods)
        result = self.k8s_manager.get_csi_pods_info()
        self.assertEqual(result[0].name, test_utils.get_fake_k8s_pods_items().items[0].metadata.name)
        self.assertEqual(result[0].node_name, test_utils.get_fake_k8s_pods_items().items[0].spec.node_name)
        self.assertEqual(len(result), number_of_pods)
        self.k8s_manager.k8s_api.list_pod_for_all_namespaces.assert_called_once_with(
            test_settings.DRIVER_PRODUCT_LABEL)

    def test_get_none_when_fail_to_get_k8s_pods(self):
        self.k8s_manager.k8s_api.list_pod_for_all_namespaces.return_value = None
        result = self.k8s_manager.get_csi_pods_info()
        self.assertEqual(result, [])
        self.k8s_manager.k8s_api.list_pod_for_all_namespaces.assert_called_once_with(
            test_settings.DRIVER_PRODUCT_LABEL)
