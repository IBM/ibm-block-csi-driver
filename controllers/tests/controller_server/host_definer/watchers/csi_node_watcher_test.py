from unittest.mock import MagicMock, patch
from copy import deepcopy

import controllers.common.settings as common_settings
import controllers.tests.controller_server.host_definer.utils.test_utils as test_utils
import controllers.tests.controller_server.host_definer.settings as test_settings
from controllers.tests.controller_server.host_definer.watchers.watcher_base import WatcherBaseSetUp
from controllers.servers.host_definer.watcher.csi_node_watcher import CsiNodeWatcher


class CsiNodeWatcherBase(WatcherBaseSetUp):
    def setUp(self):
        super().setUp()
        self.watcher = CsiNodeWatcher()
        self.watcher.resource_info_manager = MagicMock()
        self.watcher.node_manager = MagicMock()
        self.watcher.definition_manager = MagicMock()
        self.watcher.csi_node = MagicMock()
        self.watcher.k8s_api = MagicMock()
        self.watcher.host_definition_manager = MagicMock()
        self.fake_csi_node_info = test_utils.get_fake_csi_node_info()


class TestAddInitialCsiNodes(CsiNodeWatcherBase):
    def test_add_initial_csi_nodes(self):
        self.watcher.csi_node.get_csi_nodes_info_with_driver.return_value = [self.fake_csi_node_info]
        self.watcher.node_manager.is_node_can_be_defined.return_value = True
        self.watcher.add_initial_csi_nodes()
        self.watcher.csi_node.get_csi_nodes_info_with_driver.assert_called_once_with()
        self.watcher.node_manager.is_node_can_be_defined.assert_called_once_with(self.fake_csi_node_info.name)
        self.watcher.node_manager.add_node_to_nodes.assert_called_once_with(self.fake_csi_node_info)

    def test_do_not_add_initial_csi_nodes_that_cannot_be_defined(self):
        self.watcher.csi_node.get_csi_nodes_info_with_driver.return_value = [self.fake_csi_node_info]
        self.watcher.node_manager.is_node_can_be_defined.return_value = False
        self.watcher.add_initial_csi_nodes()
        self.watcher.csi_node.get_csi_nodes_info_with_driver.assert_called_once_with()
        self.watcher.node_manager.is_node_can_be_defined.assert_called_once_with(self.fake_csi_node_info.name)
        self.watcher.node_manager.add_node_to_nodes.assert_not_called()

    def test_do_not_add_empty_initial_csi_nodes(self):
        self.watcher.csi_node.get_csi_nodes_info_with_driver.return_value = []
        self.watcher.add_initial_csi_nodes()
        self.watcher.csi_node.get_csi_nodes_info_with_driver.assert_called_once_with()
        self.watcher.node_manager.is_node_can_be_defined.assert_not_called()
        self.watcher.node_manager.add_node_to_nodes.assert_not_called()


class TestWatchCsiNodesResources(CsiNodeWatcherBase):
    def setUp(self):
        super().setUp()
        self.fake_managed_nodes = test_utils.get_fake_managed_node()
        self.managed_node_with_different_node_id = deepcopy(self.fake_managed_nodes)
        self.managed_node_with_different_node_id.node_id = 'different_node_id'
        self.fake_secret_info = test_utils.get_fake_secret_info()
        self.fake_host_definition_info = test_utils.get_fake_host_definition_info()
        self.csi_node_modified_watch_manifest = test_utils.get_fake_node_watch_event(
            common_settings.MODIFIED_EVENT_TYPE)
        self.csi_node_modified_watch_munch = test_utils.convert_manifest_to_munch(
            self.csi_node_modified_watch_manifest)
        self.csi_node_deleted_watch_manifest = test_utils.get_fake_node_watch_event(common_settings.DELETED_EVENT_TYPE)
        self.csi_node_deleted_watch_munch = test_utils.convert_manifest_to_munch(self.csi_node_deleted_watch_manifest)
        self.global_managed_nodes = test_utils.patch_nodes_global_variable(
            test_settings.CSI_NODE_WATCHER_PATH)
        self.global_managed_secret = test_utils.patch_managed_secrets_global_variable(
            test_settings.CSI_NODE_WATCHER_PATH)

    @patch('{}.utils'.format(test_settings.CSI_NODE_WATCHER_PATH))
    def test_do_not_define_managed_csi_node_with_deleted_event_that_is_part_of_update_but_node_id_did_not_change(
            self, mock_utils):
        self._prepare_watch_csi_nodes_resources(
            mock_utils, self.csi_node_deleted_watch_manifest, self.csi_node_deleted_watch_munch)
        self._prepare_handle_deleted_csi_node_pod(mock_utils, True, True)
        test_utils.run_function_with_timeout(self.watcher.watch_csi_nodes_resources, 0.2)
        self._assert_watch_csi_nodes_resources(
            mock_utils, self.csi_node_deleted_watch_manifest, self.csi_node_deleted_watch_munch)
        self._assert_handle_deleted_csi_node_pod_called(mock_utils, True, True)

    @patch('{}.utils'.format(test_settings.CSI_NODE_WATCHER_PATH))
    def test_define_managed_csi_node_with_deleted_event_that_is_part_of_update_and_node_id_change(self, mock_utils):
        self._prepare_watch_csi_nodes_resources(
            mock_utils, self.csi_node_deleted_watch_manifest, self.csi_node_deleted_watch_munch)
        self._prepare_handle_deleted_csi_node_pod(mock_utils, True, True, True)
        test_utils.run_function_with_timeout(self.watcher.watch_csi_nodes_resources, 0.2)
        self._assert_watch_csi_nodes_resources(
            mock_utils, self.csi_node_deleted_watch_manifest, self.csi_node_deleted_watch_munch)
        self._assert_handle_deleted_csi_node_pod_called(mock_utils, True, True, True)

    @patch('{}.utils'.format(test_settings.CSI_NODE_WATCHER_PATH))
    def test_undefine_managed_csi_node_with_deleted_event_that_is_not_part_of_update(self, mock_utils):
        self._prepare_watch_csi_nodes_resources(
            mock_utils, self.csi_node_deleted_watch_manifest, self.csi_node_deleted_watch_munch)
        self._prepare_handle_deleted_csi_node_pod(mock_utils, True, False, False, True, False)
        test_utils.run_function_with_timeout(self.watcher.watch_csi_nodes_resources, 0.2)
        self._assert_watch_csi_nodes_resources(
            mock_utils, self.csi_node_deleted_watch_manifest, self.csi_node_deleted_watch_munch)
        self._assert_handle_deleted_csi_node_pod_called(mock_utils, True, False, False, True, False)

    @patch('{}.utils'.format(test_settings.CSI_NODE_WATCHER_PATH))
    def test_do_not_undefine_managed_csi_node_with_deleted_event_when_node_has_forbid_deletion_label(self, mock_utils):
        self._prepare_watch_csi_nodes_resources(
            mock_utils, self.csi_node_deleted_watch_manifest, self.csi_node_deleted_watch_munch)
        self._prepare_handle_deleted_csi_node_pod(mock_utils, True, False, False, True, True)
        test_utils.run_function_with_timeout(self.watcher.watch_csi_nodes_resources, 0.2)
        self._assert_watch_csi_nodes_resources(
            mock_utils, self.csi_node_deleted_watch_manifest, self.csi_node_deleted_watch_munch)
        self._assert_handle_deleted_csi_node_pod_called(mock_utils, True, False, False, True, True)

    @patch('{}.utils'.format(test_settings.CSI_NODE_WATCHER_PATH))
    def test_do_not_undefine_managed_csi_node_with_deleted_event_when_host_definer_cannot_delete_hosts(
            self, mock_utils):
        self._prepare_watch_csi_nodes_resources(
            mock_utils, self.csi_node_deleted_watch_manifest, self.csi_node_deleted_watch_munch)
        self._prepare_handle_deleted_csi_node_pod(mock_utils, True, False, False, False, False)
        test_utils.run_function_with_timeout(self.watcher.watch_csi_nodes_resources, 0.2)
        self._assert_watch_csi_nodes_resources(
            mock_utils, self.csi_node_deleted_watch_manifest, self.csi_node_deleted_watch_munch)
        self._assert_handle_deleted_csi_node_pod_called(mock_utils, True, False, False, False, False)

    @patch('{}.utils'.format(test_settings.CSI_NODE_WATCHER_PATH))
    def test_do_not_handle_unmanaged_csi_node_with_deleted_event(self, mock_utils):
        self._prepare_watch_csi_nodes_resources(
            mock_utils, self.csi_node_deleted_watch_manifest, self.csi_node_deleted_watch_munch)
        self._prepare_handle_deleted_csi_node_pod(mock_utils, False)
        test_utils.run_function_with_timeout(self.watcher.watch_csi_nodes_resources, 0.2)
        self._assert_watch_csi_nodes_resources(
            mock_utils, self.csi_node_deleted_watch_manifest, self.csi_node_deleted_watch_munch)
        self._assert_handle_deleted_csi_node_pod_called(mock_utils, False)

    @patch('{}.utils'.format(test_settings.CSI_NODE_WATCHER_PATH))
    def test_do_not_handle_csi_node_with_deleted_event_and_it_is_not_in_global_nodes(self, mock_utils):
        self._prepare_watch_csi_nodes_resources(
            mock_utils, self.csi_node_deleted_watch_manifest, self.csi_node_deleted_watch_munch)
        self.global_managed_nodes.pop(self.fake_csi_node_info.name, None)
        test_utils.run_function_with_timeout(self.watcher.watch_csi_nodes_resources, 0.2)
        self._assert_watch_csi_nodes_resources(
            mock_utils, self.csi_node_deleted_watch_manifest, self.csi_node_deleted_watch_munch)
        self._assert_handle_deleted_csi_node_pod_not_called(mock_utils)

    @patch('{}.utils'.format(test_settings.CSI_NODE_WATCHER_PATH))
    def test_define_new_csi_node_with_ibm_block_csi_driver_with_modified_event(self, mock_utils):
        self._prepare_watch_csi_nodes_resources(
            mock_utils, self.csi_node_modified_watch_manifest, self.csi_node_modified_watch_munch)
        self.global_managed_nodes.pop(self.fake_csi_node_info.name, None)
        self.watcher.node_manager.is_node_can_be_defined.return_value = True
        test_utils.run_function_with_timeout(self.watcher.watch_csi_nodes_resources, 0.2)
        self._assert_watch_csi_nodes_resources(
            mock_utils, self.csi_node_modified_watch_manifest, self.csi_node_modified_watch_munch)
        self._assert_define_new_csi_node_called(mock_utils)

    @patch('{}.utils'.format(test_settings.CSI_NODE_WATCHER_PATH))
    def test_do_not_handle_new_csi_node_with_ibm_block_csi_driver_with_modified_event_but_cannot_be_defined(
            self, mock_utils):
        self._prepare_watch_csi_nodes_resources(
            mock_utils, self.csi_node_modified_watch_manifest, self.csi_node_modified_watch_munch)
        self.global_managed_nodes.pop(self.fake_csi_node_info.name, None)
        self.watcher.node_manager.is_node_can_be_defined.return_value = False
        test_utils.run_function_with_timeout(self.watcher.watch_csi_nodes_resources, 0.2)
        self._assert_watch_csi_nodes_resources(
            mock_utils, self.csi_node_modified_watch_manifest, self.csi_node_modified_watch_munch)
        self._assert_define_new_csi_node_not_called()
        self._assert_handle_deleted_csi_node_pod_not_called(mock_utils)

    @patch('{}.utils'.format(test_settings.CSI_NODE_WATCHER_PATH))
    def test_do_not_define_managed_csi_node_with_modified_event_that_is_part_of_update_but_node_id_did_not_change(
            self, mock_utils):
        self._prepare_watch_csi_nodes_resources(
            mock_utils, self.csi_node_modified_watch_manifest, self.csi_node_modified_watch_munch)
        self._prepare_handle_deleted_csi_node_pod(mock_utils, True, True)
        test_utils.run_function_with_timeout(self.watcher.watch_csi_nodes_resources, 0.2)
        self._assert_watch_csi_nodes_resources(
            mock_utils, self.csi_node_modified_watch_manifest, self.csi_node_modified_watch_munch)
        self._assert_define_new_csi_node_not_called()
        self._assert_handle_deleted_csi_node_pod_called(mock_utils, True, True)

    @patch('{}.utils'.format(test_settings.CSI_NODE_WATCHER_PATH))
    def test_define_managed_csi_node_with_modified_event_that_is_part_of_update_and_node_id_change(self, mock_utils):
        self._prepare_watch_csi_nodes_resources(
            mock_utils, self.csi_node_modified_watch_manifest, self.csi_node_modified_watch_munch)
        self._prepare_handle_deleted_csi_node_pod(mock_utils, True, True, True)
        test_utils.run_function_with_timeout(self.watcher.watch_csi_nodes_resources, 0.2)
        self._assert_watch_csi_nodes_resources(
            mock_utils, self.csi_node_modified_watch_manifest, self.csi_node_modified_watch_munch)
        self._assert_define_new_csi_node_not_called()
        self._assert_handle_deleted_csi_node_pod_called(mock_utils, True, True, True)

    @patch('{}.utils'.format(test_settings.CSI_NODE_WATCHER_PATH))
    def test_undefine_managed_csi_node_with_modified_event_that_is_not_part_of_update(self, mock_utils):
        self._prepare_watch_csi_nodes_resources(
            mock_utils, self.csi_node_modified_watch_manifest, self.csi_node_modified_watch_munch)
        self._prepare_handle_deleted_csi_node_pod(mock_utils, True, False, False, True, False)
        test_utils.run_function_with_timeout(self.watcher.watch_csi_nodes_resources, 0.2)
        self._assert_watch_csi_nodes_resources(
            mock_utils, self.csi_node_modified_watch_manifest, self.csi_node_modified_watch_munch)
        self._assert_define_new_csi_node_not_called()
        self._assert_handle_deleted_csi_node_pod_called(mock_utils, True, False, False, True, False)

    @patch('{}.utils'.format(test_settings.CSI_NODE_WATCHER_PATH))
    def test_do_not_undefine_managed_csi_node_with_modified_event_when_node_has_forbid_deletion_label(self, mock_utils):
        self._prepare_watch_csi_nodes_resources(
            mock_utils, self.csi_node_modified_watch_manifest, self.csi_node_modified_watch_munch)
        self._prepare_handle_deleted_csi_node_pod(mock_utils, True, False, False, True, True)
        test_utils.run_function_with_timeout(self.watcher.watch_csi_nodes_resources, 0.2)
        self._assert_watch_csi_nodes_resources(
            mock_utils, self.csi_node_modified_watch_manifest, self.csi_node_modified_watch_munch)
        self._assert_define_new_csi_node_not_called()
        self._assert_handle_deleted_csi_node_pod_called(mock_utils, True, False, False, True, True)

    @patch('{}.utils'.format(test_settings.CSI_NODE_WATCHER_PATH))
    def test_do_not_undefine_managed_csi_node_with_modified_event_when_host_definer_cannot_delete_hosts(
            self, mock_utils):
        self._prepare_watch_csi_nodes_resources(
            mock_utils, self.csi_node_modified_watch_manifest, self.csi_node_modified_watch_munch)
        self._prepare_handle_deleted_csi_node_pod(mock_utils, True, False, False, False, False)
        test_utils.run_function_with_timeout(self.watcher.watch_csi_nodes_resources, 0.2)
        self._assert_watch_csi_nodes_resources(
            mock_utils, self.csi_node_modified_watch_manifest, self.csi_node_modified_watch_munch)
        self._assert_define_new_csi_node_not_called()
        self._assert_handle_deleted_csi_node_pod_called(mock_utils, True, False, False, False, False)

    def _prepare_watch_csi_nodes_resources(self, mock_utils, csi_node_watch_manifest, csi_node_watch_munch):
        self.global_managed_nodes[self.fake_csi_node_info.name] = self.fake_managed_nodes
        self.watcher.k8s_api.get_csi_node_stream.return_value = iter([csi_node_watch_manifest])
        mock_utils.munch.return_value = csi_node_watch_munch
        self.watcher.resource_info_manager.generate_csi_node_info.return_value = self.fake_csi_node_info

    def _prepare_handle_deleted_csi_node_pod(
            self, mock_utils, is_node_has_manage_node_label, is_host_part_of_update=False, is_node_id_changed=False,
            is_host_definer_can_delete_hosts=False, is_node_has_forbid_deletion_label=False):
        self.watcher.node_manager.is_node_has_manage_node_label.return_value = is_node_has_manage_node_label
        if is_node_has_manage_node_label:
            self._prepare_undefine_host_when_node_pod_is_deleted(
                mock_utils, is_host_part_of_update, is_node_id_changed, is_host_definer_can_delete_hosts,
                is_node_has_forbid_deletion_label)

    def _prepare_undefine_host_when_node_pod_is_deleted(
            self, mock_utils, is_host_part_of_update, is_node_id_changed, is_host_definer_can_delete_hosts,
            is_node_has_forbid_deletion_label):
        self.watcher.csi_node.is_host_part_of_update.return_value = is_host_part_of_update
        if is_host_part_of_update:
            self._prepare_create_definitions_when_csi_node_changed(is_node_id_changed)
        else:
            mock_utils.is_host_definer_can_delete_hosts.return_value = is_host_definer_can_delete_hosts
            self.watcher.node_manager.is_node_has_forbid_deletion_label.return_value = is_node_has_forbid_deletion_label

    def _prepare_create_definitions_when_csi_node_changed(self, is_node_id_changed):
        self.global_managed_secret.append(self.fake_secret_info)
        self.watcher.host_definition_manager.get_matching_host_definition_info.return_value = \
            self.fake_host_definition_info
        self.watcher.csi_node.is_node_id_changed.return_value = is_node_id_changed
        if is_node_id_changed:
            self.watcher.node_manager.generate_managed_node.return_value = self.managed_node_with_different_node_id

    def _assert_watch_csi_nodes_resources(self, mock_utils, csi_node_watch_manifest, csi_node_watch_munch):
        mock_utils.loop_forever.assert_called()
        self.watcher.k8s_api.get_csi_node_stream.assert_called_with()
        mock_utils.munch.assert_called_once_with(csi_node_watch_manifest)
        self.watcher.resource_info_manager.generate_csi_node_info.assert_called_once_with(
            csi_node_watch_munch.object)

    def _assert_define_new_csi_node_called(self, mock_utils):
        self.watcher.node_manager.is_node_can_be_defined.assert_called_once_with(self.fake_csi_node_info.name)
        self.watcher.node_manager.add_node_to_nodes.assert_called_once_with(self.fake_csi_node_info)
        self.watcher.definition_manager.define_node_on_all_storages.assert_called_once_with(
            self.fake_csi_node_info.name)
        self._assert_handle_deleted_csi_node_pod_not_called(mock_utils)

    def _assert_define_new_csi_node_not_called(self):
        self.watcher.node_manager.is_node_can_be_defined.assert_called_once_with(self.fake_csi_node_info.name)
        self.watcher.node_manager.add_node_to_nodes.assert_not_called()
        self.watcher.definition_manager.define_node_on_all_storages.assert_not_called()

    def _assert_handle_deleted_csi_node_pod_called(
            self, mock_utils, is_node_has_manage_node_label, is_host_part_of_update=False, is_node_id_changed=False,
            is_host_definer_can_delete_hosts=False, is_node_has_forbid_deletion_label=False):
        self.watcher.node_manager.is_node_has_manage_node_label.assert_called_once_with(self.fake_csi_node_info.name)
        if is_node_has_manage_node_label:
            self.watcher.csi_node.is_host_part_of_update.assert_called_once_with(self.fake_csi_node_info.name)
            self._assert_undefine_host_when_node_pod_is_deleted_called(
                mock_utils, is_host_part_of_update, is_node_id_changed, is_host_definer_can_delete_hosts,
                is_node_has_forbid_deletion_label)
        else:
            self._assert_undefine_host_when_node_pod_is_deleted_not_called(mock_utils)

    def _assert_undefine_host_when_node_pod_is_deleted_called(
            self, mock_utils, is_host_part_of_update, is_node_id_changed, is_host_definer_can_delete_hosts,
            is_node_has_forbid_deletion_label):
        self.watcher.csi_node.is_host_part_of_update.assert_called_once_with(self.fake_csi_node_info.name)
        if is_host_part_of_update:
            self._assert_create_definitions_when_csi_node_changed_called(is_node_id_changed)
            mock_utils.is_host_definer_can_delete_hosts.assert_not_called()
            self.watcher.node_manager.is_node_has_forbid_deletion_label.assert_not_called()
            self._assert_undefine_all_the_definitions_of_a_node_not_called()
        else:
            self._assert_create_definitions_when_csi_node_changed_not_called()
            mock_utils.is_host_definer_can_delete_hosts.assert_called_once_with()
            if is_host_definer_can_delete_hosts:
                self.watcher.node_manager.is_node_has_forbid_deletion_label.assert_called_once_with(
                    self.fake_csi_node_info.name)
            if is_host_definer_can_delete_hosts and not is_node_has_forbid_deletion_label:
                self._assert_undefine_all_the_definitions_of_a_node_called()
            else:
                self._assert_undefine_all_the_definitions_of_a_node_not_called()
                self.assertEqual(self.global_managed_nodes, {})

    def _assert_create_definitions_when_csi_node_changed_called(self, is_node_id_changed):
        self.watcher.host_definition_manager.get_matching_host_definition_info.assert_called_once_with(
            self.fake_csi_node_info.name, self.fake_secret_info.name, self.fake_secret_info.namespace)
        self.watcher.csi_node.is_node_id_changed.assert_called_once_with(self.fake_host_definition_info.node_id,
                                                                         self.fake_csi_node_info.node_id)
        if is_node_id_changed:
            self.watcher.node_manager.generate_managed_node.assert_called_once_with(self.fake_csi_node_info)
            self.watcher.definition_manager.create_definition.assert_called_once_with(self.fake_host_definition_info)
            self.assertEqual(self.global_managed_nodes[self.fake_csi_node_info.name],
                             self.managed_node_with_different_node_id)
        else:
            self.watcher.node_manager.generate_managed_node.assert_not_called()
            self.watcher.definition_manager.create_definition.assert_not_called()

    def _assert_undefine_all_the_definitions_of_a_node_called(self):
        self.watcher.definition_manager.undefine_node_definitions.assert_called_once_with(self.fake_csi_node_info.name)
        self.watcher.node_manager.remove_manage_node_label.assert_called_once_with(self.fake_csi_node_info.name)
        self.assertEqual(self.global_managed_nodes, {})

    def _assert_handle_deleted_csi_node_pod_not_called(self, mock_utils):
        self.watcher.node_manager.is_node_has_manage_node_label.assert_not_called()
        self._assert_undefine_host_when_node_pod_is_deleted_not_called(mock_utils)

    def _assert_undefine_host_when_node_pod_is_deleted_not_called(self, mock_utils):
        self.watcher.csi_node.is_host_part_of_update.assert_not_called()
        mock_utils.is_host_definer_can_delete_hosts.assert_not_called()
        self.watcher.node_manager.is_node_has_forbid_deletion_label.assert_not_called()
        self._assert_create_definitions_when_csi_node_changed_not_called()
        self._assert_undefine_all_the_definitions_of_a_node_not_called()

    def _assert_create_definitions_when_csi_node_changed_not_called(self):
        self.watcher.host_definition_manager.get_matching_host_definition_info.assert_not_called()
        self.watcher.csi_node.is_node_id_changed.assert_not_called()
        self.watcher.node_manager.generate_managed_node.assert_not_called()
        self.watcher.definition_manager.create_definition.assert_not_called()

    def _assert_undefine_all_the_definitions_of_a_node_not_called(self):
        self.watcher.definition_manager.undefine_node_definitions.assert_not_called()
        self.watcher.node_manager.remove_manage_node_label.assert_not_called()
