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
from controllers.servers.host_definer.types import DefineHostResponse
import controllers.tests.controller_server.host_definer.utils.test_utils as test_utils

HOST_DEFINER_SERVER_PATH = "controllers.servers.host_definer.storage_manager.host_definer_server"


class BaseSetUp(unittest.TestCase):

    def setUp(self):
        self.servicer = HostDefinerServicer()

        mock_array_type(self, HOST_DEFINER_SERVER_PATH)

        self.mediator = Mock()

        self.storage_agent = MagicMock()
        mock_get_agent(self, HOST_DEFINER_SERVER_PATH)

        self.request = Mock(
            spec_set=['prefix', 'connectivity_type_from_user', 'node_id_from_csi_node',
                      'node_id_from_host_definition', 'array_connection_info', 'io_group'])

        self.request.prefix = None
        self.request.connectivity_type_from_user = array_settings.ISCSI_CONNECTIVITY_TYPE
        self.request.node_id_from_csi_node = settings.FAKE_NODE_ID
        self.request.node_id_from_host_definition = settings.FAKE_NODE_ID
        self.request.array_connection_info = get_array_connection_info_from_secrets(SECRET)
        self.request.io_group = array_settings.DUMMY_MULTIPLE_IO_GROUP_STRING


class TestDefineHost(BaseSetUp):

    def _prepare_define_host(self, is_host_exist=False):
        if is_host_exist:
            self.mediator.get_host_by_host_identifiers.return_value = (HOST_NAME, '')
        else:
            self.mediator.get_host_by_host_identifiers.side_effect = HostNotFoundError('host_identifier')

    def _test_define_host_success(self, is_host_exist=False):
        self._prepare_define_host(is_host_exist)
        response = self.servicer.define_host(self.request)
        self.mediator.get_host_by_host_identifiers.assert_called_once_with(Initiators(iscsi_iqns=[settings.IQN]))
        self.assertEqual(response.error_message, '')

    def test_define_host_success(self):
        self._test_define_host_success()
        self.mediator.create_host.assert_called_once_with(
            HOST_NAME, Initiators(iscsi_iqns=[settings.IQN]),
            self.request.connectivity_type_from_user, self.request.io_group)

    def test_define_host_failed(self):
        error_message = 'error'
        self.mediator.get_host_by_host_identifiers.side_effect = Exception(error_message)
        response = self.servicer.define_host(self.request)
        self.assertEqual(response.error_message, error_message)

    def _prepare_define_host_already_exists(self, nqn, iqn):
        self._prepare_define_host()
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
        self.mediator.get_host_io_group.assert_called_once_with(HOST_NAME)
        self.mediator.remove_io_group_from_host.assert_called_once_with(HOST_NAME, '0')
        self.mediator.add_io_group_to_host.assert_called_once_with(HOST_NAME, array_settings.DUMMY_IO_GROUP_TO_ADD)

    def test_define_host_update_ports_with_different_protocol_success(self):
        self._prepare_define_host_update_ports(array_settings.NVME_OVER_FC_CONNECTIVITY_TYPE,
                                               Initiators(iscsi_iqns=[settings.IQN]))
        self.mediator.delete_host.assert_called_once_with(HOST_NAME)
        self.mediator.create_host.assert_called_once_with(
            HOST_NAME, Initiators(iscsi_iqns=[settings.IQN]),
            self.request.connectivity_type_from_user, self.request.io_group)
        self.mediator.remove_ports_from_host.assert_not_called()
        self._assert_io_group()

    def test_define_host_update_ports_with_same_protocol_success(self):
        self.mediator.get_host_connectivity_ports.return_value = [settings.IQN]
        self.request.node_id_from_csi_node = '{};;{};'.format(HOST_NAME, settings.WWPN)
        self.request.connectivity_type_from_user = array_settings.FC_CONNECTIVITY_TYPE
        self._prepare_define_host_update_ports(array_settings.ISCSI_CONNECTIVITY_TYPE,
                                               Initiators(fc_wwns=[settings.WWPN]))

        self.mediator.remove_ports_from_host.assert_called_once_with(HOST_NAME, [settings.IQN],
                                                                     array_settings.ISCSI_CONNECTIVITY_TYPE)
        self.mediator.create_host.assert_not_called()
        self.mediator.delete_host.assert_not_called()
        self._assert_io_group()

    def test_define_host_return_values(self):
        self._prepare_define_host()
        expected_response = DefineHostResponse(connectivity_type=self.request.connectivity_type_from_user,
                                               node_name_on_storage=HOST_NAME,
                                               ports=[settings.IQN])
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
