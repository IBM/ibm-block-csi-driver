import unittest
import controller.array_action.array_connection_manager as array_connection_manager
from controller.array_action.array_connection_manager import ArrayConnectionManager, NoConnectionAvailableException
from mock import patch
from controller.array_action.errors import FailedToFindStorageSystemType
from controller.array_action.array_mediator_xiv import XIVArrayMediator
from controller.array_action.array_mediator_svc import SVCArrayMediator


class TestWithFunctionality(unittest.TestCase):

    def setUp(self):
        self.fqdn = "fqdn"
        self.array_connection = ArrayConnectionManager("user", "password", [self.fqdn, self.fqdn] , XIVArrayMediator.ARRAY_TYPE)

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
            with self.array_connection as array_mediator:
                pass

        self.assertTrue(error_message in ex.exception)
        self.assertEqual(get_connection.call_count, 1)


class TestGetconnection(unittest.TestCase):

    def setUp(self):
        self.fqdn = "fqdn"
        self.connections = [self.fqdn, self.fqdn]
        self.connection_key = ",".join(self.connections )
        self.array_connection = ArrayConnectionManager("user", "password", self.connections, XIVArrayMediator.ARRAY_TYPE)
        array_connection_manager.array_connections_dict = {}

    def tearDown(self):
        array_connection_manager.array_connections_dict = {}

    @patch("controller.array_action.array_connection_manager.XIVArrayMediator._connect")
    def test_connection_adds_the_new_endpoint_for_the_first_time(self, connect):
        self.assertEqual(array_connection_manager.array_connections_dict, {})
        self.array_connection.get_array_connection()
        self.assertEqual(array_connection_manager.array_connections_dict, {self.connection_key: 1})

        new_fqdn = "new-fqdn"
        array_connection2 = ArrayConnectionManager("user", "password",[new_fqdn], XIVArrayMediator.ARRAY_TYPE)

        array_connection2.get_array_connection()
        self.assertEqual(array_connection_manager.array_connections_dict, { self.connection_key: 1, new_fqdn: 1})

    @patch("controller.array_action.array_connection_manager.XIVArrayMediator._connect")
    def test_connection_adds_connections_to_connection_dict(self, connect):
        self.assertEqual(array_connection_manager.array_connections_dict, {})
        self.array_connection.get_array_connection()
        self.assertEqual(array_connection_manager.array_connections_dict, {self.connection_key: 1})

        self.array_connection.get_array_connection()
        self.assertEqual(array_connection_manager.array_connections_dict, {self.connection_key: 2})

    @patch("controller.array_action.array_connection_manager.XIVArrayMediator._connect")
    def test_connection_returns_error_on_too_many_connection(self, connect):
        array_connection_manager.array_connections_dict = {
            self.connection_key: array_connection_manager.XIVArrayMediator.CONNECTION_LIMIT}
        with self.assertRaises(NoConnectionAvailableException):
            self.array_connection.get_array_connection()

    @patch("controller.array_action.array_connection_manager.XIVArrayMediator._connect")
    def test_connection_returns_error_from_connect_function(self, connect):
        error_msg = "some error"
        connect.side_effect = [Exception(error_msg)]

        with self.assertRaises(Exception) as ex:
            self.array_connection.get_array_connection()

        self.assertTrue(error_msg in ex.exception)

    @patch("controller.array_action.array_connection_manager._socket_connect_test")
    def test_detect_array_type(self, socket_connet):
        socket_connet.side_effect = [0, 1]

        res = self.array_connection.detect_array_type()
        self.assertEqual(res, XIVArrayMediator.ARRAY_TYPE)

        socket_connet.side_effect = [1, 1, 0, 0]


        res = self.array_connection.detect_array_type()
        self.assertEqual(res, SVCArrayMediator.ARRAY_TYPE)

        socket_connet.side_effect = [1, 1, 1, 1]
        with self.assertRaises(FailedToFindStorageSystemType):
            self.array_connection.detect_array_type()

    @patch("controller.array_action.array_connection_manager.XIVArrayMediator._connect")
    def test_exit_reduces_connection_to_zero(self, connect):
        self.array_connection.get_array_connection()
        self.assertEqual(array_connection_manager.array_connections_dict, {self.connection_key: 1})

        self.array_connection.__exit__("", "", None)
        self.assertEqual(array_connection_manager.array_connections_dict, {})


    @patch("controller.array_action.array_connection_manager.XIVArrayMediator._connect")
    def test_exit_reduces_connection(self, connect):
        self.array_connection.get_array_connection()
        self.assertEqual(array_connection_manager.array_connections_dict, {self.connection_key: 1})

        self.array_connection.get_array_connection()
        self.assertEqual(array_connection_manager.array_connections_dict, {self.connection_key: 2})

        self.array_connection.__exit__("", "", None)
        self.assertEqual(array_connection_manager.array_connections_dict, {self.connection_key: 1})

