import abc
import unittest

# from unittest import mock as umock
import grpc
from mock import patch, Mock, MagicMock, call

import controller.array_action.errors as array_errors
import controller.controller_server.config as config
import controller.controller_server.errors as controller_errors
from controller.array_action.array_mediator_xiv import XIVArrayMediator
from controller.controller_server.csi_controller_server import ControllerServicer
from controller.controller_server.test_settings import volume_name, snapshot_name, snapshot_volume_name, \
    clone_volume_name
from controller.csi_general import csi_pb2
from controller.tests import utils


class AbstractControllerTest(unittest.TestCase):

    def setUp(self):
        patch("controller.array_action.array_mediator_xiv.XIVArrayMediator._connect").start()
        detect_array_type_patcher = patch("controller.controller_server.csi_controller_server.detect_array_type")
        self.detect_array_type = detect_array_type_patcher.start()
        self.detect_array_type.return_value = "a9k"
        self.addCleanup(detect_array_type_patcher.stop)
        self.request = Mock()
        self.request.secrets = {"username": "user", "password": "pass", "management_address": "mg"}

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

    def _test_create_object_with_get_array_type_exception(self, storage_agent):
        storage_agent.return_value = self.storage_agent
        context = utils.FakeContext()
        self.detect_array_type.side_effect = [array_errors.FailedToFindStorageSystemType("endpoint")]
        self.get_create_object_method()(self.request, context)
        self.assertEqual(context.code, grpc.StatusCode.INTERNAL, "failed to find storage system")
        msg = array_errors.FailedToFindStorageSystemType("endpoint").message
        self.assertTrue(msg in context.details)


class TestControllerServerCreateSnapshot(AbstractControllerTest):

    def get_create_object_method(self):
        return self.servicer.CreateSnapshot

    def get_create_object_response_method(self):
        return csi_pb2.CreateSnapshotResponse

    def setUp(self):
        super().setUp()
        self.fqdn = "fqdn"
        self.mediator = XIVArrayMediator("user", "password", self.fqdn)
        self.mediator.client = Mock()
        self.mediator.get_snapshot = Mock()
        self.mediator.get_snapshot.return_value = None

        self.storage_agent = MagicMock()
        self.storage_agent.get_mediator.return_value.__enter__.return_value = self.mediator

        self.servicer = ControllerServicer(self.fqdn)

        self.request.parameters = {}
        self.capacity_bytes = 10
        self.request.name = snapshot_name
        self.request.source_volume_id = "A9000:12345678"

        self.context = utils.FakeContext()

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_create_snapshot_with_empty_name(self, a_enter):
        self._test_create_object_with_empty_name(a_enter)

    def _prepare_create_snapshot_mocks(self, storage_agent):
        storage_agent.return_value = self.storage_agent

        self.mediator.create_snapshot = Mock()
        self.mediator.create_snapshot.return_value = utils.get_mock_mediator_response_snapshot(10, "snapshot", "wwn",
                                                                                               "snapshot_volume", "xiv")
        self.mediator.get_volume_name = Mock()
        self.mediator.get_volume_name.return_value = snapshot_volume_name

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_create_snapshot_succeeds(self, storage_agent):
        self._prepare_create_snapshot_mocks(storage_agent)

        self.servicer.CreateSnapshot(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.OK)
        self.mediator.get_snapshot.assert_called_once_with(snapshot_name, pool_id=None)
        self.mediator.create_snapshot.assert_called_once_with(snapshot_name, snapshot_volume_name, None)

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_create_snapshot_belongs_to_wrong_volume(self, storage_agent):
        storage_agent.return_value = self.storage_agent
        self.mediator.create_snapshot = Mock()
        self.mediator.get_snapshot.return_value = utils.get_mock_mediator_response_snapshot(10, snapshot_name, "wwn",
                                                                                            "wrong_volume_name", "xiv")
        self.mediator.get_volume_name = Mock()
        self.mediator.get_volume_name.return_value = snapshot_volume_name

        self.servicer.CreateSnapshot(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.ALREADY_EXISTS)

    def test_create_snapshot_no_source_volume(self):
        self.request.source_volume_id = None

        self.servicer.CreateSnapshot(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.INVALID_ARGUMENT)

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_create_snapshot_source_volume_not_found(self, storage_agent):
        storage_agent.return_value = self.storage_agent
        self.mediator.get_volume_name = Mock()
        self.mediator.get_volume_name.side_effect = array_errors.ObjectNotFoundError("volume_id")

        self.servicer.CreateSnapshot(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.NOT_FOUND)

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_create_snapshot_with_wrong_secrets(self, storage_agent):
        self._test_create_object_with_wrong_secrets(storage_agent)

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_create_snapshot_with_array_connection_exception(self, storage_agent):
        self._test_create_object_with_array_connection_exception(storage_agent)

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_create_snapshot_with_get_array_type_exception(self, storage_agent):
        self._test_create_object_with_get_array_type_exception(storage_agent)

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_create_snapshot_get_snapshot_exception(self, storage_agent):
        storage_agent.return_value = self.storage_agent
        self.mediator.get_snapshot.side_effect = [Exception("error")]

        self.servicer.CreateSnapshot(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.INTERNAL)
        self.assertTrue("error" in self.context.details)
        self.mediator.get_snapshot.assert_called_once_with(snapshot_name, pool_id=None)

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_create_snapshot_with_get_snapshot_illegal_object_name_exception(self, storage_agent):
        storage_agent.return_value = self.storage_agent
        self.mediator.get_snapshot.side_effect = [array_errors.IllegalObjectName("snapshot")]
        msg = array_errors.IllegalObjectName("snapshot").message

        self.servicer.CreateSnapshot(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.INVALID_ARGUMENT)
        self.assertTrue(msg in self.context.details)
        self.mediator.get_snapshot.assert_called_once_with(snapshot_name, pool_id=None)

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_create_snapshot_with_prefix_too_long_exception(self, storage_agent):
        storage_agent.return_value = self.storage_agent
        self.request.parameters.update({"snapshot_name_prefix": "a" * 128})
        self.servicer.CreateSnapshot(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.INVALID_ARGUMENT)

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_create_snapshot_with_get_snapshot_name_too_long_success(self, storage_agent):
        self._prepare_create_snapshot_mocks(storage_agent)
        self.mediator.max_object_name_length = 63
        self.request.name = "a" * 128

        self.servicer.CreateSnapshot(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.OK)

    @patch("controller.array_action.array_mediator_xiv.XIVArrayMediator.create_snapshot")
    @patch("controller.controller_server.csi_controller_server.get_agent")
    def create_snapshot_returns_error(self, storage_agent, create_snapshot, return_code, err):
        storage_agent.return_value = self.storage_agent
        self.mediator.get_volume_name = Mock()
        self.mediator.get_volume_name.return_value = snapshot_volume_name
        create_snapshot.side_effect = [err]
        msg = str(err)

        self.servicer.CreateSnapshot(self.request, self.context)

        self.assertEqual(self.context.code, return_code)
        self.assertTrue(msg in self.context.details)
        self.mediator.get_snapshot.assert_called_once_with(snapshot_name, pool_id=None)
        self.mediator.create_snapshot.assert_called_once_with(snapshot_name, snapshot_volume_name, None)

    def test_create_snapshot_with_illegal_object_name_exception(self):
        self.create_snapshot_returns_error(return_code=grpc.StatusCode.INVALID_ARGUMENT,
                                           err=array_errors.IllegalObjectName("snapshot"))

    def test_create_snapshot_with_snapshot_exists_exception(self):
        self.create_snapshot_returns_error(return_code=grpc.StatusCode.ALREADY_EXISTS,
                                           err=array_errors.SnapshotAlreadyExists("snapshot", "endpoint"))

    def test_create_snapshot_with_same_volume_name_exists_exception(self):
        self.create_snapshot_returns_error(return_code=grpc.StatusCode.INTERNAL,
                                           err=array_errors.ExpectedSnapshotButFoundVolumeError("snapshot",
                                                                                                "endpoint"))

    def test_create_snapshot_with_other_exception(self):
        self.create_snapshot_returns_error(return_code=grpc.StatusCode.INTERNAL, err=Exception("error"))

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_create_snapshot_with_name_prefix(self, storage_agent):
        storage_agent.return_value = self.storage_agent
        self.mediator.get_volume_name = Mock()
        self.mediator.get_volume_name.return_value = "snapshot_volume"
        self.request.name = "some_name"
        self.request.parameters[config.PARAMETERS_SNAPSHOT_NAME_PREFIX] = "prefix"
        self.mediator.create_snapshot = Mock()
        self.mediator.create_snapshot.return_value = utils.get_mock_mediator_response_snapshot(10, "snapshot", "wwn",
                                                                                               "snapshot_volume", "xiv")

        self.servicer.CreateSnapshot(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.OK)
        self.mediator.create_snapshot.assert_called_once_with("prefix_some_name", "snapshot_volume", None)


class TestControllerServerDeleteSnapshot(AbstractControllerTest):
    def setUp(self):
        super().setUp()
        self.fqdn = "fqdn"
        self.mediator = XIVArrayMediator("user", "password", self.fqdn)
        self.mediator.client = Mock()
        self.mediator.get_snapshot = Mock()
        self.mediator.get_snapshot.return_value = None
        self.storage_agent = MagicMock()
        self.storage_agent.get_mediator.return_value.__enter__.return_value = self.mediator
        self.servicer = ControllerServicer(self.fqdn)
        self.request.parameters = {}
        self.request.snapshot_id = "A9000:BADC0FFEE0DDF00D00000000DEADBABE"
        self.context = utils.FakeContext()

    @patch("controller.array_action.array_mediator_xiv.XIVArrayMediator.delete_snapshot", Mock())
    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_delete_snapshot_succeeds(self, storage_agent):
        storage_agent.return_value = self.storage_agent

        self.servicer.DeleteSnapshot(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.OK)

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_delete_snapshot_with_wrong_secrets(self, storage_agent):
        storage_agent.return_value = self.storage_agent

        self.request.secrets = {"password": "pass", "management_address": "mg"}
        self.servicer.DeleteSnapshot(self.request, self.context)
        self.assertEqual(self.context.code, grpc.StatusCode.INVALID_ARGUMENT, "username is missing in secrets")
        self.assertTrue("secret" in self.context.details)

        self.request.secrets = {"username": "user", "management_address": "mg"}
        self.servicer.DeleteSnapshot(self.request, self.context)
        self.assertEqual(self.context.code, grpc.StatusCode.INVALID_ARGUMENT, "password is missing in secrets")
        self.assertTrue("secret" in self.context.details)

        self.request.secrets = {"username": "user", "password": "pass"}
        self.servicer.DeleteSnapshot(self.request, self.context)
        self.assertEqual(self.context.code, grpc.StatusCode.INVALID_ARGUMENT, "mgmt address is missing in secrets")
        self.assertTrue("secret" in self.context.details)

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_delete_snapshot_with_array_connection_exception(self, storage_agent):
        storage_agent.side_effect = [Exception("a_enter error")]

        self.servicer.DeleteSnapshot(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.INTERNAL, "array connection internal error")
        self.assertTrue("a_enter error" in self.context.details)

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_delete_snapshot_invalid_snapshot_id(self, storage_agent):
        storage_agent.return_value = self.storage_agent
        self.request.snapshot_id = "wrong_id"

        self.servicer.DeleteSnapshot(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.OK)


class ProtoBufMock(MagicMock):
    def HasField(self, field):
        return hasattr(self, field)


class TestControllerServerCreateVolume(AbstractControllerTest):

    def get_create_object_method(self):
        return self.servicer.CreateVolume

    def get_create_object_response_method(self):
        return csi_pb2.CreateVolumeResponse

    def setUp(self):
        super().setUp()
        self.fqdn = "fqdn"
        self.mediator = XIVArrayMediator("user", "password", self.fqdn)
        self.mediator.client = Mock()

        self.mediator.get_volume = Mock()
        self.mediator.get_volume.side_effect = [array_errors.ObjectNotFoundError("volume")]

        self.storage_agent = MagicMock()
        self.storage_agent.get_mediator.return_value.__enter__.return_value = self.mediator

        self.servicer = ControllerServicer(self.fqdn)

        caps = Mock()
        caps.mount = Mock()
        caps.mount.fs_type = "ext4"
        access_types = csi_pb2.VolumeCapability.AccessMode
        caps.access_mode.mode = access_types.SINGLE_NODE_WRITER

        self.request.volume_capabilities = [caps]

        self.mediator.maximal_volume_size_in_bytes = 10
        self.mediator.minimal_volume_size_in_bytes = 2

        self.pool = 'pool1'
        self.request.parameters = {"pool": self.pool}
        self.capacity_bytes = 10
        self.request.capacity_range = Mock()
        self.request.capacity_range.required_bytes = self.capacity_bytes
        self.request.name = volume_name
        self.request.volume_content_source = None

        self.context = utils.FakeContext()

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_create_volume_with_empty_name(self, storage_agent):
        self._test_create_object_with_empty_name(storage_agent)

    def _prepare_create_volume_mocks(self, storage_agent):
        storage_agent.return_value = self.storage_agent

        self.mediator.create_volume = Mock()
        self.mediator.create_volume.return_value = utils.get_mock_mediator_response_volume(10, "volume", "wwn", "xiv")

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_create_volume_succeeds(self, storage_agent):
        self._prepare_create_volume_mocks(storage_agent)

        response_volume = self.servicer.CreateVolume(self.request, self.context)
        self.assertEqual(self.context.code, grpc.StatusCode.OK)
        self.mediator.get_volume.assert_called_once_with(volume_name, pool_id='pool1')
        self.mediator.create_volume.assert_called_once_with(volume_name, 10, None, 'pool1')
        self.assertEqual(response_volume.volume.content_source.volume.volume_id, '')
        self.assertEqual(response_volume.volume.content_source.snapshot.snapshot_id, '')

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_create_volume_with_space_efficiency_succeeds(self, storage_agent):
        self._prepare_create_volume_mocks(storage_agent)
        self.request.parameters.update({config.PARAMETERS_SPACE_EFFICIENCY: "not_none"})
        self.mediator.validate_supported_space_efficiency = Mock()

        self.servicer.CreateVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.OK)
        self.mediator.get_volume.assert_called_once_with(volume_name, pool_id='pool1')
        self.mediator.create_volume.assert_called_once_with(volume_name, 10, "not_none", 'pool1')

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_create_volume_idempotent_no_source_succeeds(self, storage_agent):
        self._prepare_create_volume_mocks(storage_agent)
        self.mediator.get_volume = Mock()
        self.mediator.get_volume.return_value = utils.get_mock_mediator_response_volume(10, volume_name, "wwn", "xiv")

        response_volume = self.servicer.CreateVolume(self.request, self.context)
        self.assertEqual(self.context.code, grpc.StatusCode.OK)
        self.mediator.get_volume.assert_called_once_with(volume_name, pool_id='pool1')
        self.mediator.create_volume.assert_not_called()
        self.assertEqual(response_volume.volume.content_source.volume.volume_id, '')
        self.assertEqual(response_volume.volume.content_source.snapshot.snapshot_id, '')

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_create_volume_with_wrong_secrets(self, a_enter):
        self._test_create_object_with_wrong_secrets(a_enter)

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_create_volume_with_wrong_parameters(self, storage_agent):
        storage_agent.return_value = self.storage_agent

        self.request.parameters = {"pool": "pool1"}
        self.servicer.CreateVolume(self.request, self.context)
        self.assertNotEqual(self.context.code, grpc.StatusCode.INVALID_ARGUMENT)

        self.request.parameters = {"capabilities": ""}
        self.servicer.CreateVolume(self.request, self.context)
        self.assertEqual(self.context.code, grpc.StatusCode.INVALID_ARGUMENT, "capacity is missing in secrets")
        self.assertEqual(self.context.code, grpc.StatusCode.INVALID_ARGUMENT, "pool parameter is missing")
        self.assertTrue("parameter" in self.context.details)

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_create_volume_with_wrong_volume_capabilities(self, storage_agent):
        storage_agent.return_value = self.storage_agent

        caps = Mock()
        caps.mount = Mock()
        caps.mount.fs_type = "ext42"
        access_types = csi_pb2.VolumeCapability.AccessMode
        caps.access_mode.mode = access_types.SINGLE_NODE_WRITER

        self.request.volume_capabilities = [caps]

        self.servicer.CreateVolume(self.request, self.context)
        self.assertEqual(self.context.code, grpc.StatusCode.INVALID_ARGUMENT, "wrong fs_type")
        self.assertTrue("fs_type" in self.context.details)

        caps.mount.fs_type = "ext4"
        caps.access_mode.mode = access_types.MULTI_NODE_SINGLE_WRITER
        self.request.volume_capabilities = [caps]

        self.servicer.CreateVolume(self.request, self.context)
        self.assertEqual(self.context.code, grpc.StatusCode.INVALID_ARGUMENT, "wrong access_mode")
        self.assertTrue("access mode" in self.context.details)

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_create_volume_with_array_connection_exception(self, storage_agent):
        self._test_create_object_with_array_connection_exception(storage_agent)

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_create_volume_with_get_array_type_exception(self, storage_agent):
        self._test_create_object_with_get_array_type_exception(storage_agent)

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_create_volume_get_volume_exception(self, storage_agent):
        storage_agent.return_value = self.storage_agent
        self.mediator.get_volume.side_effect = [Exception("error")]

        self.servicer.CreateVolume(self.request, self.context)
        self.assertEqual(self.context.code, grpc.StatusCode.INTERNAL)
        self.assertTrue("error" in self.context.details)
        self.mediator.get_volume.assert_called_once_with(volume_name, pool_id='pool1')

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_create_volume_with_get_volume_illegal_object_name_exception(self, storage_agent):
        storage_agent.return_value = self.storage_agent
        self.mediator.get_volume.side_effect = [array_errors.IllegalObjectName("volume")]

        self.servicer.CreateVolume(self.request, self.context)
        msg = array_errors.IllegalObjectName("volume").message

        self.assertEqual(self.context.code, grpc.StatusCode.INVALID_ARGUMENT)
        self.assertTrue(msg in self.context.details)
        self.mediator.get_volume.assert_called_once_with(volume_name, pool_id='pool1')

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_create_volume_with_prefix_too_long_exception(self, storage_agent):
        storage_agent.return_value = self.storage_agent
        self.request.parameters.update({"volume_name_prefix": "a" * 128})
        self.servicer.CreateVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.INVALID_ARGUMENT)

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_create_volume_with_get_volume_name_too_long_success(self, storage_agent):
        self._prepare_create_volume_mocks(storage_agent)
        self.mediator.max_object_name_length = 63

        self.request.name = "a" * 128
        self.servicer.CreateVolume(self.request, self.context)
        self.assertEqual(self.context.code, grpc.StatusCode.OK)

    @patch("controller.array_action.array_mediator_xiv.XIVArrayMediator.create_volume")
    @patch("controller.controller_server.csi_controller_server.get_agent")
    def create_volume_returns_error(self, storage_agent, create_volume, return_code, err):
        storage_agent.return_value = self.storage_agent
        create_volume.side_effect = [err]

        self.servicer.CreateVolume(self.request, self.context)
        msg = str(err)

        self.assertEqual(self.context.code, return_code)
        self.assertTrue(msg in self.context.details)
        self.mediator.get_volume.assert_called_once_with(volume_name, pool_id='pool1')
        self.mediator.create_volume.assert_called_once_with(volume_name, self.capacity_bytes, None, self.pool)

    def test_create_volume_with_illegal_object_name_exception(self):
        self.create_volume_returns_error(return_code=grpc.StatusCode.INVALID_ARGUMENT,
                                         err=array_errors.IllegalObjectName("volume"))

    def test_create_volume_with_create_volume_with_volume_exsits_exception(self):
        self.create_volume_returns_error(return_code=grpc.StatusCode.ALREADY_EXISTS,
                                         err=array_errors.VolumeAlreadyExists("volume", "endpoint"))

    def test_create_volume_with_create_volume_with_pool_does_not_exist_exception(self):
        self.create_volume_returns_error(return_code=grpc.StatusCode.INVALID_ARGUMENT,
                                         err=array_errors.PoolDoesNotExist("pool1", "endpoint"))

    def test_create_volume_with_create_volume_with_pool_does_not_match_capabilities_exception(self):
        self.create_volume_returns_error(return_code=grpc.StatusCode.INVALID_ARGUMENT,
                                         err=array_errors.PoolDoesNotMatchCapabilities("pool1", "", "endpoint"))

    def test_create_volume_with_space_efficiency_not_supported_exception(self):
        self.create_volume_returns_error(return_code=grpc.StatusCode.INVALID_ARGUMENT,
                                         err=array_errors.SpaceEfficiencyNotSupported(["fake"]))

    def test_create_volume_with_create_volume_with_other_exception(self):
        self.create_volume_returns_error(return_code=grpc.StatusCode.INTERNAL,
                                         err=Exception("error"))

    def _test_create_volume_prefix(self, prefix, final_name):
        self.mediator.default_object_prefix = "default"
        self.request.name = "some_name"
        self.request.parameters[config.PARAMETERS_VOLUME_NAME_PREFIX] = prefix
        self.mediator.create_volume = Mock()
        self.mediator.create_volume.return_value = utils.get_mock_mediator_response_volume(10, "volume", "wwn", "xiv")
        self.servicer.CreateVolume(self.request, self.context)
        self.assertEqual(self.context.code, grpc.StatusCode.OK)
        self.mediator.create_volume.assert_called_once_with(final_name, 10, None, "pool1")

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_create_volume_with_name_prefix(self, storage_agent):
        storage_agent.return_value = self.storage_agent
        self._test_create_volume_prefix("prefix", "prefix_some_name")

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_create_volume_with_no_name_prefix(self, storage_agent):
        storage_agent.return_value = self.storage_agent
        self._test_create_volume_prefix("", "default_some_name")

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_create_volume_with_required_bytes_zero(self, storage_agent):
        self._prepare_create_volume_mocks(storage_agent)
        self.request.capacity_range.required_bytes = 0

        self.servicer.CreateVolume(self.request, self.context)
        self.assertEqual(self.context.code, grpc.StatusCode.OK)
        self.mediator.create_volume.assert_called_once_with(self.request.name, 2, None, "pool1")

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_create_volume_with_required_bytes_too_large_fail(self, storage_agent):
        self._prepare_create_volume_mocks(storage_agent)
        self.request.capacity_range.required_bytes = 11

        self.servicer.CreateVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.OUT_OF_RANGE)
        self.mediator.create_volume.assert_not_called()

    def test_create_volume_with_no_space_in_pool(self):
        self.create_volume_returns_error(return_code=grpc.StatusCode.RESOURCE_EXHAUSTED,
                                         err=array_errors.NotEnoughSpaceInPool("pool"))

    def _prepare_idempotent_tests(self):
        self.mediator.get_volume = Mock()
        self.mediator.copy_to_existing_volume_from_source = Mock()
        self.request.volume_content_source = self._get_source_snapshot("wwn1")

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_create_volume_idempotent_with_source_succeed(self, storage_agent):
        self._prepare_idempotent_tests()
        storage_agent.return_value = self.storage_agent
        snapshot_id = "wwn1"
        self.mediator.get_volume.return_value = utils.get_mock_mediator_response_volume(10, volume_name, "wwn2", "a9k",
                                                                                        copy_source_id=snapshot_id)

        response = self.servicer.CreateVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.OK)
        self.assertEqual(response.volume.content_source.snapshot.snapshot_id, snapshot_id)
        self.mediator.copy_to_existing_volume_from_source.assert_not_called()

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_create_volume_idempotent_with_source_volume_have_no_source(self, storage_agent):
        self._prepare_idempotent_tests()
        storage_agent.return_value = self.storage_agent
        self.mediator.get_volume.return_value = utils.get_mock_mediator_response_volume(10, volume_name, "wwn2",
                                                                                        "a9k")
        response = self.servicer.CreateVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.ALREADY_EXISTS)
        self.assertFalse(response.HasField("volume"))
        self.mediator.copy_to_existing_volume_from_source.assert_not_called()

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_create_volume_idempotent_source_not_requested_but_found_in_volume(self, storage_agent):
        self._prepare_idempotent_tests()
        storage_agent.return_value = self.storage_agent
        snapshot_id = "wwn1"
        self.request.volume_content_source = None
        self.mediator.get_volume.return_value = utils.get_mock_mediator_response_volume(10, volume_name, "wwn2", "a9k",
                                                                                        copy_source_id=snapshot_id)
        response = self.servicer.CreateVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.ALREADY_EXISTS)
        self.assertFalse(response.HasField("volume"))
        self.mediator.copy_to_existing_volume_from_source.assert_not_called()

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_create_volume_idempotent_with_source_volume_got_other_source(self, storage_agent):
        self._prepare_idempotent_tests()
        storage_agent.return_value = self.storage_agent
        volume_source_id = "wwn3"
        self.mediator.get_volume.return_value = utils.get_mock_mediator_response_volume(10, "volume", "wwn2", "a9k",
                                                                                        copy_source_id=volume_source_id)
        self.servicer.CreateVolume(self.request, self.context)
        self.assertEqual(self.context.code, grpc.StatusCode.ALREADY_EXISTS)
        self.mediator.copy_to_existing_volume_from_source.assert_not_called()

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_create_volume_idempotent_with_size_not_matched(self, storage_agent):
        storage_agent.return_value = self.storage_agent
        self.mediator.get_volume = Mock()
        self.mediator.get_volume.return_value = utils.get_mock_mediator_response_volume(100, volume_name, "wwn2", "a9k")

        self.servicer.CreateVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.ALREADY_EXISTS)

    def _prepare_mocks_for_copy_from_source(self):
        self.mediator.create_volume = Mock()
        self.mediator.create_volume.return_value = utils.get_mock_mediator_response_volume(10, volume_name, "wwn2",
                                                                                           "a9k")
        self.mediator.get_object_by_id = Mock()
        self.mediator.copy_to_existing_volume_from_source = Mock()

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_create_volume_from_snapshot_success(self, storage_agent):
        storage_agent.return_value = self.storage_agent
        self._prepare_mocks_for_copy_from_source()
        snapshot_id = "wwn1"
        snapshot_capacity_bytes = 100
        self.request.volume_content_source = self._get_source_snapshot(snapshot_id)
        self.mediator.get_object_by_id.return_value = utils.get_mock_mediator_response_snapshot(snapshot_capacity_bytes,
                                                                                                snapshot_name,
                                                                                                snapshot_id,
                                                                                                volume_name,
                                                                                                "a9k")
        response_volume = self.servicer.CreateVolume(self.request, self.context)
        self.assertEqual(self.context.code, grpc.StatusCode.OK)
        self.mediator.copy_to_existing_volume_from_source.assert_called_once_with(volume_name, snapshot_name,
                                                                                  snapshot_capacity_bytes,
                                                                                  self.capacity_bytes,
                                                                                  'pool1')
        self.assertEqual(response_volume.volume.content_source.volume.volume_id, '')
        self.assertEqual(response_volume.volume.content_source.snapshot.snapshot_id, snapshot_id)

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_create_volume_from_source_source_or_target_not_found(self, storage_agent):
        array_exception = array_errors.ObjectNotFoundError("")
        self._test_create_volume_from_snapshot_error(storage_agent, array_exception,
                                                     grpc.StatusCode.NOT_FOUND)

    def test_create_volume_from_source_source_snapshot_invalid(self):
        volume_content_source = self._get_source_snapshot('snapshot_id')
        volume_content_source.snapshot.snapshot_id = 'invalid_snapshot_id'
        self.request.volume_content_source = volume_content_source

        self.servicer.CreateVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.NOT_FOUND)
        self.assertTrue("invalid_snapshot_id" in self.context.details)

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_create_volume_from_source_illegal_object_name(self, storage_agent):
        array_exception = array_errors.IllegalObjectName("")
        self._test_create_volume_from_snapshot_error(storage_agent, array_exception,
                                                     grpc.StatusCode.INVALID_ARGUMENT)

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_create_volume_from_source_permission_denied(self, storage_agent):
        array_exception = array_errors.PermissionDeniedError("")
        self._test_create_volume_from_snapshot_error(storage_agent, array_exception,
                                                     grpc.StatusCode.PERMISSION_DENIED)

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_create_volume_from_source_pool_missing(self, storage_agent):
        array_exception = array_errors.PoolParameterIsMissing("")
        self._test_create_volume_from_snapshot_error(storage_agent, array_exception,
                                                     grpc.StatusCode.INVALID_ARGUMENT)

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_create_volume_from_source_general_error(self, storage_agent):
        array_exception = Exception("")
        self._test_create_volume_from_snapshot_error(storage_agent, array_exception,
                                                     grpc.StatusCode.INTERNAL)

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_create_volume_from_source_get_object_general_error(self, storage_agent):
        array_exception = Exception("")
        self._test_create_volume_from_snapshot_error(storage_agent, None,
                                                     grpc.StatusCode.INTERNAL, get_exception=array_exception)

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_create_volume_from_source_get_object_error(self, storage_agent):
        array_exception = array_errors.ExpectedSnapshotButFoundVolumeError("", "")
        self._test_create_volume_from_snapshot_error(storage_agent, None,
                                                     grpc.StatusCode.INVALID_ARGUMENT, get_exception=array_exception)

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_create_volume_from_source_get_object_none(self, storage_agent):
        self._test_create_volume_from_snapshot_error(storage_agent, None,
                                                     grpc.StatusCode.NOT_FOUND)

    def _test_create_volume_from_snapshot_error(self, storage_agent, copy_exception, return_code,
                                                get_exception=None):
        storage_agent.return_value = self.storage_agent
        self._prepare_mocks_for_copy_from_source()
        source_id = "wwn1"
        target_volume_id = "wwn2"
        self.request.volume_content_source = self._get_source_snapshot(source_id)
        if not copy_exception:
            self.mediator.get_object_by_id.side_effect = [get_exception]
            self.storage_agent.get_mediator.return_value.__exit__.side_effect = [get_exception]
        else:
            self.mediator.get_object_by_id.return_value = utils.get_mock_mediator_response_snapshot(1000, snapshot_name,
                                                                                                    target_volume_id,
                                                                                                    volume_name, "a9k")
            self.mediator.copy_to_existing_volume_from_source.side_effect = [copy_exception]

            self.storage_agent.get_mediator.return_value.__exit__.side_effect = [copy_exception]
        self.mediator.delete_volume = Mock()

        self.servicer.CreateVolume(self.request, self.context)
        self.mediator.delete_volume.assert_called_with(target_volume_id)
        self.assertEqual(self.context.code, return_code)

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_clone_volume_success(self, storage_agent):
        storage_agent.return_value = self.storage_agent
        self._prepare_mocks_for_copy_from_source()
        volume_id = "wwn1"
        volume_capacity_bytes = 100
        self.request.volume_content_source = self._get_source_volume(volume_id)
        self.mediator.get_object_by_id.return_value = utils.get_mock_mediator_response_volume(volume_capacity_bytes,
                                                                                              clone_volume_name,
                                                                                              volume_id,
                                                                                              "a9k")
        response_volume = self.servicer.CreateVolume(self.request, self.context)
        self.assertEqual(self.context.code, grpc.StatusCode.OK)
        self.mediator.copy_to_existing_volume_from_source.assert_called_once_with(volume_name, clone_volume_name,
                                                                                  volume_capacity_bytes,
                                                                                  self.capacity_bytes,
                                                                                  'pool1')
        self.assertEqual(response_volume.volume.content_source.volume.volume_id, volume_id)
        self.assertEqual(response_volume.volume.content_source.snapshot.snapshot_id, '')

    def _get_source_volume(self, object_id):
        return self._get_source(object_id, config.VOLUME_TYPE_NAME)

    def _get_source_snapshot(self, object_id):
        return self._get_source(object_id, config.SNAPSHOT_TYPE_NAME)

    @staticmethod
    def _get_source(object_id, object_type):
        source = ProtoBufMock(spec=[object_type])
        id_field_name = config.VOLUME_SOURCE_ID_FIELDS[object_type]
        object_field = MagicMock(spec=[id_field_name])
        setattr(source, object_type, object_field)
        setattr(object_field, id_field_name, "a9000:{0}".format(object_id))
        return source


class TestControllerServerDeleteVolume(AbstractControllerTest):

    def setUp(self):
        super().setUp()
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

        self.pool = 'pool1'
        self.request.volume_id = "xiv:volume-id"

        self.context = utils.FakeContext()

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_delete_volume_with_wrong_secrets(self, storage_agent):
        storage_agent.return_value = self.storage_agent

        self.request.secrets = {"password": "pass", "management_address": "mg"}
        self.servicer.DeleteVolume(self.request, self.context)
        self.assertEqual(self.context.code, grpc.StatusCode.INVALID_ARGUMENT, "username is missing in secrets")
        self.assertTrue("secret" in self.context.details)

        self.request.secrets = {"username": "user", "management_address": "mg"}
        self.servicer.DeleteVolume(self.request, self.context)
        self.assertEqual(self.context.code, grpc.StatusCode.INVALID_ARGUMENT, "password is missing in secrets")
        self.assertTrue("secret" in self.context.details)

        self.request.secrets = {"username": "user", "password": "pass"}
        self.servicer.DeleteVolume(self.request, self.context)
        self.assertEqual(self.context.code, grpc.StatusCode.INVALID_ARGUMENT, "mgmt address is missing in secrets")
        self.assertTrue("secret" in self.context.details)

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_delete_volume_invalid_volume_id(self, storage_agent):
        storage_agent.return_value = self.storage_agent
        self.request.volume_id = "wrong_id"

        self.servicer.DeleteVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.OK)

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_delete_volume_with_array_connection_exception(self, storage_agent):
        storage_agent.side_effect = [Exception("a_enter error")]

        self.servicer.DeleteVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.INTERNAL, "array connection internal error")
        self.assertTrue("a_enter error" in self.context.details)

    @patch("controller.array_action.array_mediator_xiv.XIVArrayMediator.delete_volume")
    @patch("controller.controller_server.csi_controller_server.get_agent")
    def delete_volume_returns_error(self, storage_agent, delete_volume, error, return_code):
        storage_agent.return_value = self.storage_agent
        delete_volume.side_effect = [error]

        self.servicer.DeleteVolume(self.request, self.context)

        self.assertEqual(self.context.code, return_code)
        if return_code != grpc.StatusCode.OK:
            msg = str(error)
            self.assertTrue(msg in self.context.details, "msg : {0} is not in : {1}".format(msg, self.context.details))

    def test_delete_volume_with_volume_not_found_error(self, ):
        self.delete_volume_returns_error(error=array_errors.ObjectNotFoundError("volume"),
                                         return_code=grpc.StatusCode.OK)

    def test_delete_volume_with_delete_volume_other_exception(self):
        self.delete_volume_returns_error(error=Exception("error"), return_code=grpc.StatusCode.INTERNAL)

    def test_delete_volume_has_snapshots(self):
        self.delete_volume_returns_error(error=array_errors.ObjectIsStillInUseError("a", "b"),
                                         return_code=grpc.StatusCode.FAILED_PRECONDITION)

    @patch("controller.array_action.array_mediator_xiv.XIVArrayMediator.delete_volume")
    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_delete_volume_succeeds(self, storage_agent, delete_volume):
        storage_agent.return_value = self.storage_agent
        delete_volume.return_value = Mock()
        self.servicer.DeleteVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.OK)


class TestControllerServerPublishVolume(unittest.TestCase):

    def setUp(self):
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

        self.context = utils.FakeContext()

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_publish_volume_success(self, storage_agent):
        storage_agent.return_value = self.storage_agent

        self.servicer.ControllerPublishVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.OK)

    @patch("controller.controller_server.utils.validate_publish_volume_request")
    def test_publish_volume_validateion_exception(self, publish_validation):
        publish_validation.side_effect = [controller_errors.ValidationException("msg")]

        self.servicer.ControllerPublishVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.INVALID_ARGUMENT)
        self.assertTrue("msg" in self.context.details)

    def test_publish_volume_wrong_volume_id(self):
        self.request.volume_id = "some-wrong-id-format"

        self.servicer.ControllerPublishVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.NOT_FOUND)

    def test_publish_volume_wrong_node_id(self):
        self.request.node_id = "some-wrong-id-format"

        self.servicer.ControllerPublishVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.NOT_FOUND)

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_publish_volume_get_host_by_host_identifiers_exception(self, storage_agent):
        self.mediator.get_host_by_host_identifiers = Mock()
        self.mediator.get_host_by_host_identifiers.side_effect = [array_errors.MultipleHostsFoundError("", "")]
        storage_agent.return_value = self.storage_agent

        self.servicer.ControllerPublishVolume(self.request, self.context)
        self.assertTrue("Multiple hosts" in self.context.details)
        self.assertEqual(self.context.code, grpc.StatusCode.INTERNAL)

        self.mediator.get_host_by_host_identifiers.side_effect = [array_errors.HostNotFoundError("")]
        storage_agent.return_value = self.storage_agent

        self.servicer.ControllerPublishVolume(self.request, self.context)
        self.assertEqual(self.context.code, grpc.StatusCode.NOT_FOUND)

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_publish_volume_get_volume_mappings_one_map_for_existing_host(self, storage_agent):
        self.mediator.get_volume_mappings = Mock()
        self.mediator.get_volume_mappings.return_value = {self.hostname: 2}
        storage_agent.return_value = self.storage_agent

        res = self.servicer.ControllerPublishVolume(self.request, self.context)
        self.assertEqual(self.context.code, grpc.StatusCode.OK)

        self.assertEqual(res.publish_context["PUBLISH_CONTEXT_LUN"], '2')
        self.assertEqual(res.publish_context["PUBLISH_CONTEXT_CONNECTIVITY"], "iscsi")

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_publish_volume_with_connectivity_type_fc(self, storage_agent):
        self.mediator.get_host_by_host_identifiers.return_value = self.hostname, ["iscsi", "fc"]
        self.mediator.get_array_fc_wwns = Mock()
        self.mediator.get_array_fc_wwns.return_value = ["500143802426baf4"]
        storage_agent.return_value = self.storage_agent

        res = self.servicer.ControllerPublishVolume(self.request, self.context)
        self.assertEqual(self.context.code, grpc.StatusCode.OK)

        self.assertEqual(res.publish_context["PUBLISH_CONTEXT_LUN"], '1')
        self.assertEqual(res.publish_context["PUBLISH_CONTEXT_CONNECTIVITY"], "fc")
        self.assertEqual(res.publish_context["PUBLISH_CONTEXT_ARRAY_FC_INITIATORS"], "500143802426baf4")

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_publish_volume_with_connectivity_type_iscsi(self, storage_agent):
        self.mediator.get_host_by_host_identifiers.return_value = self.hostname, ["iscsi"]
        self.mediator.get_array_fc_wwns = Mock()
        self.mediator.get_array_fc_wwns.return_value = ["500143802426baf4"]
        storage_agent.return_value = self.storage_agent

        res = self.servicer.ControllerPublishVolume(self.request, self.context)
        self.assertEqual(self.context.code, grpc.StatusCode.OK)

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
        self.request.node_id = "hostname;iqn.1994-05.com.redhat:686358c930fe;"
        self.mediator.get_host_by_host_identifiers.return_value = self.hostname, ["iscsi"]
        storage_agent.return_value = self.storage_agent

        res = self.servicer.ControllerPublishVolume(self.request, self.context)
        self.assertEqual(self.context.code, grpc.StatusCode.OK)

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
        self.request.node_id = "hostname;;500143802426baf4"
        self.mediator.get_host_by_host_identifiers.return_value = self.hostname, ["fc"]
        self.mediator.get_array_fc_wwns = Mock()
        self.mediator.get_array_fc_wwns.return_value = ["500143802426baf4"]
        self.mediator.get_iscsi_targets_by_iqn.return_value = {}
        storage_agent.return_value = self.storage_agent

        res = self.servicer.ControllerPublishVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.OK)
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

        res = self.servicer.ControllerPublishVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.OK)
        self.assertEqual(res.publish_context["PUBLISH_CONTEXT_LUN"], '1')
        self.assertEqual(res.publish_context["PUBLISH_CONTEXT_CONNECTIVITY"],
                         "fc")
        self.assertEqual(
            res.publish_context["PUBLISH_CONTEXT_ARRAY_FC_INITIATORS"],
            "500143802426baf4,500143806626bae2")

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_publish_volume_get_volume_mappings_one_map_for_other_host(self, storage_agent):
        self.mediator.get_volume_mappings = Mock()
        self.mediator.get_volume_mappings.return_value = {"other-hostname": 3}
        storage_agent.return_value = self.storage_agent

        self.servicer.ControllerPublishVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.FAILED_PRECONDITION)
        self.assertTrue("Volume is already mapped" in self.context.details)

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_publish_volume_get_volume_mappings_more_then_one_mapping(self, storage_agent):
        self.mediator.get_volume_mappings = Mock()
        self.mediator.get_volume_mappings.return_value = {"other-hostname": 3, self.hostname: 4}
        storage_agent.return_value = self.storage_agent

        self.servicer.ControllerPublishVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.FAILED_PRECONDITION)
        self.assertTrue("Volume is already mapped" in self.context.details)

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_publish_volume_map_volume_excpetions(self, storage_agent):
        self.mediator.map_volume.side_effect = [array_errors.PermissionDeniedError("msg")]

        storage_agent.return_value = self.storage_agent
        self.servicer.ControllerPublishVolume(self.request, self.context)
        self.assertEqual(self.context.code, grpc.StatusCode.PERMISSION_DENIED)

        self.mediator.map_volume.side_effect = [array_errors.ObjectNotFoundError("volume")]
        storage_agent.return_value = self.storage_agent
        self.servicer.ControllerPublishVolume(self.request, self.context)
        self.assertEqual(self.context.code, grpc.StatusCode.NOT_FOUND)

        self.mediator.map_volume.side_effect = [array_errors.HostNotFoundError("host")]
        storage_agent.return_value = self.storage_agent
        self.servicer.ControllerPublishVolume(self.request, self.context)
        self.assertEqual(self.context.code, grpc.StatusCode.NOT_FOUND)

        self.mediator.map_volume.side_effect = [array_errors.MappingError("", "", "")]
        storage_agent.return_value = self.storage_agent
        self.servicer.ControllerPublishVolume(self.request, self.context)
        self.assertEqual(self.context.code, grpc.StatusCode.INTERNAL)

    @patch.object(XIVArrayMediator, "MAX_LUN_NUMBER", 3)
    @patch.object(XIVArrayMediator, "MIN_LUN_NUMBER", 1)
    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_publish_volume_map_volume_lun_already_in_use(self, storage_agent):
        self.mediator.map_volume.side_effect = [array_errors.LunAlreadyInUseError("", ""), 2]
        storage_agent.return_value = self.storage_agent

        res = self.servicer.ControllerPublishVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.OK)
        self.assertEqual(res.publish_context["PUBLISH_CONTEXT_LUN"], '2')
        self.assertEqual(res.publish_context["PUBLISH_CONTEXT_CONNECTIVITY"], "iscsi")

        self.mediator.map_volume.side_effect = [
            array_errors.LunAlreadyInUseError("", ""), 2]
        self.mediator.get_host_by_host_identifiers = Mock()
        self.mediator.get_host_by_host_identifiers.return_value = self.hostname, ["fc"]
        self.mediator.get_array_fc_wwns = Mock()
        self.mediator.get_array_fc_wwns.return_value = ["500143802426baf4"]
        storage_agent.return_value = self.storage_agent

        res = self.servicer.ControllerPublishVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.OK)
        self.assertEqual(res.publish_context["PUBLISH_CONTEXT_LUN"], '2')
        self.assertEqual(res.publish_context["PUBLISH_CONTEXT_CONNECTIVITY"],
                         "fc")

        self.mediator.map_volume.side_effect = [array_errors.LunAlreadyInUseError("", ""),
                                                array_errors.LunAlreadyInUseError("", ""), 2]
        storage_agent.return_value = self.storage_agent

        self.servicer.ControllerPublishVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.OK)
        self.assertEqual(res.publish_context["PUBLISH_CONTEXT_LUN"], '2')
        self.assertEqual(res.publish_context["PUBLISH_CONTEXT_CONNECTIVITY"], "fc")

        self.mediator.map_volume.side_effect = [
                                                   array_errors.LunAlreadyInUseError("", "")] * (
                                                       self.mediator.max_lun_retries + 1)
        storage_agent.return_value = self.storage_agent

        self.servicer.ControllerPublishVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.RESOURCE_EXHAUSTED)

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_publish_volume_get_iscsi_targets_by_iqn_excpetions(self, storage_agent):
        self.mediator.get_iscsi_targets_by_iqn.side_effect = [array_errors.NoIscsiTargetsFoundError("some_endpoint")]
        storage_agent.return_value = self.storage_agent

        self.servicer.ControllerPublishVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.NOT_FOUND)

    @patch("controller.array_action.array_mediator_abstract.ArrayMediatorAbstract.map_volume_by_initiators")
    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_map_volume_by_initiators_exceptions(self, storage_agent, map_volume_by_initiators):
        map_volume_by_initiators.side_effect = [
            array_errors.UnsupportedConnectivityTypeError("usb")]
        storage_agent.return_value = self.storage_agent

        self.servicer.ControllerPublishVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.INVALID_ARGUMENT)


class TestControllerServerUnPublishVolume(unittest.TestCase):

    def setUp(self):
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

        self.context = utils.FakeContext()

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_unpublish_volume_success(self, storage_agent):
        storage_agent.return_value = self.storage_agent

        self.servicer.ControllerUnpublishVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.OK)

    @patch("controller.controller_server.utils.validate_unpublish_volume_request")
    def test_unpublish_volume_validation_exception(self, publish_validation):
        publish_validation.side_effect = [controller_errors.ValidationException("msg")]

        self.servicer.ControllerUnpublishVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.INVALID_ARGUMENT)
        self.assertTrue("msg" in self.context.details)

    def test_unpublish_volume_wrong_volume_id(self):
        self.request.volume_id = "some-wrong-id-format"

        self.servicer.ControllerUnpublishVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.INVALID_ARGUMENT)

    def test_unpublish_volume_wrong_node_id(self):
        self.request.node_id = "some-wrong-id-format"

        self.servicer.ControllerUnpublishVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.NOT_FOUND)

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_unpublish_volume_get_host_by_host_identifiers_exception(self, storage_agent):
        self.mediator.get_host_by_host_identifiers = Mock()
        self.mediator.get_host_by_host_identifiers.side_effect = [array_errors.MultipleHostsFoundError("", "")]
        storage_agent.return_value = self.storage_agent

        self.servicer.ControllerUnpublishVolume(self.request, self.context)
        self.assertTrue("Multiple hosts" in self.context.details)
        self.assertEqual(self.context.code, grpc.StatusCode.INTERNAL)

        self.mediator.get_host_by_host_identifiers.side_effect = [array_errors.HostNotFoundError("")]
        storage_agent.return_value = self.storage_agent

        self.servicer.ControllerUnpublishVolume(self.request, self.context)
        self.assertEqual(self.context.code, grpc.StatusCode.NOT_FOUND)

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_unpublish_volume_unmap_volume_excpetions(self, storage_agent):
        self.mediator.unmap_volume.side_effect = [array_errors.ObjectNotFoundError("volume")]
        storage_agent.return_value = self.storage_agent
        self.servicer.ControllerUnpublishVolume(self.request, self.context)
        self.assertEqual(self.context.code, grpc.StatusCode.OK)

        self.mediator.unmap_volume.side_effect = [array_errors.VolumeAlreadyUnmappedError("")]
        storage_agent.return_value = self.storage_agent
        self.servicer.ControllerUnpublishVolume(self.request, self.context)
        self.assertEqual(self.context.code, grpc.StatusCode.OK)

        self.mediator.unmap_volume.side_effect = [array_errors.PermissionDeniedError("msg")]
        storage_agent.return_value = self.storage_agent
        self.servicer.ControllerUnpublishVolume(self.request, self.context)
        self.assertEqual(self.context.code, grpc.StatusCode.PERMISSION_DENIED)

        self.mediator.unmap_volume.side_effect = [array_errors.HostNotFoundError("host")]
        storage_agent.return_value = self.storage_agent
        self.servicer.ControllerUnpublishVolume(self.request, self.context)
        self.assertEqual(self.context.code, grpc.StatusCode.NOT_FOUND)

        self.mediator.unmap_volume.side_effect = [array_errors.UnmappingError("", "", "")]
        storage_agent.return_value = self.storage_agent
        self.servicer.ControllerUnpublishVolume(self.request, self.context)
        self.assertEqual(self.context.code, grpc.StatusCode.INTERNAL)


class TestControllerServerGetCapabilities(unittest.TestCase):

    def setUp(self):
        self.fqdn = "fqdn"
        self.servicer = ControllerServicer(self.fqdn)

    def test_controller_get_capabilities(self):
        request = Mock()
        context = Mock()
        self.servicer.ControllerGetCapabilities(request, context)


class TestControllerServerExpandVolume(AbstractControllerTest):

    def get_create_object_method(self):
        return self.servicer.ControllerExpandVolume

    def get_create_object_response_method(self):
        return csi_pb2.ControllerExpandVolumeResponse

    def setUp(self):
        super().setUp()
        self.fqdn = "fqdn"
        self.mediator = XIVArrayMediator("user", "password", self.fqdn)
        self.mediator.client = Mock()

        self.storage_agent = MagicMock()
        self.storage_agent.get_mediator.return_value.__enter__.return_value = self.mediator

        self.servicer = ControllerServicer(self.fqdn)

        self.access_types = csi_pb2.VolumeCapability.AccessMode
        self.fs_type = "ext4"

        self.mediator.maximal_volume_size_in_bytes = 10
        self.mediator.minimal_volume_size_in_bytes = 2

        self.request.volume_capability = csi_pb2.VolumeCapability(
            access_mode=csi_pb2.VolumeCapability.AccessMode(mode=self.access_types.SINGLE_NODE_WRITER),
            mount=csi_pb2.VolumeCapability.MountVolume(fs_type=self.fs_type))
        self.request.parameters = {}
        self.capacity_bytes = 6
        self.request.capacity_range = Mock()
        self.request.capacity_range.required_bytes = self.capacity_bytes
        self.volume_id = "volume-id"
        self.request.volume_id = "{}:{}".format("xiv", self.volume_id)
        self.request.volume_content_source = None
        self.context = utils.FakeContext()
        self.mediator.get_object_by_id = Mock()
        self.volume_before_expand = utils.get_mock_mediator_response_volume(2,
                                                                            volume_name,
                                                                            self.volume_id,
                                                                            "a9k")
        self.volume_after_expand = utils.get_mock_mediator_response_volume(self.capacity_bytes,
                                                                           volume_name,
                                                                           self.volume_id,
                                                                           "a9k")
        self.mediator.get_object_by_id.side_effect = [self.volume_before_expand, self.volume_after_expand]

    def _prepare_expand_volume_mocks(self, storage_agent):
        storage_agent.return_value = self.storage_agent
        self.mediator.expand_volume = Mock()

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_expand_volume_with_required_bytes_too_large_fail(self, storage_agent):
        self._prepare_expand_volume_mocks(storage_agent)
        self.request.capacity_range.required_bytes = 11

        self.servicer.ControllerExpandVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.OUT_OF_RANGE)
        self.mediator.expand_volume.assert_not_called()

    def _test_no_expand_needed(self):
        response = self.servicer.ControllerExpandVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.OK)
        self.assertFalse(response.node_expansion_required)
        self.assertEqual(response.capacity_bytes, self.volume_before_expand.capacity_bytes)
        self.mediator.expand_volume.assert_not_called()

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_expand_volume_with_required_bytes_below_minimal(self, storage_agent):
        self._prepare_expand_volume_mocks(storage_agent)
        self.request.capacity_range.required_bytes = 1
        self._test_no_expand_needed()

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_expand_volume_with_required_bytes_zero(self, storage_agent):
        self._prepare_expand_volume_mocks(storage_agent)
        self.request.capacity_range.required_bytes = 0
        self._test_no_expand_needed()

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_expand_volume_with_volume_size_already_in_range(self, storage_agent):
        self._prepare_expand_volume_mocks(storage_agent)
        self.request.capacity_range.required_bytes = 2
        self._test_no_expand_needed()

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_expand_volume_succeeds(self, storage_agent):
        self._prepare_expand_volume_mocks(storage_agent)

        response = self.servicer.ControllerExpandVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.OK)
        self.assertTrue(response.node_expansion_required)
        self.assertEqual(response.capacity_bytes, self.volume_after_expand.capacity_bytes)
        self.mediator.expand_volume.assert_called_once_with(volume_id=self.volume_id,
                                                            required_bytes=self.capacity_bytes)

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_expand_volume_with_bad_id(self, storage_agent):
        self._prepare_expand_volume_mocks(storage_agent)
        self.request.volume_id = "123"

        self.servicer.ControllerExpandVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.INVALID_ARGUMENT)
        self.mediator.expand_volume.assert_not_called()

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_expand_volume_not_found_before_expansion(self, storage_agent):
        self._prepare_expand_volume_mocks(storage_agent)
        self.mediator.get_object_by_id.side_effect = [None, None]

        self.servicer.ControllerExpandVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.NOT_FOUND)

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_expand_volume_not_found_after_expansion(self, storage_agent):
        self._prepare_expand_volume_mocks(storage_agent)
        self.mediator.get_object_by_id.side_effect = [self.volume_before_expand, None]

        self.servicer.ControllerExpandVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.NOT_FOUND)

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_expand_volume_with_wrong_secrets(self, a_enter):
        self._test_create_object_with_wrong_secrets(a_enter)

    @patch("controller.controller_server.csi_controller_server.get_agent")
    def test_expand_volume_with_array_connection_exception(self, storage_agent):
        self._test_create_object_with_array_connection_exception(storage_agent)

    @patch("controller.array_action.array_mediator_xiv.XIVArrayMediator.expand_volume")
    @patch("controller.controller_server.csi_controller_server.get_agent")
    def _expand_volume_returns_error(self, storage_agent, expand_volume, return_code, err):
        storage_agent.return_value = self.storage_agent
        expand_volume.side_effect = [err]
        msg = str(err)

        self.servicer.ControllerExpandVolume(self.request, self.context)

        self.assertEqual(self.context.code, return_code)
        self.assertTrue(msg in self.context.details)
        self.mediator.expand_volume.assert_called_once_with(volume_id=self.volume_id,
                                                            required_bytes=self.capacity_bytes)

    def test_expand_volume_with_illegal_object_id_exception(self):
        self._expand_volume_returns_error(return_code=grpc.StatusCode.INVALID_ARGUMENT,
                                          err=array_errors.IllegalObjectID("123"))

    def test_expand_volume_with_permission_denied_exception(self):
        self._expand_volume_returns_error(return_code=grpc.StatusCode.PERMISSION_DENIED,
                                          err=array_errors.PermissionDeniedError("msg"))

    def test_expand_volume_with_object_not_found_exception(self):
        self._expand_volume_returns_error(return_code=grpc.StatusCode.NOT_FOUND,
                                          err=array_errors.ObjectNotFoundError("name"))

    def test_expand_volume_with_create_volume_with_other_exception(self):
        self._expand_volume_returns_error(return_code=grpc.StatusCode.INTERNAL,
                                          err=Exception("error"))

    def test_expand_volume_with_no_space_in_pool_exception(self):
        self._expand_volume_returns_error(return_code=grpc.StatusCode.RESOURCE_EXHAUSTED,
                                          err=array_errors.NotEnoughSpaceInPool("pool"))


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
