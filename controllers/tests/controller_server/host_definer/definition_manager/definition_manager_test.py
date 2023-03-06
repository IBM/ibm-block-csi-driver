import unittest
from copy import deepcopy
from unittest.mock import MagicMock, Mock, patch

import controllers.servers.host_definer.messages as messages
from controllers.servers.host_definer.utils import manifest_utils
from controllers.servers.host_definer.types import DefineHostResponse
from controllers.servers.host_definer.k8s.api import K8SApi
import controllers.tests.controller_server.host_definer.utils.test_utils as test_utils
import controllers.tests.controller_server.host_definer.utils.k8s_manifests_utils as test_manifest_utils
import controllers.tests.controller_server.host_definer.settings as test_settings
from controllers.servers.host_definer.definition_manager.definition import DefinitionManager


class TestDefinitionManager(unittest.TestCase):
    def setUp(self):
        test_utils.patch_function(K8SApi, '_load_cluster_configuration')
        test_utils.patch_function(K8SApi, '_get_dynamic_client')
        self.mock_generate_response = patch.object(
            manifest_utils, 'generate_host_definition_response_fields_manifest').start()
        self.manager = DefinitionManager()
        self.manager.secret_manager = MagicMock()
        self.manager.k8s_api = MagicMock()
        self.manager.request_manager = MagicMock()
        self.manager.host_definition_manager = MagicMock()
        self.manager.storage_host_servicer = MagicMock()
        self.global_managed_nodes = test_utils.patch_nodes_global_variable(
            test_settings.DEFINITION_MANAGER_PATH)
        self.global_managed_secrets = test_utils.patch_managed_secrets_global_variable(
            test_settings.DEFINITION_MANAGER_PATH)
        self.fake_host_define_response = test_utils.get_fake_define_host_response()
        self.fake_host_definition_info = test_utils.get_fake_host_definition_info()
        self.fake_secret_info = test_utils.get_fake_secret_info()
        self.fake_host_definition_manifest = test_manifest_utils.get_fake_k8s_host_definition_manifest()
        self.secret_info_with_no_storage_classes = test_utils.get_fake_secret_info(0)
        self.secret_info_with_storage_classes = test_utils.get_fake_secret_info(2)

    def test_define_host_on_all_storages_success(self):
        self.global_managed_secrets.append(self.secret_info_with_no_storage_classes)
        self.global_managed_secrets.append(self.secret_info_with_storage_classes)
        self.manager.host_definition_manager.get_host_definition_info_from_secret_and_node_name.return_value = \
            self.fake_host_definition_info
        self.manager.create_definition = Mock()
        self.manager.define_node_on_all_storages(test_settings.FAKE_NODE_NAME)
        self.manager.host_definition_manager.get_host_definition_info_from_secret_and_node_name.assert_called_once_with(
            test_settings.FAKE_NODE_NAME, self.secret_info_with_storage_classes)
        self.manager.create_definition.assert_called_once_with(self.fake_host_definition_info)

    def test_define_single_nodes(self):
        self.global_managed_nodes[test_settings.FAKE_NODE_NAME] = test_utils.get_fake_managed_node()
        host_definition_info = deepcopy(self.fake_host_definition_info)
        host_definition_info.node_name = 'test_name'
        self.manager.host_definition_manager.add_name_to_host_definition_info.return_value = \
            self.fake_host_definition_info
        self.manager.create_definition = Mock()
        self.manager.define_nodes(host_definition_info)
        self.manager.host_definition_manager.add_name_to_host_definition_info.assert_called_once_with(
            test_settings.FAKE_NODE_NAME, host_definition_info)
        self.manager.create_definition.assert_called_once_with(self.fake_host_definition_info)

    def test_define_multiple_nodes(self):
        self.global_managed_nodes[test_settings.FAKE_NODE_NAME] = test_utils.get_fake_managed_node()
        self.global_managed_nodes[test_settings.FAKE_NODE_NAME + '2'] = test_utils.get_fake_managed_node()
        self.manager.create_definition = Mock()
        self.manager.define_nodes(self.fake_host_definition_info)
        self.assertEqual(self.manager.create_definition.call_count, 2)
        self.assertEqual(self.manager.host_definition_manager.add_name_to_host_definition_info.call_count, 2)

    def test_create_definition_success(self):
        current_host_definition_info_on_cluster = self._prepare_create_definition(True)
        self._test_create_host_definition()

        self.manager.host_definition_manager.update_host_definition_info.assert_called_once_with(
            self.fake_host_definition_info)
        self.manager.define_host.assert_called_once_with(self.fake_host_definition_info)
        self.manager.host_definition_manager.create_host_definition_if_not_exist.assert_called_once_with(
            self.fake_host_definition_info, self.fake_host_define_response)
        self.manager.host_definition_manager.set_status_to_host_definition_after_definition.assert_called_once_with(
            self.fake_host_define_response.error_message, current_host_definition_info_on_cluster)

    def test_do_not_create_definition_when_node_should_not_be_managed_by_secret(self):
        self._prepare_create_definition(False)
        self._test_create_host_definition()
        self.manager.host_definition_manager.update_host_definition_info.assert_not_called()
        self.manager.define_host.assert_not_called()
        self.manager.host_definition_manager.create_host_definition_if_not_exist.assert_not_called()
        self.manager.host_definition_manager.set_status_to_host_definition_after_definition.assert_not_called()

    def _prepare_create_definition(self, is_node_should_be_managed):
        self.manager.secret_manager.is_node_should_be_managed_on_secret.return_value = is_node_should_be_managed
        self.manager.define_host = Mock()
        self.manager.define_host.return_value = self.fake_host_define_response
        current_host_definition_info_on_cluster = deepcopy(self.fake_host_definition_info)
        current_host_definition_info_on_cluster.node_name = 'current_host_on_cluster'
        self.manager.host_definition_manager.update_host_definition_info.return_value = self.fake_host_definition_info
        self.manager.host_definition_manager.create_host_definition_if_not_exist.return_value = \
            current_host_definition_info_on_cluster
        return current_host_definition_info_on_cluster

    def _test_create_host_definition(self):
        self.manager.create_definition(self.fake_host_definition_info)
        self._assert_is_node_should_be_managed()

    def test_define_host_success(self):
        self._test_define_host('request', 'response', self.manager.storage_host_servicer.define_host)
        self.manager.storage_host_servicer.define_host.assert_called_once_with('request')

    def test_fail_to_generate_request_for_define_host(self):
        expected_response = self._get_response_after_failing_to_generate_request()
        self._test_define_host(None, expected_response, self.manager.storage_host_servicer.define_host)
        self.manager.storage_host_servicer.define_host.assert_not_called()

    def _test_define_host(self, request, expected_response, define_function):
        self._ensure_definition_state_function(request, expected_response, define_function)
        result = self.manager.define_host(self.fake_host_definition_info)
        self._assert_definition_state(expected_response, result)

    def test_undefine_host_success(self):
        self._test_undefine_host('request', 'response', self.manager.storage_host_servicer.undefine_host)
        self.manager.storage_host_servicer.undefine_host.assert_called_once_with('request')

    def test_fail_to_generate_request_for_undefine_host(self):
        expected_response = self._get_response_after_failing_to_generate_request()
        self._test_undefine_host(None, expected_response, self.manager.storage_host_servicer.undefine_host)
        self.manager.storage_host_servicer.define_host.assert_not_called()

    def _get_response_after_failing_to_generate_request(self):
        response = DefineHostResponse()
        response.error_message = messages.FAILED_TO_GET_SECRET_EVENT.format(
            test_settings.FAKE_SECRET, test_settings.FAKE_SECRET_NAMESPACE)
        return response

    def _test_undefine_host(self, request, expected_response, define_function):
        self._ensure_definition_state_function(request, expected_response, define_function)
        result = self.manager.undefine_host(self.fake_host_definition_info)
        self._assert_definition_state(expected_response, result)

    def _ensure_definition_state_function(self, request, expected_response, define_function):
        self.manager.request_manager.generate_request.return_value = request
        define_function.return_value = expected_response

    def _assert_definition_state(self, expected_response, result):
        self.assertEqual(result, expected_response)
        self.manager.request_manager.generate_request.assert_called_once_with(self.fake_host_definition_info)

    def test_delete_definition_success(self):
        self._test_delete_definition(True, 'response')
        self.manager.undefine_host.assert_called_once_with(self.fake_host_definition_info)

    def test_do_not_undefine_host_when_node_should_not_be_managed_by_secret(self):
        self._test_delete_definition(False, DefineHostResponse())
        self.manager.undefine_host.assert_not_called()

    def _test_delete_definition(self, is_node_should_be_managed, expected_response):
        self.manager.secret_manager.is_node_should_be_managed_on_secret.return_value = is_node_should_be_managed
        self._prepare_undefine_host(expected_response)
        self.manager.delete_definition(self.fake_host_definition_info)
        self._assert_is_node_should_be_managed()
        self.manager.host_definition_manager.handle_k8s_host_definition_after_undefine_action.assert_called_once_with(
            self.fake_host_definition_info, expected_response)

    def test_undefine_multiple_node_definitions_success(self):
        self.global_managed_secrets.append(self.secret_info_with_storage_classes)
        self.global_managed_secrets.append(self.secret_info_with_storage_classes)
        self._test_undefine_node_definitions()
        self.assertEqual(
            self.manager.host_definition_manager.get_host_definition_info_from_secret_and_node_name.call_count, 2)
        self.assertEqual(self.manager.delete_definition.call_count, 2)

    def test_undefine_single_node_definition_success(self):
        self.global_managed_secrets.append(self.secret_info_with_storage_classes)
        self._test_undefine_node_definitions()
        self.manager.host_definition_manager.get_host_definition_info_from_secret_and_node_name.assert_called_once_with(
            test_settings.FAKE_NODE_NAME, self.secret_info_with_storage_classes)
        self.manager.delete_definition.assert_called_once_with(self.fake_host_definition_info)

    def _test_undefine_node_definitions(self):
        self.manager.delete_definition = Mock()
        self.manager.host_definition_manager.get_host_definition_info_from_secret_and_node_name.return_value = \
            self.fake_host_definition_info
        self.manager.undefine_node_definitions(test_settings.FAKE_NODE_NAME)

    def test_undefine_host_after_pending_success(self):
        self._test_undefine_host_after_pending(True, 'response')

    def test_do_not_undefine_host_after_pending_when_node_should_not_be_managed_by_secret(self):
        self._test_undefine_host_after_pending(False, DefineHostResponse())

    def _test_undefine_host_after_pending(self, is_node_should_be_managed, expected_response):
        self.manager.secret_manager.is_node_should_be_managed_on_secret.return_value = is_node_should_be_managed
        self._prepare_undefine_host(expected_response)
        result = self.manager.undefine_host_after_pending(self.fake_host_definition_info)
        self.assertEqual(result, expected_response)
        self._assert_is_node_should_be_managed()

    def _prepare_undefine_host(self, expected_response):
        self.manager.undefine_host = Mock()
        self.manager.undefine_host.return_value = expected_response

    def test_define_host_after_pending_success(self):
        self._test_define_host_after_pending(True, 'response')
        self.mock_generate_response.assert_called_once_with(self.fake_host_definition_info.node_name, 'response')
        self.manager.k8s_api.patch_host_definition.assert_called_once_with(self.fake_host_definition_manifest)

    def test_do_not_define_host_after_pending_when_node_should_not_be_managed_by_secret(self):
        self._test_define_host_after_pending(False, DefineHostResponse())
        self.mock_generate_response.assert_not_called()
        self.manager.k8s_api.patch_host_definition.assert_not_called()

    def _test_define_host_after_pending(self, is_node_should_be_managed, expected_response):
        self.manager.secret_manager.is_node_should_be_managed_on_secret.return_value = is_node_should_be_managed
        self._prepare_define_host(expected_response)
        self.mock_generate_response.return_value = self.fake_host_definition_manifest
        result = self.manager.define_host_after_pending(self.fake_host_definition_info)
        self.assertEqual(result, expected_response)
        self._assert_is_node_should_be_managed()

    def _prepare_define_host(self, expected_response):
        self.manager.define_host = Mock()
        self.manager.define_host.return_value = expected_response

    def _assert_is_node_should_be_managed(self):
        self.manager.secret_manager.is_node_should_be_managed_on_secret.assert_called_once_with(
            test_settings.FAKE_NODE_NAME, test_settings.FAKE_SECRET, test_settings.FAKE_SECRET_NAMESPACE)

    def test_define_nodes_when_new_secret_which_is_not_managed_yet(self):
        secret_info = deepcopy(self.fake_secret_info)
        self.manager.secret_manager.get_matching_managed_secret_info.return_value = (
            self.fake_secret_info, -1)
        self._test_define_nodes_when_new_secret(secret_info, 1)
        self._assert_define_nodes_from_secret_info_called(secret_info)

    def test_define_nodes_when_new_secret_which_is_managed_already_and_does_not_have_managed_storage_classes(self):
        self.global_managed_secrets.append(self.secret_info_with_no_storage_classes)
        secret_info = deepcopy(self.fake_secret_info)
        self.manager.secret_manager.get_matching_managed_secret_info.return_value = (
            self.fake_secret_info, 0)
        self._test_define_nodes_when_new_secret(secret_info, 1)
        self._assert_define_nodes_from_secret_info_called(secret_info)

    def test_define_nodes_when_new_secret_which_is_managed_already_with_storage_classes(self):
        self.global_managed_secrets.append(self.secret_info_with_storage_classes)
        secret_info = deepcopy(self.fake_secret_info)
        secret_info.managed_storage_classes = 2
        self.manager.secret_manager.get_matching_managed_secret_info.return_value = (
            secret_info, 0)
        self._test_define_nodes_when_new_secret(secret_info, 3)
        self.manager.host_definition_manager.get_host_definition_info_from_secret.assert_not_called()
        self.manager.define_nodes.assert_not_called()

    def _test_define_nodes_when_new_secret(self, secret_info, expected_managed_storage_classes):
        self._prepare_define_nodes_when_new_secret()
        self.manager.define_nodes_when_new_secret(secret_info)
        self.manager.secret_manager.get_matching_managed_secret_info.assert_called_once_with(secret_info)
        self._assert_define_node_when_new_secret(secret_info, expected_managed_storage_classes)

    def _assert_define_node_when_new_secret(self, secret_info, expected_managed_storage_classes):
        secret_info.managed_storage_classes = expected_managed_storage_classes
        self.assertEqual(len(self.global_managed_secrets), 1)
        self.assertEqual(self.global_managed_secrets, [secret_info])

    def _prepare_define_nodes_when_new_secret(self):
        self.manager.define_nodes = Mock()
        self.manager.host_definition_manager.get_host_definition_info_from_secret.return_value = \
            self.fake_host_definition_info

    def _assert_define_nodes_from_secret_info_called(self, secret_info):
        self.manager.host_definition_manager.get_host_definition_info_from_secret.assert_called_once_with(secret_info)
        self.manager.define_nodes.assert_called_once_with(self.fake_host_definition_info)
