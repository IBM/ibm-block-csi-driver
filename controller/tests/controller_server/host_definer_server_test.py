import unittest
from unittest.mock import Mock, patch

from mock import MagicMock

from controller.array_action.errors import HostNotFoundError
from controller.common.node_info import Initiators
from controller.controller_server.host_definer_server import HostDefinerServicer

HOST_DEFINER_SERVER_PATH = "controller.controller_server.host_definer_server"


class TestVerifyHostDefinitionOnStorage(unittest.TestCase):

    def setUp(self):
        super().setUp()
        self.servicer = HostDefinerServicer()

        detect_array_type_path = '.'.join((HOST_DEFINER_SERVER_PATH, 'detect_array_type'))
        detect_array_type_patcher = patch(detect_array_type_path)
        self.detect_array_type = detect_array_type_patcher.start()
        self.addCleanup(detect_array_type_patcher.stop)

        self.mediator = Mock()

        self.storage_agent = MagicMock()
        self.storage_agent.get_mediator.return_value.__enter__.return_value = self.mediator
        get_agent_path = '.'.join((HOST_DEFINER_SERVER_PATH, 'get_agent'))
        get_agent_patcher = patch(get_agent_path, return_value=self.storage_agent)
        self.detect_array_type = get_agent_patcher.start()
        self.addCleanup(get_agent_patcher.stop)

        self.request = Mock(spec_set=['prefix', 'connectivity_type', 'node_id', 'system_info'])

        self.hostname = "hostname"
        self.iqn = "iqn.1994-05.com.redhat:686358c930fe"

        self.request.prefix = None
        self.request.connectivity_type = 'fc'
        self.request.node_id = "{};;;{}".format(self.hostname, self.iqn)
        self.request.system_info = {"username": "user", "password": "pass", "management_address": "mg111"}

    def _prepare_verify_host_definition_on_storage_success(self, is_idempotency=False):
        if is_idempotency:
            self.mediator.get_host_by_host_identifiers.return_value = ("found_host_name", "")
        else:
            self.mediator.get_host_by_host_identifiers.side_effect = HostNotFoundError('host_identifier')

        response = self.servicer.VerifyHostDefinitionOnStorage(self.request)
        self.mediator.get_host_by_host_identifiers.assert_called_once_with(Initiators(iscsi_iqns=[self.iqn]))
        self.assertEqual(response.error_message, '')

    def test_verify_host_definition_on_storage_success(self):
        self._prepare_verify_host_definition_on_storage_success()
        self.mediator.create_host.assert_called_once_with(self.hostname, Initiators(iscsi_iqns=[self.iqn]),
                                                          self.request.connectivity_type)

    def test_verify_host_definition_on_storage_idempotency_success(self):
        self._prepare_verify_host_definition_on_storage_success(is_idempotency=True)
        self.mediator.create_host.assert_not_called()

    def test_verify_host_definition_on_storage_failed(self):
        error_message = "error"
        self.mediator.get_host_by_host_identifiers.side_effect = Exception(error_message)
        response = self.servicer.VerifyHostDefinitionOnStorage(self.request)
        self.assertEqual(response.error_message, error_message)
