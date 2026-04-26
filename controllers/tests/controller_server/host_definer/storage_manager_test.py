import unittest
from mock import Mock, MagicMock

from controllers.array_action.array_action_types import Host
from controllers.array_action.errors import HostNotFoundError, HostAlreadyExists
from controllers.common.node_info import Initiators
from controllers.servers.utils import get_array_connection_info_from_secrets
from controllers.servers.host_definer.storage_manager.host_definer_server import HostDefinerServicer
from controllers.tests.common.test_settings import HOST_NAME, SECRET
from controllers.tests.controller_server.common import mock_get_agent, mock_array_type
import controllers.tests.controller_server.host_definer.settings as settings
import controllers.tests.array_action.test_settings as array_settings
import controllers.tests.controller_server.host_definer.utils.test_utils as test_utils
import controllers.common.settings as common_settings

HOST_DEFINER_SERVER_PATH = "controllers.servers.host_definer.storage_manager.host_definer_server"


class BaseSetUp(unittest.TestCase):

    def setUp(self):

        self.servicer = HostDefinerServicer()
        mock_array_type(self, HOST_DEFINER_SERVER_PATH)

        self.mediator = Mock()
        self.storage_agent = MagicMock()
        mock_get_agent(self, HOST_DEFINER_SERVER_PATH)

        self.request = Mock(
            spec_set=[
                "prefix",
                "connectivity_type_from_user",
                "node_id_from_csi_node",
                "node_id_from_host_definition",
                "array_connection_info",
                "io_group",
                "node_initiators_from_csi_node",
                "node_initiators_from_host_definition",
            ]
        )

        self.request.prefix = None
        self.request.connectivity_type_from_user = array_settings.ISCSI_CONNECTIVITY_TYPE
        self.request.node_id_from_csi_node = settings.FAKE_NODE_ID
        self.request.node_id_from_host_definition = settings.FAKE_NODE_ID
        self.request.array_connection_info = get_array_connection_info_from_secrets(SECRET)
        self.request.io_group = array_settings.DUMMY_MULTIPLE_IO_GROUP_STRING
        self.request.node_initiators_from_csi_node = test_utils.initiators_to_json(
            Initiators(iscsi_iqns=[settings.IQN]))
        self.request.node_initiators_from_host_definition = test_utils.initiators_to_json(
            Initiators(iscsi_iqns=[settings.IQN]))


class TestDefineHost(BaseSetUp):

    def _prepare_define_host(self, is_host_exist=False):
        if is_host_exist:
            self.mediator.get_host_by_host_identifiers.return_value = (HOST_NAME, '')
        else:
            self.mediator.get_host_by_host_identifiers.side_effect = HostNotFoundError('host_identifier')

    def _test_define_host_success(self, is_host_exist=False):
        self._prepare_define_host(is_host_exist)
        self.mediator.get_host_io_group.return_value = test_utils.get_fake_host_io_group()
        response = self.servicer.define_host(self.request)
        self.mediator.get_host_by_host_identifiers.assert_called_once_with(Initiators(iscsi_iqns=[settings.IQN]))
        self.assertEqual(response.error_message, '')

    def test_define_host_success(self):
        self._test_define_host_success()
        self.mediator.create_host.assert_called_once_with(
            HOST_NAME, Initiators(iscsi_iqns=[settings.IQN]),
            self.request.connectivity_type_from_user, self.request.io_group, None, None)

    def test_define_host_failed(self):
        error_message = 'error'
        self.mediator.get_host_by_host_identifiers.side_effect = Exception(error_message)
        response = self.servicer.define_host(self.request)
        self.assertEqual(response.error_message, error_message)

    def _prepare_define_host_already_exists(self, nqn, iqn):
        self._prepare_define_host()
        self.mediator.get_host_io_group.return_value = test_utils.get_fake_host_io_group()
        self.mediator.create_host.side_effect = HostAlreadyExists(HOST_NAME, '')
        self.mediator.get_host_by_name.return_value = Host(name=HOST_NAME, nvme_nqns=[nqn], iscsi_iqns=[iqn],
                                                           connectivity_types=[])

    def test_define_host_already_exists_success(self):
        self._prepare_define_host_already_exists(settings.NQN, settings.IQN)

        response = self.servicer.define_host(self.request)

        self.mediator.get_host_by_name.assert_called_once_with(HOST_NAME)
        self.assertEqual(response.error_message, '')

    def _prepare_define_host_update_ports(self, host_connectivity_type, initiators):
        self._prepare_define_host(is_host_exist=True)
        self.mediator.get_host_connectivity_type.return_value = host_connectivity_type
        self.mediator.get_host_io_group.return_value = test_utils.get_fake_host_io_group()
        response = self.servicer.define_host(self.request)

        self.mediator.add_ports_to_host.assert_called_once_with(HOST_NAME, initiators,
                                                                self.request.connectivity_type_from_user)
        self.assertEqual(response.error_message, '')

    def _assert_io_group(self):
        self.mediator.get_host_io_group.assert_called_with(HOST_NAME)
        self.assertEqual(2, self.mediator.get_host_io_group.call_count)
        self.mediator.remove_io_group_from_host.assert_called_once_with(HOST_NAME, '0')
        self.mediator.add_io_group_to_host.assert_called_once_with(HOST_NAME, array_settings.DUMMY_IO_GROUP_TO_ADD)

    def test_define_host_update_ports_with_different_protocol_on_storage_without_chhost_success(self):
        self.mediator.change_host_protocol.side_effect = HostNotFoundError('error')
        self._prepare_define_host_update_ports(array_settings.NVME_OVER_FC_CONNECTIVITY_TYPE,
                                               Initiators(iscsi_iqns=[settings.IQN]))

        self.mediator.delete_host.assert_called_once_with(HOST_NAME)
        self.mediator.create_host.assert_called_once_with(
            HOST_NAME, Initiators(iscsi_iqns=[settings.IQN]),
            self.request.connectivity_type_from_user, self.request.io_group, None, None)
        self.mediator.remove_ports_from_host.assert_called_once()
        self._assert_io_group()

    def _prepare_define_host_update_ports_without_delete_host(self, host_connectivity_type):
        self.mediator.get_host_connectivity_ports.return_value = [settings.IQN]
        self.request.node_id_from_csi_node = HOST_NAME
        self.request.connectivity_type_from_user = array_settings.FC_CONNECTIVITY_TYPE
        self.request.node_initiators_from_csi_node = test_utils.initiators_to_json(Initiators(fc_wwns=[settings.WWPN]))
        self._prepare_define_host_update_ports(host_connectivity_type,
                                               Initiators(fc_wwns=[settings.WWPN]))

        self.mediator.create_host.assert_not_called()
        self.mediator.delete_host.assert_not_called()
        self._assert_io_group()

    def test_define_host_update_ports_with_different_protocol_on_storage_with_chhost_success(self):
        self._prepare_define_host_update_ports_without_delete_host(array_settings.NVME_OVER_FC_CONNECTIVITY_TYPE)

        self.assertEqual(2, self.mediator.get_host_connectivity_ports.call_count)
        self.mediator.remove_ports_from_host.assert_called_once_with(HOST_NAME, [settings.IQN],
                                                                     array_settings.NVME_OVER_FC_CONNECTIVITY_TYPE)
        self.mediator.change_host_protocol.assert_called_once_with(
            HOST_NAME, common_settings.SCSI_PROTOCOL)

    def test_define_host_update_ports_with_same_protocol_success(self):
        self._prepare_define_host_update_ports_without_delete_host(array_settings.ISCSI_CONNECTIVITY_TYPE)
        self.mediator.remove_ports_from_host.assert_called_once_with(HOST_NAME, [settings.IQN],
                                                                     array_settings.ISCSI_CONNECTIVITY_TYPE)

    def test_define_host_return_values(self):
        self._prepare_define_host()
        self.mediator.get_host_io_group.return_value = test_utils.get_fake_host_io_group()
        expected_response = test_utils.get_define_response(
            self.request.connectivity_type_from_user, [settings.IQN])
        self.mediator.get_host_connectivity_ports.return_value = [settings.IQN]
        response = self.servicer.define_host(self.request)
        self.assertEqual(response, expected_response)

    def test_define_host_already_exists_failed(self):
        self._prepare_define_host_already_exists(settings.NQN, "")

        response = self.servicer.define_host(self.request)

        self.mediator.get_host_by_name.assert_called_once_with(HOST_NAME)
        self.assertNotEqual(response.error_message, '')


class TestUndefineHost(BaseSetUp):

    def _prepare_undefine_host_success(self, is_found=True):
        if is_found:
            self.mediator.get_host_by_host_identifiers.return_value = (HOST_NAME, '')
        else:
            self.mediator.get_host_by_host_identifiers.side_effect = HostNotFoundError('error')

        response = self.servicer.undefine_host(self.request)
        self.mediator.get_host_by_host_identifiers.assert_called_once_with(Initiators(iscsi_iqns=[settings.IQN]))
        self.assertEqual(response.error_message, '')

    def test_undefine_host_success(self):
        self._prepare_undefine_host_success()
        self.mediator.delete_host.assert_called_once_with(HOST_NAME)

    def test_undefine_host_idempotency_success(self):
        self._prepare_undefine_host_success(is_found=False)
        self.mediator.delete_host.assert_not_called()

    def test_verify_host_definition_on_storage_failed(self):
        error_message = 'error'
        self.mediator.get_host_by_host_identifiers.side_effect = Exception(error_message)
        response = self.servicer.undefine_host(self.request)
        self.assertEqual(response.error_message, error_message)


class TestGetPortsToRemove(BaseSetUp):
    """Test cases for _get_ports_to_remove method"""

    def test_get_ports_to_remove_with_no_ports_to_keep(self):
        """Test removing all ports when do_not_remove_ports is empty"""
        all_ports = ["port1", "port2", "port3"]
        do_not_remove_ports = []
        result = self.servicer._get_ports_to_remove(all_ports, do_not_remove_ports)
        self.assertEqual(result, ["port1", "port2", "port3"])

    def test_get_ports_to_remove_with_none_ports_to_keep(self):
        """Test removing all ports when do_not_remove_ports is None"""
        all_ports = ["port1", "port2", "port3"]
        do_not_remove_ports = None
        result = self.servicer._get_ports_to_remove(all_ports, do_not_remove_ports)
        self.assertEqual(result, ["port1", "port2", "port3"])

    def test_get_ports_to_remove_with_some_ports_to_keep(self):
        """Test removing only ports not in the keep list"""
        all_ports = ["port1", "port2", "port3", "port4"]
        do_not_remove_ports = ["port2", "port4"]
        result = self.servicer._get_ports_to_remove(all_ports, do_not_remove_ports)
        self.assertEqual(result, ["port1", "port3"])

    def test_get_ports_to_remove_with_all_ports_to_keep(self):
        """Test removing no ports when all ports should be kept"""
        all_ports = ["port1", "port2", "port3"]
        do_not_remove_ports = ["port1", "port2", "port3"]
        result = self.servicer._get_ports_to_remove(all_ports, do_not_remove_ports)
        self.assertEqual(result, [])

    def test_get_ports_to_remove_with_extra_ports_to_keep(self):
        """Test when do_not_remove_ports contains ports not in all_ports"""
        all_ports = ["port1", "port2"]
        do_not_remove_ports = ["port2", "port3", "port4"]
        result = self.servicer._get_ports_to_remove(all_ports, do_not_remove_ports)
        self.assertEqual(result, ["port1"])

    def test_get_ports_to_remove_with_empty_all_ports(self):
        """Test with empty all_ports list"""
        all_ports = []
        do_not_remove_ports = ["port1", "port2"]
        result = self.servicer._get_ports_to_remove(all_ports, do_not_remove_ports)
        self.assertEqual(result, [])

    def test_get_ports_to_remove_with_duplicate_ports(self):
        """Test handling of duplicate ports in lists"""
        all_ports = ["port1", "port2", "port2", "port3"]
        do_not_remove_ports = ["port2"]
        result = self.servicer._get_ports_to_remove(all_ports, do_not_remove_ports)
        # Should remove port1 and port3, but port2 appears twice
        self.assertIn("port1", result)
        self.assertIn("port3", result)
        self.assertNotIn("port2", result)


class TestGetPortsToAdd(BaseSetUp):
    """Test cases for _get_ports_to_add method"""

    def test_get_ports_to_add_with_no_existing_ports(self):
        """Test adding all ports when existing_ports is empty"""
        new_ports = ["port1", "port2", "port3"]
        existing_ports = []
        result = self.servicer._get_ports_to_add(new_ports, existing_ports)
        self.assertEqual(result, ["port1", "port2", "port3"])

    def test_get_ports_to_add_with_none_existing_ports(self):
        """Test adding all ports when existing_ports is None"""
        new_ports = ["port1", "port2", "port3"]
        existing_ports = None
        result = self.servicer._get_ports_to_add(new_ports, existing_ports)
        self.assertEqual(result, ["port1", "port2", "port3"])

    def test_get_ports_to_add_with_some_existing_ports(self):
        """Test adding only new ports not already on host"""
        new_ports = ["port1", "port2", "port3", "port4"]
        existing_ports = ["port2", "port4"]
        result = self.servicer._get_ports_to_add(new_ports, existing_ports)
        self.assertEqual(result, ["port1", "port3"])

    def test_get_ports_to_add_with_all_ports_existing(self):
        """Test adding no ports when all are already on host"""
        new_ports = ["port1", "port2", "port3"]
        existing_ports = ["port1", "port2", "port3"]
        result = self.servicer._get_ports_to_add(new_ports, existing_ports)
        self.assertEqual(result, [])

    def test_get_ports_to_add_with_extra_existing_ports(self):
        """Test when existing_ports contains ports not in new_ports"""
        new_ports = ["port1", "port2"]
        existing_ports = ["port2", "port3", "port4"]
        result = self.servicer._get_ports_to_add(new_ports, existing_ports)
        self.assertEqual(result, ["port1"])

    def test_get_ports_to_add_with_empty_new_ports(self):
        """Test with empty new_ports list"""
        new_ports = []
        existing_ports = ["port1", "port2"]
        result = self.servicer._get_ports_to_add(new_ports, existing_ports)
        self.assertEqual(result, [])

    def test_get_ports_to_add_with_duplicate_ports(self):
        """Test handling of duplicate ports in lists"""
        new_ports = ["port1", "port2", "port2", "port3"]
        existing_ports = ["port2"]
        result = self.servicer._get_ports_to_add(new_ports, existing_ports)
        # Should add port1 and port3, but port2 appears twice in new_ports
        self.assertIn("port1", result)
        self.assertIn("port3", result)
        self.assertNotIn("port2", result)


class TestRemoveHostPorts(BaseSetUp):
    """Test cases for _remove_host_ports method"""

    def test_remove_host_ports_with_valid_ports(self):
        """Test removing ports with valid connectivity type and ports"""
        host_name = HOST_NAME
        ports_to_remove = ["port1", "port2"]
        connectivity_type = array_settings.FC_CONNECTIVITY_TYPE

        self.servicer._remove_host_ports(self.mediator, host_name, ports_to_remove, connectivity_type)

        self.mediator.remove_ports_from_host.assert_called_once_with(
            host_name, ports_to_remove, connectivity_type
        )

    def test_remove_host_ports_with_empty_ports(self):
        """Test that no removal happens when ports_to_remove is empty"""
        host_name = HOST_NAME
        ports_to_remove = []
        connectivity_type = array_settings.FC_CONNECTIVITY_TYPE

        self.servicer._remove_host_ports(self.mediator, host_name, ports_to_remove, connectivity_type)

        self.mediator.remove_ports_from_host.assert_not_called()

    def test_remove_host_ports_with_none_ports(self):
        """Test that no removal happens when ports_to_remove is None"""
        host_name = HOST_NAME
        ports_to_remove = None
        connectivity_type = array_settings.FC_CONNECTIVITY_TYPE

        self.servicer._remove_host_ports(self.mediator, host_name, ports_to_remove, connectivity_type)

        self.mediator.remove_ports_from_host.assert_not_called()

    def test_remove_host_ports_with_none_connectivity_type(self):
        """Test that no removal happens when connectivity_type is None"""
        host_name = HOST_NAME
        ports_to_remove = ["port1", "port2"]
        connectivity_type = None

        self.servicer._remove_host_ports(self.mediator, host_name, ports_to_remove, connectivity_type)

        self.mediator.remove_ports_from_host.assert_not_called()

    def test_remove_host_ports_with_empty_connectivity_type(self):
        """Test that no removal happens when connectivity_type is empty"""
        host_name = HOST_NAME
        ports_to_remove = ["port1", "port2"]
        connectivity_type = ""

        self.servicer._remove_host_ports(self.mediator, host_name, ports_to_remove, connectivity_type)

        self.mediator.remove_ports_from_host.assert_not_called()


class TestChangeHostPortsWithFiltering(TestDefineHost):
    """Test cases for _change_host_ports with port filtering logic"""

    def test_change_host_ports_removes_only_missing_ports(self):
        """Test that only ports not in new initiators list are removed"""
        self._prepare_define_host(is_host_exist=True)
        self.mediator.get_host_connectivity_type.return_value = array_settings.FC_CONNECTIVITY_TYPE
        self.mediator.get_host_io_group.return_value = test_utils.get_fake_host_io_group()

        # Current ports on host
        current_ports = ["wwn1", "wwn2", "wwn3"]
        self.mediator.get_host_connectivity_ports.return_value = current_ports

        # New initiators (wwn2 and wwn4)
        self.request.node_initiators_from_csi_node = test_utils.initiators_to_json(
            Initiators(fc_wwns=["wwn2", "wwn4"])
        )
        self.request.connectivity_type_from_user = array_settings.FC_CONNECTIVITY_TYPE

        response = self.servicer.define_host(self.request)

        # Should remove wwn1 and wwn3 (not in new list)
        self.mediator.remove_ports_from_host.assert_called_once()
        call_args = self.mediator.remove_ports_from_host.call_args[0]
        ports_removed = call_args[1]
        self.assertIn("wwn1", ports_removed)
        self.assertIn("wwn3", ports_removed)
        self.assertNotIn("wwn2", ports_removed)

        # Should add only wwn4 (not already on host)
        self.mediator.add_ports_to_host.assert_called_once()
        call_args = self.mediator.add_ports_to_host.call_args[0]
        ports_added = call_args[1]
        self.assertEqual(ports_added, Initiators(fc_wwns=["wwn4"]))

        self.assertEqual(response.error_message, '')

    def test_change_host_ports_no_removal_when_all_ports_in_new_list(self):
        """Test that no ports are removed when all current ports are in new list"""
        self._prepare_define_host(is_host_exist=True)
        self.mediator.get_host_connectivity_type.return_value = array_settings.ISCSI_CONNECTIVITY_TYPE
        self.mediator.get_host_io_group.return_value = test_utils.get_fake_host_io_group()

        # Current ports on host
        current_ports = [settings.IQN]
        self.mediator.get_host_connectivity_ports.return_value = current_ports

        # New initiators include all current ports plus new one
        new_iqn = "iqn.1994-07.com.redhat:new"
        self.request.node_initiators_from_csi_node = test_utils.initiators_to_json(
            Initiators(iscsi_iqns=[settings.IQN, new_iqn])
        )

        response = self.servicer.define_host(self.request)

        # Should not remove any ports
        self.mediator.remove_ports_from_host.assert_not_called()

        # Should add only the new IQN
        self.mediator.add_ports_to_host.assert_called_once()
        call_args = self.mediator.add_ports_to_host.call_args[0]
        ports_added = call_args[1]
        self.assertEqual(ports_added, Initiators(iscsi_iqns=[new_iqn]))

        self.assertEqual(response.error_message, '')

    def test_change_host_ports_no_addition_when_all_new_ports_exist(self):
        """Test that no ports are added when all new ports already exist on host"""
        self._prepare_define_host(is_host_exist=True)
        self.mediator.get_host_connectivity_type.return_value = array_settings.FC_CONNECTIVITY_TYPE
        self.mediator.get_host_io_group.return_value = test_utils.get_fake_host_io_group()

        # Current ports on host
        current_ports = ["wwn1", "wwn2", "wwn3"]
        self.mediator.get_host_connectivity_ports.return_value = current_ports

        # New initiators are subset of current ports
        self.request.node_initiators_from_csi_node = test_utils.initiators_to_json(
            Initiators(fc_wwns=["wwn1", "wwn2"])
        )
        self.request.connectivity_type_from_user = array_settings.FC_CONNECTIVITY_TYPE

        response = self.servicer.define_host(self.request)

        # Should remove wwn3
        self.mediator.remove_ports_from_host.assert_called_once()
        call_args = self.mediator.remove_ports_from_host.call_args[0]
        ports_removed = call_args[1]
        self.assertEqual(ports_removed, ["wwn3"])

        # Should not add any ports
        self.mediator.add_ports_to_host.assert_not_called()

        self.assertEqual(response.error_message, '')


class TestChangeHostProtocolWithChhost(TestDefineHost):
    """Test cases for _change_host_protocol_with_chhost with updated logic"""

    def test_change_host_protocol_removes_all_current_ports(self):
        """Test that all current ports are removed when changing protocol"""
        self._prepare_define_host(is_host_exist=True)
        self.mediator.get_host_connectivity_type.return_value = array_settings.NVME_OVER_FC_CONNECTIVITY_TYPE
        self.mediator.get_host_io_group.return_value = test_utils.get_fake_host_io_group()
        self.mediator.verify_host_partition.return_value = True

        # Reset any side_effect from previous tests
        self.mediator.change_host_protocol.side_effect = None

        # Current NVMe ports on host
        current_ports = ["nqn1", "nqn2"]
        self.mediator.get_host_connectivity_ports.return_value = current_ports

        # Change to FC
        self.request.connectivity_type_from_user = array_settings.FC_CONNECTIVITY_TYPE
        self.request.node_initiators_from_csi_node = test_utils.initiators_to_json(
            Initiators(fc_wwns=["wwn1", "wwn2"])
        )

        response = self.servicer.define_host(self.request)

        # Should remove all NVMe ports
        self.assertEqual(2, self.mediator.get_host_connectivity_ports.call_count)
        self.mediator.remove_ports_from_host.assert_called_once_with(
            HOST_NAME, current_ports, array_settings.NVME_OVER_FC_CONNECTIVITY_TYPE
        )

        # Should change protocol
        self.mediator.change_host_protocol.assert_called_once_with(
            HOST_NAME, common_settings.SCSI_PROTOCOL
        )

        # Should add new FC ports
        self.mediator.add_ports_to_host.assert_called_once()

        self.assertEqual(response.error_message, '')

    def test_change_host_protocol_no_removal_when_no_current_ports(self):
        """Test that no removal happens when host has no current ports"""
        self._prepare_define_host(is_host_exist=True)
        self.mediator.get_host_connectivity_type.return_value = array_settings.ISCSI_CONNECTIVITY_TYPE
        self.mediator.get_host_io_group.return_value = test_utils.get_fake_host_io_group()
        self.mediator.verify_host_partition.return_value = True

        # Reset any side_effect from previous tests
        self.mediator.change_host_protocol.side_effect = None

        # No current ports on host
        self.mediator.get_host_connectivity_ports.return_value = []

        # Change to FC
        self.request.connectivity_type_from_user = array_settings.FC_CONNECTIVITY_TYPE
        self.request.node_initiators_from_csi_node = test_utils.initiators_to_json(
            Initiators(fc_wwns=["wwn1"])
        )

        response = self.servicer.define_host(self.request)

        # Should not call remove_ports_from_host
        self.mediator.remove_ports_from_host.assert_not_called()

        # Should not change protocol because it remains SCSI
        self.mediator.change_host_protocol.assert_not_called()
        # Should still add ports
        self.mediator.add_ports_to_host.assert_called_once()

        self.assertEqual(response.error_message, '')
