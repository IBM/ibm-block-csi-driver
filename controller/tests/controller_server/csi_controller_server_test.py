import unittest
#from unittest import mock as umock
import grpc
import mock
from mock import patch, Mock, call
 
from controller.csi_general import csi_pb2
from controller.array_action.array_mediator_xiv import XIVArrayMediator
from controller.controller_server.csi_controller_server import ControllerServicer
from controller.controller_server.test_settings import vol_name


class TestControllerServer(unittest.TestCase):

    @patch("controller.array_action.array_mediator_xiv.XIVArrayMediator._connect")
    def setUp(self, connect):
        self.fqdn = "fqdn"
        self.mediator = XIVArrayMediator("user", "password", self.fqdn) 
        self.mediator.client = Mock()
        self.servicer = ControllerServicer(self.fqdn) 
 
    @patch("controller.array_action.array_connection_manager.ArrayConnectionManager.__enter__")
    @patch("controller.array_action.array_connection_manager.ArrayConnectionManager.__exit__")
    def test_create_volume_with_empty_name(self, a_enter, a_exit):
        a_enter.return_value = self.mediator
        request = Mock()
        request.name = ""
        context = Mock()
        res = self.servicer.CreateVolume(request, context)
        context.set_code.assert_called_once_with(grpc.StatusCode.INVALID_ARGUMENT)
        self.assertEqual(res, csi_pb2.CreateVolumeResponse())
     
    @patch("controller.array_action.array_connection_manager.ArrayConnectionManager.__enter__")
    @patch("controller.array_action.array_connection_manager.ArrayConnectionManager.__exit__")
    def test_create_volume_succeeds(self, a_exit, a_enter):
        a_enter.return_value = self.mediator
        self.mediator.get_volume = Mock()
        request = Mock()
        request.name = "some_name"
        context = Mock()
        res = self.servicer.CreateVolume(request, context)
        context.set_code.assert_called_once_with(grpc.StatusCode.OK)
        self.mediator.get_volume.assert_called_once_with(vol_name)
 
    @patch("controller.array_action.array_mediator_xiv.XIVArrayMediator.get_volume")
    @patch("controller.array_action.array_connection_manager.ArrayConnectionManager.__enter__")
    def test_create_volume_exception(self, a_enter, get_volume):
        a_enter.return_value = self.mediator
        get_volume.side_effect = [Exception("msg") ]
        request = Mock()
        request.name = "some_name"
        context = Mock()
        res = self.servicer.CreateVolume(request, context)
        context.set_code.assert_called_once_with(grpc.StatusCode.INTERNAL)
        self.mediator.get_volume.assert_called_once_with(vol_name)
 
    def test_controller_get_capabilities(self):
        request = Mock()
        context = Mock()
        self.servicer.ControllerGetCapabilities(request, context)
    
    @patch.object(ControllerServicer, "_ControllerServicer__get_identity_config")   
    def test_identity_plugin_get_info_succeeds(self, identity_config):
        plugin_name = "plugin-name"
        version = "1.0.0"
        identity_config.side_effect = [plugin_name, version]
        request = Mock()
        context = Mock()
        print "DIR",dir( ControllerServicer)
        res = self.servicer.GetPluginInfo(request, context)
        self.assertEqual(res, csi_pb2.GetPluginInfoResponse(name=plugin_name, vendor_version=version))
    
    @patch.object(ControllerServicer, "_ControllerServicer__get_identity_config")
    def test_identity_plugin_get_info_fails_when_attributes_from_config_are_missing(self, identity_config):
        request = Mock()
        context = Mock()
         
        identity_config.side_effect = ["name", Exception(), Exception(), "1.0.0"]
 
        res = self.servicer.GetPluginInfo(request, context)
        context.set_code.assert_called_once_with(grpc.StatusCode.INTERNAL)
        self.assertEqual(res, csi_pb2.GetPluginInfoResponse())
         
        res = self.servicer.GetPluginInfo(request, context)
        self.assertEqual(res, csi_pb2.GetPluginInfoResponse())
        context.set_code.assert_called_with(grpc.StatusCode.INTERNAL)
         
    @patch.object(ControllerServicer, "_ControllerServicer__get_identity_config")
    def test_identity_plugin_get_info_fails_when_name_or_value_are_empty(self, identity_config):
        request = Mock()
        context = Mock()
        
        identity_config.side_effect = ["", "1.0.0", "name", ""]

        res = self.servicer.GetPluginInfo(request, context)
        context.set_code.assert_called_once_with(grpc.StatusCode.INTERNAL)
        self.assertEqual(res, csi_pb2.GetPluginInfoResponse())
        
        res = self.servicer.GetPluginInfo(request, context)
        self.assertEqual(res, csi_pb2.GetPluginInfoResponse())
        self.assertEqual(context.set_code.call_args_list, [call(grpc.StatusCode.INTERNAL), call(grpc.StatusCode.INTERNAL)])

        
    def test_identity_get_plugin_capabilities(self):
        request = Mock()
        context = Mock()
        self.servicer.GetPluginCapabilities(request, context)
 
    def test_identity_probe(self):
        request = Mock()
        context = Mock()
        self.servicer.Probe(request, context)
