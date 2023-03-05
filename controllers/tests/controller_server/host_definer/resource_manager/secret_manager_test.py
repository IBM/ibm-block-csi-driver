import unittest
from copy import deepcopy
from unittest.mock import MagicMock, Mock, patch

from controllers.servers.host_definer.resource_manager.secret import SecretManager
from controllers.servers.host_definer.k8s.api import K8SApi
from controllers.servers.host_definer.types import SecretInfo
from controllers.servers.host_definer.utils import utils
import controllers.tests.controller_server.host_definer.utils.test_utils as test_utils
import controllers.tests.controller_server.host_definer.utils.k8s_manifests_utils as test_manifest_utils
import controllers.tests.controller_server.host_definer.settings as test_settings


class TestSecretManager(unittest.TestCase):
    def setUp(self):
        test_utils.patch_function(K8SApi, '_load_cluster_configuration')
        test_utils.patch_function(K8SApi, '_get_dynamic_client')
        self.secret_manager = SecretManager()
        self.secret_manager.k8s_api = MagicMock()
        self.fake_secret_info = test_utils.get_fake_secret_info()
        self.fake_secret_data = test_utils.get_fake_k8s_secret().data
        self.fake_k8s_secret = test_utils.get_fake_k8s_secret()
        self.secret_config_with_system_info = test_manifest_utils.get_fake_secret_config_with_system_info_manifest()
        self.mock_decode_secret_config = patch.object(utils, 'change_decode_base64_secret_config').start()
        self.mock_validate_secret = patch.object(utils, 'validate_secret').start()
        self.mock_is_topology_label = patch.object(utils, 'is_topology_label').start()
        self.mock_utils_get_secret_config = patch.object(utils, 'get_secret_config').start()
        self.mock_is_deleted_watch_object_type = patch.object(utils, 'is_watch_object_type_is_delete').start()
        self.mock_utils_get_array_connectivity_info = patch.object(
            utils, 'get_array_connection_info_from_secret_data').start()
        self.expected_decode_secret_config = 'decoded secret config'
        self.managed_secrets = patch('{}.MANAGED_SECRETS'.format(test_settings.SECRET_MANAGER_PATH),
                                     [self.fake_secret_info]).start()
        self.mock_is_topology_match = patch('{}.is_topology_match'.format(test_settings.SECRET_MANAGER_PATH)).start()

    def test_get_secret_data_success(self):
        result = self._test_get_secret_data(self.fake_secret_data)
        self.mock_decode_secret_config.assert_called_once_with(self.fake_secret_data)
        self.assertEqual(result, self.expected_decode_secret_config)

    def test_get_empty_dict_when_there_is_no_secret_data(self):
        result = self._test_get_secret_data({})
        self.mock_decode_secret_config.assert_not_called()
        self.assertEqual(result, {})

    def _test_get_secret_data(self, secret_data):
        self.secret_manager.k8s_api.get_secret_data.return_value = secret_data
        self.mock_decode_secret_config.return_value = self.expected_decode_secret_config
        result = self.secret_manager.get_secret_data(test_settings.FAKE_SECRET, test_settings.FAKE_SECRET_NAMESPACE)
        self.secret_manager.k8s_api.get_secret_data.assert_called_once_with(test_settings.FAKE_SECRET,
                                                                            test_settings.FAKE_SECRET_NAMESPACE)
        return result

    def test_return_true_when_node_should_be_managed_on_secret(self):
        self._prepare_is_node_should_be_managed_on_secret()
        result = self._test_is_node_should_be_managed_on_secret(True)
        self.assertTrue(result)

    def test_return_false_when_node_should_be_managed_on_secret(self):
        self._prepare_is_node_should_be_managed_on_secret()
        result = self._test_is_node_should_be_managed_on_secret(False)
        self.assertFalse(result)

    def _prepare_is_node_should_be_managed_on_secret(self):
        self._prepare_get_secret_data(self.fake_secret_data)
        self.secret_manager.generate_secret_info = Mock()
        self.secret_manager.generate_secret_info.return_value = self.fake_secret_info
        self._prepare_get_matching_managed_secret_info(0)
        self.secret_manager.is_node_should_managed_on_secret_info = Mock()

    def _test_is_node_should_be_managed_on_secret(self, is_node_should_be_managed):
        self.secret_manager.is_node_should_managed_on_secret_info.return_value = is_node_should_be_managed
        result = self.secret_manager.is_node_should_be_managed_on_secret(
            test_settings.FAKE_NODE_NAME, test_settings.FAKE_SECRET, test_settings.FAKE_SECRET_NAMESPACE)
        self.secret_manager.get_secret_data.assert_called_once_with(
            test_settings.FAKE_SECRET, test_settings.FAKE_SECRET_NAMESPACE)
        self.mock_validate_secret.assert_called_once_with(self.fake_secret_data)
        self.secret_manager.generate_secret_info.assert_called_once_with(
            test_settings.FAKE_SECRET, test_settings.FAKE_SECRET_NAMESPACE)
        self.secret_manager.get_matching_managed_secret_info.assert_called_once_with(self.fake_secret_info)
        self.secret_manager.is_node_should_managed_on_secret_info.assert_called_once_with(
            test_settings.FAKE_NODE_NAME, self.fake_secret_info)
        return result

    def test_generate_secret_info_success(self):
        result = self.secret_manager.generate_secret_info(
            test_settings.FAKE_SECRET, test_settings.FAKE_SECRET_NAMESPACE, 'input1', 'input2')
        self.assertEqual(result.name, test_settings.FAKE_SECRET)
        self.assertEqual(result.namespace, test_settings.FAKE_SECRET_NAMESPACE)
        self.assertEqual(result.nodes_with_system_id, 'input1')
        self.assertEqual(result.system_ids_topologies, 'input2')
        self.assertEqual(type(result), SecretInfo)

    def test_generate_secret_info_defaults_success(self):
        result = self.secret_manager.generate_secret_info(test_settings.FAKE_SECRET,
                                                          test_settings.FAKE_SECRET_NAMESPACE)
        self.assertEqual(result.name, test_settings.FAKE_SECRET)
        self.assertEqual(result.namespace, test_settings.FAKE_SECRET_NAMESPACE)
        self.assertEqual(result.nodes_with_system_id, {})
        self.assertEqual(result.system_ids_topologies, {})
        self.assertEqual(type(result), SecretInfo)

    def test_node_should_be_managed_when_node_name_in_nodes_with_system_id(self):
        result = self.secret_manager.is_node_should_managed_on_secret_info(
            test_settings.FAKE_NODE_NAME, self.fake_secret_info)
        self.assertTrue(result)

    def test_node_should_be_managed_when_nodes_with_system_id_is_empty(self):
        secret_info = deepcopy(self.fake_secret_info)
        secret_info.nodes_with_system_id = {}
        result = self.secret_manager.is_node_should_managed_on_secret_info(
            test_settings.FAKE_NODE_NAME, self.fake_secret_info)
        self.assertTrue(result)

    def test_node_should_not_be_managed_when_node_not_in_nodes_with_system_id(self):
        result = self.secret_manager.is_node_should_managed_on_secret_info('bad_node', self.fake_secret_info)
        self.assertFalse(result)

    def test_node_should_not_be_managed_on_empty_secret_info(self):
        result = self.secret_manager.is_node_should_managed_on_secret_info(test_settings.FAKE_NODE_NAME, None)
        self.assertFalse(result)

    def test_get_matching_managed_secret_info_success(self):
        result = self.secret_manager.get_matching_managed_secret_info(self.fake_secret_info)
        self.assertEqual(result, (self.fake_secret_info, 0))

    def test_do_not_find_matching_secret_info(self):
        secret_info = deepcopy(self.fake_secret_info)
        secret_info.name = 'bad_name'
        result = self.secret_manager.get_matching_managed_secret_info(secret_info)
        self.assertEqual(result, (secret_info, -1))

    def test_get_second_matching_managed_secret_info_success(self):
        secret_info = deepcopy(self.fake_secret_info)
        secret_info.name = 'name'
        self.managed_secrets.append(secret_info)
        result = self.secret_manager.get_matching_managed_secret_info(secret_info)
        self.assertEqual(result, (secret_info, 1))

    def test_return_true_when_node_in_system_ids_topologies(self):
        result = self._test_is_node_in_system_ids_topologies(test_settings.FAKE_SYSTEM_ID)
        self.assertTrue(result)

    def test_return_false_when_node_not_in_system_ids_topologies(self):
        result = self._test_is_node_in_system_ids_topologies('')
        self.assertFalse(result)

    def _test_is_node_in_system_ids_topologies(self, system_id):
        node_labels = [test_settings.MANAGE_NODE_LABEL]
        self.secret_manager.get_system_id_for_node_labels = Mock()
        self.secret_manager.get_system_id_for_node_labels.return_value = system_id
        result = self.secret_manager.is_node_in_system_ids_topologies(self.fake_secret_info, node_labels)
        self.secret_manager.get_system_id_for_node_labels.assert_called_once_with(self.fake_secret_info, node_labels)
        return result

    def test_get_system_id_when_system_ids_topologies_with_multiple_system_ids(self):
        system_ids_topologies = {
            test_settings.FAKE_SYSTEM_ID + '1': test_settings.FAKE_TOPOLOGY_LABELS,
            test_settings.FAKE_SYSTEM_ID + '2': test_settings.FAKE_TOPOLOGY_LABELS}
        result = self._test_get_system_id_for_node_labels([False, True], system_ids_topologies)
        self.assertEqual(result, test_settings.FAKE_SYSTEM_ID + '2')
        self.assertEqual(self.mock_is_topology_match.call_count, 2)

    def test_get_system_id_when_node_topology_labels_match(self):
        result = self._test_get_system_id_for_node_labels([True], test_settings.FAKE_SYSTEM_IDS_TOPOLOGIES)
        self.assertEqual(result, test_settings.FAKE_SYSTEM_ID)
        self.mock_is_topology_match.assert_called_once_with(test_settings.FAKE_TOPOLOGY_LABELS,
                                                            test_settings.FAKE_TOPOLOGY_LABELS)

    def test_get_empty_string_when_node_topology_labels_do_not_match(self):
        result = self._test_get_system_id_for_node_labels([False], test_settings.FAKE_SYSTEM_IDS_TOPOLOGIES)
        self.assertEqual(result, '')
        self.mock_is_topology_match.assert_called_once_with(test_settings.FAKE_TOPOLOGY_LABELS,
                                                            test_settings.FAKE_TOPOLOGY_LABELS)

    def test_get_empty_string_when_system_ids_topologies_is_empty(self):
        result = self._test_get_system_id_for_node_labels([False], {})
        self.assertEqual(result, '')
        self.mock_is_topology_match.assert_not_called()

    def _test_get_system_id_for_node_labels(self, is_topology_match, system_ids_topologies):
        self._prepare_get_topology_labels()
        self.mock_is_topology_match.side_effect = is_topology_match
        result = self.secret_manager.get_system_id_for_node_labels(system_ids_topologies,
                                                                   test_settings.FAKE_TOPOLOGY_LABELS)
        self.secret_manager.get_topology_labels.assert_called_once_with(test_settings.FAKE_TOPOLOGY_LABELS)
        return result

    def test_generate_k8s_secret_to_secret_info_success(self):
        result = self.secret_manager.generate_k8s_secret_to_secret_info(self.fake_k8s_secret, 'input1', 'input2')
        self.assertEqual(result.name, test_settings.FAKE_SECRET)
        self.assertEqual(result.namespace, test_settings.FAKE_SECRET_NAMESPACE)
        self.assertEqual(result.nodes_with_system_id, 'input1')
        self.assertEqual(result.system_ids_topologies, 'input2')
        self.assertEqual(type(result), SecretInfo)

    def test_generate_k8s_secret_to_secret_info_defaults_success(self):
        result = self.secret_manager.generate_k8s_secret_to_secret_info(self.fake_k8s_secret)
        self.assertEqual(result.name, test_settings.FAKE_SECRET)
        self.assertEqual(result.namespace, test_settings.FAKE_SECRET_NAMESPACE)
        self.assertEqual(result.nodes_with_system_id, {})
        self.assertEqual(result.system_ids_topologies, {})
        self.assertEqual(type(result), SecretInfo)

    def test_when_secret_have_config_field_it_is_considered_as_topology(self):
        result = self._test_is_topology_secret('secret_config')
        self.assertTrue(result)

    def test_when_secret_do_not_have_config_field_it_is_do_not_considered_as_topology(self):
        result = self._test_is_topology_secret({})
        self.assertFalse(result)

    def _test_is_topology_secret(self, secret_config):
        self.mock_utils_get_secret_config.return_value = secret_config
        result = self.secret_manager.is_topology_secret(self.fake_secret_data)
        self.mock_validate_secret.assert_called_once_with(self.fake_secret_data)
        self.mock_utils_get_secret_config.assert_called_once_with(self.fake_secret_data)
        return result

    def test_get_only_first_label_when_it_is_a_topology(self):
        expected_result = {test_settings.FAKE_TOPOLOGY_LABEL + '1': test_settings.TRUE_STRING}
        self._test_get_topology_labels([True, False], test_settings.FAKE_TOPOLOGY_LABELS, expected_result, 2)

    def test_get_both_labels_when_they_are_topology(self):
        expected_result = test_settings.FAKE_TOPOLOGY_LABELS
        self._test_get_topology_labels([True, True], test_settings.FAKE_TOPOLOGY_LABELS, expected_result, 2)

    def test_get_empty_dict_when_non_of_the_labels_are_topology(self):
        expected_result = {}
        self._test_get_topology_labels([False, False], test_settings.FAKE_TOPOLOGY_LABELS, expected_result, 2)

    def test_get_empty_dict_when_getting_empty_dict_to_check(self):
        expected_result = {}
        self._test_get_topology_labels([], {}, expected_result, 0)

    def _test_get_topology_labels(self, is_topology_label, labels_to_check, expected_result,
                                  expected_is_topology_call_count):
        self.mock_is_topology_label.side_effect = is_topology_label
        result = self.secret_manager.get_topology_labels(labels_to_check)
        self.assertEqual(self.mock_is_topology_label.call_count, expected_is_topology_call_count)
        self.assertEqual(result, expected_result)

    def test_generate_secret_system_ids_topologies_success(self):
        expected_result = {
            'system_id_with_supported_topologies' + '1': [test_settings.FAKE_TOPOLOGY_LABEL],
            'system_id_with_no_supported_topologies' + '2': None
        }
        self._test_generate_secret_system_ids_topologies(self.secret_config_with_system_info, expected_result)

    def test_generate_empty_secret_system_ids_topologies(self):
        self._test_generate_secret_system_ids_topologies({}, {})

    def _test_generate_secret_system_ids_topologies(self, secret_config, expected_result):
        self.mock_utils_get_secret_config.return_value = secret_config
        result = self.secret_manager.generate_secret_system_ids_topologies(self.fake_secret_data)
        self.assertEqual(result, expected_result)
        self.mock_utils_get_secret_config.assert_called_once_with(self.fake_secret_data)

    def test_return_true_when_parameter_is_secret_success(self):
        self._test_is_secret_success(test_settings.STORAGE_CLASS_SECRET_FIELD, True)

    def test_return_false_when_parameter_has_bad_suffix_is_secret_success(self):
        self._test_is_secret_success('csi.storage.k8s.io/bad_suffix', False)

    def test_return_false_when_parameter_has_bad_prefix_is_secret_success(self):
        self._test_is_secret_success('bad_prefix/secret-name', False)

    def _test_is_secret_success(self, parameter, expected_result):
        result = self.secret_manager.is_secret(parameter)
        self.assertEqual(result, expected_result)

    def test_get_secret_name_and_namespace_from_storage_class_success(self):
        result = self.secret_manager.get_secret_name_and_namespace(
            test_utils.get_fake_storage_class_info(), test_settings.STORAGE_CLASS_SECRET_FIELD)
        self.assertEqual(result, (test_settings.FAKE_SECRET, test_settings.FAKE_SECRET_NAMESPACE))

    def test_add_unique_secret_info_to_list_success(self):
        self._test_add_unique_secret_info_to_list([])

    def test_do_not_change_list_when_secret_is_already_there_success(self):
        self._test_add_unique_secret_info_to_list([self.fake_secret_info])

    def _test_add_unique_secret_info_to_list(self, secrets_info_list):
        result = self.secret_manager.add_unique_secret_info_to_list(self.fake_secret_info, secrets_info_list)
        self.assertEqual(result, [self.fake_secret_info])

    def test_secret_can_be_changed_when_secret_is_managed_and_the_watch_event_type_is_not_deleted(self):
        self.mock_is_deleted_watch_object_type.return_value = False
        self._test_is_secret_can_be_changed(0, True)
        self.mock_is_deleted_watch_object_type.assert_called_once_with('event_type')

    def test_secret_cannot_be_changed_when_secret_is_not_managed(self):
        self._test_is_secret_can_be_changed(-1, False)
        self.mock_is_deleted_watch_object_type.assert_not_called()

    def test_secret_cannot_be_changed_when_watch_event_type_is_deleted(self):
        self.mock_is_deleted_watch_object_type.return_value = True
        self._test_is_secret_can_be_changed(0, False)
        self.mock_is_deleted_watch_object_type.assert_called_once_with('event_type')

    def _test_is_secret_can_be_changed(self, managed_secret_index, expected_result):
        self._prepare_get_matching_managed_secret_info(managed_secret_index)
        result = self.secret_manager.is_secret_can_be_changed(self.fake_secret_info, 'event_type')
        self.secret_manager.get_matching_managed_secret_info.assert_called_once_with(self.fake_secret_info)
        self.assertEqual(result, expected_result)

    def _prepare_get_matching_managed_secret_info(self, managed_secret_index):
        self.secret_manager.get_matching_managed_secret_info = Mock()
        self.secret_manager.get_matching_managed_secret_info.return_value = (
            self.fake_secret_info, managed_secret_index)

    def test_get_array_connectivity_info_success(self):
        self.mock_utils_get_array_connectivity_info.return_value = 'fake_array_connectivity_info'
        self._test_get_array_connectivity_info(self.fake_secret_data, 'fake_array_connectivity_info')
        self.secret_manager.get_topology_labels.assert_called_once_with(test_settings.FAKE_TOPOLOGY_LABELS)
        self.mock_utils_get_array_connectivity_info.assert_called_once_with(
            self.fake_secret_data, test_settings.FAKE_TOPOLOGY_LABELS)

    def test_get_empty_array_connectivity_info_when_secret_data_is_empty_success(self):
        self._test_get_array_connectivity_info(None, {})
        self.secret_manager.get_topology_labels.assert_not_called()
        self.mock_utils_get_array_connectivity_info.assert_not_called()

    def _test_get_array_connectivity_info(self, secret_data, expected_result):
        self._prepare_get_secret_data(secret_data)
        self._prepare_get_topology_labels()
        result = self.secret_manager.get_array_connection_info(
            test_settings.FAKE_SECRET, test_settings.FAKE_SECRET_NAMESPACE, test_settings.FAKE_TOPOLOGY_LABELS)
        self.assertEqual(result, expected_result)
        self.secret_manager.get_secret_data.assert_called_once_with(
            test_settings.FAKE_SECRET, test_settings.FAKE_SECRET_NAMESPACE)

    def _prepare_get_secret_data(self, secret_data):
        self.secret_manager.get_secret_data = Mock()
        self.secret_manager.get_secret_data.return_value = secret_data

    def _prepare_get_topology_labels(self):
        self.secret_manager.get_topology_labels = Mock()
        self.secret_manager.get_topology_labels.return_value = test_settings.FAKE_TOPOLOGY_LABELS
