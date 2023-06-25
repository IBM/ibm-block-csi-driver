import unittest
import grpc
from mock import patch, Mock, call

from csi_general import identity_pb2 as pb2
from controllers.servers.csi.csi_addons_server.identity_controller_servicer import IdentityControllerServicer


class TestIdentityControllerServicer(unittest.TestCase):
    def setUp(self):
        self.servicer = IdentityControllerServicer()
        self.request = Mock()
        self.context = Mock()

    @patch("controllers.common.config.config.identity")
    def test_get_identity_succeeds(self, identity_config):
        plugin_name = "plugin-name"
        version = "1.1.0"
        identity_config.name = plugin_name
        identity_config.version = version
        self.request.volume_capabilities = []
        response = self.servicer.GetIdentity(self.request, self.context)
        self.assertEqual(response, pb2.GetIdentityResponse(name=plugin_name, vendor_version=version))

    @patch("controllers.common.config.config.identity")
    def test_get_identity_fails_when_attributes_from_config_are_missing(self, identity_config):
        identity_config.mock_add_spec(spec=["name"])
        response = self.servicer.GetIdentity(self.request, self.context)
        self.context.set_code.assert_called_once_with(grpc.StatusCode.INTERNAL)
        self.assertEqual(response, pb2.GetIdentityResponse())

        identity_config.mock_add_spec(spec=["version"])
        response = self.servicer.GetIdentity(self.request, self.context)
        self.assertEqual(response, pb2.GetIdentityResponse())
        self.context.set_code.assert_called_with(grpc.StatusCode.INTERNAL)

    @patch("controllers.common.config.config.identity")
    def test_get_identity_fails_when_name_or_version_are_empty(self, identity_config):
        identity_config.name = ""
        identity_config.version = "1.1.0"
        response = self.servicer.GetIdentity(self.request, self.context)
        self.context.set_code.assert_called_once_with(grpc.StatusCode.INTERNAL)
        self.assertEqual(response, pb2.GetIdentityResponse())

        identity_config.name = "name"
        identity_config.version = ""
        response = self.servicer.GetIdentity(self.request, self.context)
        self.assertEqual(response, pb2.GetIdentityResponse())
        self.assertEqual(self.context.set_code.call_args_list,
                         [call(grpc.StatusCode.INTERNAL), call(grpc.StatusCode.INTERNAL)])

    def test_get_capabilities_succeeds(self):
        response = self.servicer.GetCapabilities(self.request, self.context)
        supported_capabilities = 3
        self.assertIn('VolumeReplication', dir(response.capabilities[0]))
        self.assertIn('Service', dir(response.capabilities[1]))
        self.assertEqual(len(response.capabilities), supported_capabilities)

    def test_probe_succeeds(self):
        response = self.servicer.Probe(self.request, self.context)
        self.assertEqual(response, pb2.ProbeResponse())
