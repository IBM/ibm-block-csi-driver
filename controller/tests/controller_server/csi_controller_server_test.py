import unittest
# from unittest import mock as umock
import grpc

from mock import patch, Mock, call
from controller.tests import utils

from controller.csi_general import csi_pb2, csi_pb2_grpc
from controller.array_action.array_mediator_xiv import XIVArrayMediator
from controller.controller_server.csi_controller_server import ControllerServicer
from controller.controller_server.test_settings import vol_name
from controller.array_action.errors import VolumeNotFoundError, FailedToFindStorageSystemType, IllegalObjectName, \
    VolumeAlreadyExists, PoolDoesNotExist, PoolDoesNotMatchCapabilities, StorageClassCapabilityNotSupported
from controller.controller_server.config import PARAMETERS_PREFIX


class TestControllerServerCreateVolume(unittest.TestCase):

    @patch("controller.array_action.array_mediator_xiv.XIVArrayMediator._connect")
    def setUp(self, connect):
        self.fqdn = "fqdn"
        self.mediator = XIVArrayMediator("user", "password", self.fqdn)
        self.mediator.client = Mock()

        self.mediator.get_volume = Mock()
        self.mediator.get_volume.side_effect = [VolumeNotFoundError("vol")]

        self.servicer = ControllerServicer(self.fqdn)

        self.request = Mock()
        caps = Mock()
        caps.mount = Mock()
        caps.mount.fs_type = "ext4"
        access_types = csi_pb2.VolumeCapability.AccessMode
        caps.access_mode.mode = access_types.SINGLE_NODE_WRITER

        self.request.volume_capabilities = [caps]

        self.pool = 'pool1'
        self.request.secrets = {"username": "user", "password": "pass", "management_address": "mg"}
        self.request.parameters = {"capacity": "pool={0}".format(self.pool), "capabilities": ""}
        self.capacity_bytes = 10
        self.request.capacity_range.required_bytes = self.capacity_bytes
        self.request.name = vol_name

    @patch("controller.array_action.array_connection_manager.ArrayConnectionManager.__enter__")
    @patch("controller.array_action.array_connection_manager.ArrayConnectionManager.__exit__")
    def test_create_volume_with_empty_name(self, a_enter, a_exit):
        a_enter.return_value = self.mediator
        self.request.name = ""
        context = utils.FakeContext()
        res = self.servicer.CreateVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.INVALID_ARGUMENT)
        self.assertTrue("name" in context.details)
        self.assertEqual(res, csi_pb2.CreateVolumeResponse())

    @patch("controller.array_action.array_connection_manager.ArrayConnectionManager.detect_array_type")
    @patch("controller.array_action.array_connection_manager.ArrayConnectionManager.__enter__")
    @patch("controller.array_action.array_connection_manager.ArrayConnectionManager.__exit__")
    def test_create_volume_succeeds(self, a_exit, a_enter, array_type):
        a_enter.return_value = self.mediator
        context = utils.FakeContext()

        self.mediator.create_volume = Mock()
        self.mediator.create_volume.return_value = utils.get_mock_mediator_response_volume(10, "vol", "wwn", "xiv")
        array_type.return_value = "a9k"
        res = self.servicer.CreateVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.OK)
        self.mediator.get_volume.assert_called_once_with(vol_name)
        self.mediator.create_volume.assert_called_once_with(vol_name, 10, '', 'pool1')

    @patch("controller.array_action.array_connection_manager.ArrayConnectionManager.detect_array_type")
    @patch("controller.array_action.array_connection_manager.ArrayConnectionManager.__enter__")
    @patch("controller.array_action.array_connection_manager.ArrayConnectionManager.__exit__")
    def test_create_volume_with_wrong_secrets(self, a_enter, a_exit, array_type):
        a_enter.return_value = self.mediator
        context = utils.FakeContext()

        self.request.secrets = {"password": "pass", "management_address": "mg"}
        res = self.servicer.CreateVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.INVALID_ARGUMENT, "username is missing in secrets")
        self.assertTrue("secret" in context.details)

        self.request.secrets = {"username": "user", "management_address": "mg"}
        res = self.servicer.CreateVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.INVALID_ARGUMENT, "password is missing in secrets")
        self.assertTrue("secret" in context.details)

        self.request.secrets = {"username": "user", "password": "pass"}
        res = self.servicer.CreateVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.INVALID_ARGUMENT, "mgmt address is missing in secrets")
        self.assertTrue("secret" in context.details)

        self.request.secrets = []

    @patch("controller.array_action.array_connection_manager.ArrayConnectionManager.detect_array_type")
    @patch("controller.array_action.array_connection_manager.ArrayConnectionManager.__enter__")
    @patch("controller.array_action.array_connection_manager.ArrayConnectionManager.__exit__")
    def test_create_volume_with_wrong_parameters(self, a_enter, a_exit, array_type):
        a_enter.return_value = self.mediator
        context = utils.FakeContext()

        self.request.parameters = {"capacity": "pool=pool1"}
        res = self.servicer.CreateVolume(self.request, context)
        self.assertNotEqual(context.code, grpc.StatusCode.INVALID_ARGUMENT)

        self.request.parameters = {"capabilities": ""}
        res = self.servicer.CreateVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.INVALID_ARGUMENT, "capacity is missing in secrets")
        self.assertTrue("parameter" in context.details)

    @patch("controller.array_action.array_connection_manager.ArrayConnectionManager.__enter__")
    @patch("controller.array_action.array_connection_manager.ArrayConnectionManager.__exit__")
    def test_create_volume_with_wrong_volume_capabilities(self, a_enter, a_exit):
        a_enter.return_value = self.mediator
        context = utils.FakeContext()

        caps = Mock()
        caps.mount = Mock()
        caps.mount.fs_type = "ext42"
        access_types = csi_pb2.VolumeCapability.AccessMode
        caps.access_mode.mode = access_types.SINGLE_NODE_WRITER

        self.request.volume_capabilities = [caps]

        res = self.servicer.CreateVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.INVALID_ARGUMENT, "wrong fs_type")
        self.assertTrue("fs_type" in context.details)

        caps.mount.fs_type = "ext4"
        caps.access_mode.mode = access_types.MULTI_NODE_SINGLE_WRITER
        self.request.volume_capabilities = [caps]

        res = self.servicer.CreateVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.INVALID_ARGUMENT, "wrong access_mode")
        self.assertTrue("access mode" in context.details)

    @patch("controller.array_action.array_connection_manager.ArrayConnectionManager.detect_array_type")
    @patch("controller.array_action.array_connection_manager.ArrayConnectionManager.__enter__")
    @patch("controller.array_action.array_connection_manager.ArrayConnectionManager.__exit__")
    def test_create_volume_with_array_connection_exception(self, a_enter, a_exit, array_type):
        a_enter.side_effect = [Exception("error")]
        context = utils.FakeContext()
        res = self.servicer.CreateVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.INTERNAL, "connection error occured in array_connection")
        self.assertTrue("error" in context.details)

    @patch("controller.array_action.array_connection_manager.ArrayConnectionManager.detect_array_type")
    @patch("controller.array_action.array_connection_manager.ArrayConnectionManager.__enter__")
    @patch("controller.array_action.array_connection_manager.ArrayConnectionManager.__exit__")
    def test_create_volume_with_get_array_type_exception(self, a_enter, a_exit, array_type):
        a_enter.return_value = self.mediator
        context = utils.FakeContext()
        array_type.side_effect = [FailedToFindStorageSystemType("endpoint")]
        res = self.servicer.CreateVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.INTERNAL, "failed to find storage system")
        msg = FailedToFindStorageSystemType("endpoint").message
        self.assertTrue(msg in context.details)

    @patch("controller.array_action.array_connection_manager.ArrayConnectionManager.detect_array_type")
    @patch("controller.array_action.array_mediator_xiv.XIVArrayMediator.get_volume")
    @patch("controller.array_action.array_connection_manager.ArrayConnectionManager.__enter__")
    def test_create_volume_get_volume_exception(self, a_enter, get_volume, array_type):
        a_enter.return_value = self.mediator
        self.mediator.get_volume.side_effect = [Exception("error")]
        context = utils.FakeContext()
        res = self.servicer.CreateVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.INTERNAL)
        self.assertTrue("error" in context.details)
        self.mediator.get_volume.assert_called_once_with(vol_name)

    @patch("controller.array_action.array_connection_manager.ArrayConnectionManager.detect_array_type")
    @patch("controller.array_action.array_mediator_xiv.XIVArrayMediator.get_volume")
    @patch("controller.array_action.array_connection_manager.ArrayConnectionManager.__enter__")
    def test_create_volume_with_get_volume_illegal_object_name_exception(self, a_enter, get_volume, array_type):
        a_enter.return_value = self.mediator
        self.mediator.get_volume.side_effect = [IllegalObjectName("vol")]
        context = utils.FakeContext()
        res = self.servicer.CreateVolume(self.request, context)
        msg = IllegalObjectName("vol").message

        self.assertEqual(context.code, grpc.StatusCode.INVALID_ARGUMENT)
        self.assertTrue(msg in context.details)
        self.mediator.get_volume.assert_called_once_with(vol_name)

    @patch("controller.array_action.array_connection_manager.ArrayConnectionManager.detect_array_type")
    @patch("controller.array_action.array_mediator_xiv.XIVArrayMediator.create_volume")
    @patch("controller.array_action.array_connection_manager.ArrayConnectionManager.__enter__")
    def create_volume_returns_error(self, a_enter, create_volume, array_type, return_code, err):
        a_enter.return_value = self.mediator
        create_volume.side_effect = [err]

        context = utils.FakeContext()
        res = self.servicer.CreateVolume(self.request, context)
        msg = err.message

        self.assertEqual(context.code, return_code)
        self.assertTrue(msg in context.details)
        self.mediator.get_volume.assert_called_once_with(vol_name)
        self.mediator.create_volume.assert_called_once_with(vol_name, self.capacity_bytes, "", self.pool)

    @patch("controller.array_action.array_connection_manager.ArrayConnectionManager.detect_array_type")
    @patch("controller.array_action.array_connection_manager.ArrayConnectionManager.__enter__")
    @patch("controller.array_action.array_connection_manager.ArrayConnectionManager.__exit__")
    def test_create_volume_cuts_name_if_its_too_long(self, a_exit, a_enter, array_type):
        a_enter.return_value = self.mediator
        context = utils.FakeContext()

        self.request.name = "a" * 128
        self.mediator.create_volume = Mock()
        self.mediator.create_volume.return_value = utils.get_mock_mediator_response_volume(10, "vol", "wwn", "xiv")
        array_type.return_value = "a9k"
        res = self.servicer.CreateVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.OK)
        self.mediator.get_volume.assert_called_once_with("a" * self.mediator.MAX_VOL_NAME_LENGTH)

    def test_create_volume_with_illegal_object_name_exception(self):
        self.create_volume_returns_error(return_code=grpc.StatusCode.INVALID_ARGUMENT, err=IllegalObjectName("vol"))

    def test_create_volume_with_create_volume_with_volume_exsits_exception(self):
        self.create_volume_returns_error(return_code=grpc.StatusCode.ALREADY_EXISTS, err=VolumeAlreadyExists("vol", "endpoint"))

    def test_create_volume_with_create_volume_with_pool_does_not_exist_exception(self):
        self.create_volume_returns_error(return_code=grpc.StatusCode.INVALID_ARGUMENT, err=PoolDoesNotExist("pool1","endpoint"))

    def test_create_volume_with_create_volume_with_pool_does_not_match_capabilities_exception(self):
        self.create_volume_returns_error(return_code=grpc.StatusCode.INVALID_ARGUMENT,
                                         err=PoolDoesNotMatchCapabilities("pool1", "", "endpoint"))

    def test_create_volume_with_create_volume_with_capability_not_supported_exception(self):
        self.create_volume_returns_error(return_code=grpc.StatusCode.INVALID_ARGUMENT,
                                         err=StorageClassCapabilityNotSupported(["cap"]))

    def test_create_volume_with_create_volume_with_other_exception(self):
        self.create_volume_returns_error(return_code=grpc.StatusCode.INTERNAL,
                                         err=Exception("error"))

    @patch("controller.array_action.array_connection_manager.ArrayConnectionManager.detect_array_type")
    @patch("controller.array_action.array_connection_manager.ArrayConnectionManager.__enter__")
    @patch("controller.array_action.array_connection_manager.ArrayConnectionManager.__exit__")
    def test_create_volume_with_name_prefix(self, a_exit, a_enter, array_type):
        a_enter.return_value = self.mediator
        context = utils.FakeContext()

        self.request.name = "some_name"
        self.request.parameters[PARAMETERS_PREFIX] =  "prefix"
        self.mediator.create_volume = Mock()
        self.mediator.create_volume.return_value = utils.get_mock_mediator_response_volume(10, "vol", "wwn", "xiv")
        array_type.return_value = "a9k"
        res = self.servicer.CreateVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.OK)
        self.mediator.create_volume.assert_called_once_with("prefix_some_name", 10 , "", "pool1")


class TestControllerServerDeleteVolume(unittest.TestCase):

    @patch("controller.array_action.array_mediator_xiv.XIVArrayMediator._connect")
    def setUp(self, connect):
        self.fqdn = "fqdn"
        self.mediator = XIVArrayMediator("user", "password", self.fqdn)
        self.mediator.client = Mock()

        self.mediator.get_volume = Mock()

        self.servicer = ControllerServicer(self.fqdn)

        self.request = Mock()

        self.pool = 'pool1'
        self.request.secrets = {"username": "user", "password": "pass", "management_address": "mg"}

        self.request.volume_id = "xiv:vol-id"

    @patch("controller.array_action.array_connection_manager.ArrayConnectionManager.__enter__")
    @patch("controller.array_action.array_connection_manager.ArrayConnectionManager.__exit__")
    def test_delete_volume_with_wrong_secrets(self, a_enter, a_exit):
        a_enter.return_value = self.mediator
        context = utils.FakeContext()

        self.request.secrets = {"password": "pass", "management_address": "mg"}
        res = self.servicer.DeleteVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.INVALID_ARGUMENT, "username is missing in secrets")
        self.assertTrue("secret" in context.details)

        self.request.secrets = {"username": "user", "management_address": "mg"}
        res = self.servicer.DeleteVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.INVALID_ARGUMENT, "password is missing in secrets")
        self.assertTrue("secret" in context.details)

        self.request.secrets = {"username": "user", "password": "pass"}
        res = self.servicer.DeleteVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.INVALID_ARGUMENT, "mgmt address is missing in secrets")
        self.assertTrue("secret" in context.details)

    @patch("controller.array_action.array_connection_manager.ArrayConnectionManager.__enter__")
    @patch("controller.array_action.array_connection_manager.ArrayConnectionManager.__exit__")
    def test_delete_volume_invalid_volume_id(self, a_enter, a_exit):
        a_enter.return_value = self.mediator
        context = utils.FakeContext()
        self.request.volume_id = "wrong_id"
        res = self.servicer.DeleteVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.INVALID_ARGUMENT, "volume id is not set correctly")
        self.assertTrue("volume id" in context.details)

    @patch("controller.array_action.array_connection_manager.ArrayConnectionManager.__enter__")
    @patch("controller.array_action.array_connection_manager.ArrayConnectionManager.__exit__")
    def test_delete_volume_with_array_connection_exception(self, a_enter, a_exit):
        a_enter.side_effect = [Exception("a_enter error")]
        context = utils.FakeContext()
        res = self.servicer.DeleteVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.INTERNAL, "array connection internal error")
        self.assertTrue("a_enter error" in context.details)

    @patch("controller.array_action.array_mediator_xiv.XIVArrayMediator.delete_volume")
    @patch("controller.array_action.array_connection_manager.ArrayConnectionManager.__enter__")
    def delete_volume_returns_error(self, a_enter, delete_volume, error, return_code):
        a_enter.return_value = self.mediator
        delete_volume.side_effect = [error]
        context = utils.FakeContext()
        res = self.servicer.DeleteVolume(self.request, context)
        self.assertEqual(context.code, return_code)
        if return_code != grpc.StatusCode.OK:
            msg = error.message
            self.assertTrue(msg in context.details, "msg : {0} is not in : {1}".format(msg, context.details))

    def test_delete_volume_with_volume_not_found_error(self, ):
        self.delete_volume_returns_error(error=VolumeNotFoundError("vol"), return_code=grpc.StatusCode.OK)

    def test_delete_volume_with_delete_volume_other_exception(self):
        self.delete_volume_returns_error(error=Exception("error"), return_code=grpc.StatusCode.INTERNAL)

    @patch("controller.array_action.array_mediator_xiv.XIVArrayMediator.delete_volume")
    @patch("controller.array_action.array_connection_manager.ArrayConnectionManager.__enter__")
    def test_delete_volume_succeeds(self, a_enter, delete_volume):
        a_enter.return_value = self.mediator
        context = utils.FakeContext()
        res = self.servicer.DeleteVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.OK)


class TestControllerServerGetCapabilities(unittest.TestCase):

    def setUp(self):
        self.fqdn = "fqdn"
        self.servicer = ControllerServicer(self.fqdn)

    def test_controller_get_capabilities(self):
        request = Mock()
        context = Mock()
        self.servicer.ControllerGetCapabilities(request, context)


class TestIdentityServer(unittest.TestCase):

    def setUp(self):
        self.fqdn = "fqdn"
        self.servicer = ControllerServicer(self.fqdn)

    @patch.object(ControllerServicer, "_ControllerServicer__get_identity_config")
    def test_identity_plugin_get_info_succeeds(self, identity_config):
        plugin_name = "plugin-name"
        version = "1.0.0"
        identity_config.side_effect = [plugin_name, version]
        request = Mock()
        context = Mock()
        request.volume_capabilities = []
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
        self.assertEqual(context.set_code.call_args_list,
                         [call(grpc.StatusCode.INTERNAL), call(grpc.StatusCode.INTERNAL)])

    def test_identity_get_plugin_capabilities(self):
        request = Mock()
        context = Mock()
        self.servicer.GetPluginCapabilities(request, context)

    def test_identity_probe(self):
        request = Mock()
        context = Mock()
        self.servicer.Probe(request, context)
