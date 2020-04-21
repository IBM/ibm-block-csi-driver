import unittest

# from unittest import mock as umock
import grpc
from mock import patch, Mock, call

import controller.array_action.errors as array_errors
import controller.controller_server.errors as controller_errors
from controller.array_action.array_mediator_xiv import XIVArrayMediator
from controller.controller_server.config import PARAMETERS_PREFIX
from controller.controller_server.csi_controller_server import ControllerServicer
from controller.controller_server.test_settings import vol_name
from controller.csi_general import csi_pb2
from controller.tests import utils


class TestControllerServerCreateVolume(unittest.TestCase):

    @patch("controller.array_action.array_mediator_xiv.XIVArrayMediator._connect")
    def setUp(self, connect):
        self.fqdn = "fqdn"
        self.mediator = XIVArrayMediator("user", "password", self.fqdn)
        self.mediator.client = Mock()

        self.mediator.get_volume = Mock()
        self.mediator.get_volume.side_effect = [array_errors.VolumeNotFoundError("vol")]

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
        self.request.parameters = {"pool": self.pool}
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
        self.mediator.get_volume.assert_called_once_with(vol_name, volume_context={'pool': 'pool1'}, volume_prefix="")
        self.mediator.create_volume.assert_called_once_with(vol_name, 10, {}, 'pool1', "")

    @patch("controller.array_action.array_connection_manager.ArrayConnectionManager.detect_array_type")
    @patch("controller.array_action.array_connection_manager.ArrayConnectionManager.__enter__")
    @patch("controller.array_action.array_connection_manager.ArrayConnectionManager.__exit__")
    def test_create_volume_with_wrong_secrets(self, a_enter, a_exit, array_type):
        a_enter.return_value = self.mediator
        context = utils.FakeContext()

        self.request.secrets = {"password": "pass", "management_address": "mg"}
        self.servicer.CreateVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.INVALID_ARGUMENT, "username is missing in secrets")
        self.assertTrue("secret" in context.details)

        self.request.secrets = {"username": "user", "management_address": "mg"}
        self.servicer.CreateVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.INVALID_ARGUMENT, "password is missing in secrets")
        self.assertTrue("secret" in context.details)

        self.request.secrets = {"username": "user", "password": "pass"}
        self.servicer.CreateVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.INVALID_ARGUMENT, "mgmt address is missing in secrets")
        self.assertTrue("secret" in context.details)

        self.request.secrets = []

    @patch("controller.array_action.array_connection_manager.ArrayConnectionManager.detect_array_type")
    @patch("controller.array_action.array_connection_manager.ArrayConnectionManager.__enter__")
    @patch("controller.array_action.array_connection_manager.ArrayConnectionManager.__exit__")
    def test_create_volume_with_wrong_parameters(self, a_enter, a_exit, array_type):
        a_enter.return_value = self.mediator
        context = utils.FakeContext()

        self.request.parameters = {"pool": "pool1"}
        res = self.servicer.CreateVolume(self.request, context)
        self.assertNotEqual(context.code, grpc.StatusCode.INVALID_ARGUMENT)

        self.request.parameters = {"capabilities": ""}
        res = self.servicer.CreateVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.INVALID_ARGUMENT, "capacity is missing in secrets")
        self.assertEqual(context.code, grpc.StatusCode.INVALID_ARGUMENT, "pool parameter is missing")
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
        array_type.side_effect = [array_errors.FailedToFindStorageSystemType("endpoint")]
        res = self.servicer.CreateVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.INTERNAL, "failed to find storage system")
        msg = array_errors.FailedToFindStorageSystemType("endpoint").message
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
        self.mediator.get_volume.assert_called_once_with(vol_name, volume_context={'pool': 'pool1'}, volume_prefix="")

    @patch("controller.array_action.array_connection_manager.ArrayConnectionManager.detect_array_type")
    @patch("controller.array_action.array_mediator_xiv.XIVArrayMediator.get_volume")
    @patch("controller.array_action.array_connection_manager.ArrayConnectionManager.__enter__")
    def test_create_volume_with_get_volume_illegal_object_name_exception(self, a_enter, get_volume, array_type):
        a_enter.return_value = self.mediator
        self.mediator.get_volume.side_effect = [array_errors.IllegalObjectName("vol")]
        context = utils.FakeContext()
        res = self.servicer.CreateVolume(self.request, context)
        msg = array_errors.IllegalObjectName("vol").message

        self.assertEqual(context.code, grpc.StatusCode.INVALID_ARGUMENT)
        self.assertTrue(msg in context.details)
        self.mediator.get_volume.assert_called_once_with(vol_name, volume_context={'pool': 'pool1'}, volume_prefix="")

    @patch("controller.array_action.array_connection_manager.ArrayConnectionManager.detect_array_type")
    @patch("controller.array_action.array_mediator_xiv.XIVArrayMediator.create_volume")
    @patch("controller.array_action.array_connection_manager.ArrayConnectionManager.__enter__")
    def create_volume_returns_error(self, a_enter, create_volume, array_type, return_code, err):
        a_enter.return_value = self.mediator
        create_volume.side_effect = [err]

        context = utils.FakeContext()
        res = self.servicer.CreateVolume(self.request, context)
        msg = str(err)

        self.assertEqual(context.code, return_code)
        self.assertTrue(msg in context.details)
        self.mediator.get_volume.assert_called_once_with(vol_name, volume_context={'pool': 'pool1'}, volume_prefix="")
        self.mediator.create_volume.assert_called_once_with(vol_name, self.capacity_bytes, {}, self.pool, "")

    def test_create_volume_with_illegal_object_name_exception(self):
        self.create_volume_returns_error(return_code=grpc.StatusCode.INVALID_ARGUMENT,
                                         err=array_errors.IllegalObjectName("vol"))

    def test_create_volume_with_create_volume_with_volume_exsits_exception(self):
        self.create_volume_returns_error(return_code=grpc.StatusCode.ALREADY_EXISTS,
                                         err=array_errors.VolumeAlreadyExists("vol", "endpoint"))

    def test_create_volume_with_create_volume_with_pool_does_not_exist_exception(self):
        self.create_volume_returns_error(return_code=grpc.StatusCode.INVALID_ARGUMENT,
                                         err=array_errors.PoolDoesNotExist("pool1", "endpoint"))

    def test_create_volume_with_create_volume_with_pool_does_not_match_capabilities_exception(self):
        self.create_volume_returns_error(return_code=grpc.StatusCode.INVALID_ARGUMENT,
                                         err=array_errors.PoolDoesNotMatchCapabilities("pool1", "", "endpoint"))

    def test_create_volume_with_create_volume_with_capability_not_supported_exception(self):
        self.create_volume_returns_error(return_code=grpc.StatusCode.INVALID_ARGUMENT,
                                         err=array_errors.StorageClassCapabilityNotSupported(["cap"]))

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
        self.request.parameters[PARAMETERS_PREFIX] = "prefix"
        self.mediator.create_volume = Mock()
        self.mediator.create_volume.return_value = utils.get_mock_mediator_response_volume(10, "vol", "wwn", "xiv")
        array_type.return_value = "a9k"
        res = self.servicer.CreateVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.OK)
        self.mediator.create_volume.assert_called_once_with("prefix_some_name", 10, {}, "pool1", "prefix")

    @patch("controller.array_action.array_connection_manager.ArrayConnectionManager.detect_array_type")
    @patch("controller.array_action.array_connection_manager.ArrayConnectionManager.__enter__")
    @patch("controller.array_action.array_connection_manager.ArrayConnectionManager.__exit__")
    def test_create_volume_with_zero_size(self, a_exit, a_enter, array_type):
        a_enter.return_value = self.mediator
        context = utils.FakeContext()

        self.request.capacity_range.required_bytes = 0
        self.mediator.create_volume = Mock()
        self.mediator.create_volume.return_value = utils.get_mock_mediator_response_volume(10, "vol", "wwn", "xiv")
        array_type.return_value = "a9k"
        res = self.servicer.CreateVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.OK)
        self.mediator.create_volume.assert_called_once_with(self.request.name, 1 * 1024 * 1024 * 1024, {}, "pool1", "")


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
        self.assertEqual(context.code, grpc.StatusCode.OK)

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
            msg = str(error)
            self.assertTrue(msg in context.details, "msg : {0} is not in : {1}".format(msg, context.details))

    def test_delete_volume_with_volume_not_found_error(self, ):
        self.delete_volume_returns_error(error=array_errors.VolumeNotFoundError("vol"), return_code=grpc.StatusCode.OK)

    def test_delete_volume_with_delete_volume_other_exception(self):
        self.delete_volume_returns_error(error=Exception("error"), return_code=grpc.StatusCode.INTERNAL)

    @patch("controller.array_action.array_mediator_xiv.XIVArrayMediator.delete_volume")
    @patch("controller.array_action.array_connection_manager.ArrayConnectionManager.__enter__")
    def test_delete_volume_succeeds(self, a_enter, delete_volume):
        a_enter.return_value = self.mediator
        context = utils.FakeContext()
        res = self.servicer.DeleteVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.OK)


class TestControllerServerPublishVolume(unittest.TestCase):

    @patch("controller.array_action.array_mediator_xiv.XIVArrayMediator._connect")
    def setUp(self, connect):
        self.fqdn = "fqdn"
        self.hostname = "hostname"
        self.mediator = XIVArrayMediator("user", "password", self.fqdn)
        self.mediator.client = Mock()

        self.mediator.get_host_by_host_identifiers = Mock()
        self.mediator.get_host_by_host_identifiers.return_value = self.hostname, ["iscsi"]

        self.mediator.get_volume_mappings = Mock()
        self.mediator.get_volume_mappings.return_value = {}

        self.mediator.map_volume = Mock()
        self.mediator.map_volume.return_value = 1

        self.mediator.get_iscsi_targets_by_iqn = Mock()
        self.mediator.get_iscsi_targets_by_iqn.return_value = {"iqn1": ["1.1.1.1", "2.2.2.2"], "iqn2": ["[::1]"]}

        self.servicer = ControllerServicer(self.fqdn)

        self.request = Mock()
        arr_type = XIVArrayMediator.array_type
        self.request.volume_id = "{}:wwn1".format(arr_type)
        self.request.node_id = "hostname;iqn.1994-05.com.redhat:686358c930fe;500143802426baf4"
        self.request.readonly = False
        self.request.readonly = False
        self.request.secrets = {"username": "user", "password": "pass", "management_address": "mg"}
        self.request.volume_context = {}

        caps = Mock()
        caps.mount = Mock()
        caps.mount.fs_type = "ext4"
        access_types = csi_pb2.VolumeCapability.AccessMode
        caps.access_mode.mode = access_types.SINGLE_NODE_WRITER
        self.request.volume_capability = caps

    @patch("controller.array_action.array_connection_manager.ArrayConnectionManager.__enter__")
    def test_publish_volume_success(self, enter):
        enter.return_value = self.mediator

        context = utils.FakeContext()
        self.servicer.ControllerPublishVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.OK)

    @patch("controller.controller_server.utils.validate_publish_volume_request")
    def test_publish_volume_validateion_exception(self, publish_validation):
        publish_validation.side_effect = [controller_errors.ValidationException("msg")]
        context = utils.FakeContext()
        self.servicer.ControllerPublishVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.INVALID_ARGUMENT)
        self.assertTrue("msg" in context.details)

    def test_publish_volume_wrong_volume_id(self):
        self.request.volume_id = "some-wrong-id-format"

        context = utils.FakeContext()
        self.servicer.ControllerPublishVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.NOT_FOUND)

    def test_publish_volume_wrong_node_id(self):
        self.request.node_id = "some-wrong-id-format"

        context = utils.FakeContext()
        self.servicer.ControllerPublishVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.NOT_FOUND)

    @patch("controller.array_action.array_connection_manager.ArrayConnectionManager.__enter__")
    def test_publish_volume_get_host_by_host_identifiers_exception(self, enter):
        context = utils.FakeContext()

        self.mediator.get_host_by_host_identifiers = Mock()
        self.mediator.get_host_by_host_identifiers.side_effect = [array_errors.MultipleHostsFoundError("", "")]
        enter.return_value = self.mediator

        self.servicer.ControllerPublishVolume(self.request, context)
        self.assertTrue("Multiple hosts" in context.details)
        self.assertEqual(context.code, grpc.StatusCode.INTERNAL)

        self.mediator.get_host_by_host_identifiers.side_effect = [array_errors.HostNotFoundError("")]
        enter.return_value = self.mediator

        self.servicer.ControllerPublishVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.NOT_FOUND)

    @patch("controller.array_action.array_connection_manager.ArrayConnectionManager.__enter__")
    def test_publish_volume_get_volume_mappings_one_map_for_existing_host(self, enter):
        context = utils.FakeContext()
        self.mediator.get_volume_mappings = Mock()
        self.mediator.get_volume_mappings.return_value = {self.hostname: 2}
        enter.return_value = self.mediator

        res = self.servicer.ControllerPublishVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.OK)

        self.assertEqual(res.publish_context["PUBLISH_CONTEXT_LUN"], '2')
        self.assertEqual(res.publish_context["PUBLISH_CONTEXT_CONNECTIVITY"], "iscsi")

    @patch("controller.array_action.array_connection_manager.ArrayConnectionManager.__enter__")
    def test_publish_volume_with_connectivity_type_fc(self, enter):
        context = utils.FakeContext()
        self.mediator.get_host_by_host_identifiers.return_value = self.hostname, ["iscsi", "fc"]
        self.mediator.get_array_fc_wwns = Mock()
        self.mediator.get_array_fc_wwns.return_value = ["500143802426baf4"]
        enter.return_value = self.mediator

        res = self.servicer.ControllerPublishVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.OK)

        self.assertEqual(res.publish_context["PUBLISH_CONTEXT_LUN"], '1')
        self.assertEqual(res.publish_context["PUBLISH_CONTEXT_CONNECTIVITY"], "fc")
        self.assertEqual(res.publish_context["PUBLISH_CONTEXT_ARRAY_FC_INITIATORS"], "500143802426baf4")

    @patch("controller.array_action.array_connection_manager.ArrayConnectionManager.__enter__")
    def test_publish_volume_with_connectivity_type_iscsi(self, enter):
        context = utils.FakeContext()
        self.mediator.get_host_by_host_identifiers.return_value = self.hostname, ["iscsi"]
        self.mediator.get_array_fc_wwns = Mock()
        self.mediator.get_array_fc_wwns.return_value = ["500143802426baf4"]
        enter.return_value = self.mediator

        res = self.servicer.ControllerPublishVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.OK)

        self.assertEqual(res.publish_context["PUBLISH_CONTEXT_LUN"], '1')
        self.assertEqual(res.publish_context["PUBLISH_CONTEXT_CONNECTIVITY"],
                         "iscsi")
        self.assertEqual(res.publish_context["PUBLISH_CONTEXT_ARRAY_IQN"],
                         "iqn1,iqn2")
        self.assertEqual(res.publish_context["iqn1"],
                         "1.1.1.1,2.2.2.2")
        self.assertEqual(res.publish_context["iqn2"],
                         "[::1]")

    @patch("controller.array_action.array_connection_manager.ArrayConnectionManager.__enter__")
    def test_publish_volume_with_node_id_only_has_iqns(self, enter):
        context = utils.FakeContext()
        self.request.node_id = "hostname;iqn.1994-05.com.redhat:686358c930fe;"
        self.mediator.get_host_by_host_identifiers.return_value = self.hostname, ["iscsi"]
        enter.return_value = self.mediator

        res = self.servicer.ControllerPublishVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.OK)

        self.assertEqual(res.publish_context["PUBLISH_CONTEXT_LUN"], '1')
        self.assertEqual(res.publish_context["PUBLISH_CONTEXT_CONNECTIVITY"],
                         "iscsi")
        self.assertEqual(res.publish_context["PUBLISH_CONTEXT_ARRAY_IQN"],
                         "iqn1,iqn2")
        self.assertEqual(res.publish_context["iqn1"],
                         "1.1.1.1,2.2.2.2")
        self.assertEqual(res.publish_context["iqn2"],
                         "[::1]")

    @patch("controller.array_action.array_connection_manager.ArrayConnectionManager.__enter__")
    def test_publish_volume_with_node_id_only_has_wwns(self, enter):
        context = utils.FakeContext()
        self.request.node_id = "hostname;;500143802426baf4"
        self.mediator.get_host_by_host_identifiers.return_value = self.hostname, ["fc"]
        self.mediator.get_array_fc_wwns = Mock()
        self.mediator.get_array_fc_wwns.return_value = ["500143802426baf4"]
        self.mediator.get_iscsi_targets_by_iqn.return_value = {}
        enter.return_value = self.mediator

        res = self.servicer.ControllerPublishVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.OK)

        self.assertEqual(res.publish_context["PUBLISH_CONTEXT_LUN"], '1')
        self.assertEqual(res.publish_context["PUBLISH_CONTEXT_CONNECTIVITY"],
                         "fc")
        self.assertEqual(
            res.publish_context["PUBLISH_CONTEXT_ARRAY_FC_INITIATORS"],
            "500143802426baf4")

        self.request.node_id = "hostname;;500143802426baf4:500143806626bae2"
        self.mediator.get_host_by_host_identifiers.return_value = self.hostname, ["fc"]
        self.mediator.get_array_fc_wwns = Mock()
        self.mediator.get_array_fc_wwns.return_value = ["500143802426baf4",
                                                        "500143806626bae2"]
        enter.return_value = self.mediator

        res = self.servicer.ControllerPublishVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.OK)

        self.assertEqual(res.publish_context["PUBLISH_CONTEXT_LUN"], '1')
        self.assertEqual(res.publish_context["PUBLISH_CONTEXT_CONNECTIVITY"],
                         "fc")
        self.assertEqual(
            res.publish_context["PUBLISH_CONTEXT_ARRAY_FC_INITIATORS"],
            "500143802426baf4,500143806626bae2")

    @patch("controller.array_action.array_connection_manager.ArrayConnectionManager.__enter__")
    def test_publish_volume_get_volume_mappings_one_map_for_other_host(self, enter):
        context = utils.FakeContext()
        self.mediator.get_volume_mappings = Mock()
        self.mediator.get_volume_mappings.return_value = {"other-hostname": 3}
        enter.return_value = self.mediator

        self.servicer.ControllerPublishVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.FAILED_PRECONDITION)
        self.assertTrue("Volume is already mapped" in context.details)

    @patch("controller.array_action.array_connection_manager.ArrayConnectionManager.__enter__")
    def test_publish_volume_get_volume_mappings_more_then_one_mapping(self, enter):
        context = utils.FakeContext()
        self.mediator.get_volume_mappings = Mock()
        self.mediator.get_volume_mappings.return_value = {"other-hostname": 3, self.hostname: 4}
        enter.return_value = self.mediator

        self.servicer.ControllerPublishVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.FAILED_PRECONDITION)
        self.assertTrue("Volume is already mapped" in context.details)

    @patch("controller.array_action.array_connection_manager.ArrayConnectionManager.__enter__")
    def test_publish_volume_map_volume_excpetions(self, enter):
        context = utils.FakeContext()

        self.mediator.map_volume.side_effect = [array_errors.PermissionDeniedError("msg")]

        enter.return_value = self.mediator
        self.servicer.ControllerPublishVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.PERMISSION_DENIED)

        self.mediator.map_volume.side_effect = [array_errors.VolumeNotFoundError("vol")]
        enter.return_value = self.mediator
        self.servicer.ControllerPublishVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.NOT_FOUND)

        self.mediator.map_volume.side_effect = [array_errors.HostNotFoundError("host")]
        enter.return_value = self.mediator
        self.servicer.ControllerPublishVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.NOT_FOUND)

        self.mediator.map_volume.side_effect = [array_errors.MappingError("", "", "")]
        enter.return_value = self.mediator
        self.servicer.ControllerPublishVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.INTERNAL)

    @patch.object(XIVArrayMediator, "MAX_LUN_NUMBER", 3)
    @patch.object(XIVArrayMediator, "MIN_LUN_NUMBER", 1)
    @patch("controller.array_action.array_connection_manager.ArrayConnectionManager.__enter__")
    def test_publish_volume_map_volume_lun_already_in_use(self, enter):
        context = utils.FakeContext()

        self.mediator.map_volume.side_effect = [array_errors.LunAlreadyInUseError("", ""), 2]
        enter.return_value = self.mediator
        res = self.servicer.ControllerPublishVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.OK)
        self.assertEqual(res.publish_context["PUBLISH_CONTEXT_LUN"], '2')
        self.assertEqual(res.publish_context["PUBLISH_CONTEXT_CONNECTIVITY"], "iscsi")

        self.mediator.map_volume.side_effect = [
            array_errors.LunAlreadyInUseError("", ""), 2]
        self.mediator.get_host_by_host_identifiers = Mock()
        self.mediator.get_host_by_host_identifiers.return_value = self.hostname, ["fc"]
        self.mediator.get_array_fc_wwns = Mock()
        self.mediator.get_array_fc_wwns.return_value = ["500143802426baf4"]
        enter.return_value = self.mediator
        res = self.servicer.ControllerPublishVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.OK)
        self.assertEqual(res.publish_context["PUBLISH_CONTEXT_LUN"], '2')
        self.assertEqual(res.publish_context["PUBLISH_CONTEXT_CONNECTIVITY"],
                         "fc")

        self.mediator.map_volume.side_effect = [array_errors.LunAlreadyInUseError("", ""),
                                                array_errors.LunAlreadyInUseError("", ""), 2]
        enter.return_value = self.mediator
        self.servicer.ControllerPublishVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.OK)
        self.assertEqual(res.publish_context["PUBLISH_CONTEXT_LUN"], '2')
        self.assertEqual(res.publish_context["PUBLISH_CONTEXT_CONNECTIVITY"], "fc")

        self.mediator.map_volume.side_effect = [
                                                   array_errors.LunAlreadyInUseError("", "")] * (
                                                       self.mediator.max_lun_retries + 1)
        enter.return_value = self.mediator
        self.servicer.ControllerPublishVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.RESOURCE_EXHAUSTED)

    @patch("controller.array_action.array_connection_manager.ArrayConnectionManager.__enter__")
    def test_publish_volume_get_iscsi_targets_by_iqn_excpetions(self, enter):
        context = utils.FakeContext()
        self.mediator.get_iscsi_targets_by_iqn.side_effect = [array_errors.NoIscsiTargetsFoundError("some_endpoint")]
        enter.return_value = self.mediator

        self.servicer.ControllerPublishVolume(self.request, context)

        self.assertEqual(context.code, grpc.StatusCode.NOT_FOUND)

    @patch("controller.array_action.array_mediator_abstract.ArrayMediatorAbstract.map_volume_by_initiators")
    @patch("controller.array_action.array_connection_manager.ArrayConnectionManager.__enter__")
    def test_map_volume_by_initiators_exceptions(self, enter, map_volume_by_initiators):
        context = utils.FakeContext()
        map_volume_by_initiators.side_effect = [
            array_errors.UnsupportedConnectivityTypeError("usb")]
        enter.return_value = self.mediator
        self.servicer.ControllerPublishVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.INVALID_ARGUMENT)


class TestControllerServerUnPublishVolume(unittest.TestCase):

    @patch("controller.array_action.array_mediator_xiv.XIVArrayMediator._connect")
    def setUp(self, connect):
        self.fqdn = "fqdn"
        self.hostname = "hostname"
        self.mediator = XIVArrayMediator("user", "password", self.fqdn)
        self.mediator.client = Mock()

        self.mediator.get_host_by_host_identifiers = Mock()
        self.mediator.get_host_by_host_identifiers.return_value = self.hostname, ["iscsi"]

        self.mediator.unmap_volume = Mock()
        self.mediator.unmap_volume.return_value = None

        self.servicer = ControllerServicer(self.fqdn)

        self.request = Mock()
        arr_type = XIVArrayMediator.array_type
        self.request.volume_id = "{}:wwn1".format(arr_type)
        self.request.node_id = "hostname;iqn1;500143802426baf4"
        self.request.secrets = {"username": "user", "password": "pass", "management_address": "mg"}
        self.request.volume_context = {}

    @patch("controller.array_action.array_connection_manager.ArrayConnectionManager.__enter__")
    def test_unpublish_volume_success(self, enter):
        enter.return_value = self.mediator
        context = utils.FakeContext()
        self.servicer.ControllerUnpublishVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.OK)

    @patch("controller.controller_server.utils.validate_unpublish_volume_request")
    def test_unpublish_volume_validation_exception(self, publish_validation):
        publish_validation.side_effect = [controller_errors.ValidationException("msg")]
        context = utils.FakeContext()
        self.servicer.ControllerUnpublishVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.INVALID_ARGUMENT)
        self.assertTrue("msg" in context.details)

    def test_unpublish_volume_wrong_volume_id(self):
        self.request.volume_id = "some-wrong-id-format"

        context = utils.FakeContext()
        self.servicer.ControllerUnpublishVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.INVALID_ARGUMENT)

    def test_unpublish_volume_wrong_node_id(self):
        self.request.node_id = "some-wrong-id-format"

        context = utils.FakeContext()
        self.servicer.ControllerUnpublishVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.NOT_FOUND)

    @patch("controller.array_action.array_connection_manager.ArrayConnectionManager.__enter__")
    def test_unpublish_volume_get_host_by_host_identifiers_exception(self, enter):
        context = utils.FakeContext()

        self.mediator.get_host_by_host_identifiers = Mock()
        self.mediator.get_host_by_host_identifiers.side_effect = [array_errors.MultipleHostsFoundError("", "")]
        enter.return_value = self.mediator

        self.servicer.ControllerUnpublishVolume(self.request, context)
        self.assertTrue("Multiple hosts" in context.details)
        self.assertEqual(context.code, grpc.StatusCode.INTERNAL)

        self.mediator.get_host_by_host_identifiers.side_effect = [array_errors.HostNotFoundError("")]
        enter.return_value = self.mediator

        self.servicer.ControllerUnpublishVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.NOT_FOUND)

    @patch("controller.array_action.array_connection_manager.ArrayConnectionManager.__enter__")
    def test_unpublish_volume_unmap_volume_excpetions(self, enter):
        context = utils.FakeContext()

        self.mediator.unmap_volume.side_effect = [array_errors.PermissionDeniedError("msg")]
        enter.return_value = self.mediator
        self.servicer.ControllerUnpublishVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.PERMISSION_DENIED)

        context = utils.FakeContext()
        self.mediator.unmap_volume.side_effect = [array_errors.VolumeNotFoundError("vol")]
        enter.return_value = self.mediator
        self.servicer.ControllerUnpublishVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.NOT_FOUND)

        context = utils.FakeContext()
        self.mediator.unmap_volume.side_effect = [array_errors.HostNotFoundError("host")]
        enter.return_value = self.mediator
        self.servicer.ControllerUnpublishVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.NOT_FOUND)

        context = utils.FakeContext()
        self.mediator.unmap_volume.side_effect = [array_errors.UnMappingError("", "", "")]
        enter.return_value = self.mediator
        self.servicer.ControllerUnpublishVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.INTERNAL)

        context = utils.FakeContext()
        self.mediator.unmap_volume.side_effect = [array_errors.VolumeAlreadyUnmappedError("")]
        enter.return_value = self.mediator
        self.servicer.ControllerUnpublishVolume(self.request, context)
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
        version = "1.1.0"
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

        identity_config.side_effect = ["name", Exception(), Exception(), "1.1.0"]

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

        identity_config.side_effect = ["", "1.1.0", "name", ""]

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
