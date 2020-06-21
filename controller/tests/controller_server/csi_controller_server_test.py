import unittest

# from unittest import mock as umock
import grpc
import abc
from mock import patch, Mock, MagicMock, call

import controller.array_action.errors as array_errors
import controller.controller_server.errors as controller_errors
from controller.array_action.array_mediator_xiv import XIVArrayMediator
from controller.controller_server.config import PARAMETERS_VOLUME_NAME_PREFIX, PARAMETERS_SNAPSHOT_NAME_PREFIX
from controller.controller_server.csi_controller_server import ControllerServicer
from controller.controller_server.test_settings import vol_name, snap_name, snap_vol_name
from controller.csi_general import csi_pb2
from controller.tests import utils


class AbstractControllerTest(unittest.TestCase):

    @abc.abstractmethod
    def get_create_object_method(self):
        raise NotImplementedError

    @abc.abstractmethod
    def get_create_object_response_method(self):
        raise NotImplementedError

    def _test_create_object_with_empty_name(self, storage_agent):
        storage_agent.return_value = self.storage_agent
        self.request.name = ""
        context = utils.FakeContext()
        res = self.get_create_object_method()(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.INVALID_ARGUMENT)
        self.assertTrue("name" in context.details)
        self.assertEqual(res, self.get_create_object_response_method()())

    def _test_create_object_with_wrong_secrets(self, storage_agent):
        storage_agent.return_value = self.storage_agent
        context = utils.FakeContext()

        self.request.secrets = {"password": "pass", "management_address": "mg"}
        self.get_create_object_method()(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.INVALID_ARGUMENT, "username is missing in secrets")
        self.assertTrue("secret" in context.details)

        self.request.secrets = {"username": "user", "management_address": "mg"}
        self.get_create_object_method()(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.INVALID_ARGUMENT, "password is missing in secrets")
        self.assertTrue("secret" in context.details)

        self.request.secrets = {"username": "user", "password": "pass"}
        self.get_create_object_method()(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.INVALID_ARGUMENT, "mgmt address is missing in secrets")
        self.assertTrue("secret" in context.details)

        self.request.secrets = []

    def _test_create_object_with_array_connection_exception(self, storage_agent):
        storage_agent.side_effect = [Exception("error")]
        context = utils.FakeContext()
        self.get_create_object_method()(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.INTERNAL, "connection error occured in array_connection")
        self.assertTrue("error" in context.details)

    def _test_create_object_with_get_array_type_exception(self, storage_agent, array_type):
        storage_agent.return_value = self.storage_agent
        context = utils.FakeContext()
        array_type.side_effect = [array_errors.FailedToFindStorageSystemType("endpoint")]
        self.get_create_object_method()(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.INTERNAL, "failed to find storage system")
        msg = array_errors.FailedToFindStorageSystemType("endpoint").message
        self.assertTrue(msg in context.details)


class TestControllerServerCreateSnapshot(AbstractControllerTest):

    def get_create_object_method(self):
        return self.servicer.CreateSnapshot

    def get_create_object_response_method(self):
        return csi_pb2.CreateSnapshotResponse

    @patch("controller.array_action.array_mediator_xiv.XIVArrayMediator._connect")
    def setUp(self, connect):
        self.fqdn = "fqdn"
        self.mediator = XIVArrayMediator("user", "password", self.fqdn)
        self.mediator.client = Mock()
        self.mediator.get_snapshot = Mock()
        self.mediator.get_snapshot.return_value = None

        self.storage_agent = MagicMock()
        self.storage_agent.get_mediator.return_value.__enter__.return_value = self.mediator

        self.servicer = ControllerServicer(self.fqdn)

        self.request = Mock()
        self.request.secrets = {"username": "user", "password": "pass", "management_address": "mg"}
        self.request.parameters = {}
        self.capacity_bytes = 10
        self.request.name = snap_name
        self.request.source_volume_id = "A9000:12345678"

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_create_snapshot_with_empty_name(self, a_enter):
        self._test_create_object_with_empty_name(a_enter)

    def _prepare_create_snapshot_mocks(self, storage_agent, array_type):
        storage_agent.return_value = self.storage_agent

        self.mediator.create_snapshot = Mock()
        self.mediator.create_snapshot.return_value = utils.get_mock_mediator_response_snapshot(10, "snap", "wwn",
                                                                                               "snap_vol", "xiv")
        self.mediator.get_volume_name = Mock()
        self.mediator.get_volume_name.return_value = snap_vol_name
        array_type.return_value = "a9k"

    @patch("controller.controller_server.csi_controller_server.detect_array_type")
    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_create_snapshot_succeeds(self, storage_agent, array_type):
        self._prepare_create_snapshot_mocks(storage_agent, array_type)
        context = utils.FakeContext()

        self.servicer.CreateSnapshot(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.OK)
        self.mediator.get_snapshot.assert_called_once_with(snap_name, volume_context={})
        self.mediator.create_snapshot.assert_called_once_with(snap_name, snap_vol_name, {})

    @patch("controller.controller_server.csi_controller_server.detect_array_type")
    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_create_snapshot_belongs_to_wrong_volume(self, storage_agent, array_type):
        storage_agent.return_value = self.storage_agent
        context = utils.FakeContext()
        self.mediator.create_snapshot = Mock()
        self.mediator.get_snapshot.return_value = utils.get_mock_mediator_response_snapshot(10, snap_name, "wwn",
                                                                                            "wrong_volume_name", "xiv")
        self.mediator.get_volume_name = Mock()
        self.mediator.get_volume_name.return_value = snap_vol_name

        array_type.return_value = "a9k"
        self.servicer.CreateSnapshot(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.ALREADY_EXISTS)

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_create_snapshot_with_wrong_secrets(self, storage_agent):
        self._test_create_object_with_wrong_secrets(storage_agent)

    @patch("controller.controller_server.csi_controller_server.detect_array_type")
    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_create_snapshot_with_array_connection_exception(self, storage_agent, array_type):
        self._test_create_object_with_array_connection_exception(storage_agent)

    @patch("controller.controller_server.csi_controller_server.detect_array_type")
    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_create_snapshot_with_get_array_type_exception(self, storage_agent, array_type):
        self._test_create_object_with_get_array_type_exception(storage_agent, array_type)

    @patch("controller.controller_server.csi_controller_server.detect_array_type")
    @patch("controller.array_action.array_mediator_xiv.XIVArrayMediator.get_snapshot")
    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_create_snapshot_get_snapshot_exception(self, storage_agent, get_snapshot, array_type):
        storage_agent.return_value = self.storage_agent
        self.mediator.get_snapshot.side_effect = [Exception("error")]
        context = utils.FakeContext()
        self.servicer.CreateSnapshot(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.INTERNAL)
        self.assertTrue("error" in context.details)
        self.mediator.get_snapshot.assert_called_once_with(snap_name, volume_context={})

    @patch("controller.controller_server.csi_controller_server.detect_array_type")
    @patch("controller.array_action.array_mediator_xiv.XIVArrayMediator.get_snapshot")
    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_create_snapshot_with_get_snapshot_illegal_object_name_exception(self, storage_agent, get_snapshot,
                                                                             array_type):
        storage_agent.return_value = self.storage_agent
        self.mediator.get_snapshot.side_effect = [array_errors.IllegalObjectName("snap")]
        context = utils.FakeContext()
        self.servicer.CreateSnapshot(self.request, context)
        msg = array_errors.IllegalObjectName("snap").message

        self.assertEqual(context.code, grpc.StatusCode.INVALID_ARGUMENT)
        self.assertTrue(msg in context.details)
        self.mediator.get_snapshot.assert_called_once_with(snap_name, volume_context={})

    @patch("controller.controller_server.csi_controller_server.detect_array_type")
    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_create_snapshot_with_get_snapshot_name_too_long_success(self, storage_agent, array_type):
        self._prepare_create_snapshot_mocks(storage_agent, array_type)
        self.mediator.max_snapshot_name_length = 63
        context = utils.FakeContext()
        self.request.name = "a" * 128
        self.servicer.CreateSnapshot(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.OK)

    @patch("controller.controller_server.csi_controller_server.detect_array_type")
    @patch("controller.array_action.array_mediator_xiv.XIVArrayMediator.create_snapshot")
    @patch("controller.controller_server.csi_controller_server.get_agent")
    def create_snapshot_returns_error(self, storage_agent, create_snapshot, array_type, return_code, err):
        storage_agent.return_value = self.storage_agent

        self.mediator.get_volume_name = Mock()
        self.mediator.get_volume_name.return_value = snap_vol_name
        create_snapshot.side_effect = [err]
        context = utils.FakeContext()
        self.servicer.CreateSnapshot(self.request, context)
        msg = str(err)

        self.assertEqual(context.code, return_code)
        self.assertTrue(msg in context.details)
        self.mediator.get_snapshot.assert_called_once_with(snap_name, volume_context={})
        self.mediator.create_snapshot.assert_called_once_with(snap_name, snap_vol_name, {})

    def test_create_snapshot_with_illegal_object_name_exception(self):
        self.create_snapshot_returns_error(return_code=grpc.StatusCode.INVALID_ARGUMENT,
                                           err=array_errors.IllegalObjectName("snap"))

    def test_create_snapshot_with_snapshot_exists_exception(self):
        self.create_snapshot_returns_error(return_code=grpc.StatusCode.ALREADY_EXISTS,
                                           err=array_errors.SnapshotAlreadyExists("snap", "endpoint"))

    def test_create_snapshot_with_same_volume_name_exists_exception(self):
        self.create_snapshot_returns_error(return_code=grpc.StatusCode.INTERNAL,
                                           err=array_errors.SnapshotNameBelongsToVolumeError("snap",
                                                                                             "endpoint"))

    def test_create_snapshot_with_other_exception(self):
        self.create_snapshot_returns_error(return_code=grpc.StatusCode.INTERNAL, err=Exception("error"))

    @patch("controller.controller_server.csi_controller_server.detect_array_type")
    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_create_snapshot_with_name_prefix(self, storage_agent, array_type):
        storage_agent.return_value = self.storage_agent
        context = utils.FakeContext()
        self.mediator.get_volume_name = Mock()
        self.mediator.get_volume_name.return_value = "snap_vol"

        self.request.name = "some_name"
        self.request.parameters[PARAMETERS_SNAPSHOT_NAME_PREFIX] = "prefix"
        self.mediator.create_snapshot = Mock()
        self.mediator.create_snapshot.return_value = utils.get_mock_mediator_response_snapshot(10, "snap", "wwn",
                                                                                               "snap_vol", "xiv")
        array_type.return_value = "a9k"
        self.servicer.CreateSnapshot(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.OK)
        self.mediator.create_snapshot.assert_called_once_with("prefix_some_name", "snap_vol",
                                                              {'snapshot_name_prefix': 'prefix'})


class TestControllerServerDeleteSnapshot(unittest.TestCase):
    @patch("controller.array_action.array_mediator_xiv.XIVArrayMediator._connect", Mock())
    def setUp(self):
        self.fqdn = "fqdn"
        self.mediator = XIVArrayMediator("user", "password", self.fqdn)
        self.mediator.client = Mock()
        self.mediator.get_snapshot = Mock()
        self.mediator.get_snapshot.return_value = None
        self.storage_agent = MagicMock()
        self.storage_agent.get_mediator.return_value.__enter__.return_value = self.mediator
        self.servicer = ControllerServicer(self.fqdn)
        self.request = Mock()
        self.request.secrets = {"username": "user", "password": "pass", "management_address": "mg"}
        self.request.parameters = {}
        self.request.snapshot_id = "A9000:BADC0FFEE0DDF00D00000000DEADBABE"

    @patch("controller.array_action.array_mediator_xiv.XIVArrayMediator.delete_snapshot", Mock())
    @patch("controller.controller_server.csi_controller_server.detect_array_type")
    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_delete_snapshot_succeeds(self, storage_agent, array_type):
        storage_agent.return_value = self.storage_agent
        context = utils.FakeContext()
        array_type.return_value = "a9k"

        self.servicer.DeleteSnapshot(self.request, context)

        self.assertEqual(context.code, grpc.StatusCode.OK)

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_delete_snapshot_with_wrong_secrets(self, storage_agent):
        storage_agent.return_value = self.storage_agent
        context = utils.FakeContext()

        self.request.secrets = {"password": "pass", "management_address": "mg"}
        self.servicer.DeleteSnapshot(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.INVALID_ARGUMENT, "username is missing in secrets")
        self.assertTrue("secret" in context.details)

        self.request.secrets = {"username": "user", "management_address": "mg"}
        self.servicer.DeleteSnapshot(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.INVALID_ARGUMENT, "password is missing in secrets")
        self.assertTrue("secret" in context.details)

        self.request.secrets = {"username": "user", "password": "pass"}
        self.servicer.DeleteSnapshot(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.INVALID_ARGUMENT, "mgmt address is missing in secrets")
        self.assertTrue("secret" in context.details)

    @patch("controller.controller_server.csi_controller_server.detect_array_type")
    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_delete_snapshot_with_array_connection_exception(self, storage_agent, array_type):
        storage_agent.side_effect = [Exception("a_enter error")]
        context = utils.FakeContext()
        array_type.return_value = "a9k"

        self.servicer.DeleteSnapshot(self.request, context)

        self.assertEqual(context.code, grpc.StatusCode.INTERNAL, "array connection internal error")
        self.assertTrue("a_enter error" in context.details)

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_delete_snapshot_invalid_snapshot_id(self, storage_agent):
        storage_agent.return_value = self.storage_agent
        context = utils.FakeContext()
        self.request.snapshot_id = "wrong_id"
        self.servicer.DeleteSnapshot(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.OK)


class TestControllerServerCreateVolume(AbstractControllerTest):

    def get_create_object_method(self):
        return self.servicer.CreateVolume

    def get_create_object_response_method(self):
        return csi_pb2.CreateVolumeResponse

    @patch("controller.array_action.array_mediator_xiv.XIVArrayMediator._connect")
    def setUp(self, connect):
        self.fqdn = "fqdn"
        self.mediator = XIVArrayMediator("user", "password", self.fqdn)
        self.mediator.client = Mock()

        self.mediator.get_volume = Mock()
        self.mediator.get_volume.side_effect = [array_errors.VolumeNotFoundError("vol")]

        self.storage_agent = MagicMock()
        self.storage_agent.get_mediator.return_value.__enter__.return_value = self.mediator

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
        self.request.capacity_range = Mock()
        self.request.capacity_range.required_bytes = self.capacity_bytes
        self.request.name = vol_name
        self.request.volume_content_source = None

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_create_volume_with_empty_name(self, storage_agent):
        self._test_create_object_with_empty_name(storage_agent)

    def _prepare_create_volume_mocks(self, storage_agent, array_type):
        storage_agent.return_value = self.storage_agent

        self.mediator.create_volume = Mock()
        self.mediator.create_volume.return_value = utils.get_mock_mediator_response_volume(10, "vol", "wwn", "xiv")
        array_type.return_value = "a9k"

    @patch("controller.controller_server.csi_controller_server.detect_array_type")
    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_create_volume_succeeds(self, storage_agent, array_type):
        self._prepare_create_volume_mocks(storage_agent, array_type)
        context = utils.FakeContext()

        self.servicer.CreateVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.OK)
        self.mediator.get_volume.assert_called_once_with(vol_name, volume_context={'pool': 'pool1'}, volume_prefix="")
        self.mediator.create_volume.assert_called_once_with(vol_name, 10, {}, 'pool1', "")

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_create_volume_with_wrong_secrets(self, a_enter):
        self._test_create_object_with_wrong_secrets(a_enter)

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_create_volume_with_wrong_parameters(self, storage_agent):
        storage_agent.return_value = self.storage_agent
        context = utils.FakeContext()

        self.request.parameters = {"pool": "pool1"}
        res = self.servicer.CreateVolume(self.request, context)
        self.assertNotEqual(context.code, grpc.StatusCode.INVALID_ARGUMENT)

        self.request.parameters = {"capabilities": ""}
        res = self.servicer.CreateVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.INVALID_ARGUMENT, "capacity is missing in secrets")
        self.assertEqual(context.code, grpc.StatusCode.INVALID_ARGUMENT, "pool parameter is missing")
        self.assertTrue("parameter" in context.details)

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_create_volume_with_wrong_volume_capabilities(self, storage_agent):
        storage_agent.return_value = self.storage_agent
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

    @patch("controller.controller_server.csi_controller_server.detect_array_type")
    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_create_volume_with_array_connection_exception(self, storage_agent, array_type):
        self._test_create_object_with_array_connection_exception(storage_agent)

    @patch("controller.controller_server.csi_controller_server.detect_array_type")
    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_create_volume_with_get_array_type_exception(self, storage_agent, array_type):
        self._test_create_object_with_get_array_type_exception(storage_agent, array_type)

    @patch("controller.controller_server.csi_controller_server.detect_array_type")
    @patch("controller.array_action.array_mediator_xiv.XIVArrayMediator.get_volume")
    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_create_volume_get_volume_exception(self, storage_agent, get_volume, array_type):
        storage_agent.return_value = self.storage_agent
        self.mediator.get_volume.side_effect = [Exception("error")]
        context = utils.FakeContext()
        res = self.servicer.CreateVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.INTERNAL)
        self.assertTrue("error" in context.details)
        self.mediator.get_volume.assert_called_once_with(vol_name, volume_context={'pool': 'pool1'}, volume_prefix="")

    @patch("controller.controller_server.csi_controller_server.detect_array_type")
    @patch("controller.array_action.array_mediator_xiv.XIVArrayMediator.get_volume")
    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_create_volume_with_get_volume_illegal_object_name_exception(self, storage_agent, get_volume, array_type):
        storage_agent.return_value = self.storage_agent
        self.mediator.get_volume.side_effect = [array_errors.IllegalObjectName("vol")]
        context = utils.FakeContext()
        res = self.servicer.CreateVolume(self.request, context)
        msg = array_errors.IllegalObjectName("vol").message

        self.assertEqual(context.code, grpc.StatusCode.INVALID_ARGUMENT)
        self.assertTrue(msg in context.details)
        self.mediator.get_volume.assert_called_once_with(vol_name, volume_context={'pool': 'pool1'}, volume_prefix="")

    @patch("controller.controller_server.csi_controller_server.detect_array_type")
    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_create_volume_with_get_volume_name_too_long_success(self, storage_agent, array_type):
        self._prepare_create_volume_mocks(storage_agent, array_type)
        self.mediator.max_volume_name_length = 63
        context = utils.FakeContext()
        self.request.name = "a" * 128
        self.servicer.CreateVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.OK)

    @patch("controller.controller_server.csi_controller_server.detect_array_type")
    @patch("controller.array_action.array_mediator_xiv.XIVArrayMediator.create_volume")
    @patch("controller.controller_server.csi_controller_server.get_agent")
    def create_volume_returns_error(self, storage_agent, create_volume, array_type, return_code, err):
        storage_agent.return_value = self.storage_agent
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

    @patch("controller.controller_server.csi_controller_server.detect_array_type")
    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_create_volume_with_name_prefix(self, storage_agent, array_type):
        storage_agent.return_value = self.storage_agent
        context = utils.FakeContext()

        self.request.name = "some_name"
        self.request.parameters[PARAMETERS_VOLUME_NAME_PREFIX] = "prefix"
        self.mediator.create_volume = Mock()
        self.mediator.create_volume.return_value = utils.get_mock_mediator_response_volume(10, "vol", "wwn", "xiv")
        array_type.return_value = "a9k"
        res = self.servicer.CreateVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.OK)
        self.mediator.create_volume.assert_called_once_with("prefix_some_name", 10, {}, "pool1", "prefix")

    @patch("controller.controller_server.csi_controller_server.detect_array_type")
    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_create_volume_with_zero_size(self, storage_agent, array_type):
        storage_agent.return_value = self.storage_agent
        context = utils.FakeContext()

        self.request.capacity_range.required_bytes = 0
        self.mediator.create_volume = Mock()
        self.mediator.create_volume.return_value = utils.get_mock_mediator_response_volume(10, "vol", "wwn", "xiv")
        array_type.return_value = "a9k"
        res = self.servicer.CreateVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.OK)
        self.mediator.create_volume.assert_called_once_with(self.request.name, 1 * 1024 * 1024 * 1024, {}, "pool1", "")

    @patch("controller.controller_server.csi_controller_server.detect_array_type")
    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_create_volume_from_snapshot_success(self, storage_agent, array_type):
        storage_agent.return_value = self.storage_agent
        context = utils.FakeContext()
        snapshot_id = "wwn1"
        snap_capacity_bytes = 100
        array_type.return_value = "a9k"
        self.request.volume_content_source = self._get_snapshot_source(snapshot_id)
        self.mediator.create_volume = Mock()
        self.mediator.create_volume.return_value = utils.get_mock_mediator_response_volume(10, vol_name, "wwn2", "a9k")
        self.mediator.get_snapshot_by_id = Mock()
        self.mediator.get_snapshot_by_id.return_value = utils.get_mock_mediator_response_snapshot(snap_capacity_bytes,
                                                                                                  snap_name,
                                                                                                  snapshot_id, vol_name,
                                                                                                  "a9k")
        self.mediator.copy_to_existing_volume_from_snapshot = Mock()
        self.servicer.CreateVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.OK)
        self.mediator.copy_to_existing_volume_from_snapshot.assert_called_once_with(vol_name, snap_name,
                                                                                    snap_capacity_bytes,
                                                                                    self.capacity_bytes,
                                                                                    'pool1')

    @patch("controller.controller_server.csi_controller_server.detect_array_type")
    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_create_volume_from_snapshot_idempotent(self, storage_agent, array_type):
        storage_agent.return_value = self.storage_agent
        context = utils.FakeContext()
        snap_id = "wwn1"
        self.request.volume_content_source = self._get_snapshot_source(snap_id)
        self.mediator.get_volume = Mock()
        self.mediator.get_volume.return_value = utils.get_mock_mediator_response_volume(10, vol_name, "wwn2", "a9k",
                                                                                        copy_src_object_id=snap_id)
        array_type.return_value = "a9k"
        self.servicer.CreateVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.OK)
        self.mediator.copy_to_existing_volume_from_snapshot = Mock()
        self.mediator.copy_to_existing_volume_from_snapshot.assert_not_called()

    @patch("controller.controller_server.csi_controller_server.detect_array_type")
    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_create_volume_from_snapshot_volume_without_source(self, storage_agent, array_type):
        storage_agent.return_value = self.storage_agent
        context = utils.FakeContext()
        snap_id = "wwn1"
        vol_src_id = "wwn3"
        self.request.volume_content_source = self._get_snapshot_source(snap_id)
        self.mediator.get_volume = Mock()
        self.mediator.get_volume.return_value = utils.get_mock_mediator_response_volume(10, "vol", "wwn2", "a9k")
        self.mediator.get_snapshot_by_id = Mock()
        self.mediator.get_snapshot_by_id.return_value = utils.get_mock_mediator_response_snapshot(1000, snap_name,
                                                                                                  "wwn", vol_name,
                                                                                                  "a9k")
        self.mediator.copy_to_existing_volume_from_snapshot = Mock()
        array_type.return_value = "a9k"
        self.servicer.CreateVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.OK)

    @patch("controller.controller_server.csi_controller_server.detect_array_type")
    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_create_volume_from_snapshot_error_other_source(self, storage_agent, array_type):
        storage_agent.return_value = self.storage_agent
        context = utils.FakeContext()
        snap_id = "wwn1"
        vol_src_id = "wwn3"
        self.request.volume_content_source = self._get_snapshot_source(snap_id)
        self.mediator.get_volume = Mock()
        self.mediator.get_volume.return_value = utils.get_mock_mediator_response_volume(10, "vol", "wwn2", "a9k",
                                                                                        copy_src_object_id=vol_src_id)
        array_type.return_value = "a9k"
        self.servicer.CreateVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.INTERNAL)

    @patch("controller.controller_server.csi_controller_server.detect_array_type")
    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_create_volume_from_snapshot_src_snapshot_not_found(self, storage_agent, array_type):
        array_exception = array_errors.SnapshotNotFoundError("")
        self._test_create_volume_from_snapshot_error(storage_agent, array_type, array_exception,
                                                     grpc.StatusCode.INTERNAL)

    @patch("controller.controller_server.csi_controller_server.detect_array_type")
    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_create_volume_from_snapshot_target_volume_not_found(self, storage_agent, array_type):
        array_exception = array_errors.VolumeNotFoundError("")
        self._test_create_volume_from_snapshot_error(storage_agent, array_type, array_exception,
                                                     grpc.StatusCode.INTERNAL, rollback_called=False)

    @patch("controller.controller_server.csi_controller_server.detect_array_type")
    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_create_volume_from_snapshot_illegal_object_name(self, storage_agent, array_type):
        array_exception = array_errors.IllegalObjectName("")
        self._test_create_volume_from_snapshot_error(storage_agent, array_type, array_exception,
                                                     grpc.StatusCode.INVALID_ARGUMENT)

    @patch("controller.controller_server.csi_controller_server.detect_array_type")
    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_create_volume_from_snapshot_permission_denied(self, storage_agent, array_type):
        array_exception = array_errors.PermissionDeniedError("")
        self._test_create_volume_from_snapshot_error(storage_agent, array_type, array_exception,
                                                     grpc.StatusCode.PERMISSION_DENIED)

    @patch("controller.controller_server.csi_controller_server.detect_array_type")
    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_create_volume_from_snapshot_general_error(self, storage_agent, array_type):
        array_exception = Exception("")
        self._test_create_volume_from_snapshot_error(storage_agent, array_type, array_exception,
                                                     grpc.StatusCode.INTERNAL)

    def _test_create_volume_from_snapshot_error(self, storage_agent, array_type, array_exception, return_code,
                                                rollback_called=True):
        storage_agent.return_value = self.storage_agent
        context = utils.FakeContext()
        snap_id = "wwn1"
        vol_id = "wwn2"
        self.request.volume_content_source = self._get_snapshot_source(snap_id)
        self.mediator.get_volume = Mock()
        self.mediator.get_volume.return_value = utils.get_mock_mediator_response_volume(10, "vol", "wwn2", "a9k")
        self.mediator.get_snapshot_by_id = Mock()
        self.mediator.get_snapshot_by_id.return_value = utils.get_mock_mediator_response_snapshot(1000, snap_name,
                                                                                                  vol_id, vol_name,
                                                                                                  "a9k")
        self.mediator.copy_to_existing_volume_from_snapshot = Mock()
        self.mediator.copy_to_existing_volume_from_snapshot.side_effect = [array_exception]
        self.storage_agent.get_mediator.return_value.__exit__.side_effect = [array_exception]
        self.mediator.delete_volume = Mock()
        array_type.return_value = "a9k"
        self.servicer.CreateVolume(self.request, context)
        if rollback_called:
            self.mediator.delete_volume.assert_called_with(vol_id)
        self.assertEqual(context.code, return_code)

    def _get_snapshot_source(self, snapshot_id):
        source = Mock()
        snapshot = Mock()
        source.snapshot = snapshot
        snapshot.snapshot_id = "a9000:{0}".format(snapshot_id)
        is_snapshot_source = True
        source.HasField.return_value = is_snapshot_source
        return source


class TestControllerServerDeleteVolume(unittest.TestCase):

    @patch("controller.array_action.array_mediator_xiv.XIVArrayMediator._connect")
    def setUp(self, connect):
        self.fqdn = "fqdn"
        self.mediator = XIVArrayMediator("user", "password", self.fqdn)
        self.storage_agent = MagicMock()

        self.mediator.get_volume = Mock()
        self.mediator.is_volume_has_snapshots = Mock()
        self.mediator.is_volume_has_snapshots.return_value = False

        self.mediator.client = Mock()

        self.storage_agent = MagicMock()
        self.storage_agent.get_mediator.return_value.__enter__.return_value = self.mediator

        self.servicer = ControllerServicer(self.fqdn)

        self.request = Mock()

        self.pool = 'pool1'
        self.request.secrets = {"username": "user", "password": "pass", "management_address": "mg"}

        self.request.volume_id = "xiv:vol-id"

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_delete_volume_with_wrong_secrets(self, storage_agent):
        storage_agent.return_value = self.storage_agent
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

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_delete_volume_invalid_volume_id(self, storage_agent):
        storage_agent.return_value = self.storage_agent
        context = utils.FakeContext()
        self.request.volume_id = "wrong_id"
        res = self.servicer.DeleteVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.OK)

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_delete_volume_with_array_connection_exception(self, storage_agent):
        storage_agent.side_effect = [Exception("a_enter error")]
        context = utils.FakeContext()
        res = self.servicer.DeleteVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.INTERNAL, "array connection internal error")
        self.assertTrue("a_enter error" in context.details)

    @patch("controller.array_action.array_mediator_xiv.XIVArrayMediator.delete_volume")
    @patch("controller.controller_server.csi_controller_server.get_agent")
    def delete_volume_returns_error(self, storage_agent, delete_volume, error, return_code):
        storage_agent.return_value = self.storage_agent
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

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_delete_volume_has_snapshots(self, storage_agent):
        storage_agent.return_value = self.storage_agent
        context = utils.FakeContext()
        self.mediator.is_volume_has_snapshots.return_value = True
        self.servicer.DeleteVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.FAILED_PRECONDITION)

    @patch("controller.array_action.array_mediator_xiv.XIVArrayMediator.delete_volume")
    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_delete_volume_succeeds(self, storage_agent, delete_volume):
        storage_agent.return_value = self.storage_agent
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

        self.mediator.client = Mock()

        self.storage_agent = MagicMock()
        self.storage_agent.get_mediator.return_value.__enter__.return_value = self.mediator

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

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_publish_volume_success(self, storage_agent):
        storage_agent.return_value = self.storage_agent

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

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_publish_volume_get_host_by_host_identifiers_exception(self, storage_agent):
        context = utils.FakeContext()

        self.mediator.get_host_by_host_identifiers = Mock()
        self.mediator.get_host_by_host_identifiers.side_effect = [array_errors.MultipleHostsFoundError("", "")]
        storage_agent.return_value = self.storage_agent

        self.servicer.ControllerPublishVolume(self.request, context)
        self.assertTrue("Multiple hosts" in context.details)
        self.assertEqual(context.code, grpc.StatusCode.INTERNAL)

        self.mediator.get_host_by_host_identifiers.side_effect = [array_errors.HostNotFoundError("")]
        storage_agent.return_value = self.storage_agent

        self.servicer.ControllerPublishVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.NOT_FOUND)

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_publish_volume_get_volume_mappings_one_map_for_existing_host(self, storage_agent):
        context = utils.FakeContext()
        self.mediator.get_volume_mappings = Mock()
        self.mediator.get_volume_mappings.return_value = {self.hostname: 2}
        storage_agent.return_value = self.storage_agent

        res = self.servicer.ControllerPublishVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.OK)

        self.assertEqual(res.publish_context["PUBLISH_CONTEXT_LUN"], '2')
        self.assertEqual(res.publish_context["PUBLISH_CONTEXT_CONNECTIVITY"], "iscsi")

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_publish_volume_with_connectivity_type_fc(self, storage_agent):
        context = utils.FakeContext()
        self.mediator.get_host_by_host_identifiers.return_value = self.hostname, ["iscsi", "fc"]
        self.mediator.get_array_fc_wwns = Mock()
        self.mediator.get_array_fc_wwns.return_value = ["500143802426baf4"]
        storage_agent.return_value = self.storage_agent

        res = self.servicer.ControllerPublishVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.OK)

        self.assertEqual(res.publish_context["PUBLISH_CONTEXT_LUN"], '1')
        self.assertEqual(res.publish_context["PUBLISH_CONTEXT_CONNECTIVITY"], "fc")
        self.assertEqual(res.publish_context["PUBLISH_CONTEXT_ARRAY_FC_INITIATORS"], "500143802426baf4")

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_publish_volume_with_connectivity_type_iscsi(self, storage_agent):
        context = utils.FakeContext()
        self.mediator.get_host_by_host_identifiers.return_value = self.hostname, ["iscsi"]
        self.mediator.get_array_fc_wwns = Mock()
        self.mediator.get_array_fc_wwns.return_value = ["500143802426baf4"]
        storage_agent.return_value = self.storage_agent

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

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_publish_volume_with_node_id_only_has_iqns(self, storage_agent):
        context = utils.FakeContext()
        self.request.node_id = "hostname;iqn.1994-05.com.redhat:686358c930fe;"
        self.mediator.get_host_by_host_identifiers.return_value = self.hostname, ["iscsi"]
        storage_agent.return_value = self.storage_agent

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

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_publish_volume_with_node_id_only_has_wwns(self, storage_agent):
        context = utils.FakeContext()
        self.request.node_id = "hostname;;500143802426baf4"
        self.mediator.get_host_by_host_identifiers.return_value = self.hostname, ["fc"]
        self.mediator.get_array_fc_wwns = Mock()
        self.mediator.get_array_fc_wwns.return_value = ["500143802426baf4"]
        self.mediator.get_iscsi_targets_by_iqn.return_value = {}
        storage_agent.return_value = self.storage_agent

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
        storage_agent.return_value = self.storage_agent

        res = self.servicer.ControllerPublishVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.OK)

        self.assertEqual(res.publish_context["PUBLISH_CONTEXT_LUN"], '1')
        self.assertEqual(res.publish_context["PUBLISH_CONTEXT_CONNECTIVITY"],
                         "fc")
        self.assertEqual(
            res.publish_context["PUBLISH_CONTEXT_ARRAY_FC_INITIATORS"],
            "500143802426baf4,500143806626bae2")

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_publish_volume_get_volume_mappings_one_map_for_other_host(self, storage_agent):
        context = utils.FakeContext()
        self.mediator.get_volume_mappings = Mock()
        self.mediator.get_volume_mappings.return_value = {"other-hostname": 3}
        storage_agent.return_value = self.storage_agent

        self.servicer.ControllerPublishVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.FAILED_PRECONDITION)
        self.assertTrue("Volume is already mapped" in context.details)

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_publish_volume_get_volume_mappings_more_then_one_mapping(self, storage_agent):
        context = utils.FakeContext()
        self.mediator.get_volume_mappings = Mock()
        self.mediator.get_volume_mappings.return_value = {"other-hostname": 3, self.hostname: 4}
        storage_agent.return_value = self.storage_agent

        self.servicer.ControllerPublishVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.FAILED_PRECONDITION)
        self.assertTrue("Volume is already mapped" in context.details)

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_publish_volume_map_volume_excpetions(self, storage_agent):
        context = utils.FakeContext()

        self.mediator.map_volume.side_effect = [array_errors.PermissionDeniedError("msg")]

        storage_agent.return_value = self.storage_agent
        self.servicer.ControllerPublishVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.PERMISSION_DENIED)

        self.mediator.map_volume.side_effect = [array_errors.VolumeNotFoundError("vol")]
        storage_agent.return_value = self.storage_agent
        self.servicer.ControllerPublishVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.NOT_FOUND)

        self.mediator.map_volume.side_effect = [array_errors.HostNotFoundError("host")]
        storage_agent.return_value = self.storage_agent
        self.servicer.ControllerPublishVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.NOT_FOUND)

        self.mediator.map_volume.side_effect = [array_errors.MappingError("", "", "")]
        storage_agent.return_value = self.storage_agent
        self.servicer.ControllerPublishVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.INTERNAL)

    @patch.object(XIVArrayMediator, "MAX_LUN_NUMBER", 3)
    @patch.object(XIVArrayMediator, "MIN_LUN_NUMBER", 1)
    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_publish_volume_map_volume_lun_already_in_use(self, storage_agent):
        context = utils.FakeContext()

        self.mediator.map_volume.side_effect = [array_errors.LunAlreadyInUseError("", ""), 2]
        storage_agent.return_value = self.storage_agent
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
        storage_agent.return_value = self.storage_agent
        res = self.servicer.ControllerPublishVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.OK)
        self.assertEqual(res.publish_context["PUBLISH_CONTEXT_LUN"], '2')
        self.assertEqual(res.publish_context["PUBLISH_CONTEXT_CONNECTIVITY"],
                         "fc")

        self.mediator.map_volume.side_effect = [array_errors.LunAlreadyInUseError("", ""),
                                                array_errors.LunAlreadyInUseError("", ""), 2]
        storage_agent.return_value = self.storage_agent
        self.servicer.ControllerPublishVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.OK)
        self.assertEqual(res.publish_context["PUBLISH_CONTEXT_LUN"], '2')
        self.assertEqual(res.publish_context["PUBLISH_CONTEXT_CONNECTIVITY"], "fc")

        self.mediator.map_volume.side_effect = [
                                                   array_errors.LunAlreadyInUseError("", "")] * (
                                                       self.mediator.max_lun_retries + 1)
        storage_agent.return_value = self.storage_agent
        self.servicer.ControllerPublishVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.RESOURCE_EXHAUSTED)

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_publish_volume_get_iscsi_targets_by_iqn_excpetions(self, storage_agent):
        context = utils.FakeContext()
        self.mediator.get_iscsi_targets_by_iqn.side_effect = [array_errors.NoIscsiTargetsFoundError("some_endpoint")]
        storage_agent.return_value = self.storage_agent

        self.servicer.ControllerPublishVolume(self.request, context)

        self.assertEqual(context.code, grpc.StatusCode.NOT_FOUND)

    @patch("controller.array_action.array_mediator_abstract.ArrayMediatorAbstract.map_volume_by_initiators")
    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_map_volume_by_initiators_exceptions(self, storage_agent, map_volume_by_initiators):
        context = utils.FakeContext()
        map_volume_by_initiators.side_effect = [
            array_errors.UnsupportedConnectivityTypeError("usb")]
        storage_agent.return_value = self.storage_agent
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

        self.storage_agent = MagicMock()
        self.storage_agent.get_mediator.return_value.__enter__.return_value = self.mediator

        self.servicer = ControllerServicer(self.fqdn)

        self.request = Mock()
        arr_type = XIVArrayMediator.array_type
        self.request.volume_id = "{}:wwn1".format(arr_type)
        self.request.node_id = "hostname;iqn1;500143802426baf4"
        self.request.secrets = {"username": "user", "password": "pass", "management_address": "mg"}
        self.request.volume_context = {}

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_unpublish_volume_success(self, storage_agent):
        storage_agent.return_value = self.storage_agent
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

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_unpublish_volume_get_host_by_host_identifiers_exception(self, storage_agent):
        context = utils.FakeContext()

        self.mediator.get_host_by_host_identifiers = Mock()
        self.mediator.get_host_by_host_identifiers.side_effect = [array_errors.MultipleHostsFoundError("", "")]
        storage_agent.return_value = self.storage_agent

        self.servicer.ControllerUnpublishVolume(self.request, context)
        self.assertTrue("Multiple hosts" in context.details)
        self.assertEqual(context.code, grpc.StatusCode.INTERNAL)

        self.mediator.get_host_by_host_identifiers.side_effect = [array_errors.HostNotFoundError("")]
        storage_agent.return_value = self.storage_agent

        self.servicer.ControllerUnpublishVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.NOT_FOUND)

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_unpublish_volume_unmap_volume_excpetions(self, storage_agent):
        context = utils.FakeContext()

        self.mediator.unmap_volume.side_effect = [array_errors.PermissionDeniedError("msg")]
        storage_agent.return_value = self.storage_agent
        self.servicer.ControllerUnpublishVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.PERMISSION_DENIED)

        context = utils.FakeContext()
        self.mediator.unmap_volume.side_effect = [array_errors.HostNotFoundError("host")]
        storage_agent.return_value = self.storage_agent
        self.servicer.ControllerUnpublishVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.NOT_FOUND)

        context = utils.FakeContext()
        self.mediator.unmap_volume.side_effect = [array_errors.UnMappingError("", "", "")]
        storage_agent.return_value = self.storage_agent
        self.servicer.ControllerUnpublishVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.INTERNAL)

        context = utils.FakeContext()
        self.mediator.unmap_volume.side_effect = [array_errors.VolumeNotFoundError("vol")]
        storage_agent.return_value = self.storage_agent
        self.servicer.ControllerUnpublishVolume(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.OK)

        context = utils.FakeContext()
        self.mediator.unmap_volume.side_effect = [array_errors.VolumeAlreadyUnmappedError("")]
        storage_agent.return_value = self.storage_agent
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
