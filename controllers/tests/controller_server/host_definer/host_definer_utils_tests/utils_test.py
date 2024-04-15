import unittest
from copy import deepcopy
from unittest.mock import patch

from controllers.servers.host_definer.utils import utils
import controllers.tests.controller_server.host_definer.utils.test_utils as test_utils
import controllers.tests.controller_server.host_definer.utils.k8s_manifests_utils as test_manifest_utils
import controllers.tests.controller_server.host_definer.settings as test_settings
import controllers.common.settings as common_settings
from controllers.servers.errors import ValidationException


class TestUtils(unittest.TestCase):
    def setUp(self):
        self.fake_k8s_metadata = test_utils.get_fake_k8s_metadata()
        self.fake_array_connectivity_info = test_utils.get_fake_array_connectivity_info()
        self.mock_validate_secrets = patch('{}.validate_secrets'.format(test_settings.UTILS_PATH)).start()
        self.mock_get_array_connectivity_info = patch('{}.get_array_connection_info_from_secrets'.format(
            test_settings.UTILS_PATH)).start()
        self.mock_json = patch('{}.json'.format(test_settings.UTILS_PATH)).start()
        self.mock_os = patch('{}.os'.format(test_settings.UTILS_PATH)).start()

    def test_generate_multiple_io_group_from_labels_success(self):
        result = utils.generate_io_group_from_labels(test_utils.get_fake_io_group_labels(2))
        self.assertEqual(result, test_settings.FAKE_MULTIPLE_IO_GROUP_STRING)

    def test_generate_single_io_group_from_labels_success(self):
        result = utils.generate_io_group_from_labels(test_utils.get_fake_io_group_labels(1))
        self.assertEqual(result, test_settings.FAKE_SINGLE_IO_GROUP_STRING)

    def test_get_k8s_object_resource_version_success(self):
        result = utils.get_k8s_object_resource_version(self.fake_k8s_metadata)
        self.assertEqual(result, test_settings.FAKE_RESOURCE_VERSION)

    def test_get_k8s_object_resource_version_with_not_default_resource_version_field_success(self):
        self.fake_k8s_metadata.metadata.resourceVersion = self.fake_k8s_metadata.metadata.pop(
            common_settings.RESOURCE_VERSION_FIELD)
        result = utils.get_k8s_object_resource_version(self.fake_k8s_metadata)
        self.assertEqual(result, test_settings.FAKE_RESOURCE_VERSION)

    @patch('{}.decode_base64_to_string'.format(test_settings.UTILS_PATH))
    def test_get_secret_config_encoded_in_base64_success(self, mock_decode_base64_to_string):
        secret_data = deepcopy(test_settings.FAKE_ENCODED_CONFIG)
        mock_decode_base64_to_string.return_value = test_settings.FAKE_DECODED_CONFIG_STRING[
            common_settings.SECRET_CONFIG_FIELD]
        result = utils.change_decode_base64_secret_config(secret_data)
        self.assertEqual(result, test_settings.FAKE_DECODED_CONFIG)
        mock_decode_base64_to_string.assert_called_once_with(
            test_settings.FAKE_ENCODED_CONFIG[common_settings.SECRET_CONFIG_FIELD])

    @patch('{}.decode_base64_to_string'.format(test_settings.UTILS_PATH))
    def test_get_decoded_secret_not_encoded_success(self, mock_decode_base64_to_string):
        secret_data = deepcopy(test_settings.FAKE_DECODED_CONFIG_STRING)
        mock_decode_base64_to_string.return_value = test_settings.FAKE_DECODED_CONFIG_STRING[
            common_settings.SECRET_CONFIG_FIELD]
        result = utils.change_decode_base64_secret_config(secret_data)
        self.assertEqual(result, test_settings.FAKE_DECODED_CONFIG)
        mock_decode_base64_to_string.assert_called_once_with(
            test_settings.FAKE_DECODED_CONFIG_STRING[common_settings.SECRET_CONFIG_FIELD])

    def test_get_secret_config_success(self):
        secret_data = deepcopy(test_settings.FAKE_DECODED_CONFIG_STRING)
        self.mock_json.loads.return_value = test_settings.FAKE_DECODED_CONFIG[common_settings.SECRET_CONFIG_FIELD]
        result = utils.get_secret_config(secret_data)
        self.assertEqual(result, test_settings.FAKE_DECODED_CONFIG[common_settings.SECRET_CONFIG_FIELD])
        self.mock_json.loads.assert_called_once_with(
            test_settings.FAKE_DECODED_CONFIG_STRING[common_settings.SECRET_CONFIG_FIELD])

    def test_do_not_call_json_load_when_getting_dict_secret_config_success(self):
        secret_data = deepcopy(test_settings.FAKE_DECODED_CONFIG)
        result = utils.get_secret_config(secret_data)
        self.assertEqual(result, test_settings.FAKE_DECODED_CONFIG[common_settings.SECRET_CONFIG_FIELD])
        self.mock_json.loads.assert_not_called()

    def test_get_secret_config_from_secret_data_with_no_config_field_success(self):
        secret_data = deepcopy(test_manifest_utils.get_fake_k8s_secret_manifest()[test_settings.SECRET_DATA_FIELD])
        result = utils.get_secret_config(secret_data)
        self.assertEqual(result, {})
        self.mock_json.loads.assert_not_called()

    def test_munch_success(self):
        result = utils.munch(test_manifest_utils.get_empty_k8s_list_manifest())
        self.assertEqual(result, test_utils.get_fake_empty_k8s_list())

    def test_loop_forever_success(self):
        result = utils.loop_forever()
        self.assertEqual(result, True)

    def test_validate_secret_success(self):
        secret_data = deepcopy(test_settings.FAKE_DECODED_CONFIG)
        self.mock_json.dumps.return_value = test_settings.FAKE_DECODED_CONFIG_STRING[
            common_settings.SECRET_CONFIG_FIELD]
        utils.validate_secret(secret_data)
        self.mock_validate_secrets.assert_called_once_with(test_settings.FAKE_DECODED_CONFIG_STRING)
        self.mock_json.dumps.assert_called_once_with(
            test_settings.FAKE_DECODED_CONFIG[common_settings.SECRET_CONFIG_FIELD])

    def test_do_not_call_json_dumps_when_getting_string_secret_config_on_validate_secret_success(self):
        secret_data = deepcopy(test_settings.FAKE_DECODED_CONFIG_STRING)
        utils.validate_secret(secret_data)
        self.mock_validate_secrets.assert_called_once_with(test_settings.FAKE_DECODED_CONFIG_STRING)
        self.mock_json.dumps.assert_not_called()

    def test_validate_secret_from_secret_data_with_no_config_field_success(self):
        secret_data = deepcopy(test_manifest_utils.get_fake_k8s_secret_manifest()[test_settings.SECRET_DATA_FIELD])
        utils.validate_secret(secret_data)
        self.mock_validate_secrets.assert_called_once_with(secret_data)
        self.mock_json.dumps.assert_not_called()

    def test_validate_secret_handle_validation_error_success(self):
        secret_data = deepcopy(test_manifest_utils.get_fake_k8s_secret_manifest()[test_settings.SECRET_DATA_FIELD])
        self.mock_validate_secrets.side_effect = ValidationException('message')
        utils.validate_secret(secret_data)
        self.mock_validate_secrets.assert_called_once_with(secret_data)
        self.mock_json.dumps.assert_not_called()

    def test_get_prefix_success(self):
        self.mock_os.getenv.return_value = common_settings.TRUE_STRING
        result = utils.get_prefix()
        self.assertEqual(result, common_settings.TRUE_STRING)
        self.mock_os.getenv.assert_called_once_with(common_settings.PREFIX_ENV_VAR)

    def test_get_connectivity_type_when_it_set_in_the_labels_success(self):
        result = utils.get_connectivity_type_from_user(test_settings.ISCSI_CONNECTIVITY_TYPE)
        self.assertEqual(result, test_settings.ISCSI_CONNECTIVITY_TYPE)
        self.mock_os.getenv.assert_not_called()

    def test_get_connectivity_type_when_it_set_in_the_env_vars_success(self):
        self.mock_os.getenv.return_value = test_settings.ISCSI_CONNECTIVITY_TYPE
        result = utils.get_connectivity_type_from_user('')
        self.assertEqual(result, test_settings.ISCSI_CONNECTIVITY_TYPE)
        self.mock_os.getenv.assert_called_once_with(common_settings.CONNECTIVITY_ENV_VAR)

    def test_is_topology_label_true_success(self):
        result = utils.is_topology_label(test_settings.FAKE_TOPOLOGY_LABEL)
        self.assertEqual(result, True)

    def test_is_topology_label_false_success(self):
        result = utils.is_topology_label(test_settings.FAKE_LABEL)
        self.assertEqual(result, False)

    @patch('{}.decode_array_connectivity_info'.format(test_settings.UTILS_PATH))
    def test_get_array_connectivity_info_from_secret_config_success(self, mock_decode):
        connectivity_info = 'connectivity_info'
        secret_data = deepcopy(test_settings.FAKE_DECODED_CONFIG)
        self.mock_json.dumps.return_value = test_settings.FAKE_DECODED_CONFIG_STRING[
            common_settings.SECRET_CONFIG_FIELD]
        self.mock_get_array_connectivity_info.return_value = connectivity_info
        mock_decode.return_value = connectivity_info
        result = utils.get_array_connection_info_from_secret_data(secret_data, [])
        self.assertEqual(result, connectivity_info)
        self.mock_get_array_connectivity_info.assert_called_once_with(secret_data, [])
        mock_decode.assert_called_once_with(connectivity_info)
        self.mock_json.dumps.assert_called_once_with(
            test_settings.FAKE_DECODED_CONFIG[common_settings.SECRET_CONFIG_FIELD])

    @patch('{}.decode_array_connectivity_info'.format(test_settings.UTILS_PATH))
    def test_do_not_call_json_dumps_when_getting_string_secret_config_on_get_array_info_success(self, mock_decode):
        secret_data = deepcopy(test_settings.FAKE_DECODED_CONFIG_STRING)
        self.mock_get_array_connectivity_info.return_value = None
        mock_decode.return_value = None
        result = utils.get_array_connection_info_from_secret_data(secret_data, [])
        self.assertEqual(result, None)
        self.mock_get_array_connectivity_info.assert_called_once_with(secret_data, [])
        mock_decode.assert_called_once_with(None)
        self.mock_json.dumps.assert_not_called()

    @patch('{}.decode_array_connectivity_info'.format(test_settings.UTILS_PATH))
    def test_get_array_info_from_secret_data_with_no_config_field_success(self, mock_decode):
        secret_data = deepcopy(test_manifest_utils.get_fake_k8s_secret_manifest()[test_settings.SECRET_DATA_FIELD])
        self.mock_get_array_connectivity_info.return_value = None
        mock_decode.return_value = None
        result = utils.get_array_connection_info_from_secret_data(secret_data, [])
        self.assertEqual(result, None)
        self.mock_get_array_connectivity_info.assert_called_once_with(secret_data, [])
        mock_decode.assert_called_once_with(None)
        self.mock_json.dumps.assert_not_called()

    @patch('{}.decode_array_connectivity_info'.format(test_settings.UTILS_PATH))
    def test_get_array_connection_info_from_secret_data_handle_validation_error_success(self, mock_decode):
        secret_data = deepcopy(test_manifest_utils.get_fake_k8s_secret_manifest()[test_settings.SECRET_DATA_FIELD])
        self.mock_get_array_connectivity_info.side_effect = ValidationException('message')
        result = utils.get_array_connection_info_from_secret_data(secret_data, [])
        self.assertEqual(result, None)
        self.mock_get_array_connectivity_info.assert_called_once_with(secret_data, [])
        mock_decode.assert_not_called()
        self.mock_json.dumps.assert_not_called()

    @patch('{}.decode_base64_to_string'.format(test_settings.UTILS_PATH))
    def test_decode_array_connectivity_info_success(self, mock_decode):
        mock_decode.side_effect = [test_settings.FAKE_SECRET_ARRAY,
                                   test_settings.FAKE_SECRET_USER_NAME, test_settings.FAKE_SECRET_PASSWORD]
        result = utils.decode_array_connectivity_info(self.fake_array_connectivity_info)
        self.assertEqual(result, self.fake_array_connectivity_info)
        self.assertEqual(mock_decode.call_count, 3)

    def test_decode_base64_to_string_success(self):
        result = utils.decode_base64_to_string(test_settings.BASE64_STRING)
        self.assertIn(test_settings.DECODED_BASE64_STRING, result)

    def test_decode_base64_to_string_handle_getting_decoded_string_success(self):
        result = utils.decode_base64_to_string(test_settings.DECODED_BASE64_STRING)
        self.assertEqual(result, test_settings.DECODED_BASE64_STRING)

    def test_get_random_string_success(self):
        result = utils.get_random_string()
        self.assertEqual(type(result), str)
        self.assertEqual(len(result), 20)

    def test_return_true_when_watch_object_is_deleted(self):
        result = utils.is_watch_object_type_is_delete(common_settings.DELETED_EVENT_TYPE)
        self.assertTrue(result)

    def test_return_false_when_watch_object_is_not_deleted(self):
        result = utils.is_watch_object_type_is_delete(common_settings.ADDED_EVENT_TYPE)
        self.assertFalse(result)

    def test_return_true_when_host_definer_can_delete_hosts_success(self):
        self.mock_os.getenv.return_value = common_settings.TRUE_STRING
        result = utils.is_host_definer_can_delete_hosts()
        self.assertTrue(result)
        self.mock_os.getenv.assert_called_once_with(common_settings.ALLOW_DELETE_ENV_VAR)

    def test_return_false_when_host_definer_cannot_delete_hosts_success(self):
        self.mock_os.getenv.return_value = ''
        result = utils.is_host_definer_can_delete_hosts()
        self.assertFalse(result)
        self.mock_os.getenv.assert_called_once_with(common_settings.ALLOW_DELETE_ENV_VAR)

    def test_return_true_when_dynamic_node_labeling_allowed_success(self):
        self.mock_os.getenv.return_value = common_settings.TRUE_STRING
        result = utils.is_dynamic_node_labeling_allowed()
        self.assertTrue(result)
        self.mock_os.getenv.assert_called_once_with(common_settings.DYNAMIC_NODE_LABELING_ENV_VAR)

    def test_return_false_when_dynamic_node_labeling_is_not_allowed_success(self):
        self.mock_os.getenv.return_value = ''
        result = utils.is_dynamic_node_labeling_allowed()
        self.assertFalse(result)
        self.mock_os.getenv.assert_called_once_with(common_settings.DYNAMIC_NODE_LABELING_ENV_VAR)

    def test_get_define_action_when_phase_is_pending_creation(self):
        result = utils.get_action(common_settings.PENDING_CREATION_PHASE)
        self.assertEqual(result, common_settings.DEFINE_ACTION)

    def test_get_undefine_action_when_phase_is_not_pending_creation(self):
        result = utils.get_action(common_settings.PENDING_DELETION_PHASE)
        self.assertEqual(result, common_settings.UNDEFINE_ACTION)
