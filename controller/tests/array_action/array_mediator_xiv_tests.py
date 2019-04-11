import unittest
from pyxcli import errors as xcli_errors
from controller.array_action.array_mediator_xiv import XIVArrayMediator
from mock import patch, Mock
import controller.array_action.errors as array_errors
import bunch


class TestArrayMediatorXIV(unittest.TestCase):
    
    @patch("controller.array_action.array_mediator_xiv.XIVArrayMediator._connect")
    def setUp(self, connect):
        self.fqdn = "fqdn"
        self.mediator = XIVArrayMediator("user", "password", self.fqdn) 
        self.mediator.client = Mock()
    
    def test_get_volume_return_correct_errors(self):
        error_msg = "ex"
        self.mediator.client.cmd.vol_list.side_effect = [Exception("ex")]
        with self.assertRaises(Exception) as ex:
            self.mediator.get_volume("some name")

        self.assertTrue(error_msg in ex.exception)
        
    def test_get_volume_return_correct_value(self):
        error_msg = "ex"
        vol = bunch.Bunch()
        vol.size = 10
        self.mediator.client.cmd.vol_list.return_value = bunch.Bunch(as_single_element=vol)
        res = self.mediator.get_volume("some name")            

        self.assertTrue(res.size == self.mediator._convert_volume_size(vol.size))
        
    @patch("controller.array_action.array_mediator_xiv.XCLIClient")
    def test_connect_errors(self, client):
        client.connect_multiendpoint_ssl.return_value = Mock()
        client.connect_multiendpoint_ssl.side_effect = [xcli_errors.CredentialsError("a", "b", "c")]
        with self.assertRaises(array_errors.CredentailsError):
            self.mediator._connect()
        
        client.connect_multiendpoint_ssl.side_effect = [xcli_errors.XCLIError()]
        with self.assertRaises(array_errors.CredentailsError) as ex:
            self.mediator._connect()
            
    @patch("controller.array_action.array_mediator_xiv.XCLIClient")
    def test_close(self, client):
        self.mediator.client.is_connected = lambda : True
        self.mediator.close()
        self.mediator.client.close.assert_called_once_with()
        
        self.mediator.client.is_connected = lambda : False
        self.mediator.close()
        self.mediator.client.close.assert_called_once_with()

        
