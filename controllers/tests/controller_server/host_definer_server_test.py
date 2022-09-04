import unittest

from mock import Mock, patch, MagicMock

from controllers.array_action.array_action_types import Host
from controllers.array_action.errors import HostNotFoundError, HostAlreadyExists
from controllers.common.node_info import Initiators
from controllers.servers.host_definer.storage_manager.host_definer_server import HostDefinerServicer
from controllers.tests.common.test_settings import HOST_NAME, SECRET
from controllers.tests.controller_server.common import mock_get_agent

HOST_DEFINER_SERVER_PATH = "controllers.servers.host_definer.storage_manager.host_definer_server"


class BaseSetUp(unittest.TestCase):
    def setUp(self):
        self.servicer = HostDefinerServicer()

        detect_array_type_path = '.'.join((HOST_DEFINER_SERVER_PATH, 'detect_array_type'))
        detect_array_type_patcher = patch(detect_array_type_path)
        detect_array_type_patcher.start()
        self.addCleanup(detect_array_type_patcher.stop)

        self.mediator = Mock()

        self.storage_agent = MagicMock()
        mock_get_agent(self, HOST_DEFINER_SERVER_PATH)

        self.request = Mock(spec_set=['prefix', 'connectivity_type', 'node_id', 'system_info'])

        self.iqn = 'iqn.1994-05.com.redhat:686358c930fe'
        self.nqn = 'nqn.2014-08.org.nvmexpress:uuid:b57708c7-5bb6-46a0-b2af-9d824bf539e1'
        self.request.prefix = None
        self.request.connectivity_type = 'fc'
        self.request.node_id = '{};;;{}'.format(HOST_NAME, self.iqn)
        self.request.system_info = SECRET


class TestDefineHost(BaseSetUp):

    def _prepare_define_host(self, is_idempotency=False):
        if is_idempotency:
            self.mediator.get_host_by_host_identifiers.return_value = (HOST_NAME, '')
        else:
            self.mediator.get_host_by_host_identifiers.side_effect = HostNotFoundError('host_identifier')

    def _test_define_host_success(self, is_idempotency=False):
        self._prepare_define_host(is_idempotency)
        response = self.servicer.define_host(self.request)
        self.mediator.get_host_by_host_identifiers.assert_called_once_with(Initiators(iscsi_iqns=[self.iqn]))
        self.assertEqual(response.error_message, '')

    def test_define_host_success(self):
        self._test_define_host_success()
        self.mediator.create_host.assert_called_once_with(HOST_NAME, Initiators(iscsi_iqns=[self.iqn]),
                                                          self.request.connectivity_type)

    def test_define_host_idempotency_success(self):
        self._test_define_host_success(is_idempotency=True)
        self.mediator.create_host.assert_not_called()

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
        self._prepare_define_host_already_exists(self.nqn, self.iqn)

        response = self.servicer.define_host(self.request)

        self.mediator.get_host_by_name.assert_called_once_with(HOST_NAME)
        self.assertEqual(response.error_message, '')

    def test_define_host_already_exists_failed(self):
        self._prepare_define_host_already_exists(self.nqn, "")

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
        self.mediator.get_host_by_host_identifiers.assert_called_once_with(Initiators(iscsi_iqns=[self.iqn]))
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
