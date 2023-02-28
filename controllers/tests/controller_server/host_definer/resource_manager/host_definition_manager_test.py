import unittest
from copy import deepcopy
from unittest.mock import MagicMock, Mock, patch

from controllers.servers.host_definer.resource_manager.host_definition import HostDefinitionManager
from controllers.servers.host_definer.k8s.api import K8SApi
import controllers.common.settings as common_settings
from controllers.servers.host_definer.utils import utils
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
        self.host_definition_manager.k8s_manager = MagicMock()
        self.fake_host_definition_info = test_utils.get_fake_host_definition_info()
        self.fake_secret_info = test_utils.get_fake_secret_info()
        self.mock_get_status_manifest = patch.object(manifest_utils, 'get_host_definition_status_manifest').start()
        self.mock_get_finalizer_manifest = patch.object(manifest_utils, 'get_finalizer_manifest').start()
        self.mock_random_string = patch.object(utils, 'get_random_string').start()
        self.mock_get_host_definition_manifest = patch.object(manifest_utils, 'get_host_definition_manifest').start()
        self.mock_global_managed_nodes = test_utils.patch_nodes_global_variable(
            test_settings.HOST_DEFINITION_MANAGER_PATH)
        self.mock_global_managed_nodes[test_settings.FAKE_NODE_NAME] = test_utils.get_fake_managed_node()
        self.define_response = test_utils.get_fake_define_host_response()

    def test_get_host_definition_info_from_secret_success(self):
        result = self.host_definition_manager.get_host_definition_info_from_secret(self.fake_secret_info)
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

    def test_get_host_definition_info_from_secret_and_node_name_success(self):
        self.host_definition_manager.get_host_definition_info_from_secret = Mock()
        self.host_definition_manager.add_name_to_host_definition_info = Mock()
        self.host_definition_manager.get_host_definition_info_from_secret.return_value = self.fake_host_definition_info
        self.host_definition_manager.add_name_to_host_definition_info.return_value = self.fake_host_definition_info
        result = self.host_definition_manager.get_host_definition_info_from_secret_and_node_name(
            test_settings.FAKE_NODE_NAME, self.fake_secret_info)
        self.assertEqual(result, self.fake_host_definition_info)
        self.host_definition_manager.get_host_definition_info_from_secret.assert_called_once_with(
            self.fake_secret_info)
        self.host_definition_manager.add_name_to_host_definition_info.assert_called_once_with(
            test_settings.FAKE_NODE_NAME, self.fake_host_definition_info)

    def test_add_name_to_host_definition_info_success(self):
        random_string = '2049530945i3094i'
        self.mock_random_string.return_value = random_string
        result = self.host_definition_manager.add_name_to_host_definition_info(
            test_settings.FAKE_NODE_NAME, test_utils.get_fake_empty_host_definition_info())
        self.assertEqual(result.name, '{0}-{1}'.format(test_settings.FAKE_NODE_NAME, random_string).replace('_', '.'))
        self.assertEqual(result.node_name, test_settings.FAKE_NODE_NAME)
        self.assertEqual(result.node_id, test_settings.FAKE_NODE_ID)

    def test_update_host_definition_info_success(self):
        result = self._test_update_host_definition_info(self.fake_host_definition_info)
        self.assertEqual(result.connectivity_type, self.fake_host_definition_info.connectivity_type)
        self.assertEqual(result.node_id, self.fake_host_definition_info.node_id)

    def test_do_not_update_not_found_host_definition_info_success(self):
        result = self._test_update_host_definition_info(None)
        self.assertEqual(result.connectivity_type, 'some_connectivity')
        self.assertEqual(result.node_id, 'some_node_id')

    def _test_update_host_definition_info(self, matching_host_definition_info):
        host_definition_info = deepcopy(self.fake_host_definition_info)
        host_definition_info.connectivity_type = 'some_connectivity'
        host_definition_info.node_id = 'some_node_id'
        self._prepare_get_matching_host_definition_info_function_as_mock(matching_host_definition_info)
        result = self.host_definition_manager.update_host_definition_info(host_definition_info)
        self._assert_get_matching_host_definition_called_once_with()
        return result

    def test_create_exist_host_definition_success(self):
        host_definition_manifest = self._test_create_host_definition_if_not_exist(
            'different_name', self.fake_host_definition_info, None)
        host_definition_manifest[test_settings.METADATA_FIELD][common_settings.NAME_FIELD] = \
            test_settings.FAKE_NODE_NAME
        self.host_definition_manager.k8s_api.patch_host_definition.assert_called_once_with(host_definition_manifest)
        self.host_definition_manager.create_host_definition.assert_not_called()

    def test_create_new_host_definition_success(self):
        host_definition_manifest = self._test_create_host_definition_if_not_exist(
            test_settings.FAKE_NODE_NAME, None, self.fake_host_definition_info)
        self.host_definition_manager.k8s_api.patch_host_definition.assert_not_called()
        self.host_definition_manager.create_host_definition.assert_called_once_with(host_definition_manifest)

    def _test_create_host_definition_if_not_exist(self, new_host_definition_name,
                                                  matching_host_definition, created_host_definition):
        host_definition_manifest = deepcopy(test_manifest_utils.get_fake_k8s_host_definition_manifest())
        host_definition_manifest[test_settings.METADATA_FIELD][common_settings.NAME_FIELD] = new_host_definition_name
        self.mock_get_host_definition_manifest.return_value = host_definition_manifest
        self._prepare_get_matching_host_definition_info_function_as_mock(matching_host_definition)
        self.host_definition_manager.create_host_definition = Mock()
        self.host_definition_manager.create_host_definition.return_value = created_host_definition
        result = self.host_definition_manager.create_host_definition_if_not_exist(
            self.fake_host_definition_info, self.define_response)

        self.assertEqual(result, self.fake_host_definition_info)
        self.mock_get_host_definition_manifest.assert_called_once_with(
            self.fake_host_definition_info, self.define_response, test_settings.FAKE_NODE_ID)
        self._assert_get_matching_host_definition_called_once_with()
        return host_definition_manifest

    def test_set_host_definition_status_to_ready_success(self):
        self.host_definition_manager.set_host_definition_status = Mock()
        self.host_definition_manager.create_k8s_event_for_host_definition = Mock()
        self.host_definition_manager.set_host_definition_status_to_ready(self.fake_host_definition_info)
        self.host_definition_manager.set_host_definition_status.assert_called_once_with(
            self.fake_host_definition_info.name, test_settings.READY_PHASE)
        self.host_definition_manager.create_k8s_event_for_host_definition.assert_called_once_with(
            self.fake_host_definition_info, test_settings.MESSAGE,
            test_settings.DEFINE_ACTION, test_settings.SUCCESSFUL_MESSAGE_TYPE)

    def test_create_k8s_event_for_host_definition_success(self):
        k8s_event = test_utils.get_event_object_metadata()
        self.host_definition_manager.k8s_manager.generate_k8s_event.return_value = k8s_event
        self.host_definition_manager.create_k8s_event_for_host_definition(
            self.fake_host_definition_info, test_settings.MESSAGE,
            test_settings.DEFINE_ACTION, test_settings.SUCCESSFUL_MESSAGE_TYPE)
        self.host_definition_manager.k8s_manager.generate_k8s_event.assert_called_once_with(
            self.fake_host_definition_info, test_settings.MESSAGE,
            test_settings.DEFINE_ACTION, test_settings.SUCCESSFUL_MESSAGE_TYPE)
        self.host_definition_manager.k8s_api.create_event.assert_called_once_with(test_settings.DEFAULT_NAMESPACE,
                                                                                  k8s_event)

    def test_set_host_definition_status_to_pending_and_create_event_after_failed_definition(self):
        self._prepare_set_status_to_host_definition_after_definition(test_settings.MESSAGE)
        self.host_definition_manager.set_host_definition_status.assert_called_once_with(
            self.fake_host_definition_info.name, test_settings.PENDING_CREATION_PHASE)
        self.host_definition_manager.create_k8s_event_for_host_definition.assert_called_once_with(
            self.fake_host_definition_info, test_settings.MESSAGE,
            test_settings.DEFINE_ACTION, test_settings.FAILED_MESSAGE_TYPE)
        self.host_definition_manager.set_host_definition_status_to_ready.assert_not_called()

    def test_set_host_definition_status_to_ready_after_successful_definition(self):
        self._prepare_set_status_to_host_definition_after_definition('')
        self.host_definition_manager.set_host_definition_status.assert_not_called()
        self.host_definition_manager.create_k8s_event_for_host_definition.assert_not_called()
        self.host_definition_manager.set_host_definition_status_to_ready.assert_called_once_with(
            self.fake_host_definition_info)

    def _prepare_set_status_to_host_definition_after_definition(self, message_from_storage):
        self.host_definition_manager.set_host_definition_status = Mock()
        self.host_definition_manager.create_k8s_event_for_host_definition = Mock()
        self.host_definition_manager.set_host_definition_status_to_ready = Mock()
        self.host_definition_manager.set_status_to_host_definition_after_definition(
            message_from_storage, self.fake_host_definition_info)

    def test_handle_host_definition_after_failed_undefine_action_and_when_host_definition_exist(self):
        self._test_handle_k8s_host_definition_after_undefine_action_if_exist(
            self.fake_host_definition_info, self.define_response)
        self.host_definition_manager.set_host_definition_status.assert_called_once_with(
            self.fake_host_definition_info.name, test_settings.PENDING_DELETION_PHASE)
        self.host_definition_manager.create_k8s_event_for_host_definition.assert_called_once_with(
            self.fake_host_definition_info, self.define_response.error_message, test_settings.UNDEFINE_ACTION,
            test_settings.FAILED_MESSAGE_TYPE)
        self.host_definition_manager.delete_host_definition.assert_not_called()

    def test_handle_host_definition_after_successful_undefine_action_and_when_host_definition_exist(self):
        define_response = deepcopy(self.define_response)
        define_response.error_message = ''
        self._test_handle_k8s_host_definition_after_undefine_action_if_exist(
            self.fake_host_definition_info, define_response)
        self.host_definition_manager.set_host_definition_status.assert_not_called()
        self.host_definition_manager.create_k8s_event_for_host_definition.assert_not_called()
        self.host_definition_manager.delete_host_definition.assert_called_once_with(
            self.fake_host_definition_info.name)

    def test_handle_k8s_host_definition_after_undefine_action_when_not_exist(self):
        self._test_handle_k8s_host_definition_after_undefine_action_if_exist(None, self.define_response)
        self.host_definition_manager.set_host_definition_status.assert_not_called()
        self.host_definition_manager.create_k8s_event_for_host_definition.assert_not_called()
        self.host_definition_manager.delete_host_definition.assert_not_called()

    def _test_handle_k8s_host_definition_after_undefine_action_if_exist(
            self, matching_host_definition, define_response):
        self.host_definition_manager.set_host_definition_status = Mock()
        self.host_definition_manager.create_k8s_event_for_host_definition = Mock()
        self.host_definition_manager.delete_host_definition = Mock()
        self._prepare_get_matching_host_definition_info_function_as_mock(matching_host_definition)
        self.host_definition_manager.handle_k8s_host_definition_after_undefine_action_if_exist(
            self.fake_host_definition_info, define_response)
        self._assert_get_matching_host_definition_called_once_with()

    def test_return_true_when_host_definition_phase_is_pending(self):
        result = self.host_definition_manager.is_host_definition_in_pending_phase(test_settings.PENDING_CREATION_PHASE)
        self.assertTrue(result)

    def test_return_false_when_host_definition_phase_is_not_pending(self):
        result = self.host_definition_manager.is_host_definition_in_pending_phase(test_settings.READY_PHASE)
        self.assertFalse(result)

    def test_set_host_definition_status_to_error_success(self):
        self.host_definition_manager.set_host_definition_status = Mock()
        self.host_definition_manager.set_host_definition_phase_to_error(self.fake_host_definition_info)
        self.host_definition_manager.set_host_definition_status.assert_called_once_with(
            self.fake_host_definition_info.name, test_settings.ERROR_PHASE)

    def test_return_true_when_host_definition_is_not_pending_and_exist(self):
        result = self._test_is_host_definition_not_pending(self.fake_host_definition_info)
        self.assertTrue(result)

    def test_return_true_when_host_definition_is_not_exist_after_it_was_pending(self):
        result = self._test_is_host_definition_not_pending(None)
        self.assertTrue(result)

    def test_return_false_when_host_definition_exist_but_still_pending(self):
        host_definition_info = deepcopy(self.fake_host_definition_info)
        host_definition_info.phase = test_settings.PENDING_CREATION_PHASE
        result = self._test_is_host_definition_not_pending(host_definition_info)
        self.assertFalse(result)

    def _test_is_host_definition_not_pending(self, matching_host_definition):
        self._prepare_get_matching_host_definition_info_function_as_mock(matching_host_definition)
        result = self.host_definition_manager.is_host_definition_not_pending(self.fake_host_definition_info)
        self._assert_get_matching_host_definition_called_once_with()
        return result

    def _prepare_get_matching_host_definition_info_function_as_mock(self, matching_host_definition):
        self.host_definition_manager.get_matching_host_definition_info = Mock()
        self.host_definition_manager.get_matching_host_definition_info.return_value = matching_host_definition

    def _assert_get_matching_host_definition_called_once_with(self):
        self.host_definition_manager.get_matching_host_definition_info.assert_called_once_with(
            self.fake_host_definition_info.name, self.fake_host_definition_info.secret_name,
            self.fake_host_definition_info.secret_namespace)
