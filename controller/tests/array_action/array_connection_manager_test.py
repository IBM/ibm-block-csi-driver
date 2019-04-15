import unittest
import controller.array_action.array_connection_manager as array_connection_manager
from controller.array_action.array_connection_manager import ArrayConnectionManager, NoConnctionAvailableException, xiv_type
from mock import patch, Mock

class TestWithFunctionality(unittest.TestCase):
    def setUp(self):
        self.fqdn = "fqdn"
        self.array_connection = ArrayConnectionManager(xiv_type, "user", "password",  self.fqdn) 
    
    @patch("controller.array_action.array_connection_manager.XIVArrayMediator._connect")
    @patch("controller.array_action.array_connection_manager.XIVArrayMediator.close")
    def test_with_opens_and_closes_the_connection(self, close, connect):
        with self.array_connection as array_mediator:
            self.assertEqual(self.array_connection.connected, True)
            self.assertEqual(array_mediator.endpoint,  self.fqdn)
        connect.assert_called_with()
        close.assert_called_with()
     
    @patch("controller.array_action.array_connection_manager.sleep")    
    @patch("controller.array_action.array_connection_manager.ArrayConnectionManager.get_array_connection")
    def test_with_retries_2_times_and_succeeds_on_third(self, get_connection,  sleep):
        get_connection.side_effect = [NoConnctionAvailableException(self.fqdn), NoConnctionAvailableException(self.fqdn), Mock()]
        with self.array_connection as array_mediator:
            pass
        self.assertEqual(sleep.call_count, 2)
        get_connection.assert_called_with()
     
    @patch("controller.array_action.array_connection_manager.MAX_TRIES", 3)    
    @patch("controller.array_action.array_connection_manager.sleep")    
    @patch("controller.array_action.array_connection_manager.ArrayConnectionManager.get_array_connection")
    def test_with_throws_error_in_case_of_max_retires_reached(self,  get_connection,  sleep):

        get_connection.side_effect = [NoConnctionAvailableException(self.fqdn), NoConnctionAvailableException(self.fqdn), NoConnctionAvailableException(self.fqdn)]
        with self.assertRaises(NoConnctionAvailableException) as ex:
            with self.array_connection as array_mediator:
                pass
            
        self.assertEqual(sleep.call_count, 3)
        self.assertEqual(get_connection.call_count, 3)
        
    @patch("controller.array_action.array_connection_manager.MAX_TRIES", 3)    
    @patch("controller.array_action.array_connection_manager.sleep")    
    @patch("controller.array_action.array_connection_manager.ArrayConnectionManager.get_array_connection")
    def test_with_throws_error_if_other_error_occures(self,  get_connection,  sleep):
        error_message = "this is a dummy error"
        get_connection.side_effect = [NoConnctionAvailableException(self.fqdn), Exception(error_message)]
        with self.assertRaises(Exception) as ex:
            with self.array_connection as array_mediator:
                pass
        
        self.assertTrue(error_message in ex.exception)
        self.assertEqual(sleep.call_count, 1)
        self.assertEqual(get_connection.call_count, 2)
    
class TestGetconnection(unittest.TestCase):
    def setUp(self):
        print "setUP"
        self.fqdn = "fqdn"
        self.array_connection = ArrayConnectionManager(xiv_type, "user", "password",  self.fqdn)
        array_connection_manager.array_connections_dict = {}
        
    def tearDown(self):
        array_connection_manager.array_connections_dict = {}
        
    @patch("controller.array_action.array_connection_manager.XIVArrayMediator._connect")
    def test_connection_adds_the_new_endpoint_for_the_first_time(self,connect):
        self.assertEqual(array_connection_manager.array_connections_dict, {})
        self.array_connection.get_array_connection()
        self.assertEqual(array_connection_manager.array_connections_dict, {self.fqdn : 1})
        
        new_fqdn = "new-fqdn"
        array_connection2 = ArrayConnectionManager(xiv_type, "user", "password",  new_fqdn)
        array_connection2.get_array_connection()  
        self.assertEqual(array_connection_manager.array_connections_dict, {self.fqdn : 1, new_fqdn:1})   
        
    @patch("controller.array_action.array_connection_manager.XIVArrayMediator._connect")
    def test_connection_adds_connections_to_connection_dict(self,connect):
        self.assertEqual(array_connection_manager.array_connections_dict, {})
        self.array_connection.get_array_connection()
        self.assertEqual(array_connection_manager.array_connections_dict, {self.fqdn : 1})
        
        self.array_connection.get_array_connection()
        self.assertEqual(array_connection_manager.array_connections_dict, {self.fqdn : 2})   
    
    @patch("controller.array_action.array_connection_manager.XIVArrayMediator._connect")
    def test_connection_returns_error_on_too_many_connection(self,connect):
        array_connection_manager.array_connections_dict = {self.fqdn : array_connection_manager.XIVArrayMediator.CONNECTION_LIMIT}
        with self.assertRaises(NoConnctionAvailableException):
            self.array_connection.get_array_connection()
                   
        
    @patch("controller.array_action.array_connection_manager.XIVArrayMediator._connect")
    def test_connection_returns_error_from_connect_function(self,connect):
        error_msg = "some error"
        connect.side_effect = [Exception(error_msg)]
        
        with self.assertRaises(Exception) as ex:
            self.array_connection.get_array_connection()
    
        self.assertTrue(error_msg in ex.exception)
       

