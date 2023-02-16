from copy import deepcopy
from unittest.mock import MagicMock, Mock, patch

from controllers.servers.host_definer.types import ManagedNode
from controllers.servers.errors import ValidationException
from controllers.servers.host_definer.resource_manager.node import NodeManager
from controllers.tests.controller_server.host_definer.resource_manager.base_resource_manager import BaseResourceManager
import controllers.tests.controller_server.host_definer.utils.test_utils as test_utils
import controllers.tests.controller_server.host_definer.utils.k8s_manifests_utils as test_manifest_utils
import controllers.tests.controller_server.host_definer.settings as test_settings


class TestNodeManager(BaseResourceManager):
    def setUp(self):
        super().setUp()
        self.node_manager = NodeManager()
        self.node_manager.k8s_api = MagicMock()
        self.node_manager.host_definition_manager = MagicMock()
        self.node_manager.secret_manager = MagicMock()
        self.node_manager.definition_manager = MagicMock()
        self.node_manager.resource_info_manager = MagicMock()
        self.fake_node_info = test_utils.get_fake_node_info()
        self.fake_csi_node_info = test_utils.get_fake_csi_node_info()
        self.fake_managed_node = test_utils.get_fake_managed_node()
        self.fake_host_definitions_info = test_utils.get_fake_k8s_host_definitions_items()
        self.fake_secret_config = 'fake_secret_config'
        self.fake_secret_data = test_utils.get_fake_k8s_secret().data
        self.fake_secret_info = test_utils.get_fake_secret_info()
        self.global_managed_nodes = test_utils.patch_nodes_global_variable(test_settings.NODE_MANAGER_PATH)
        self.global_managed_secrets = test_utils.patch_managed_secrets_global_variable(
            test_settings.NODE_MANAGER_PATH)
        self.manage_node_labels_manifest = test_manifest_utils.get_metadata_with_manage_node_labels_manifest(
            test_settings.MANAGE_NODE_LABEL)
        self.mock_get_system_info_for_topologies = patch('{}.get_system_info_for_topologies'.format(
            test_settings.NODE_MANAGER_PATH)).start()

    def test_get_nodes_info_success(self):
        self._test_get_k8s_resources_info_success(
            self.node_manager.get_nodes_info, self.node_manager.k8s_api.list_node,
            self.node_manager.resource_info_manager.generate_node_info, self.fake_node_info,
            test_utils.get_fake_k8s_nodes_items())

    def test_get_nodes_info_empty_list_success(self):
        self._test_get_k8s_resources_info_empty_list_success(
            self.node_manager.get_nodes_info, self.node_manager.k8s_api.list_node,
            self.node_manager.resource_info_manager.generate_node_info)

    @patch('{}.utils'.format(test_settings.NODE_MANAGER_PATH))
    def test_node_can_be_defined_when_dynamic_node_labeling_allowed(self, manifest_utils):
        self._prepare_is_node_can_be_defined(True, manifest_utils)
        self._test_is_node_can_be_defined(True, manifest_utils)
        self.node_manager.is_node_has_manage_node_label.assert_not_called()

    @patch('{}.utils'.format(test_settings.NODE_MANAGER_PATH))
    def test_node_can_be_defined_when_node_has_manage_node_label(self, manifest_utils):
        self._prepare_is_node_can_be_defined(False, manifest_utils)
        self.node_manager.is_node_has_manage_node_label.return_value = True
        self._test_is_node_can_be_defined(True, manifest_utils)
        self.node_manager.is_node_has_manage_node_label.assert_called_once_with(test_settings.FAKE_NODE_NAME)

    @patch('{}.utils'.format(test_settings.NODE_MANAGER_PATH))
    def test_node_cannot_be_defined(self, manifest_utils):
        self._prepare_is_node_can_be_defined(False, manifest_utils)
        self.node_manager.is_node_has_manage_node_label.return_value = False
        self._test_is_node_can_be_defined(False, manifest_utils)
        self.node_manager.is_node_has_manage_node_label.assert_called_once_with(test_settings.FAKE_NODE_NAME)

    def _prepare_is_node_can_be_defined(self, is_dynamic_node_labeling_allowed, manifest_utils):
        manifest_utils.is_dynamic_node_labeling_allowed.return_value = is_dynamic_node_labeling_allowed
        self.node_manager.is_node_has_manage_node_label = Mock()

    def _test_is_node_can_be_defined(self, expected_result, manifest_utils):
        result = self.node_manager.is_node_can_be_defined(test_settings.FAKE_NODE_NAME)
        self.assertEqual(result, expected_result)
        manifest_utils.is_dynamic_node_labeling_allowed.assert_called_once_with()

    @patch('{}.utils'.format(test_settings.NODE_MANAGER_PATH))
    def test_node_can_be_undefined(self, mock_utils):
        self._prepare_is_node_can_be_undefined(mock_utils, True, True, False)
        self._test_is_node_can_be_undefined(mock_utils, True)
        self.node_manager.is_node_has_manage_node_label.assert_called_once_with(test_settings.FAKE_NODE_NAME)
        self.node_manager.is_node_has_forbid_deletion_label.assert_called_once_with(test_settings.FAKE_NODE_NAME)

    @patch('{}.utils'.format(test_settings.NODE_MANAGER_PATH))
    def test_node_cannot_be_undefined_when_host_definer_cannot_delete_hosts(self, mock_utils):
        self._prepare_is_node_can_be_undefined(mock_utils, False)
        self._test_is_node_can_be_undefined(mock_utils, False)
        self.node_manager.is_node_has_manage_node_label.assert_not_called()
        self.node_manager.is_node_has_forbid_deletion_label.assert_not_called()

    @patch('{}.utils'.format(test_settings.NODE_MANAGER_PATH))
    def test_node_cannot_be_undefined_when_node_not_has_manage_node_label(self, mock_utils):
        self._prepare_is_node_can_be_undefined(mock_utils, True, False)
        self._test_is_node_can_be_undefined(mock_utils, False)
        self.node_manager.is_node_has_manage_node_label.assert_called_once_with(test_settings.FAKE_NODE_NAME)
        self.node_manager.is_node_has_forbid_deletion_label.assert_not_called()

    @patch('{}.utils'.format(test_settings.NODE_MANAGER_PATH))
    def test_node_cannot_be_undefined_when_node_has_forbid_deletion_label(self, mock_utils):
        self._prepare_is_node_can_be_undefined(mock_utils, True, True, True)
        self._test_is_node_can_be_undefined(mock_utils, False)
        self.node_manager.is_node_has_manage_node_label.assert_called_once_with(test_settings.FAKE_NODE_NAME)
        self.node_manager.is_node_has_forbid_deletion_label.assert_called_once_with(test_settings.FAKE_NODE_NAME)

    def _prepare_is_node_can_be_undefined(
            self, mock_utils, is_dynamic_node_labeling_allowed=False, is_node_has_manage_node_label=False,
            is_node_has_forbid_deletion_label=False):
        mock_utils.is_host_definer_can_delete_hosts.return_value = is_dynamic_node_labeling_allowed
        self._prepare_is_node_has_manage_node_label_mock(is_node_has_manage_node_label)
        self.node_manager.is_node_has_forbid_deletion_label = Mock()
        self.node_manager.is_node_has_forbid_deletion_label.return_value = is_node_has_forbid_deletion_label

    def _test_is_node_can_be_undefined(self, mock_utils, expected_result):
        result = self.node_manager.is_node_can_be_undefined(test_settings.FAKE_NODE_NAME)
        self.assertEqual(result, expected_result)
        mock_utils.is_host_definer_can_delete_hosts.assert_called_once_with()

    def test_node_has_manage_node_label(self):
        self._test_is_node_has_label(self.fake_node_info, True, self.node_manager.is_node_has_manage_node_label)

    def test_node_do_not_has_manage_node_label(self):
        node_info = deepcopy(self.fake_node_info)
        node_info.labels.pop(test_settings.MANAGE_NODE_LABEL)
        self._test_is_node_has_label(node_info, False, self.node_manager.is_node_has_manage_node_label)

    def test_node_has_forbid_deletion_label(self):
        node_info = deepcopy(self.fake_node_info)
        node_info.labels[test_settings.FORBID_DELETION_LABEL] = test_settings.TRUE_STRING
        self._test_is_node_has_label(node_info, True, self.node_manager.is_node_has_forbid_deletion_label)

    def test_node_do_not_has_forbid_deletion_label(self):
        node_info_without_forbid_deletion_label = deepcopy(self.fake_node_info)
        self._test_is_node_has_label(node_info_without_forbid_deletion_label, False,
                                     self.node_manager.is_node_has_forbid_deletion_label)

    def _test_is_node_has_label(self, node_info, expected_result, function_to_run):
        self.node_manager.resource_info_manager.get_node_info.return_value = node_info
        result = function_to_run(test_settings.FAKE_NODE_NAME)
        self.node_manager.resource_info_manager.get_node_info.assert_called_once_with(test_settings.FAKE_NODE_NAME)
        self.assertEqual(result, expected_result)

    @patch('{}.manifest_utils'.format(test_settings.NODE_MANAGER_PATH))
    def test_add_node_to_nodes_when_node_do_not_has_manage_node_label_success(self, mock_manifest_utils):
        excepted_managed_node = {self.fake_csi_node_info.name: self.fake_managed_node}
        self._prepare_add_node_to_nodes(False, mock_manifest_utils)
        self.node_manager.add_node_to_nodes(self.fake_csi_node_info)
        self._assert_add_node_to_nodes(excepted_managed_node)
        self._assert_update_manage_node_label_called(
            mock_manifest_utils, self.manage_node_labels_manifest, test_settings.TRUE_STRING)

    @patch('{}.manifest_utils'.format(test_settings.NODE_MANAGER_PATH))
    def test_add_node_to_nodes_when_node_has_manage_node_label_success(self, mock_manifest_utils):
        excepted_managed_node = {self.fake_csi_node_info.name: self.fake_managed_node}
        self._prepare_add_node_to_nodes(True, mock_manifest_utils)
        self.node_manager.add_node_to_nodes(self.fake_csi_node_info)
        self._assert_add_node_to_nodes(excepted_managed_node)
        self._assert_update_manage_node_label_not_called(mock_manifest_utils)

    def _prepare_add_node_to_nodes(self, is_node_has_manage_node_label, mock_manifest_utils):
        self._prepare_is_node_has_manage_node_label_mock(is_node_has_manage_node_label)
        mock_manifest_utils.get_body_manifest_for_labels.return_value = self.manage_node_labels_manifest
        self.node_manager.generate_managed_node = Mock()
        self.node_manager.generate_managed_node.return_value = self.fake_managed_node

    def _prepare_is_node_has_manage_node_label_mock(self, is_node_has_manage_node_label):
        self.node_manager.is_node_has_manage_node_label = Mock()
        self.node_manager.is_node_has_manage_node_label.return_value = is_node_has_manage_node_label

    def _assert_add_node_to_nodes(self, excepted_managed_node):
        self.assertEqual(self.global_managed_nodes, excepted_managed_node)
        self.node_manager.generate_managed_node.assert_called_once_with(self.fake_csi_node_info)

    @patch('{}.utils'.format(test_settings.TYPES_PATH))
    def test_generate_managed_node(self, mock_utils):
        self.node_manager.resource_info_manager.get_node_info.return_value = self.fake_node_info
        mock_utils.generate_io_group_from_labels.return_value = test_settings.IO_GROUP_NAMES
        result = self.node_manager.generate_managed_node(self.fake_csi_node_info)
        self.assertEqual(result.name, self.fake_csi_node_info.name)
        self.assertEqual(result.node_id, self.fake_csi_node_info.node_id)
        self.assertEqual(result.io_group, test_settings.IO_GROUP_NAMES)
        self.assertEqual(type(result), ManagedNode)
        self.node_manager.resource_info_manager.get_node_info.assert_called_once_with(self.fake_csi_node_info.name)
        mock_utils.generate_io_group_from_labels.assert_called_once_with(self.fake_node_info.labels)

    @patch('{}.utils'.format(test_settings.NODE_MANAGER_PATH))
    @patch('{}.manifest_utils'.format(test_settings.NODE_MANAGER_PATH))
    def test_remove_manage_node_label_when_node_should_be_removed(self, mock_manifest_utils, mock_utils):
        self._prepare_remove_manage_node_label(mock_manifest_utils, mock_utils, True, '', False)
        self._test_remove_manage_node_label(mock_utils)
        self.node_manager.resource_info_manager.get_csi_node_info.assert_called_once_with(test_settings.FAKE_NODE_NAME)
        self.node_manager.is_node_has_host_definitions.assert_called_once_with(test_settings.FAKE_NODE_NAME)
        self._assert_update_manage_node_label_called(mock_manifest_utils, self.manage_node_labels_manifest, None)

    @patch('{}.utils'.format(test_settings.NODE_MANAGER_PATH))
    @patch('{}.manifest_utils'.format(test_settings.NODE_MANAGER_PATH))
    def test_do_not_remove_manage_node_label_when_dynamic_node_labeling_is_not_allowed(
            self, mock_manifest_utils, mock_utils):
        self._prepare_remove_manage_node_label(mock_manifest_utils, mock_utils, False, '')
        self._test_remove_manage_node_label(mock_utils)
        self.node_manager.resource_info_manager.get_csi_node_info.assert_not_called()
        self.node_manager.is_node_has_host_definitions.assert_not_called()
        self._assert_update_manage_node_label_not_called(mock_manifest_utils)

    @patch('{}.utils'.format(test_settings.NODE_MANAGER_PATH))
    @patch('{}.manifest_utils'.format(test_settings.NODE_MANAGER_PATH))
    def test_do_not_remove_manage_node_label_when_node_has_ibm_block_csi(self, mock_manifest_utils, mock_utils):
        self._prepare_remove_manage_node_label(mock_manifest_utils, mock_utils, True, 'something')
        self._test_remove_manage_node_label(mock_utils)
        self.node_manager.resource_info_manager.get_csi_node_info.assert_called_once_with(test_settings.FAKE_NODE_NAME)
        self.node_manager.is_node_has_host_definitions.assert_not_called()
        self._assert_update_manage_node_label_not_called(mock_manifest_utils)

    @patch('{}.utils'.format(test_settings.NODE_MANAGER_PATH))
    @patch('{}.manifest_utils'.format(test_settings.NODE_MANAGER_PATH))
    def test_do_not_remove_manage_node_label_when_node_has_host_definitions(self, mock_manifest_utils, mock_utils):
        self._prepare_remove_manage_node_label(mock_manifest_utils, mock_utils, True, '', True)
        self._test_remove_manage_node_label(mock_utils)
        self.node_manager.resource_info_manager.get_csi_node_info.assert_called_once_with(test_settings.FAKE_NODE_NAME)
        self.node_manager.is_node_has_host_definitions.assert_called_once_with(test_settings.FAKE_NODE_NAME)
        self._assert_update_manage_node_label_not_called(mock_manifest_utils)

    def _prepare_remove_manage_node_label(self, mock_manifest_utils, mock_utils, is_dynamic_node_labeling_allowed,
                                          node_id, is_node_has_host_definitions=False):
        csi_node_info = deepcopy(self.fake_csi_node_info)
        csi_node_info.node_id = node_id
        mock_utils.is_dynamic_node_labeling_allowed.return_value = is_dynamic_node_labeling_allowed
        self.node_manager.resource_info_manager.get_csi_node_info.return_value = csi_node_info
        self.node_manager.is_node_has_host_definitions = Mock()
        self.node_manager.is_node_has_host_definitions.return_value = is_node_has_host_definitions
        mock_manifest_utils.get_body_manifest_for_labels.return_value = self.manage_node_labels_manifest

    def _test_remove_manage_node_label(self, mock_utils):
        self.node_manager.remove_manage_node_label(test_settings.FAKE_NODE_NAME)
        mock_utils.is_dynamic_node_labeling_allowed.assert_called_once_with()

    def _assert_update_manage_node_label_called(self, mock_manifest_utils, excepted_body, expected_label_value):
        mock_manifest_utils.get_body_manifest_for_labels.assert_called_once_with(expected_label_value)
        self.node_manager.k8s_api.patch_node.assert_called_once_with(test_settings.FAKE_NODE_NAME, excepted_body)

    def _assert_update_manage_node_label_not_called(self, mock_manifest_utils):
        mock_manifest_utils.get_body_manifest_for_labels.assert_not_called()
        self.node_manager.k8s_api.patch_node.assert_not_called()

    def test_return_true_when_node_has_host_definitions(self):
        self._test_is_node_has_host_definitions(self.fake_host_definitions_info.items, True)

    def test_return_false_when_node_has_host_definitions(self):
        self._test_is_node_has_host_definitions([], False)

    def _test_is_node_has_host_definitions(self, host_definitions, expected_result):
        self.node_manager.host_definition_manager.get_all_host_definitions_info_of_the_node.return_value = \
            host_definitions
        result = self.node_manager.is_node_has_host_definitions(test_settings.FAKE_NODE_NAME)
        self.assertEqual(result, expected_result)
        self.node_manager.host_definition_manager.get_all_host_definitions_info_of_the_node.assert_called_with(
            test_settings.FAKE_NODE_NAME)

    @patch('{}.utils'.format(test_settings.NODE_MANAGER_PATH))
    def test_generate_single_node_with_system_id_success(self, mock_utils):
        self._prepare_generate_nodes_with_system_id(mock_utils, [self.fake_node_info])
        result = self.node_manager.generate_nodes_with_system_id(self.fake_secret_data)
        self.assertEqual(result, {self.fake_node_info.name: test_settings.FAKE_SYSTEM_ID})
        self._assert_generate_nodes_with_system_id(mock_utils)
        self._assert_get_system_id_for_node_called_once()

    @patch('{}.utils'.format(test_settings.NODE_MANAGER_PATH))
    def test_generate_single_node_with_empty_system_id_when_fail_to_get_system_info(self, mock_utils):
        self._prepare_generate_nodes_with_system_id(mock_utils, [self.fake_node_info])
        self.mock_get_system_info_for_topologies.side_effect = ValidationException('fail')
        result = self.node_manager.generate_nodes_with_system_id(self.fake_secret_data)
        self.assertEqual(result, {self.fake_node_info.name: ''})
        self._assert_generate_nodes_with_system_id(mock_utils)
        self._assert_get_system_id_for_node_called_once()

    @patch('{}.utils'.format(test_settings.NODE_MANAGER_PATH))
    def test_generate_multiple_nodes_with_system_id_success(self, mock_utils):
        second_node_info = deepcopy(self.fake_node_info)
        second_node_info.name = 'second_node_info'
        expected_result = {self.fake_node_info.name: test_settings.FAKE_SYSTEM_ID,
                           second_node_info.name: test_settings.FAKE_SYSTEM_ID}
        self._prepare_generate_nodes_with_system_id(mock_utils, [self.fake_node_info, second_node_info])
        result = self.node_manager.generate_nodes_with_system_id(self.fake_secret_data)
        self.assertEqual(result, expected_result)
        self._assert_generate_nodes_with_system_id(mock_utils)
        self.assertEqual(self.node_manager.secret_manager.get_topology_labels.call_count, 2)
        self.assertEqual(self.mock_get_system_info_for_topologies.call_count, 2)

    @patch('{}.utils'.format(test_settings.NODE_MANAGER_PATH))
    def test_get_empty_dict_when_there_are_no_nodes_success(self, mock_utils):
        self._prepare_generate_nodes_with_system_id(mock_utils, [])
        result = self.node_manager.generate_nodes_with_system_id(self.fake_secret_data)
        self.assertEqual(result, {})
        self._assert_generate_nodes_with_system_id(mock_utils)
        self.node_manager.secret_manager.get_topology_labels.assert_not_called()
        self.mock_get_system_info_for_topologies.assert_not_called()

    def _prepare_generate_nodes_with_system_id(self, mock_utils, nodes_info):
        mock_utils.get_secret_config.return_value = self.fake_secret_config
        self.node_manager.get_nodes_info = Mock()
        self.node_manager.get_nodes_info.return_value = nodes_info
        self.node_manager.secret_manager.get_topology_labels.return_value = test_settings.FAKE_TOPOLOGY_LABELS
        self.mock_get_system_info_for_topologies.return_value = (None, test_settings.FAKE_SYSTEM_ID)

    def _assert_generate_nodes_with_system_id(self, mock_utils):
        mock_utils.get_secret_config.assert_called_once_with(self.fake_secret_data)
        self.node_manager.get_nodes_info.assert_called_once_with()

    def _assert_get_system_id_for_node_called_once(self):
        self.node_manager.secret_manager.get_topology_labels.assert_called_once_with(self.fake_node_info.labels)
        self.mock_get_system_info_for_topologies.assert_called_once_with(
            self.fake_secret_config, test_settings.FAKE_TOPOLOGY_LABELS)

    @patch('{}.utils'.format(test_settings.NODE_MANAGER_PATH))
    def test_return_true_when_node_has_new_manage_node_label_success(self, manifest_utils):
        self._prepare_is_node_has_new_manage_node_label(manifest_utils, False, True)
        self._test_is_node_has_new_manage_node_label(True, self.fake_csi_node_info)
        manifest_utils.is_dynamic_node_labeling_allowed.assert_called_once_with()
        self.node_manager.is_node_has_manage_node_label.assert_called_once_with(self.fake_csi_node_info.name)

    @patch('{}.utils'.format(test_settings.NODE_MANAGER_PATH))
    def test_return_false_when_dynamic_node_labeling_allowed_success(self, manifest_utils):
        self._prepare_is_node_has_new_manage_node_label(manifest_utils, True)
        self._test_is_node_has_new_manage_node_label(False, self.fake_csi_node_info)
        manifest_utils.is_dynamic_node_labeling_allowed.assert_called_once_with()
        self.node_manager.is_node_has_manage_node_label.assert_not_called()

    @patch('{}.utils'.format(test_settings.NODE_MANAGER_PATH))
    def test_return_false_when_node_not_has_manage_node_label_success(self, manifest_utils):
        self._prepare_is_node_has_new_manage_node_label(manifest_utils, False, False)
        self._test_is_node_has_new_manage_node_label(False, self.fake_csi_node_info)
        manifest_utils.is_dynamic_node_labeling_allowed.assert_called_once_with()
        self.node_manager.is_node_has_manage_node_label.assert_called_once_with(self.fake_csi_node_info.name)

    @patch('{}.utils'.format(test_settings.NODE_MANAGER_PATH))
    def test_return_false_when_node_is_already_managed_success(self, manifest_utils):
        self.global_managed_nodes[test_settings.FAKE_NODE_NAME] = self.fake_managed_node
        self._prepare_is_node_has_new_manage_node_label(manifest_utils, False, True)
        self._test_is_node_has_new_manage_node_label(False, self.fake_csi_node_info)
        manifest_utils.is_dynamic_node_labeling_allowed.assert_called_once_with()
        self.node_manager.is_node_has_manage_node_label.assert_called_once_with(self.fake_csi_node_info.name)

    @patch('{}.utils'.format(test_settings.NODE_MANAGER_PATH))
    def test_return_false_when_csi_node_do_not_have_node_id_success(self, manifest_utils):
        csi_node_info = deepcopy(self.fake_csi_node_info)
        csi_node_info.node_id = ''
        self._prepare_is_node_has_new_manage_node_label(manifest_utils, False, True)
        self._test_is_node_has_new_manage_node_label(False, csi_node_info)
        manifest_utils.is_dynamic_node_labeling_allowed.assert_called_once_with()
        self.node_manager.is_node_has_manage_node_label.assert_called_once_with(csi_node_info.name)

    @patch('{}.utils'.format(test_settings.NODE_MANAGER_PATH))
    def test_return_false_when_node_is_not_in_unmanaged_nodes_list_success(self, manifest_utils):
        csi_node_info = deepcopy(self.fake_csi_node_info)
        csi_node_info.name = 'bad-name'
        self._prepare_is_node_has_new_manage_node_label(manifest_utils, False, True)
        self._test_is_node_has_new_manage_node_label(False, csi_node_info)
        manifest_utils.is_dynamic_node_labeling_allowed.assert_called_once_with()
        self.node_manager.is_node_has_manage_node_label.assert_called_once_with(csi_node_info.name)

    def _prepare_is_node_has_new_manage_node_label(
            self, manifest_utils, is_dynamic_node_labeling_allowed, is_node_has_manage_node_label=False):
        manifest_utils.is_dynamic_node_labeling_allowed.return_value = is_dynamic_node_labeling_allowed
        self.node_manager.is_node_has_manage_node_label = Mock()
        self.node_manager.is_node_has_manage_node_label.return_value = is_node_has_manage_node_label

    def _test_is_node_has_new_manage_node_label(self, expected_result, csi_node_info):
        result = self.node_manager.is_node_has_new_manage_node_label(
            csi_node_info, [test_settings.FAKE_NODE_NAME])
        self.assertEqual(result, expected_result)

    def test_do_not_handle_node_topologies_when_node_is_not_managed(self):
        self.global_managed_nodes = {}
        self.node_manager.handle_node_topologies(self.fake_node_info, test_settings.MODIFIED_EVENT_TYPE)
        self._assert_do_not_handle_node_topologies()

    def test_do_not_handle_node_topologies_when_watch_event_is_not_modified_type(self):
        self.global_managed_nodes[test_settings.FAKE_NODE_NAME] = self.fake_managed_node
        self.node_manager.handle_node_topologies(self.fake_node_info, test_settings.ADDED_EVENT)
        self._assert_do_not_handle_node_topologies()

    def _assert_do_not_handle_node_topologies(self):
        self.node_manager.secret_manager.is_node_should_managed_on_secret_info.assert_not_called()
        self.node_manager.secret_manager.is_node_labels_in_system_ids_topologies.assert_not_called()
        self.node_manager.secret_manager.get_system_id_for_node_labels.assert_not_called()
        self.node_manager.definition_manager.define_node_on_all_storages.assert_not_called()

    def test_do_not_handle_node_topologies_when_node_is_not_in_secret_topologies(self):
        self._prepare_handle_node_topologies(self.fake_secret_info, False, False)
        global_managed_secrets = deepcopy(self.global_managed_secrets)
        self.node_manager.handle_node_topologies(self.fake_node_info, test_settings.MODIFIED_EVENT_TYPE)
        self.assertEqual(global_managed_secrets[0].nodes_with_system_id,
                         self.global_managed_secrets[0].nodes_with_system_id)
        self.node_manager.secret_manager.is_node_should_managed_on_secret_info.assert_called_once_with(
            self.fake_node_info.name, self.fake_secret_info)
        self.node_manager.secret_manager.is_node_labels_in_system_ids_topologies.assert_called_once_with(
            self.fake_secret_info.system_ids_topologies, self.fake_node_info.labels)
        self.node_manager.secret_manager.get_system_id_for_node_labels.assert_not_called()
        self.node_manager.definition_manager.define_node_on_all_storages.assert_not_called()

    def test_remove_node_from_secret_topology_fields_if_topology_not_match_anymore(self):
        fake_secret_info = deepcopy(self.fake_secret_info)
        self._prepare_handle_node_topologies(fake_secret_info, True, False)
        self.node_manager.handle_node_topologies(self.fake_node_info, test_settings.MODIFIED_EVENT_TYPE)
        self._assert_global_secrets_changed_as_wanted({})
        self._assert_called_remove_node_if_topology_not_match(fake_secret_info)

    def _assert_called_remove_node_if_topology_not_match(self, fake_secret_info):
        self.node_manager.secret_manager.is_node_should_managed_on_secret_info.assert_called_once_with(
            self.fake_node_info.name, fake_secret_info)
        self.node_manager.secret_manager.is_node_labels_in_system_ids_topologies.assert_called_once_with(
            fake_secret_info.system_ids_topologies, self.fake_node_info.labels)
        self.node_manager.secret_manager.get_system_id_for_node_labels.assert_not_called()
        self.node_manager.definition_manager.define_node_on_all_storages.assert_not_called()

    def test_define_host_with_new_topology(self):
        fake_secret_info = deepcopy(self.fake_secret_info)
        fake_secret_info.nodes_with_system_id = {}
        self._prepare_handle_node_topologies(fake_secret_info, False, True)
        self.node_manager.handle_node_topologies(self.fake_node_info, test_settings.MODIFIED_EVENT_TYPE)
        self._assert_called_define_host_with_new_topology(fake_secret_info)
        self._assert_global_secrets_changed_as_wanted(self.fake_secret_info.nodes_with_system_id)

    def _prepare_handle_node_topologies(self, fake_secret_info, is_node_should_managed,
                                        is_node_labels_in_system_ids_topologies):
        self.global_managed_nodes[test_settings.FAKE_NODE_NAME] = self.fake_managed_node
        self.global_managed_secrets.append(fake_secret_info)
        self.node_manager.secret_manager.is_node_should_managed_on_secret_info.return_value = is_node_should_managed
        self.node_manager.secret_manager.is_node_labels_in_system_ids_topologies.return_value = \
            is_node_labels_in_system_ids_topologies
        self.node_manager.secret_manager.get_system_id_for_node_labels.return_value = test_settings.FAKE_SYSTEM_ID

    def _assert_called_define_host_with_new_topology(self, fake_secret_info):
        self.node_manager.secret_manager.is_node_should_managed_on_secret_info.assert_called_once_with(
            self.fake_node_info.name, fake_secret_info)
        self.node_manager.secret_manager.is_node_labels_in_system_ids_topologies.assert_called_once_with(
            fake_secret_info.system_ids_topologies, self.fake_node_info.labels)
        self.node_manager.secret_manager.get_system_id_for_node_labels.assert_called_once_with(
            fake_secret_info.system_ids_topologies, self.fake_node_info.labels)
        self.node_manager.definition_manager.define_node_on_all_storages.assert_called_once_with(
            test_settings.FAKE_NODE_NAME)

    def _assert_global_secrets_changed_as_wanted(self, expected_nodes_with_system_id):
        managed_secret_info = self.global_managed_secrets[0]
        self.assertEqual(managed_secret_info.name, self.fake_secret_info.name)
        self.assertEqual(managed_secret_info.namespace, self.fake_secret_info.namespace)
        self.assertEqual(managed_secret_info.nodes_with_system_id, expected_nodes_with_system_id)
        self.assertEqual(managed_secret_info.system_ids_topologies, self.fake_secret_info.system_ids_topologies)
        self.assertEqual(managed_secret_info.managed_storage_classes, self.fake_secret_info.managed_storage_classes)

    @patch('{}.utils'.format(test_settings.NODE_MANAGER_PATH))
    def test_update_node_io_group_when_node_is_managed_and_his_io_group_was_changed(self, mock_utils):
        fake_managed_node = deepcopy(self.fake_managed_node)
        self._test_update_node_io_group_when_node_is_managed(
            mock_utils, fake_managed_node, 'different_io_group', 'different_io_group')
        self.node_manager.definition_manager.define_node_on_all_storages.assert_called_once_with(
            test_settings.FAKE_NODE_NAME)

    @patch('{}.utils'.format(test_settings.NODE_MANAGER_PATH))
    def test_do_not_update_node_io_group_when_node_is_managed_and_his_io_group_was_not_changed(self, mock_utils):
        fake_managed_node = deepcopy(self.fake_managed_node)
        self._test_update_node_io_group_when_node_is_managed(
            mock_utils, fake_managed_node, fake_managed_node.io_group, self.fake_managed_node.io_group)

    def _test_update_node_io_group_when_node_is_managed(
            self, mock_utils, fake_managed_node, io_group_from_labels, expected_io_group):
        self.global_managed_nodes[test_settings.FAKE_NODE_NAME] = fake_managed_node
        mock_utils.generate_io_group_from_labels.return_value = io_group_from_labels
        self.node_manager.update_node_io_group(self.fake_node_info)
        managed_node = self.global_managed_nodes[test_settings.FAKE_NODE_NAME]
        self.assertEqual(managed_node.name, self.fake_managed_node.name)
        self.assertEqual(managed_node.node_id, self.fake_managed_node.node_id)
        self.assertEqual(managed_node.io_group, expected_io_group)

    @patch('{}.utils'.format(test_settings.TYPES_PATH))
    def test_do_not_update_node_io_group_when_node_is_not_managed(self, mock_utils):
        self.global_managed_nodes = []
        mock_utils.generate_io_group_from_labels.return_value = self.fake_managed_node.io_group
        self.node_manager.update_node_io_group(self.fake_node_info)
        self.assertEqual(len(self.global_managed_nodes), 0)
        self.node_manager.definition_manager.define_node_on_all_storages.assert_not_called()
