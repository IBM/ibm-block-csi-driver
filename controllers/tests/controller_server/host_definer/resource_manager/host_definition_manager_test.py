import unittest
from unittest.mock import MagicMock, Mock, patch

from controllers.servers.host_definer.resource_manager.host_definition import HostDefinitionManager
from controllers.servers.host_definer.k8s.api import K8SApi
import controllers.common.settings as common_settings
from controllers.servers.host_definer.utils import manifest_utils
import controllers.tests.controller_server.host_definer.utils.test_utils as test_utils
import controllers.tests.controller_server.host_definer.utils.k8s_manifests_utils as test_manifest_utils
import controllers.tests.controller_server.host_definer.settings as test_settings


class TestHostDefinitionManager(unittest.TestCase):
    def setUp(self):
        test_utils.patch_function(K8SApi, '_load_cluster_configuration')
        test_utils.patch_function(K8SApi, '_get_dynamic_client')
        self.host_definition_manager = HostDefinitionManager()
        self.host_definition_manager.k8s_api = MagicMock()
        self.fake_host_definition_info = test_utils.get_fake_host_definition_info()
        self.mock_get_status_manifest = patch.object(manifest_utils, 'get_host_definition_status_manifest').start()
        self.mock_get_finalizer_manifest = patch.object(manifest_utils, 'get_finalizer_manifest').start()

    def test_get_host_definition_info_from_secret_success(self):
        result = self.host_definition_manager.get_host_definition_info_from_secret(test_utils.get_fake_secret_info())
        self.assertEqual(result.secret_name, test_settings.FAKE_SECRET)
        self.assertEqual(result.secret_namespace, test_settings.FAKE_SECRET_NAMESPACE)

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
        self.host_definition_manager.k8s_api.list_host_definition.return_value = \
            test_utils.get_fake_k8s_host_definitions_items()
        self.host_definition_manager.generate_host_definition_info = Mock()
        self.host_definition_manager.generate_host_definition_info.return_value = self.fake_host_definition_info
        result = self.host_definition_manager.get_matching_host_definition_info(
            node_name, secret_name, secret_namespace)
        self.assertEqual(result, expected_result)
        self.host_definition_manager.generate_host_definition_info.assert_called_once_with(
            test_utils.get_fake_k8s_host_definitions_items().items[0])
        self.host_definition_manager.k8s_api.list_host_definition.assert_called_once_with()

    def test_get_none_when_host_definition_list_empty_success(self):
        self.host_definition_manager.k8s_api.list_host_definition.return_value = test_utils.get_fake_empty_k8s_list()
        self.host_definition_manager.generate_host_definition_info = Mock()
        result = self.host_definition_manager.get_matching_host_definition_info('', '', '')
        self.assertEqual(result, None)
        self.host_definition_manager.generate_host_definition_info.assert_not_called()
        self.host_definition_manager.k8s_api.list_host_definition.assert_called_once_with()

    def test_create_host_definition_success(self):
        finalizers_manifest = test_manifest_utils.get_finalizers_manifest([test_settings.CSI_IBM_FINALIZER, ])
        self.host_definition_manager.k8s_api.create_host_definition.return_value = \
            test_utils._get_fake_k8s_host_definitions(test_settings.READY_PHASE)
        self.mock_get_finalizer_manifest.return_value = finalizers_manifest
        self.host_definition_manager.generate_host_definition_info = Mock()
        self.host_definition_manager.generate_host_definition_info.return_value = self.fake_host_definition_info
        result = self.host_definition_manager.create_host_definition(
            test_manifest_utils.get_fake_k8s_host_definition_manifest())
        self.assertEqual(result, self.fake_host_definition_info)
        self.host_definition_manager.generate_host_definition_info.assert_called_once_with(
            test_utils._get_fake_k8s_host_definitions(test_settings.READY_PHASE))
        self.mock_get_finalizer_manifest.assert_called_once_with(
            test_settings.FAKE_NODE_NAME, [test_settings.CSI_IBM_FINALIZER, ])
        self.host_definition_manager.k8s_api.patch_host_definition.assert_called_once_with(finalizers_manifest)
        self.host_definition_manager.k8s_api.create_host_definition.assert_called_once_with(
            test_manifest_utils.get_fake_k8s_host_definition_manifest())

    def test_create_host_definition_failure(self):
        self.host_definition_manager.k8s_api.create_host_definition.return_value = None
        self.host_definition_manager.generate_host_definition_info = Mock()
        result = self.host_definition_manager.create_host_definition(
            test_manifest_utils.get_fake_k8s_host_definition_manifest())
        self.assertEqual(result.name, "")
        self.assertEqual(result.node_id, "")
        self.mock_get_finalizer_manifest.assert_not_called()
        self.host_definition_manager.generate_host_definition_info.assert_not_called()
        self.host_definition_manager.k8s_api.patch_host_definition.assert_not_called()
        self.host_definition_manager.k8s_api.create_host_definition.assert_called_once_with(
            test_manifest_utils.get_fake_k8s_host_definition_manifest())

    def test_generate_host_definition_info_success(self):
        k8s_host_definition = test_utils._get_fake_k8s_host_definitions(test_settings.READY_PHASE)
        result = self.host_definition_manager.generate_host_definition_info(k8s_host_definition)
        self.assertEqual(result.name, self.fake_host_definition_info.name)
        self.assertEqual(result.resource_version, self.fake_host_definition_info.resource_version)
        self.assertEqual(result.uid, self.fake_host_definition_info.uid)
        self.assertEqual(result.phase, self.fake_host_definition_info.phase)
        self.assertEqual(result.secret_name, self.fake_host_definition_info.secret_name)
        self.assertEqual(result.secret_namespace, self.fake_host_definition_info.secret_namespace)
        self.assertEqual(result.node_name, self.fake_host_definition_info.node_name)
        self.assertEqual(result.node_id, self.fake_host_definition_info.node_id)
        self.assertEqual(result.connectivity_type, self.fake_host_definition_info.connectivity_type)

    def test_delete_host_definition_success(self):
        self._test_delete_host_definition(200)
        self.host_definition_manager.k8s_api.delete_host_definition.assert_called_once_with(
            test_settings.FAKE_NODE_NAME)

    def test_fail_to_delete_host_definition_because_the_finalizers_fails_to_be_deleted(self):
        self._test_delete_host_definition(405)
        self.host_definition_manager.k8s_api.delete_host_definition.assert_not_called()

    def _test_delete_host_definition(self, finalizers_status_code):
        self.mock_get_finalizer_manifest.return_value = test_manifest_utils.get_finalizers_manifest([])
        self.host_definition_manager.k8s_api.patch_host_definition.return_value = finalizers_status_code
        self.host_definition_manager.delete_host_definition(test_settings.FAKE_NODE_NAME)
        self.host_definition_manager.k8s_api.patch_host_definition.assert_called_once_with(
            test_manifest_utils.get_finalizers_manifest([]))

    def test_set_host_definition_status_success(self):
        status_phase_manifest = test_manifest_utils.get_status_phase_manifest(test_settings.READY_PHASE)
        self.mock_get_status_manifest.return_value = status_phase_manifest
        self.host_definition_manager.set_host_definition_status(test_settings.FAKE_NODE_NAME, test_settings.READY_PHASE)
        self.mock_get_status_manifest.assert_called_once_with(test_settings.READY_PHASE)
        self.host_definition_manager.k8s_api.patch_cluster_custom_object_status.assert_called_once_with(
            common_settings.CSI_IBM_GROUP, common_settings.VERSION, common_settings.HOST_DEFINITION_PLURAL,
            test_settings.FAKE_NODE_NAME, status_phase_manifest)
