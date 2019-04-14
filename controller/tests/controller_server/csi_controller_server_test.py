import unittest
import grpc
from mock import patch, Mock
 
from controller.csi_general import csi_pb2
from controller.array_action.array_mediator_xiv import XIVArrayMediator
from controller.controller_server.csi_controller_server import ControllerServicer
from controller.controller_server.test_settings import vol_name


class TestControllerServer(unittest.TestCase):
    pass

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
        request.name = "some name1"
        context = Mock()
        res = self.servicer.CreateVolume(request, context)
        context.set_code.assert_called_once_with(grpc.StatusCode.OK)
        self.mediator.get_volume.assert_called_once_with(vol_name)

    @patch("controller.array_action.array_mediator_xiv.XIVArrayMediator.get_volume")
    @patch("controller.array_action.array_connection_manager.ArrayConnectionManager.__enter__")
    def test_create_volume_exception(self, a_enter, get_volume):
        a_enter.return_value = self.mediator
        print "blah"
        get_volume.side_effect = [Exception("msg") ]
        request = Mock()
        request.name = "some name12"
        context = Mock()
        res = self.servicer.CreateVolume(request, context)
        print res
        context.set_code.assert_called_once_with(grpc.StatusCode.INTERNAL)
        self.mediator.get_volume.assert_called_once_with(vol_name)
