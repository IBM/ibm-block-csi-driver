from copy import deepcopy
from unittest.mock import patch, MagicMock

import controllers.common.settings as common_settings
import controllers.tests.controller_server.host_definer.utils.test_utils as test_utils
import controllers.tests.controller_server.host_definer.settings as test_settings
from controllers.tests.controller_server.host_definer.watchers.watcher_base import WatcherBaseSetUp
from controllers.servers.host_definer.watcher.node_watcher import NodeWatcher


class NodeWatcherBase(WatcherBaseSetUp):
    def setUp(self):
        super().setUp()
        self.watcher = NodeWatcher()
        self.watcher.k8s_api = MagicMock()
        self.watcher.resource_info_manager = MagicMock()
        self.watcher.node_manager = MagicMock()
        self.watcher.host_definition_manager = MagicMock()
        self.watcher.definition_manager = MagicMock()
        self.fake_node_info = test_utils.get_fake_node_info()
        self.fake_csi_node_info = test_utils.get_fake_csi_node_info()
        self.fake_host_definition_info = test_utils.get_fake_host_definition_info()
        self.unmanaged_csi_nodes_with_driver = patch(
            '{}.unmanaged_csi_nodes_with_driver'.format(test_settings.NODES_WATCHER_PATH), set()).start()
        self.expected_unmanaged_csi_nodes_with_driver = set()

    def _prepare_is_unmanaged_csi_node_has_driver(self, is_node_can_be_defined):
        self.watcher.node_manager.is_node_can_be_defined.return_value = is_node_can_be_defined


class TestAddInitialNodes(NodeWatcherBase):
    def test_add_initial_unmanaged_node_with_ibm_block_csi_driver(self):
        self.expected_unmanaged_csi_nodes_with_driver.add(test_settings.FAKE_NODE_NAME)
        self._prepare_add_initial_nodes(self.fake_csi_node_info)
        self._prepare_is_unmanaged_csi_node_has_driver(False)
        self.watcher.add_initial_nodes()
        self._assert_add_initial_nodes()
        self._assert_delete_host_definitions_not_called()
        self.watcher.node_manager.is_node_can_be_defined.assert_called_once_with(self.fake_csi_node_info.name)

    def test_do_not_add_initial_unmanaged_node_with_ibm_block_csi_driver_because_it_can_be_defined(self):
        self._prepare_add_initial_nodes(self.fake_csi_node_info)
        self._prepare_is_unmanaged_csi_node_has_driver(True)
        self.watcher.add_initial_nodes()
        self._assert_add_initial_nodes()
        self._assert_delete_host_definitions_not_called()
        self.watcher.node_manager.is_node_can_be_defined.assert_called_once_with(self.fake_csi_node_info.name)

    def test_undefined_initial_managed_node_that_do_not_have_ibm_block_csi_driver_anymore(self):
        csi_node_info = deepcopy(self.fake_csi_node_info)
        csi_node_info.node_id = ''
        self._prepare_add_initial_nodes(csi_node_info)
        self._prepare_csi_node_pod_deleted_while_host_definer_was_down(True, True)
        self._prepare_delete_host_definitions_called(True)
        self.watcher.add_initial_nodes()
        self._assert_add_initial_nodes()
        self._assert_delete_host_definitions_called(True)
        self.watcher.node_manager.is_node_has_manage_node_label.assert_called_once_with(csi_node_info.name)
        self.watcher.node_manager.is_node_has_host_definitions.assert_called_once_with(csi_node_info.name)
        self.watcher.node_manager.is_node_can_be_defined.assert_not_called()

    def _prepare_delete_host_definitions_called(self, is_node_can_be_undefined):
        self.watcher.node_manager.is_node_can_be_undefined.return_value = is_node_can_be_undefined
        if is_node_can_be_undefined:
            self.watcher.host_definition_manager.get_all_host_definitions_info_of_the_node.return_value = \
                [self.fake_host_definition_info]

    def _assert_delete_host_definitions_called(self, is_node_can_be_undefined):
        self.watcher.node_manager.is_node_can_be_undefined.assert_called_once_with(self.fake_node_info.name)
        if is_node_can_be_undefined:
            self.watcher.host_definition_manager.get_all_host_definitions_info_of_the_node.assert_called_once_with(
                self.fake_node_info.name)
            self.watcher.definition_manager.delete_definition.assert_called_once_with(self.fake_host_definition_info)
            self.assertEqual(self.watcher.node_manager.remove_manage_node_label.call_count, 2)
        else:
            self.watcher.host_definition_manager.get_all_host_definitions_info_of_the_node.assert_not_called()
            self.watcher.definition_manager.delete_definition.assert_not_called()
            self.watcher.node_manager.remove_manage_node_label.assert_called_once_with(self.fake_node_info.name)

    def test_do_not_undefined_initial_that_do_not_have_ibm_block_csi_driver_because_it_is_not_managed(self):
        csi_node_info = deepcopy(self.fake_csi_node_info)
        csi_node_info.node_id = ''
        self._prepare_add_initial_nodes(csi_node_info)
        self._prepare_csi_node_pod_deleted_while_host_definer_was_down(False, True)
        self.watcher.add_initial_nodes()
        self._assert_add_initial_nodes()
        self._assert_delete_host_definitions_not_called()
        self.watcher.node_manager.is_node_has_manage_node_label.assert_called_once_with(csi_node_info.name)
        self.watcher.node_manager.is_node_has_host_definitions.assert_not_called()
        self.watcher.node_manager.is_node_can_be_defined.assert_not_called()

    def test_do_not_undefined_initial_that_do_not_have_ibm_block_csi_driver_because_it_is_not_have_host_definitions(
            self):
        csi_node_info = deepcopy(self.fake_csi_node_info)
        csi_node_info.node_id = ''
        self._prepare_add_initial_nodes(csi_node_info)
        self._prepare_csi_node_pod_deleted_while_host_definer_was_down(True, False)
        self.watcher.add_initial_nodes()
        self._assert_add_initial_nodes()
        self._assert_delete_host_definitions_not_called()
        self.watcher.node_manager.is_node_has_manage_node_label.assert_called_once_with(csi_node_info.name)
        self.watcher.node_manager.is_node_has_host_definitions.assert_called_once_with(csi_node_info.name)
        self.watcher.node_manager.is_node_can_be_defined.assert_not_called()

    def _prepare_add_initial_nodes(self, csi_node_info):
        self.watcher.node_manager.get_nodes_info.return_value = [self.fake_node_info]
        self.watcher.resource_info_manager.get_csi_node_info.return_value = csi_node_info

    def _prepare_csi_node_pod_deleted_while_host_definer_was_down(
            self, is_node_has_manage_node_label, is_node_has_host_definitions):
        self.watcher.node_manager.is_node_has_manage_node_label.return_value = is_node_has_manage_node_label
        self.watcher.node_manager.is_node_has_host_definitions.return_value = is_node_has_host_definitions

    def _assert_add_initial_nodes(self):
        self.watcher.node_manager.get_nodes_info.assert_called_once()
        self.watcher.resource_info_manager.get_csi_node_info.assert_called_once_with(self.fake_node_info.name)
        self.assertEqual(self.unmanaged_csi_nodes_with_driver, self.expected_unmanaged_csi_nodes_with_driver)

    def _assert_delete_host_definitions_not_called(self):
        self.watcher.node_manager.is_node_can_be_undefined.assert_not_called()
        self.watcher.host_definition_manager.get_all_host_definitions_info_of_the_node.assert_not_called()
        self.watcher.definition_manager.delete_definition.assert_not_called()
        self.watcher.node_manager.remove_manage_node_label.assert_not_called()


class TestWatchNodesResources(NodeWatcherBase):
    def setUp(self):
        super().setUp()
        self.node_modified_watch_manifest = test_utils.get_fake_node_watch_event(common_settings.MODIFIED_EVENT_TYPE)
        self.node_modified_watch_munch = test_utils.convert_manifest_to_munch(self.node_modified_watch_manifest)
        self.node_added_watch_manifest = test_utils.get_fake_node_watch_event(common_settings.ADDED_EVENT_TYPE)
        self.node_added_watch_munch = test_utils.convert_manifest_to_munch(self.node_added_watch_manifest)

    @patch('{}.utils'.format(test_settings.NODES_WATCHER_PATH))
    def test_watch_and_add_unmanaged_node_with_ibm_block_csi_driver_but_do_not_define_it(self, mock_utils):
        self.expected_unmanaged_csi_nodes_with_driver.add(test_settings.FAKE_NODE_NAME)
        self._prepare_watch_nodes_resources(self.node_modified_watch_manifest,
                                            self.node_modified_watch_munch, mock_utils)
        self._prepare_is_unmanaged_csi_node_has_driver(False)
        self.watcher.node_manager.is_node_has_new_manage_node_label.return_value = False
        self.watcher.watch_nodes_resources()
        self._assert_watch_nodes_resources(self.node_modified_watch_manifest,
                                           self.node_modified_watch_munch, mock_utils)
        self._assert_define_node_not_called()
        self.watcher.node_manager.is_node_has_new_manage_node_label.assert_called_once_with(
            self.fake_csi_node_info, self.unmanaged_csi_nodes_with_driver)

    @patch('{}.utils'.format(test_settings.NODES_WATCHER_PATH))
    def test_watch_and_add_unmanaged_node_with_ibm_block_csi_driver_and_define_it(self, mock_utils):
        self._prepare_watch_nodes_resources(self.node_modified_watch_manifest,
                                            self.node_modified_watch_munch, mock_utils)
        self._prepare_is_unmanaged_csi_node_has_driver(False)
        self.watcher.node_manager.is_node_has_new_manage_node_label.return_value = True
        self.watcher.watch_nodes_resources()
        self._assert_watch_nodes_resources(self.node_modified_watch_manifest,
                                           self.node_modified_watch_munch, mock_utils)
        self._assert_define_node_called()
        self.watcher.node_manager.is_node_has_new_manage_node_label.assert_called_once_with(
            self.fake_csi_node_info, self.unmanaged_csi_nodes_with_driver)

    @patch('{}.utils'.format(test_settings.NODES_WATCHER_PATH))
    def test_watch_and_do_not_add_unmanaged_node_with_ibm_block_csi_driver_and_define_it(self, mock_utils):
        self.unmanaged_csi_nodes_with_driver.add(test_settings.FAKE_NODE_NAME)
        self.expected_unmanaged_csi_nodes_with_driver = set()
        self._prepare_watch_nodes_resources(self.node_modified_watch_manifest,
                                            self.node_modified_watch_munch, mock_utils)
        self._prepare_is_unmanaged_csi_node_has_driver(True)
        self.watcher.node_manager.is_node_has_new_manage_node_label.return_value = True
        self.watcher.watch_nodes_resources()
        self._assert_watch_nodes_resources(self.node_modified_watch_manifest,
                                           self.node_modified_watch_munch, mock_utils)
        self._assert_define_node_called()
        self.watcher.node_manager.is_node_has_new_manage_node_label.assert_called_once_with(
            self.fake_csi_node_info, self.unmanaged_csi_nodes_with_driver)

    @patch('{}.utils'.format(test_settings.NODES_WATCHER_PATH))
    def test_watch_and_do_not_add_unmanaged_node_and_do_not_define_it_when_the_event_type_is_not_modified(
            self, mock_utils):
        self.expected_unmanaged_csi_nodes_with_driver = set()
        self._prepare_watch_nodes_resources(self.node_added_watch_manifest, self.node_added_watch_munch, mock_utils)
        self._prepare_is_unmanaged_csi_node_has_driver(True)
        self.watcher.watch_nodes_resources()
        self._assert_watch_nodes_resources(self.node_added_watch_manifest, self.node_added_watch_munch, mock_utils)
        self._assert_define_node_not_called()
        self.watcher.node_manager.is_node_has_new_manage_node_label.assert_not_called()
        self.watcher.node_manager.is_node_can_be_defined.assert_not_called()

    def _prepare_watch_nodes_resources(self, node_watch_manifest, node_watch_munch, mock_utils):
        mock_utils.loop_forever.side_effect = [True, False]
        self.watcher.k8s_api.get_node_stream.return_value = iter([node_watch_manifest])
        mock_utils.munch.return_value = node_watch_munch
        self.watcher.resource_info_manager.get_csi_node_info.return_value = self.fake_csi_node_info
        self.watcher.resource_info_manager.generate_node_info.return_value = self.fake_node_info

    def _assert_watch_nodes_resources(self, node_watch_manifest, node_watch_munch, mock_utils):
        self.watcher.k8s_api.get_node_stream.assert_called_once_with()
        mock_utils.munch.assert_called_once_with(node_watch_manifest)
        self.watcher.resource_info_manager.get_csi_node_info.assert_called_once_with(test_settings.FAKE_NODE_NAME)
        self.watcher.resource_info_manager.generate_node_info.assert_called_once_with(
            node_watch_munch.object)
        self.watcher.node_manager.handle_node_topologies.assert_called_once_with(
            self.fake_node_info, node_watch_munch.type)
        self.watcher.node_manager.update_node_io_group.assert_called_once_with(self.fake_node_info)
        self.assertEqual(self.unmanaged_csi_nodes_with_driver, self.expected_unmanaged_csi_nodes_with_driver)

    def _assert_define_node_not_called(self):
        self.watcher.node_manager.add_node_to_nodes.assert_not_called()
        self.watcher.definition_manager.define_node_on_all_storages.assert_not_called()

    def _assert_define_node_called(self):
        self.watcher.node_manager.add_node_to_nodes.assert_called_once_with(self.fake_csi_node_info)
        self.watcher.definition_manager.define_node_on_all_storages.assert_called_once_with(
            test_settings.FAKE_NODE_NAME)
