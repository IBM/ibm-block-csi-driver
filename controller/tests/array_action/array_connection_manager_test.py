import unittest

from mock import patch

import controller.array_action.array_connection_manager as array_connection_manager
from controller.array_action.array_connection_manager import ArrayConnectionManager, NoConnectionAvailableException
from controller.array_action.array_mediator_ds8k import DS8KArrayMediator
from controller.array_action.array_mediator_svc import SVCArrayMediator
from controller.array_action.array_mediator_xiv import XIVArrayMediator
from controller.array_action.errors import FailedToFindStorageSystemType


class TestWithFunctionality(unittest.TestCase):

    def setUp(self):
        self.fqdn = "fqdn"
        self.array_connection = ArrayConnectionManager(
            "user", "password", [self.fqdn, self.fqdn], XIVArrayMediator.array_type)

    @patch("controller.array_action.array_connection_manager.XIVArrayMediator._connect")
    @patch("controller.array_action.array_connection_manager.XIVArrayMediator.disconnect")
    def test_with_opens_and_closes_the_connection(self, close, connect):
        with self.array_connection as array_mediator:
            self.assertEqual(self.array_connection.connected, True)
            self.assertEqual(array_mediator.endpoint, [self.fqdn, self.fqdn])
        connect.assert_called_with()
        close.assert_called_with()

    @patch("controller.array_action.array_connection_manager.ArrayConnectionManager.get_array_connection")
    def test_with_throws_error_if_other_error_occures(self, get_connection):
        error_message = "this is a dummy error "
        get_connection.side_effect = [Exception(error_message)]
        with self.assertRaises(Exception) as ex:
            with self.array_connection:
                pass

        self.assertTrue(error_message in str(ex.exception))
        self.assertEqual(get_connection.call_count, 1)


class TestGetconnection(unittest.TestCase):

    def setUp(self):
        self.fqdn = "fqdn"
        self.connections = [self.fqdn, self.fqdn]
        self.connection_key = ",".join(self.connections)
        self.array_connection = ArrayConnectionManager(
            "user", "password", self.connections, XIVArrayMediator.array_type)
        array_connection_manager.array_connections_dict = {}
        self.connect_patcher = patch("controller.array_action.array_connection_manager.XIVArrayMediator._connect")
        self.connect = self.connect_patcher.start()

    def tearDown(self):
        array_connection_manager.array_connections_dict = {}
        self.connect_patcher.stop()

    def test_connection_adds_the_new_endpoint_for_the_first_time(self):
        self.assertEqual(array_connection_manager.array_connections_dict, {})
        self.array_connection.get_array_connection()
        self.assertEqual(array_connection_manager.array_connections_dict, {self.connection_key: 1})

        new_fqdn = "new-fqdn"
        array_connection2 = ArrayConnectionManager("user", "password", [new_fqdn], XIVArrayMediator.array_type)

        array_connection2.get_array_connection()
        self.assertEqual(array_connection_manager.array_connections_dict, {self.connection_key: 1, new_fqdn: 1})

    def test_connection_adds_connections_to_connection_dict(self):
        self.assertEqual(array_connection_manager.array_connections_dict, {})
        self.array_connection.get_array_connection()
        self.assertEqual(array_connection_manager.array_connections_dict, {self.connection_key: 1})

        self.array_connection.get_array_connection()
        self.assertEqual(array_connection_manager.array_connections_dict, {self.connection_key: 2})

    def test_connection_returns_error_on_too_many_connection(self):
        array_connection_manager.array_connections_dict = {
            self.connection_key: array_connection_manager.XIVArrayMediator.max_connections}
        with self.assertRaises(NoConnectionAvailableException):
            self.array_connection.get_array_connection()

    def test_connection_returns_error_from_connect_function(self):
        error_msg = "some error"
        self.connect.side_effect = [Exception(error_msg)]

        with self.assertRaises(Exception) as ex:
            self.array_connection.get_array_connection()

        self.assertTrue(error_msg in str(ex.exception))

    @patch("controller.array_action.array_connection_manager._socket_connect_test")
    def test_detect_array_type(self, socket_connect_test_mock):

        # arrays is a [host, open_ports] dict, note that both port 22 and 8452 are opened in ds8k
        arrays = {
            "svc_host": [SVCArrayMediator.port, ],
            "ds8k_host": [DS8KArrayMediator.port, 22],
            "xiv_host": [XIVArrayMediator.port, ],

        }

        def side_effect(host, port):
            if host in arrays and port in arrays[host]:
                return 0
            return 1

        socket_connect_test_mock.side_effect = side_effect

        self.assertEqual(
            ArrayConnectionManager("", "", ["svc_host", ]).detect_array_type(),
            SVCArrayMediator.array_type
        )

        self.assertEqual(
            ArrayConnectionManager("", "", ["ds8k_host", ]).detect_array_type(),
            DS8KArrayMediator.array_type
        )

        self.assertEqual(
            ArrayConnectionManager("", "", ["xiv_host", ]).detect_array_type(),
            XIVArrayMediator.array_type
        )

        with self.assertRaises(FailedToFindStorageSystemType):
            ArrayConnectionManager("", "", ["unkonwn_host", ]).detect_array_type()

    def test_exit_reduces_connection_to_zero(self):
        self.array_connection.get_array_connection()
        self.assertEqual(array_connection_manager.array_connections_dict, {self.connection_key: 1})

        self.array_connection.__exit__("", "", None)
        self.assertEqual(array_connection_manager.array_connections_dict, {})

    def test_exit_reduces_connection(self):
        self.array_connection.get_array_connection()
        self.assertEqual(array_connection_manager.array_connections_dict, {self.connection_key: 1})

        self.array_connection.get_array_connection()
        self.assertEqual(array_connection_manager.array_connections_dict, {self.connection_key: 2})

        self.array_connection.__exit__("", "", None)
        self.assertEqual(array_connection_manager.array_connections_dict, {self.connection_key: 1})
