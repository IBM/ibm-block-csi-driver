import abc
import json
import unittest

import grpc
from csi_general import csi_pb2
from mock import patch, Mock, MagicMock, call

import controllers.array_action.errors as array_errors
import controllers.servers.config as config
import controllers.servers.errors as controller_errors
from controllers.array_action.array_action_types import Host, ObjectIds
from controllers.array_action.array_mediator_xiv import XIVArrayMediator
from controllers.servers.csi.csi_controller_server import CSIControllerServicer
from controllers.servers.csi.sync_lock import SyncLock
from controllers.tests import utils
from controllers.tests.controller_server.test_settings import (CLONE_VOLUME_NAME,
                                                               OBJECT_INTERNAL_ID,
                                                               POOL, SPACE_EFFICIENCY,
                                                               IO_GROUP, VOLUME_GROUP,
                                                               VOLUME_NAME, SNAPSHOT_NAME,
                                                               SNAPSHOT_VOLUME_NAME,
                                                               SNAPSHOT_VOLUME_WWN, VIRT_SNAP_FUNC_TRUE)
from controllers.tests.utils import ProtoBufMock


class BaseControllerSetUp(unittest.TestCase):

    def setUp(self):
        patch("controllers.array_action.array_mediator_xiv.XIVArrayMediator._connect").start()
        detect_array_type_patcher = patch("controllers.servers.csi.csi_controller_server.detect_array_type")
        self.detect_array_type = detect_array_type_patcher.start()
        self.detect_array_type.return_value = "a9k"
        self.addCleanup(detect_array_type_patcher.stop)
        self.fqdn = "fqdn"
        self.mediator = XIVArrayMediator("user", "password", self.fqdn)
        self.mediator.client = Mock()

        self.storage_agent = MagicMock()
        self.storage_agent.get_mediator.return_value.__enter__.return_value = self.mediator

        self.servicer = CSIControllerServicer()

        self.request = ProtoBufMock()
        self.request.secrets = {"username": "user", "password": "pass", "management_address": "mg"}

        self.request.parameters = {}
        self.request.volume_context = {}
        self.volume_capability = utils.get_mock_volume_capability()
        self.capacity_bytes = 10
        self.request.capacity_range = Mock()
        self.request.capacity_range.required_bytes = self.capacity_bytes
        self.mediator.maximal_volume_size_in_bytes = 10
        self.mediator.minimal_volume_size_in_bytes = 2
        self.context = utils.FakeContext()


class CommonControllerTest:

    @property
    @abc.abstractmethod
    def tested_method(self):
        raise NotImplementedError

    @property
    @abc.abstractmethod
    def tested_method_response_class(self):
        raise NotImplementedError

    def _test_create_object_with_empty_name(self, storage_agent):
        storage_agent.return_value = self.storage_agent
        self.request.name = ""
        context = utils.FakeContext()
        response = self.tested_method(self.request, context)
        self.assertEqual(grpc.StatusCode.INVALID_ARGUMENT, context.code)
        self.assertIn("name", context.details)
        self.assertEqual(self.tested_method_response_class(), response)

    def _test_request_with_wrong_secrets_parameters(self, secrets, message="secret"):
        context = utils.FakeContext()

        self.request.secrets = secrets
        self.tested_method(self.request, context)
        self.assertEqual(grpc.StatusCode.INVALID_ARGUMENT, context.code)
        self.assertIn(message, context.details)

    def _test_request_with_wrong_secrets(self, storage_agent):
        storage_agent.return_value = self.storage_agent

        secrets = {"password": "pass", "management_address": "mg"}
        self._test_request_with_wrong_secrets_parameters(secrets)

        secrets = {"username": "user", "management_address": "mg"}
        self._test_request_with_wrong_secrets_parameters(secrets)

        secrets = {"username": "user", "password": "pass"}
        self._test_request_with_wrong_secrets_parameters(secrets)

        secrets = utils.get_fake_secret_config(system_id="u-")
        self._test_request_with_wrong_secrets_parameters(secrets, message="system id")

        self.request.secrets = []

    def _test_request_already_processing(self, storage_agent, request_attribute, object_id):
        storage_agent.side_effect = self.storage_agent
        with SyncLock(request_attribute, object_id, "test_request_already_processing"):
            response = self.tested_method(self.request, self.context)
        self.assertEqual(grpc.StatusCode.ABORTED, self.context.code)
        self.assertEqual(self.tested_method_response_class, type(response))

    def _test_request_with_array_connection_exception(self, storage_agent):
        storage_agent.side_effect = [Exception("error")]
        context = utils.FakeContext()
        self.tested_method(self.request, context)
        self.assertEqual(grpc.StatusCode.INTERNAL, context.code)
        self.assertIn("error", context.details)

    def _test_request_with_get_array_type_exception(self, storage_agent):
        storage_agent.return_value = self.storage_agent
        context = utils.FakeContext()
        self.detect_array_type.side_effect = [array_errors.FailedToFindStorageSystemType("endpoint")]
        self.tested_method(self.request, context)
        self.assertEqual(grpc.StatusCode.INTERNAL, context.code)
        msg = array_errors.FailedToFindStorageSystemType("endpoint").message
        self.assertIn(msg, context.details)

    def _test_request_with_wrong_parameters(self, storage_agent):
        storage_agent.return_value = self.storage_agent
        context = utils.FakeContext()
        parameters = [{}, {"": ""}, {"pool": ""}]

        for request_parameters in parameters:
            self.request.parameters = request_parameters
            self.tested_method(self.request, context)
            self.assertEqual(grpc.StatusCode.INVALID_ARGUMENT, context.code)


class TestCreateSnapshot(BaseControllerSetUp, CommonControllerTest):

    @property
    def tested_method(self):
        return self.servicer.CreateSnapshot

    @property
    def tested_method_response_class(self):
        return csi_pb2.CreateSnapshotResponse

    def setUp(self):
        super().setUp()

        self.mediator.get_snapshot = Mock()
        self.mediator.get_snapshot.return_value = None

        self.request.name = SNAPSHOT_NAME
        self.request.source_volume_id = "{}:{};{}".format("A9000", OBJECT_INTERNAL_ID, SNAPSHOT_VOLUME_WWN)
        self.mediator.get_object_by_id = Mock()
        self.mediator.get_object_by_id.return_value = utils.get_mock_mediator_response_volume(10, SNAPSHOT_VOLUME_NAME,
                                                                                              "wwn", "xiv")
        self.context = utils.FakeContext()

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_create_snapshot_with_empty_name(self, a_enter):
        self._test_create_object_with_empty_name(a_enter)

    def _prepare_create_snapshot_mocks(self, storage_agent):
        storage_agent.return_value = self.storage_agent
        self.mediator.get_snapshot = Mock()
        self.mediator.get_snapshot.return_value = None
        self.mediator.create_snapshot = Mock()
        self.mediator.create_snapshot.return_value = utils.get_mock_mediator_response_snapshot(10, SNAPSHOT_NAME, "wwn",
                                                                                               SNAPSHOT_VOLUME_NAME,
                                                                                               "xiv")

    def _test_create_snapshot_succeeds(self, storage_agent, expected_space_efficiency=None, expected_pool=None,
                                       system_id=None):
        self._prepare_create_snapshot_mocks(storage_agent)

        response_snapshot = self.servicer.CreateSnapshot(self.request, self.context)

        self.assertEqual(grpc.StatusCode.OK, self.context.code)
        self.mediator.get_snapshot.assert_called_once_with(SNAPSHOT_VOLUME_WWN, SNAPSHOT_NAME, expected_pool, False)
        self.mediator.create_snapshot.assert_called_once_with(SNAPSHOT_VOLUME_WWN, SNAPSHOT_NAME,
                                                              expected_space_efficiency, expected_pool, False)
        system_id_part = ':{}'.format(system_id) if system_id else ''
        snapshot_id = 'xiv{}:0;wwn'.format(system_id_part)
        self.assertEqual(snapshot_id, response_snapshot.snapshot.snapshot_id)

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_create_snapshot_succeeds(self, storage_agent):
        self._test_create_snapshot_succeeds(storage_agent)

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_create_snapshot_with_pool_parameter_succeeds(self, storage_agent):
        self.request.parameters = {config.PARAMETERS_POOL: POOL}
        self._test_create_snapshot_succeeds(storage_agent, expected_pool=POOL)

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_create_snapshot_with_space_efficiency_parameter_succeeds(self, storage_agent):
        self.mediator.validate_supported_space_efficiency = Mock()
        self.request.parameters = {config.PARAMETERS_SPACE_EFFICIENCY: SPACE_EFFICIENCY}
        self._test_create_snapshot_succeeds(storage_agent, expected_space_efficiency=SPACE_EFFICIENCY)

    def test_create_snapshot_with_space_efficiency_and_virt_snap_func_enabled_fail(self):
        self.request.parameters = {config.PARAMETERS_SPACE_EFFICIENCY: SPACE_EFFICIENCY,
                                   config.PARAMETERS_VIRT_SNAP_FUNC: VIRT_SNAP_FUNC_TRUE}

        self.servicer.CreateSnapshot(self.request, self.context)

        self.assertEqual(grpc.StatusCode.INVALID_ARGUMENT, self.context.code)

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_create_snapshot_already_processing(self, storage_agent):
        self._test_request_already_processing(storage_agent, "name", self.request.name)

    def _test_create_snapshot_with_by_system_id_parameter(self, storage_agent, system_id, expected_pool):
        system_id_part = ':{}'.format(system_id) if system_id else ''
        self.request.source_volume_id = "{}{}:{}".format("A9000", system_id_part, SNAPSHOT_VOLUME_WWN)
        self.request.parameters = {config.PARAMETERS_BY_SYSTEM: json.dumps(
            {"u1": {config.PARAMETERS_POOL: POOL}, "u2": {config.PARAMETERS_POOL: "other_pool"}})}
        self._test_create_snapshot_succeeds(storage_agent, expected_pool=expected_pool, system_id=system_id)

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_create_snapshot_with_by_system_id_parameter_succeeds(self, storage_agent):
        self._test_create_snapshot_with_by_system_id_parameter(storage_agent, "u1", POOL)
        self._test_create_snapshot_with_by_system_id_parameter(storage_agent, "u2", "other_pool")
        self._test_create_snapshot_with_by_system_id_parameter(storage_agent, None, None)

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_create_snapshot_belongs_to_wrong_volume(self, storage_agent):
        storage_agent.return_value = self.storage_agent
        self.mediator.create_snapshot = Mock()
        self.mediator.get_snapshot.return_value = utils.get_mock_mediator_response_snapshot(10, SNAPSHOT_NAME, "wwn",
                                                                                            "wrong_volume_name", "xiv")

        self.servicer.CreateSnapshot(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.ALREADY_EXISTS)

    def test_create_snapshot_no_source_volume(self):
        self.request.source_volume_id = None

        self.servicer.CreateSnapshot(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.INVALID_ARGUMENT)

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_create_snapshot_with_wrong_secrets(self, storage_agent):
        self._test_request_with_wrong_secrets(storage_agent)

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_create_snapshot_with_array_connection_exception(self, storage_agent):
        self._test_request_with_array_connection_exception(storage_agent)

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def _test_create_snapshot_get_snapshot_raise_error(self, storage_agent, exception, grpc_status):
        storage_agent.return_value = self.storage_agent
        self.mediator.get_snapshot.side_effect = [exception]

        self.servicer.CreateSnapshot(self.request, self.context)

        self.assertEqual(self.context.code, grpc_status)
        self.assertIn(str(exception), self.context.details)
        self.mediator.get_snapshot.assert_called_once_with(SNAPSHOT_VOLUME_WWN, SNAPSHOT_NAME, None, False)

    def test_create_snapshot_get_snapshot_exception(self):
        self._test_create_snapshot_get_snapshot_raise_error(exception=Exception("error"),
                                                            grpc_status=grpc.StatusCode.INTERNAL)

    def test_create_snapshot_with_get_snapshot_illegal_object_name_exception(self):
        self._test_create_snapshot_get_snapshot_raise_error(exception=array_errors.InvalidArgumentError("snapshot"),
                                                            grpc_status=grpc.StatusCode.INVALID_ARGUMENT)

    def test_create_snapshot_with_get_snapshot_illegal_object_id_exception(self):
        self._test_create_snapshot_get_snapshot_raise_error(exception=array_errors.InvalidArgumentError("volume-id"),
                                                            grpc_status=grpc.StatusCode.INVALID_ARGUMENT)

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_create_snapshot_with_prefix_too_long_exception(self, storage_agent):
        storage_agent.return_value = self.storage_agent
        self.request.parameters.update({"snapshot_name_prefix": "a" * 128})
        self.servicer.CreateSnapshot(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.INVALID_ARGUMENT)

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_create_snapshot_with_get_snapshot_name_too_long_success(self, storage_agent):
        self._prepare_create_snapshot_mocks(storage_agent)
        self.mediator.max_object_name_length = 63
        self.request.name = "a" * 128

        self.servicer.CreateSnapshot(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.OK)

    @patch("controllers.array_action.array_mediator_xiv.XIVArrayMediator.create_snapshot")
    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def create_snapshot_returns_error(self, storage_agent, create_snapshot, return_code, err):
        storage_agent.return_value = self.storage_agent
        create_snapshot.side_effect = [err]
        msg = str(err)

        self.servicer.CreateSnapshot(self.request, self.context)

        self.assertEqual(self.context.code, return_code)
        self.assertIn(msg, self.context.details)
        self.mediator.get_snapshot.assert_called_once_with(SNAPSHOT_VOLUME_WWN, SNAPSHOT_NAME, None, False)
        self.mediator.create_snapshot.assert_called_once_with(SNAPSHOT_VOLUME_WWN, SNAPSHOT_NAME, None, None, False)

    def test_create_snapshot_with_not_found_exception(self):
        self.create_snapshot_returns_error(return_code=grpc.StatusCode.NOT_FOUND,
                                           err=array_errors.ObjectNotFoundError("source_volume"))

    def test_create_snapshot_with_illegal_object_name_exception(self):
        self.create_snapshot_returns_error(return_code=grpc.StatusCode.INVALID_ARGUMENT,
                                           err=array_errors.InvalidArgumentError("snapshot"))

    def test_create_snapshot_with_snapshot_source_pool_mismatch_exception(self):
        self.create_snapshot_returns_error(return_code=grpc.StatusCode.INVALID_ARGUMENT,
                                           err=array_errors.SnapshotSourcePoolMismatch("snapshot_pool", "source_pool"))

    def test_create_snapshot_with_same_volume_name_exists_exception(self):
        self.create_snapshot_returns_error(return_code=grpc.StatusCode.INTERNAL,
                                           err=array_errors.ExpectedSnapshotButFoundVolumeError("snapshot",
                                                                                                "endpoint"))

    def test_create_snapshot_with_illegal_object_id_exception(self):
        self.create_snapshot_returns_error(return_code=grpc.StatusCode.INVALID_ARGUMENT,
                                           err=array_errors.InvalidArgumentError("volume-id"))

    def test_create_snapshot_with_space_efficiency_not_supported_exception(self):
        self.create_snapshot_returns_error(return_code=grpc.StatusCode.INVALID_ARGUMENT,
                                           err=array_errors.SpaceEfficiencyNotSupported(["fake"]))

    def test_create_snapshot_with_other_exception(self):
        self.create_snapshot_returns_error(return_code=grpc.StatusCode.INTERNAL, err=Exception("error"))

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_create_snapshot_with_name_prefix(self, storage_agent):
        storage_agent.return_value = self.storage_agent
        self.request.name = "some_name"
        self.request.parameters[config.PARAMETERS_SNAPSHOT_NAME_PREFIX] = "prefix"
        self.mediator.create_snapshot = Mock()
        self.mediator.create_snapshot.return_value = utils.get_mock_mediator_response_snapshot(10, SNAPSHOT_NAME, "wwn",
                                                                                               SNAPSHOT_VOLUME_NAME,
                                                                                               "xiv")

        self.servicer.CreateSnapshot(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.OK)
        self.mediator.create_snapshot.assert_called_once_with(SNAPSHOT_VOLUME_WWN, "prefix_some_name", None, None,
                                                              False)


class TestDeleteSnapshot(BaseControllerSetUp, CommonControllerTest):
    @property
    def tested_method(self):
        return self.servicer.DeleteSnapshot

    @property
    def tested_method_response_class(self):
        return csi_pb2.DeleteSnapshotResponse

    def setUp(self):
        super().setUp()
        self.mediator.get_snapshot = Mock()
        self.mediator.get_snapshot.return_value = None
        self.mediator.delete_snapshot = Mock()
        self.request.snapshot_id = "A9000:0;BADC0FFEE0DDF00D00000000DEADBABE"

    @patch("controllers.array_action.array_mediator_xiv.XIVArrayMediator.delete_snapshot", Mock())
    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def _test_delete_snapshot_succeeds(self, snapshot_id, storage_agent):
        storage_agent.return_value = self.storage_agent
        self.request.snapshot_id = snapshot_id

        self.servicer.DeleteSnapshot(self.request, self.context)
        self.assertEqual(self.context.code, grpc.StatusCode.OK)

    def test_delete_snapshot_with_internal_id_succeeds(self):
        self._test_delete_snapshot_succeeds("xiv:0;volume-id")
        self.mediator.delete_snapshot.assert_called_once()

    def test_delete_snapshot_with_system_id_succeeds(self):
        self._test_delete_snapshot_succeeds("xiv:system_id:volume-id")
        self.mediator.delete_snapshot.assert_called_once()

    def test_delete_snapshot_with_system_id_internal_id_succeeds(self):
        self._test_delete_snapshot_succeeds("xiv:system_id:0;volume-id")
        self.mediator.delete_snapshot.assert_called_once()

    def test_delete_snapshot_no_internal_id_succeeds(self):
        self._test_delete_snapshot_succeeds("xiv:volume-id")
        self.mediator.delete_snapshot.assert_called_once()

    def test_delete_snapshot_bad_id_succeeds(self):
        self._test_delete_snapshot_succeeds("xiv:a:a:volume-id")
        self.mediator.delete_snapshot.assert_not_called()

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_delete_snapshot_already_processing(self, storage_agent):
        self._test_request_already_processing(storage_agent, "snapshot_id", self.request.snapshot_id)

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_delete_snapshot_with_wrong_secrets(self, storage_agent):
        self._test_request_with_wrong_secrets(storage_agent)

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_delete_snapshot_with_array_connection_exception(self, storage_agent):
        self._test_request_with_array_connection_exception(storage_agent)

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_delete_snapshot_invalid_snapshot_id(self, storage_agent):
        storage_agent.return_value = self.storage_agent
        self.request.snapshot_id = "wrong_id"

        self.servicer.DeleteSnapshot(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.OK)


class TestCreateVolume(BaseControllerSetUp, CommonControllerTest):

    @property
    def tested_method(self):
        return self.servicer.CreateVolume

    @property
    def tested_method_response_class(self):
        return csi_pb2.CreateVolumeResponse

    def setUp(self):
        super().setUp()

        self.mediator.get_volume = Mock()
        self.mediator.get_volume.side_effect = array_errors.ObjectNotFoundError("vol")
        self.mediator.validate_space_efficiency_matches_source = Mock()

        self.request.parameters = {config.PARAMETERS_POOL: POOL,
                                   config.PARAMETERS_IO_GROUP: IO_GROUP,
                                   config.PARAMETERS_VOLUME_GROUP: VOLUME_GROUP}
        self.request.volume_capabilities = [self.volume_capability]
        self.request.name = VOLUME_NAME
        self.request.volume_content_source = None

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_create_volume_with_empty_name(self, storage_agent):
        self._test_create_object_with_empty_name(storage_agent)

    def _prepare_create_volume_mocks(self, storage_agent):
        storage_agent.return_value = self.storage_agent

        self.mediator.create_volume = Mock()
        self.mediator.create_volume.return_value = utils.get_mock_mediator_response_volume(10, "volume", "wwn", "xiv")

    def _test_create_volume_succeeds(self, storage_agent, expected_volume_id, expected_pool=POOL):
        self._prepare_create_volume_mocks(storage_agent)

        response_volume = self.servicer.CreateVolume(self.request, self.context)
        self.assertEqual(self.context.code, grpc.StatusCode.OK)
        self.mediator.get_volume.assert_called_once_with(VOLUME_NAME, expected_pool, False)
        self.mediator.create_volume.assert_called_once_with(VOLUME_NAME, 10, None, expected_pool, IO_GROUP,
                                                            VOLUME_GROUP,
                                                            ObjectIds(internal_id='', uid=''), None, False)
        self.assertEqual(response_volume.volume.content_source.volume.volume_id, '')
        self.assertEqual(response_volume.volume.content_source.snapshot.snapshot_id, '')
        self.assertEqual(response_volume.volume.volume_id, expected_volume_id)

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_create_volume_already_processing(self, storage_agent):
        self._test_request_already_processing(storage_agent, "name", self.request.name)

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_create_volume_succeeds(self, storage_agent):
        self._test_create_volume_succeeds(storage_agent, 'xiv:0;wwn')

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_create_volume_with_topologies_succeeds(self, storage_agent):
        self.request.secrets = utils.get_fake_secret_config(system_id="u2", supported_topologies=[
            {"topology.block.csi.ibm.com/test": "topology_value"}])
        self.request.accessibility_requirements.preferred = [
            ProtoBufMock(segments={"topology.block.csi.ibm.com/test": "topology_value",
                                   "topology.block.csi.ibm.com/test2": "topology_value2"})]
        second_system_parameters = self.request.parameters.copy()
        second_system_parameters[config.PARAMETERS_POOL] = "other_pool"
        self.request.parameters = {config.PARAMETERS_BY_SYSTEM: json.dumps(
            {"u1": self.request.parameters, "u2": second_system_parameters})}
        self._test_create_volume_succeeds(storage_agent, 'xiv:u2:0;wwn', expected_pool="other_pool")

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_create_volume_with_space_efficiency_succeeds(self, storage_agent):
        self._prepare_create_volume_mocks(storage_agent)
        self.request.parameters.update({config.PARAMETERS_SPACE_EFFICIENCY: "not_none"})
        self.mediator.validate_supported_space_efficiency = Mock()

        self.servicer.CreateVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.OK)
        self.mediator.get_volume.assert_called_once_with(VOLUME_NAME, POOL, False)
        self.mediator.create_volume.assert_called_once_with(VOLUME_NAME, 10, "not_none", POOL, IO_GROUP, VOLUME_GROUP,
                                                            ObjectIds(internal_id='', uid=''), None, False)

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_create_volume_idempotent_no_source_succeeds(self, storage_agent):
        self._prepare_create_volume_mocks(storage_agent)
        self.mediator.get_volume = Mock()
        self.mediator.get_volume.return_value = utils.get_mock_mediator_response_volume(10, VOLUME_NAME, "wwn", "xiv")

        response_volume = self.servicer.CreateVolume(self.request, self.context)
        self.assertEqual(self.context.code, grpc.StatusCode.OK)
        self.mediator.get_volume.assert_called_once_with(VOLUME_NAME, POOL, False)
        self.mediator.create_volume.assert_not_called()
        self.assertEqual(response_volume.volume.content_source.volume.volume_id, '')
        self.assertEqual(response_volume.volume.content_source.snapshot.snapshot_id, '')

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_create_volume_with_wrong_secrets(self, a_enter):
        self._test_request_with_wrong_secrets(a_enter)

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_create_volume_no_pool(self, storage_agent):
        self._prepare_create_volume_mocks(storage_agent)
        self.request.parameters = {"by_system_id": json.dumps({"u1": POOL, "u2": "other_pool"})}
        self.servicer.CreateVolume(self.request, self.context)
        self.assertEqual(self.context.code, grpc.StatusCode.INVALID_ARGUMENT)

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_create_volume_with_wrong_parameters(self, storage_agent):
        self._test_request_with_wrong_parameters(storage_agent)

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_create_volume_with_wrong_volume_capabilities(self, storage_agent):
        storage_agent.return_value = self.storage_agent

        volume_capability = utils.get_mock_volume_capability(fs_type="ext42")
        self.request.volume_capabilities = [volume_capability]

        self.servicer.CreateVolume(self.request, self.context)
        self.assertEqual(self.context.code, grpc.StatusCode.INVALID_ARGUMENT, "wrong fs_type")
        self.assertIn("fs_type", self.context.details)

        access_mode = csi_pb2.VolumeCapability.AccessMode
        volume_capability = utils.get_mock_volume_capability(mode=access_mode.MULTI_NODE_SINGLE_WRITER)
        self.request.volume_capabilities = [volume_capability]

        self.servicer.CreateVolume(self.request, self.context)
        self.assertEqual(self.context.code, grpc.StatusCode.INVALID_ARGUMENT)
        self.assertTrue("access mode" in self.context.details)

        volume_capability = utils.get_mock_volume_capability(mount_flags=["no_formatting"])
        self.request.volume_capabilities = [volume_capability]

        self.servicer.CreateVolume(self.request, self.context)
        self.assertEqual(self.context.code, grpc.StatusCode.INVALID_ARGUMENT)
        self.assertTrue("mount_flags is unsupported" in self.context.details)

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_create_volume_with_array_connection_exception(self, storage_agent):
        self._test_request_with_array_connection_exception(storage_agent)

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_create_volume_with_get_array_type_exception(self, storage_agent):
        self._test_request_with_get_array_type_exception(storage_agent)

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_create_volume_get_volume_exception(self, storage_agent):
        storage_agent.return_value = self.storage_agent
        self.mediator.get_volume.side_effect = [Exception("error")]

        self.servicer.CreateVolume(self.request, self.context)
        self.assertEqual(self.context.code, grpc.StatusCode.INTERNAL)
        self.assertIn("error", self.context.details)
        self.mediator.get_volume.assert_called_once_with(VOLUME_NAME, POOL, False)

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_create_volume_with_get_volume_illegal_object_name_exception(self, storage_agent):
        storage_agent.return_value = self.storage_agent
        self.mediator.get_volume.side_effect = [array_errors.InvalidArgumentError("volume")]

        self.servicer.CreateVolume(self.request, self.context)
        msg = array_errors.InvalidArgumentError("volume").message

        self.assertEqual(self.context.code, grpc.StatusCode.INVALID_ARGUMENT)
        self.assertIn(msg, self.context.details)
        self.mediator.get_volume.assert_called_once_with(VOLUME_NAME, POOL, False)

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_create_volume_with_prefix_too_long_exception(self, storage_agent):
        storage_agent.return_value = self.storage_agent
        self.request.parameters.update({"volume_name_prefix": "a" * 128})
        self.servicer.CreateVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.INVALID_ARGUMENT)

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_create_volume_with_get_volume_name_too_long_success(self, storage_agent):
        self._prepare_create_volume_mocks(storage_agent)
        self.mediator.max_object_name_length = 63

        self.request.name = "a" * 128
        self.servicer.CreateVolume(self.request, self.context)
        self.assertEqual(self.context.code, grpc.StatusCode.OK)

    @patch("controllers.array_action.array_mediator_xiv.XIVArrayMediator.create_volume")
    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def create_volume_returns_error(self, storage_agent, create_volume, return_code, err):
        storage_agent.return_value = self.storage_agent
        create_volume.side_effect = [err]

        self.servicer.CreateVolume(self.request, self.context)
        msg = str(err)

        self.assertEqual(self.context.code, return_code)
        self.assertIn(msg, self.context.details)
        self.mediator.get_volume.assert_called_once_with(VOLUME_NAME, POOL, False)
        self.mediator.create_volume.assert_called_once_with(VOLUME_NAME, self.capacity_bytes, None, POOL, IO_GROUP,
                                                            VOLUME_GROUP, ObjectIds(internal_id='', uid=''),
                                                            None, False)

    def test_create_volume_with_illegal_object_name_exception(self):
        self.create_volume_returns_error(return_code=grpc.StatusCode.INVALID_ARGUMENT,
                                         err=array_errors.InvalidArgumentError("volume"))

    def test_create_volume_with_volume_exists_exception(self):
        self.create_volume_returns_error(return_code=grpc.StatusCode.ALREADY_EXISTS,
                                         err=array_errors.VolumeAlreadyExists("volume", "endpoint"))

    def test_create_volume_with_pool_does_not_exist_exception(self):
        self.create_volume_returns_error(return_code=grpc.StatusCode.INVALID_ARGUMENT,
                                         err=array_errors.PoolDoesNotExist(POOL, "endpoint"))

    def test_create_volume_with_pool_does_not_match_space_efficiency_exception(self):
        self.create_volume_returns_error(return_code=grpc.StatusCode.INVALID_ARGUMENT,
                                         err=array_errors.PoolDoesNotMatchSpaceEfficiency("pool1", "", "endpoint"))

    def test_create_volume_with_space_efficiency_not_supported_exception(self):
        self.create_volume_returns_error(return_code=grpc.StatusCode.INVALID_ARGUMENT,
                                         err=array_errors.SpaceEfficiencyNotSupported(["fake"]))

    def test_create_volume_with_other_exception(self):
        self.create_volume_returns_error(return_code=grpc.StatusCode.INTERNAL,
                                         err=Exception("error"))

    def _test_create_volume_parameters(self, final_name="default_some_name", space_efficiency=None):
        self.mediator.default_object_prefix = "default"
        self.request.name = "some_name"
        self.mediator.create_volume = Mock()
        self.mediator.create_volume.return_value = utils.get_mock_mediator_response_volume(10, "volume", "wwn", "xiv")
        self.mediator.validate_supported_space_efficiency = Mock()
        self.servicer.CreateVolume(self.request, self.context)
        self.assertEqual(self.context.code, grpc.StatusCode.OK)
        self.mediator.create_volume.assert_called_once_with(final_name, 10, space_efficiency, POOL, IO_GROUP,
                                                            VOLUME_GROUP, ObjectIds(internal_id='', uid=''),
                                                            None, False)

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_create_volume_with_name_prefix(self, storage_agent):
        storage_agent.return_value = self.storage_agent
        self.request.parameters[config.PARAMETERS_VOLUME_NAME_PREFIX] = "prefix"
        self._test_create_volume_parameters("prefix_some_name")

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_create_volume_with_no_name_prefix(self, storage_agent):
        storage_agent.return_value = self.storage_agent
        self.request.parameters[config.PARAMETERS_VOLUME_NAME_PREFIX] = ""
        self._test_create_volume_parameters()

    def _test_create_volume_with_parameters_by_system_prefix(self, get_array_connection_info_from_secrets, prefix,
                                                             final_name="default_some_name",
                                                             space_efficiency=None):
        get_array_connection_info_from_secrets.side_effect = [utils.get_fake_array_connection_info()]
        system_parameters = self.request.parameters
        system_parameters.update({config.PARAMETERS_VOLUME_NAME_PREFIX: prefix,
                                  config.PARAMETERS_SPACE_EFFICIENCY: space_efficiency})
        self.request.parameters = {config.PARAMETERS_BY_SYSTEM: json.dumps({"u1": system_parameters})}
        self._test_create_volume_parameters(final_name, space_efficiency)

    @patch("controllers.servers.utils.get_array_connection_info_from_secrets")
    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_create_volume_with_parameters_by_system_no_name_prefix(self, storage_agent,
                                                                    get_array_connection_info_from_secrets):
        storage_agent.return_value = self.storage_agent
        self._test_create_volume_with_parameters_by_system_prefix(get_array_connection_info_from_secrets, "")

    @patch("controllers.servers.utils.get_array_connection_info_from_secrets")
    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_create_volume_with_parameters_by_system_name_prefix(self, storage_agent,
                                                                 get_array_connection_info_from_secrets):
        storage_agent.return_value = self.storage_agent
        self._test_create_volume_with_parameters_by_system_prefix(get_array_connection_info_from_secrets, "prefix",
                                                                  "prefix_some_name")

    @patch("controllers.servers.utils.get_array_connection_info_from_secrets")
    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_create_volume_with_parameters_by_system_space_efficiency(self, storage_agent,
                                                                      get_array_connection_info_from_secrets):
        storage_agent.return_value = self.storage_agent
        self._test_create_volume_with_parameters_by_system_prefix(get_array_connection_info_from_secrets, "",
                                                                  space_efficiency="not_none")

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_create_volume_with_required_bytes_zero(self, storage_agent):
        self._prepare_create_volume_mocks(storage_agent)
        self.request.capacity_range.required_bytes = 0

        self.servicer.CreateVolume(self.request, self.context)
        self.assertEqual(self.context.code, grpc.StatusCode.OK)
        self.mediator.create_volume.assert_called_once_with(self.request.name, 2, None, POOL, IO_GROUP, VOLUME_GROUP,
                                                            ObjectIds(internal_id='', uid=''), None, False)

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_create_volume_with_required_bytes_too_large_fail(self, storage_agent):
        self._prepare_create_volume_mocks(storage_agent)
        self.request.capacity_range.required_bytes = 11

        self.servicer.CreateVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.OUT_OF_RANGE)
        self.mediator.create_volume.assert_not_called()

    def test_create_volume_with_no_space_in_pool(self):
        self.create_volume_returns_error(return_code=grpc.StatusCode.INTERNAL,
                                         err=array_errors.NotEnoughSpaceInPool("pool"))

    def _prepare_idempotent_tests(self):
        self.mediator.get_volume = Mock()
        self.mediator.copy_to_existing_volume = Mock()
        self.request.volume_content_source = self._get_source_snapshot("wwn1")

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_create_volume_idempotent_with_source_succeed(self, storage_agent):
        self._prepare_idempotent_tests()
        storage_agent.return_value = self.storage_agent
        snapshot_id = "wwn1"
        self.mediator.get_volume.return_value = utils.get_mock_mediator_response_volume(10, VOLUME_NAME, "wwn2", "a9k",
                                                                                        source_id=snapshot_id)

        response = self.servicer.CreateVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.OK)
        self.assertEqual(response.volume.content_source.snapshot.snapshot_id, snapshot_id)
        self.mediator.copy_to_existing_volume.assert_not_called()

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_create_volume_idempotent_with_source_volume_have_no_source(self, storage_agent):
        self._prepare_idempotent_tests()
        storage_agent.return_value = self.storage_agent
        self.mediator.get_volume.return_value = utils.get_mock_mediator_response_volume(10, VOLUME_NAME, "wwn2",
                                                                                        "a9k")
        response = self.servicer.CreateVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.ALREADY_EXISTS)
        self.assertFalse(response.HasField("volume"))
        self.mediator.copy_to_existing_volume.assert_not_called()

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_create_volume_idempotent_source_not_requested_but_found_in_volume(self, storage_agent):
        self._prepare_idempotent_tests()
        storage_agent.return_value = self.storage_agent
        snapshot_id = "wwn1"
        self.request.volume_content_source = None
        self.mediator.get_volume.return_value = utils.get_mock_mediator_response_volume(10, VOLUME_NAME, "wwn2", "a9k",
                                                                                        source_id=snapshot_id)
        response = self.servicer.CreateVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.ALREADY_EXISTS)
        self.assertFalse(response.HasField("volume"))
        self.mediator.copy_to_existing_volume.assert_not_called()

    def _prepare_idempotent_test_with_other_source(self, storage_agent):
        self._prepare_idempotent_tests()
        storage_agent.return_value = self.storage_agent
        volume_source_id = "wwn3"
        self.mediator.get_volume.return_value = utils.get_mock_mediator_response_volume(10, "volume", "wwn2", "a9k",
                                                                                        source_id=volume_source_id)
        self.servicer.CreateVolume(self.request, self.context)
        self.mediator.copy_to_existing_volume.assert_not_called()

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_create_volume_idempotent_with_source_volume_got_other_source(self, storage_agent):
        self._prepare_idempotent_test_with_other_source(storage_agent)
        self.assertEqual(self.context.code, grpc.StatusCode.ALREADY_EXISTS)

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_create_volume_idempotent_with_other_source_and_virt_snap_func_enabled(self, storage_agent):

        self.request.parameters[config.PARAMETERS_VIRT_SNAP_FUNC] = "true"
        self._prepare_idempotent_test_with_other_source(storage_agent)
        self.assertEqual(self.context.code, grpc.StatusCode.OK)

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_create_volume_idempotent_with_size_not_matched(self, storage_agent):
        storage_agent.return_value = self.storage_agent
        self.mediator.get_volume = Mock()
        self.mediator.get_volume.return_value = utils.get_mock_mediator_response_volume(9, VOLUME_NAME, "wwn2", "a9k")

        self.servicer.CreateVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.ALREADY_EXISTS)

    def _prepare_mocks_for_copy_from_source(self):
        self.mediator.create_volume = Mock()
        self.mediator.create_volume.return_value = utils.get_mock_mediator_response_volume(10, VOLUME_NAME, "wwn2",
                                                                                           "a9k")
        self.mediator.get_object_by_id = Mock()
        self.mediator.copy_to_existing_volume = Mock()

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_create_volume_from_snapshot_success(self, storage_agent):
        storage_agent.return_value = self.storage_agent
        self._prepare_mocks_for_copy_from_source()
        snapshot_id = "wwn1"
        snapshot_capacity_bytes = 100
        self.request.volume_content_source = self._get_source_snapshot(snapshot_id)
        self.mediator.get_object_by_id.return_value = utils.get_mock_mediator_response_snapshot(snapshot_capacity_bytes,
                                                                                                SNAPSHOT_NAME,
                                                                                                snapshot_id,
                                                                                                VOLUME_NAME,
                                                                                                "a9k")
        response_volume = self.servicer.CreateVolume(self.request, self.context)
        self.assertEqual(self.context.code, grpc.StatusCode.OK)
        self.mediator.copy_to_existing_volume.assert_called_once_with('wwn2', snapshot_id,
                                                                      snapshot_capacity_bytes,
                                                                      self.capacity_bytes)
        self.assertEqual(response_volume.volume.content_source.volume.volume_id, '')
        self.assertEqual(response_volume.volume.content_source.snapshot.snapshot_id, snapshot_id)

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
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
        self.assertIn("invalid_snapshot_id", self.context.details)

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_create_volume_from_source_illegal_object_id(self, storage_agent):
        array_exception = array_errors.InvalidArgumentError("")
        self._test_create_volume_from_snapshot_error(storage_agent, array_exception,
                                                     grpc.StatusCode.INVALID_ARGUMENT)

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_create_volume_from_source_permission_denied(self, storage_agent):
        array_exception = array_errors.PermissionDeniedError("")
        self._test_create_volume_from_snapshot_error(storage_agent, array_exception,
                                                     grpc.StatusCode.PERMISSION_DENIED)

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_create_volume_from_source_pool_missing(self, storage_agent):
        array_exception = array_errors.PoolParameterIsMissing("")
        self._test_create_volume_from_snapshot_error(storage_agent, array_exception,
                                                     grpc.StatusCode.INVALID_ARGUMENT)

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_create_volume_from_source_general_error(self, storage_agent):
        array_exception = Exception("")
        self._test_create_volume_from_snapshot_error(storage_agent, array_exception,
                                                     grpc.StatusCode.INTERNAL)

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_create_volume_from_source_get_object_general_error(self, storage_agent):
        array_exception = Exception("")
        self._test_create_volume_from_snapshot_error(storage_agent, None,
                                                     grpc.StatusCode.INTERNAL, get_exception=array_exception)

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_create_volume_from_source_get_object_error(self, storage_agent):
        array_exception = array_errors.ExpectedSnapshotButFoundVolumeError("", "")
        self._test_create_volume_from_snapshot_error(storage_agent, None,
                                                     grpc.StatusCode.INVALID_ARGUMENT, get_exception=array_exception)

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
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
            self.mediator.get_object_by_id.return_value = utils.get_mock_mediator_response_snapshot(1000, SNAPSHOT_NAME,
                                                                                                    target_volume_id,
                                                                                                    VOLUME_NAME, "a9k")
            self.mediator.copy_to_existing_volume.side_effect = [copy_exception]

            self.storage_agent.get_mediator.return_value.__exit__.side_effect = [copy_exception]
        self.mediator.delete_volume = Mock()

        response = self.servicer.CreateVolume(self.request, self.context)
        self.mediator.delete_volume.assert_called_with(target_volume_id)
        self.assertEqual(self.context.code, return_code)
        self.assertIsInstance(response, csi_pb2.CreateVolumeResponse)

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_clone_volume_success(self, storage_agent):
        storage_agent.return_value = self.storage_agent
        self._prepare_mocks_for_copy_from_source()
        volume_id = "wwn1"
        volume_capacity_bytes = 100
        self.request.volume_content_source = self._get_source_volume(volume_id)
        self.mediator.get_object_by_id.return_value = utils.get_mock_mediator_response_volume(volume_capacity_bytes,
                                                                                              CLONE_VOLUME_NAME,
                                                                                              volume_id,
                                                                                              "a9k")
        response_volume = self.servicer.CreateVolume(self.request, self.context)
        self.assertEqual(self.context.code, grpc.StatusCode.OK)
        self.mediator.copy_to_existing_volume.assert_called_once_with('wwn2', volume_id,
                                                                      volume_capacity_bytes,
                                                                      self.capacity_bytes)
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


class TestDeleteVolume(BaseControllerSetUp, CommonControllerTest):

    @property
    def tested_method(self):
        return self.servicer.DeleteVolume

    @property
    def tested_method_response_class(self):
        return csi_pb2.DeleteVolumeResponse

    def get_create_object_method(self):
        return self.servicer.DeleteVolume

    def get_create_object_response_method(self):
        return csi_pb2.DeleteVolumeResponse

    def setUp(self):
        super().setUp()

        self.mediator.get_volume = Mock()
        self.mediator.is_volume_has_snapshots = Mock()
        self.mediator.is_volume_has_snapshots.return_value = False

        self.request.volume_id = "xiv:0;volume-id"

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_delete_volume_already_processing(self, storage_agent):
        self._test_request_already_processing(storage_agent, "volume_id", self.request.volume_id)

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_delete_volume_with_wrong_secrets(self, storage_agent):
        self._test_request_with_wrong_secrets(storage_agent)

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_delete_volume_invalid_volume_id(self, storage_agent):
        storage_agent.return_value = self.storage_agent
        self.request.volume_id = "wrong_id"

        self.servicer.DeleteVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.OK)

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_delete_volume_with_array_connection_exception(self, storage_agent):
        storage_agent.side_effect = [Exception("a_enter error")]

        self.servicer.DeleteVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.INTERNAL)
        self.assertTrue("a_enter error" in self.context.details)

    @patch("controllers.array_action.array_mediator_xiv.XIVArrayMediator.delete_volume")
    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def delete_volume_returns_error(self, storage_agent, delete_volume, error, return_code):
        storage_agent.return_value = self.storage_agent
        delete_volume.side_effect = [error]

        self.servicer.DeleteVolume(self.request, self.context)

        self.assertEqual(self.context.code, return_code)
        if return_code != grpc.StatusCode.OK:
            msg = str(error)
            self.assertIn(msg, self.context.details, "msg : {0} is not in : {1}".format(msg, self.context.details))

    def test_delete_volume_with_volume_not_found_error(self, ):
        self.delete_volume_returns_error(error=array_errors.ObjectNotFoundError("volume"),
                                         return_code=grpc.StatusCode.OK)

    def test_delete_volume_with_delete_volume_other_exception(self):
        self.delete_volume_returns_error(error=Exception("error"), return_code=grpc.StatusCode.INTERNAL)

    def test_delete_volume_has_snapshots(self):
        self.delete_volume_returns_error(error=array_errors.ObjectIsStillInUseError("a", ["b"]),
                                         return_code=grpc.StatusCode.FAILED_PRECONDITION)

    @patch("controllers.array_action.array_mediator_xiv.XIVArrayMediator.delete_volume")
    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def _test_delete_volume_succeeds(self, volume_id, storage_agent, delete_volume):
        storage_agent.return_value = self.storage_agent
        delete_volume.return_value = Mock()
        self.request.volume_id = volume_id
        self.servicer.DeleteVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.OK)

    def test_delete_volume_with_internal_id_succeeds(self):
        self._test_delete_volume_succeeds("xiv:0;volume-id")

    def test_delete_volume_with_system_id_succeeds(self):
        self._test_delete_volume_succeeds("xiv:system_id:volume-id")

    def test_delete_volume_with_system_id_internal_id_succeeds(self):
        self._test_delete_volume_succeeds("xiv:system_id:0;volume-id")

    def test_delete_volume_no_internal_id_succeeds(self):
        self._test_delete_volume_succeeds("xiv:volume-id")


class TestPublishVolume(BaseControllerSetUp, CommonControllerTest):

    @property
    def tested_method(self):
        return self.servicer.ControllerPublishVolume

    @property
    def tested_method_response_class(self):
        return csi_pb2.ControllerPublishVolumeResponse

    def setUp(self):
        super().setUp()

        self.hostname = "hostname"
        self.mediator.get_host_by_host_identifiers = Mock()
        self.mediator.get_host_by_host_identifiers.return_value = self.hostname, ["iscsi"]

        self.mediator.get_volume_mappings = Mock()
        self.mediator.get_volume_mappings.return_value = {}

        self.mediator.map_volume = Mock()
        self.mediator.map_volume.return_value = 1

        self.mediator.get_iscsi_targets_by_iqn = Mock()
        self.mediator.get_iscsi_targets_by_iqn.return_value = {"iqn1": ["1.1.1.1", "2.2.2.2"], "iqn2": ["[::1]"]}

        arr_type = XIVArrayMediator.array_type
        self.request.volume_id = "{}:wwn1".format(arr_type)
        self.iqn = "iqn.1994-05.com.redhat:686358c930fe"
        self.fc_port = "500143802426baf4"
        self.request.node_id = "{};;{};{}".format(self.hostname, self.fc_port, self.iqn)
        self.request.readonly = False
        self.request.readonly = False

        self.request.volume_capability = utils.get_mock_volume_capability()

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_publish_volume_success(self, storage_agent):
        storage_agent.return_value = self.storage_agent

        self.servicer.ControllerPublishVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.OK)

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_publish_volume_already_processing(self, storage_agent):
        self._test_request_already_processing(storage_agent, "volume_id", self.request.volume_id)

    @patch("controllers.servers.utils.validate_publish_volume_request")
    def test_publish_volume_validateion_exception(self, publish_validation):
        publish_validation.side_effect = [controller_errors.ValidationException("msg")]

        self.servicer.ControllerPublishVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.INVALID_ARGUMENT)
        self.assertIn("msg", self.context.details)

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_publish_volume_with_wrong_secrets(self, storage_agent):
        self._test_request_with_wrong_secrets(storage_agent)

    def test_publish_volume_wrong_volume_id(self):
        self.request.volume_id = "some-wrong-id-format"

        self.servicer.ControllerPublishVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.NOT_FOUND)

    def test_publish_volume_wrong_node_id(self):
        self.request.node_id = "some-wrong-id-format"

        self.servicer.ControllerPublishVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.NOT_FOUND)

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
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

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_publish_volume_get_volume_mappings_one_map_for_existing_host(self, storage_agent):
        self.mediator.get_volume_mappings = Mock()
        self.mediator.get_volume_mappings.return_value = {self.hostname: 2}
        self.mediator.get_host_by_name = Mock()
        self.mediator.get_host_by_name.return_value = Host(name=self.hostname, connectivity_types=['iscsi'],
                                                           iscsi_iqns=[self.iqn])
        storage_agent.return_value = self.storage_agent

        response = self.servicer.ControllerPublishVolume(self.request, self.context)
        self.assertEqual(self.context.code, grpc.StatusCode.OK)

        self.assertEqual(response.publish_context["PUBLISH_CONTEXT_LUN"], '2')
        self.assertEqual(response.publish_context["PUBLISH_CONTEXT_CONNECTIVITY"], "iscsi")

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_publish_volume_with_connectivity_type_fc(self, storage_agent):
        self.mediator.get_host_by_host_identifiers.return_value = self.hostname, ["iscsi", "fc"]
        self.mediator.get_array_fc_wwns = Mock()
        self.mediator.get_array_fc_wwns.return_value = ["500143802426baf4"]
        storage_agent.return_value = self.storage_agent

        response = self.servicer.ControllerPublishVolume(self.request, self.context)
        self.assertEqual(self.context.code, grpc.StatusCode.OK)

        self.assertEqual(response.publish_context["PUBLISH_CONTEXT_LUN"], '1')
        self.assertEqual(response.publish_context["PUBLISH_CONTEXT_CONNECTIVITY"], "fc")
        self.assertEqual(response.publish_context["PUBLISH_CONTEXT_ARRAY_FC_INITIATORS"], "500143802426baf4")

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_publish_volume_with_connectivity_type_iscsi(self, storage_agent):
        self.mediator.get_host_by_host_identifiers.return_value = self.hostname, ["iscsi"]
        self.mediator.get_array_fc_wwns = Mock()
        self.mediator.get_array_fc_wwns.return_value = ["500143802426baf4"]
        storage_agent.return_value = self.storage_agent

        response = self.servicer.ControllerPublishVolume(self.request, self.context)
        self.assertEqual(self.context.code, grpc.StatusCode.OK)

        self.assertEqual(response.publish_context["PUBLISH_CONTEXT_LUN"], '1')
        self.assertEqual(response.publish_context["PUBLISH_CONTEXT_CONNECTIVITY"],
                         "iscsi")
        self.assertEqual(response.publish_context["PUBLISH_CONTEXT_ARRAY_IQN"],
                         "iqn1,iqn2")
        self.assertEqual(response.publish_context["iqn1"],
                         "1.1.1.1,2.2.2.2")
        self.assertEqual(response.publish_context["iqn2"],
                         "[::1]")

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_publish_volume_with_node_id_only_has_iqns(self, storage_agent):
        self.request.node_id = "hostname;iqn.1994-05.com.redhat:686358c930fe;"
        self.mediator.get_host_by_host_identifiers.return_value = self.hostname, ["iscsi"]
        storage_agent.return_value = self.storage_agent

        response = self.servicer.ControllerPublishVolume(self.request, self.context)
        self.assertEqual(self.context.code, grpc.StatusCode.OK)

        self.assertEqual(response.publish_context["PUBLISH_CONTEXT_LUN"], '1')
        self.assertEqual(response.publish_context["PUBLISH_CONTEXT_CONNECTIVITY"],
                         "iscsi")
        self.assertEqual(response.publish_context["PUBLISH_CONTEXT_ARRAY_IQN"],
                         "iqn1,iqn2")
        self.assertEqual(response.publish_context["iqn1"],
                         "1.1.1.1,2.2.2.2")
        self.assertEqual(response.publish_context["iqn2"],
                         "[::1]")

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_publish_volume_with_node_id_only_has_wwns(self, storage_agent):
        self.request.node_id = "hostname;;500143802426baf4"
        self.mediator.get_host_by_host_identifiers.return_value = self.hostname, ["fc"]
        self.mediator.get_array_fc_wwns = Mock()
        self.mediator.get_array_fc_wwns.return_value = ["500143802426baf4"]
        self.mediator.get_iscsi_targets_by_iqn.return_value = {}
        storage_agent.return_value = self.storage_agent

        response = self.servicer.ControllerPublishVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.OK)
        self.assertEqual(response.publish_context["PUBLISH_CONTEXT_LUN"], '1')
        self.assertEqual(response.publish_context["PUBLISH_CONTEXT_CONNECTIVITY"],
                         "fc")
        self.assertEqual(
            response.publish_context["PUBLISH_CONTEXT_ARRAY_FC_INITIATORS"],
            "500143802426baf4")

        self.request.node_id = "hostname;;500143802426baf4:500143806626bae2"
        self.mediator.get_host_by_host_identifiers.return_value = self.hostname, ["fc"]
        self.mediator.get_array_fc_wwns = Mock()
        self.mediator.get_array_fc_wwns.return_value = ["500143802426baf4",
                                                        "500143806626bae2"]
        storage_agent.return_value = self.storage_agent

        response = self.servicer.ControllerPublishVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.OK)
        self.assertEqual(response.publish_context["PUBLISH_CONTEXT_LUN"], '1')
        self.assertEqual(response.publish_context["PUBLISH_CONTEXT_CONNECTIVITY"],
                         "fc")
        self.assertEqual(
            response.publish_context["PUBLISH_CONTEXT_ARRAY_FC_INITIATORS"],
            "500143802426baf4,500143806626bae2")

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_publish_volume_get_volume_mappings_one_map_for_other_host(self, storage_agent):
        self.mediator.get_volume_mappings = Mock()
        self.mediator.get_volume_mappings.return_value = {self.hostname: 3}
        self.mediator.get_host_by_name = Mock()
        self.mediator.get_host_by_name.return_value = Host(name=self.hostname, connectivity_types=['iscsi'],
                                                           iscsi_iqns="other_iqn")
        storage_agent.return_value = self.storage_agent

        self.servicer.ControllerPublishVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.FAILED_PRECONDITION)
        self.assertTrue("Volume is already mapped" in self.context.details)

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_publish_volume_get_volume_mappings_more_then_one_mapping(self, storage_agent):
        self.mediator.get_volume_mappings = Mock()
        self.mediator.get_volume_mappings.return_value = {"other-hostname": 3, self.hostname: 4}
        storage_agent.return_value = self.storage_agent

        self.servicer.ControllerPublishVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.FAILED_PRECONDITION)
        self.assertTrue("Volume is already mapped" in self.context.details)

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
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
    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_publish_volume_map_volume_lun_already_in_use(self, storage_agent):
        self.mediator.map_volume.side_effect = [array_errors.LunAlreadyInUseError("", ""), 2]
        storage_agent.return_value = self.storage_agent

        response = self.servicer.ControllerPublishVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.OK)
        self.assertEqual(response.publish_context["PUBLISH_CONTEXT_LUN"], '2')
        self.assertEqual(response.publish_context["PUBLISH_CONTEXT_CONNECTIVITY"], "iscsi")

        self.mediator.map_volume.side_effect = [
            array_errors.LunAlreadyInUseError("", ""), 2]
        self.mediator.get_host_by_host_identifiers = Mock()
        self.mediator.get_host_by_host_identifiers.return_value = self.hostname, ["fc"]
        self.mediator.get_array_fc_wwns = Mock()
        self.mediator.get_array_fc_wwns.return_value = ["500143802426baf4"]
        storage_agent.return_value = self.storage_agent

        response = self.servicer.ControllerPublishVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.OK)
        self.assertEqual(response.publish_context["PUBLISH_CONTEXT_LUN"], '2')
        self.assertEqual(response.publish_context["PUBLISH_CONTEXT_CONNECTIVITY"],
                         "fc")

        self.mediator.map_volume.side_effect = [array_errors.LunAlreadyInUseError("", ""),
                                                array_errors.LunAlreadyInUseError("", ""), 2]
        storage_agent.return_value = self.storage_agent

        self.servicer.ControllerPublishVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.OK)
        self.assertEqual(response.publish_context["PUBLISH_CONTEXT_LUN"], '2')
        self.assertEqual(response.publish_context["PUBLISH_CONTEXT_CONNECTIVITY"], "fc")

        self.mediator.map_volume.side_effect = [array_errors.LunAlreadyInUseError("", "")] * (
                    self.mediator.max_lun_retries + 1)
        storage_agent.return_value = self.storage_agent

        self.servicer.ControllerPublishVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.RESOURCE_EXHAUSTED)

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_publish_volume_get_iscsi_targets_by_iqn_excpetions(self, storage_agent):
        self.mediator.get_iscsi_targets_by_iqn.side_effect = [array_errors.NoIscsiTargetsFoundError("some_endpoint")]
        storage_agent.return_value = self.storage_agent

        self.servicer.ControllerPublishVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.NOT_FOUND)

    @patch("controllers.array_action.array_mediator_abstract.ArrayMediatorAbstract.map_volume_by_initiators")
    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_map_volume_by_initiators_exceptions(self, storage_agent, map_volume_by_initiators):
        map_volume_by_initiators.side_effect = [
            array_errors.UnsupportedConnectivityTypeError("usb")]
        storage_agent.return_value = self.storage_agent

        self.servicer.ControllerPublishVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.INVALID_ARGUMENT)


class TestUnpublishVolume(BaseControllerSetUp, CommonControllerTest):

    @property
    def tested_method(self):
        return self.servicer.ControllerUnpublishVolume

    @property
    def tested_method_response_class(self):
        return csi_pb2.ControllerUnpublishVolumeResponse

    def setUp(self):
        super().setUp()
        self.hostname = "hostname"

        self.mediator.get_host_by_host_identifiers = Mock()
        self.mediator.get_host_by_host_identifiers.return_value = self.hostname, ["iscsi"]

        self.mediator.unmap_volume = Mock()
        self.mediator.unmap_volume.return_value = None

        arr_type = XIVArrayMediator.array_type
        self.request.volume_id = "{}:wwn1".format(arr_type)
        self.request.node_id = "hostname;iqn1;500143802426baf4"

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_unpublish_volume_success(self, storage_agent):
        storage_agent.return_value = self.storage_agent

        self.servicer.ControllerUnpublishVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.OK)

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_unpublish_volume_already_processing(self, storage_agent):
        self._test_request_already_processing(storage_agent, "volume_id", self.request.volume_id)

    @patch("controllers.servers.utils.validate_unpublish_volume_request")
    def test_unpublish_volume_validation_exception(self, publish_validation):
        publish_validation.side_effect = [controller_errors.ValidationException("msg")]

        self.servicer.ControllerUnpublishVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.INVALID_ARGUMENT)
        self.assertIn("msg", self.context.details)

    @patch("controllers.servers.utils.get_volume_id_info")
    def test_unpublish_volume_object_id_error(self, get_volume_id_info):
        get_volume_id_info.side_effect = [controller_errors.ObjectIdError("object_type", "object_id")]

        self.servicer.ControllerUnpublishVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.INVALID_ARGUMENT)
        self.assertIn("object_type", self.context.details)
        self.assertIn("object_id", self.context.details)

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_unpublish_volume_with_wrong_secrets(self, storage_agent):
        self._test_request_with_wrong_secrets(storage_agent)

    def test_unpublish_volume_with_too_much_delimiters_in_volume_id(self):
        self.request.volume_id = "too:much:delimiters:in:id"

        self.servicer.ControllerUnpublishVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.INVALID_ARGUMENT)

    def test_unpublish_volume_wrong_node_id(self):
        self.request.node_id = "some-wrong-id-format"

        self.servicer.ControllerUnpublishVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.OK)

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_unpublish_volume_get_host_by_host_identifiers_multiple_hosts_found_error(self, storage_agent):
        self.mediator.get_host_by_host_identifiers = Mock()
        self.mediator.get_host_by_host_identifiers.side_effect = [array_errors.MultipleHostsFoundError("", "")]
        storage_agent.return_value = self.storage_agent

        self.servicer.ControllerUnpublishVolume(self.request, self.context)
        self.assertTrue("Multiple hosts" in self.context.details)
        self.assertEqual(self.context.code, grpc.StatusCode.INTERNAL)

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_unpublish_volume_get_host_by_host_identifiers_host_not_found_error(self, storage_agent):
        self.mediator.get_host_by_host_identifiers = Mock()
        self.mediator.get_host_by_host_identifiers.side_effect = [array_errors.HostNotFoundError("")]
        storage_agent.return_value = self.storage_agent

        self.servicer.ControllerUnpublishVolume(self.request, self.context)
        self.assertEqual(self.context.code, grpc.StatusCode.OK)

    def _test_unpublish_volume_unmap_volume_with_error(self, storage_agent, array_error, status_code):
        self.mediator.unmap_volume.side_effect = [array_error]
        storage_agent.return_value = self.storage_agent
        self.servicer.ControllerUnpublishVolume(self.request, self.context)
        self.assertEqual(self.context.code, status_code)

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_unpublish_volume_unmap_volume_object_not_found_error(self, storage_agent):
        self._test_unpublish_volume_unmap_volume_with_error(storage_agent, array_errors.ObjectNotFoundError("volume"),
                                                            grpc.StatusCode.OK)

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_unpublish_volume_unmap_volume_volume_already_unmapped_error(self, storage_agent):
        self._test_unpublish_volume_unmap_volume_with_error(storage_agent, array_errors.VolumeAlreadyUnmappedError(""),
                                                            grpc.StatusCode.OK)

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_unpublish_volume_unmap_volume_permission_denied_error(self, storage_agent):
        self._test_unpublish_volume_unmap_volume_with_error(storage_agent, array_errors.PermissionDeniedError("msg"),
                                                            grpc.StatusCode.PERMISSION_DENIED)

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_unpublish_volume_unmap_volume_host_not_found_error(self, storage_agent):
        self._test_unpublish_volume_unmap_volume_with_error(storage_agent, array_errors.HostNotFoundError("host"),
                                                            grpc.StatusCode.OK)

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_unpublish_volume_unmap_volume_unmapping_error(self, storage_agent):
        self._test_unpublish_volume_unmap_volume_with_error(storage_agent, array_errors.UnmappingError("", "", ""),
                                                            grpc.StatusCode.INTERNAL)


class TestGetCapabilities(BaseControllerSetUp):

    def test_controller_get_capabilities(self):
        self.servicer.ControllerGetCapabilities(self.request, self.context)


class TestExpandVolume(BaseControllerSetUp, CommonControllerTest):

    @property
    def tested_method(self):
        return self.servicer.ControllerExpandVolume

    @property
    def tested_method_response_class(self):
        return csi_pb2.ControllerExpandVolumeResponse

    def setUp(self):
        super().setUp()

        self.request.parameters = {}
        self.volume_id = "vol-id"
        self.request.volume_id = "{}:{}".format("xiv", self.volume_id)
        self.request.volume_content_source = None
        self.mediator.get_object_by_id = Mock()
        self.volume_before_expand = utils.get_mock_mediator_response_volume(2,
                                                                            VOLUME_NAME,
                                                                            self.volume_id,
                                                                            "a9k")
        self.volume_after_expand = utils.get_mock_mediator_response_volume(self.capacity_bytes,
                                                                           VOLUME_NAME,
                                                                           self.volume_id,
                                                                           "a9k")
        self.mediator.get_object_by_id.side_effect = [self.volume_before_expand, self.volume_after_expand]
        self.request.volume_capability = self.volume_capability

    def _prepare_expand_volume_mocks(self, storage_agent):
        storage_agent.return_value = self.storage_agent
        self.mediator.expand_volume = Mock()

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_expand_volume_already_processing(self, storage_agent):
        self._test_request_already_processing(storage_agent, "volume_id", self.request.volume_id)

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
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

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_expand_volume_with_required_bytes_below_minimal(self, storage_agent):
        self._prepare_expand_volume_mocks(storage_agent)
        self.request.capacity_range.required_bytes = 1
        self._test_no_expand_needed()

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_expand_volume_with_required_bytes_zero(self, storage_agent):
        self._prepare_expand_volume_mocks(storage_agent)
        self.request.capacity_range.required_bytes = 0
        self._test_no_expand_needed()

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_expand_volume_with_volume_size_already_in_range(self, storage_agent):
        self._prepare_expand_volume_mocks(storage_agent)
        self.request.capacity_range.required_bytes = 2
        self._test_no_expand_needed()

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_expand_volume_succeeds(self, storage_agent):
        self._prepare_expand_volume_mocks(storage_agent)

        response = self.servicer.ControllerExpandVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.OK)
        self.assertTrue(response.node_expansion_required)
        self.assertEqual(response.capacity_bytes, self.volume_after_expand.capacity_bytes)
        self.mediator.expand_volume.assert_called_once_with(volume_id=self.volume_id,
                                                            required_bytes=self.capacity_bytes)

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_expand_volume_with_bad_id(self, storage_agent):
        self._prepare_expand_volume_mocks(storage_agent)
        self.request.volume_id = "123"

        self.servicer.ControllerExpandVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.INVALID_ARGUMENT)
        self.mediator.expand_volume.assert_not_called()

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_expand_volume_not_found_before_expansion(self, storage_agent):
        self._prepare_expand_volume_mocks(storage_agent)
        self.mediator.get_object_by_id.side_effect = [None, None]

        self.servicer.ControllerExpandVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.NOT_FOUND)

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_expand_volume_not_found_after_expansion(self, storage_agent):
        self._prepare_expand_volume_mocks(storage_agent)
        self.mediator.get_object_by_id.side_effect = [self.volume_before_expand, None]

        self.servicer.ControllerExpandVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.NOT_FOUND)

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_expand_volume_with_wrong_secrets(self, a_enter):
        self._test_request_with_wrong_secrets(a_enter)

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_expand_volume_with_array_connection_exception(self, storage_agent):
        self._test_request_with_array_connection_exception(storage_agent)

    @patch("controllers.array_action.array_mediator_xiv.XIVArrayMediator.expand_volume")
    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def _expand_volume_returns_error(self, storage_agent, expand_volume, return_code, err):
        storage_agent.return_value = self.storage_agent
        expand_volume.side_effect = [err]
        msg = str(err)

        self.servicer.ControllerExpandVolume(self.request, self.context)

        self.assertEqual(self.context.code, return_code)
        self.assertIn(msg, self.context.details)
        self.mediator.expand_volume.assert_called_once_with(volume_id=self.volume_id,
                                                            required_bytes=self.capacity_bytes)

    def test_expand_volume_with_illegal_object_id_exception(self):
        self._expand_volume_returns_error(return_code=grpc.StatusCode.INVALID_ARGUMENT,
                                          err=array_errors.InvalidArgumentError("123"))

    def test_expand_volume_with_permission_denied_exception(self):
        self._expand_volume_returns_error(return_code=grpc.StatusCode.PERMISSION_DENIED,
                                          err=array_errors.PermissionDeniedError("msg"))

    def test_expand_volume_with_object_not_found_exception(self):
        self._expand_volume_returns_error(return_code=grpc.StatusCode.NOT_FOUND,
                                          err=array_errors.ObjectNotFoundError("name"))

    def test_expand_volume_with_object_in_use_exception(self):
        self._expand_volume_returns_error(return_code=grpc.StatusCode.INTERNAL,
                                          err=array_errors.ObjectIsStillInUseError("a", ["b"]))

    def test_expand_volume_with_other_exception(self):
        self._expand_volume_returns_error(return_code=grpc.StatusCode.INTERNAL,
                                          err=Exception("error"))

    def test_expand_volume_with_no_space_in_pool_exception(self):
        self._expand_volume_returns_error(return_code=grpc.StatusCode.RESOURCE_EXHAUSTED,
                                          err=array_errors.NotEnoughSpaceInPool("pool"))


class TestIdentityServer(BaseControllerSetUp):

    @patch("controllers.common.config.config.identity")
    def test_identity_plugin_get_info_succeeds(self, identity_config):
        plugin_name = "plugin-name"
        version = "1.1.0"
        identity_config.name = plugin_name
        identity_config.version = version
        request = Mock()
        context = Mock()
        request.volume_capabilities = []
        response = self.servicer.GetPluginInfo(request, context)
        self.assertEqual(response, csi_pb2.GetPluginInfoResponse(name=plugin_name, vendor_version=version))

    @patch("controllers.common.config.config.identity")
    def test_identity_plugin_get_info_fails_when_attributes_from_config_are_missing(self, identity_config):
        request = Mock()
        context = Mock()

        identity_config.mock_add_spec(spec=["name"])
        response = self.servicer.GetPluginInfo(request, context)
        context.set_code.assert_called_once_with(grpc.StatusCode.INTERNAL)
        self.assertEqual(response, csi_pb2.GetPluginInfoResponse())

        identity_config.mock_add_spec(spec=["version"])
        response = self.servicer.GetPluginInfo(request, context)
        self.assertEqual(response, csi_pb2.GetPluginInfoResponse())
        context.set_code.assert_called_with(grpc.StatusCode.INTERNAL)

    @patch("controllers.common.config.config.identity")
    def test_identity_plugin_get_info_fails_when_name_or_version_are_empty(self, identity_config):
        request = Mock()
        context = Mock()

        identity_config.name = ""
        identity_config.version = "1.1.0"
        response = self.servicer.GetPluginInfo(request, context)
        context.set_code.assert_called_once_with(grpc.StatusCode.INTERNAL)
        self.assertEqual(response, csi_pb2.GetPluginInfoResponse())

        identity_config.name = "name"
        identity_config.version = ""
        response = self.servicer.GetPluginInfo(request, context)
        self.assertEqual(response, csi_pb2.GetPluginInfoResponse())
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


class TestValidateVolumeCapabilities(BaseControllerSetUp, CommonControllerTest):

    @property
    def tested_method(self):
        return self.servicer.ValidateVolumeCapabilities

    @property
    def tested_method_response_class(self):
        return csi_pb2.ValidateVolumeCapabilitiesResponse

    def setUp(self):
        super().setUp()

        arr_type = XIVArrayMediator.array_type
        self.request.volume_id = "{}:wwn1".format(arr_type)
        self.request.parameters = {config.PARAMETERS_POOL: "pool1"}

        self.mediator.get_object_by_id = Mock()
        self.mediator.get_object_by_id.return_value = utils.get_mock_mediator_response_volume(10, "vol", "wwn2", "a9k")
        self.request.volume_capabilities = [self.volume_capability]

    def _assert_response(self, expected_status_code, expected_details_substring):
        self.assertEqual(self.context.code, expected_status_code)
        self.assertTrue(expected_details_substring in self.context.details)

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_validate_volume_capabilities_already_processing(self, storage_agent):
        self._test_request_already_processing(storage_agent, "volume_id", self.request.volume_id)

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_validate_volume_capabilities_success(self, storage_agent):
        storage_agent.return_value = self.storage_agent

        self.servicer.ValidateVolumeCapabilities(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.OK)

    @patch("controllers.servers.utils.get_volume_id_info")
    def test_validate_volume_capabilities_object_id_error(self, get_volume_id_info):
        get_volume_id_info.side_effect = [controller_errors.ObjectIdError("object_type", "object_id")]

        self.servicer.ValidateVolumeCapabilities(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.NOT_FOUND)
        self.assertIn("object_type", self.context.details)
        self.assertIn("object_id", self.context.details)

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_validate_volume_capabilities_with_empty_id(self, storage_agent):
        storage_agent.return_value = self.storage_agent
        self.request.volume_id = ""

        self.servicer.ValidateVolumeCapabilities(self.request, self.context)

        self._assert_response(grpc.StatusCode.INVALID_ARGUMENT, "volume id")

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_validate_volume_capabilities_with_wrong_secrets(self, storage_agent):
        self._test_request_with_wrong_secrets(storage_agent)

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_validate_volume_capabilities_with_unsupported_access_mode(self, storage_agent):
        storage_agent.return_value = self.storage_agent
        self.request.volume_capabilities[0].access_mode.mode = 999

        self.servicer.ValidateVolumeCapabilities(self.request, self.context)

        self._assert_response(grpc.StatusCode.INVALID_ARGUMENT, "unsupported access mode")

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_validate_volume_capabilities_with_unsupported_fs_type(self, storage_agent):
        storage_agent.return_value = self.storage_agent

        volume_capability = utils.get_mock_volume_capability(fs_type="ext3")
        self.request.volume_capabilities = [volume_capability]

        self.servicer.ValidateVolumeCapabilities(self.request, self.context)

        self._assert_response(grpc.StatusCode.INVALID_ARGUMENT, "fs_type")

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_validate_volume_capabilities_with_no_capabilities(self, storage_agent):
        storage_agent.return_value = self.storage_agent
        self.request.volume_capabilities = {}

        self.servicer.ValidateVolumeCapabilities(self.request, self.context)

        self._assert_response(grpc.StatusCode.INVALID_ARGUMENT, "not set")

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_validate_volume_capabilities_with_bad_id(self, storage_agent):
        storage_agent.return_value = self.storage_agent
        self.request.volume_id = "wwn1"

        self.servicer.ValidateVolumeCapabilities(self.request, self.context)

        self._assert_response(grpc.StatusCode.NOT_FOUND, "id format")

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_validate_volume_capabilities_with_volume_not_found(self, storage_agent):
        storage_agent.return_value = self.storage_agent
        self.mediator.get_object_by_id.return_value = None

        self.servicer.ValidateVolumeCapabilities(self.request, self.context)

        self._assert_response(grpc.StatusCode.NOT_FOUND, "wwn")

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_validate_volume_capabilities_with_volume_context_not_match(self, storage_agent):
        storage_agent.return_value = self.storage_agent
        self.request.volume_context = {config.VOLUME_CONTEXT_VOLUME_NAME: "fake"}

        self.servicer.ValidateVolumeCapabilities(self.request, self.context)

        self._assert_response(grpc.StatusCode.INVALID_ARGUMENT, "volume context")

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_validate_volume_capabilities_with_space_efficiency_not_match(self, storage_agent):
        storage_agent.return_value = self.storage_agent
        self.request.parameters.update({config.PARAMETERS_SPACE_EFFICIENCY: "not_none"})
        self.mediator.validate_supported_space_efficiency = Mock()

        self.servicer.ValidateVolumeCapabilities(self.request, self.context)

        self._assert_response(grpc.StatusCode.INVALID_ARGUMENT, "space efficiency")

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_validate_volume_capabilities_with_pool_not_match(self, storage_agent):
        storage_agent.return_value = self.storage_agent
        self.request.parameters.update({config.PARAMETERS_POOL: "other pool"})

        self.servicer.ValidateVolumeCapabilities(self.request, self.context)

        self._assert_response(grpc.StatusCode.INVALID_ARGUMENT, "pool")

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_validate_volume_capabilities_with_prefix_not_match(self, storage_agent):
        storage_agent.return_value = self.storage_agent
        self.request.parameters.update({config.PARAMETERS_VOLUME_NAME_PREFIX: "prefix"})

        self.servicer.ValidateVolumeCapabilities(self.request, self.context)

        self._assert_response(grpc.StatusCode.INVALID_ARGUMENT, "prefix")

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_validate_volume_capabilities_parameters_success(self, storage_agent):
        storage_agent.return_value = self.storage_agent
        self.request.parameters = {config.PARAMETERS_VOLUME_NAME_PREFIX: "prefix",
                                   config.PARAMETERS_POOL: "pool2",
                                   config.PARAMETERS_SPACE_EFFICIENCY: "not_none"}
        volume_response = utils.get_mock_mediator_response_volume(10, "prefix_vol", "wwn2", "a9k",
                                                                  space_efficiency="not_none")
        volume_response.pool = "pool2"
        self.mediator.get_object_by_id.return_value = volume_response
        self.mediator.validate_supported_space_efficiency = Mock()

        self.servicer.ValidateVolumeCapabilities(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.OK)
