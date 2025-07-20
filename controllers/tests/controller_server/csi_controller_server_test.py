import abc
import json
import unittest

import grpc
from csi_general import csi_pb2
from mock import patch, Mock, MagicMock, call

import controllers.array_action.errors as array_errors
import controllers.servers.errors as controller_errors
import controllers.servers.settings as servers_settings
from controllers.array_action.array_action_types import ObjectIds
from controllers.array_action.array_mediator_xiv import XIVArrayMediator
from controllers.servers.csi.csi_controller_server import CSIControllerServicer
from controllers.servers.csi.sync_lock import SyncLock
from controllers.tests import utils
from controllers.tests.common.test_settings import (CLONE_VOLUME_NAME,
                                                    OBJECT_INTERNAL_ID,
                                                    DUMMY_POOL1, SPACE_EFFICIENCY,
                                                    DUMMY_IO_GROUP, DUMMY_VOLUME_GROUP,
                                                    VOLUME_NAME, SNAPSHOT_NAME,
                                                    SNAPSHOT_VOLUME_NAME,
                                                    SNAPSHOT_VOLUME_UID, VIRT_SNAP_FUNC_TRUE, SECRET_PASSWORD_VALUE,
                                                    SECRET_USERNAME_VALUE,
                                                    VOLUME_UID, INTERNAL_VOLUME_ID, DUMMY_POOL2,
                                                    SECRET_MANAGEMENT_ADDRESS_VALUE,
                                                    NAME_PREFIX, INTERNAL_SNAPSHOT_ID, SOURCE_VOLUME_ID,
                                                    SECRET_MANAGEMENT_ADDRESS_KEY, SECRET_PASSWORD_KEY,
                                                    SECRET_USERNAME_KEY, SECRET)
from controllers.tests.controller_server.common import mock_get_agent, mock_array_type, mock_mediator
from controllers.tests.utils import ProtoBufMock

CONTROLLER_SERVER_PATH = "controllers.servers.csi.csi_controller_server"


class BaseControllerSetUp(unittest.TestCase):

    def setUp(self):
        self.servicer = CSIControllerServicer()

        mock_array_type(self, CONTROLLER_SERVER_PATH)

        self.mediator = mock_mediator()

        self.storage_agent = MagicMock()
        mock_get_agent(self, CONTROLLER_SERVER_PATH)

        self.request = ProtoBufMock()
        self.request.secrets = SECRET

        self.request.parameters = {}
        self.request.volume_context = {}
        self.volume_capability = utils.get_mock_volume_capability()
        self.capacity_bytes = 10
        self.request.capacity_range = Mock()
        self.request.capacity_range.required_bytes = self.capacity_bytes
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

    def _test_create_object_with_empty_name(self):
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

    def _test_request_with_wrong_secrets(self):
        secrets = {SECRET_PASSWORD_KEY: SECRET_PASSWORD_VALUE,
                   SECRET_MANAGEMENT_ADDRESS_KEY: SECRET_MANAGEMENT_ADDRESS_VALUE}
        self._test_request_with_wrong_secrets_parameters(secrets)

        secrets = {SECRET_USERNAME_KEY: SECRET_USERNAME_VALUE,
                   SECRET_MANAGEMENT_ADDRESS_KEY: SECRET_MANAGEMENT_ADDRESS_VALUE}
        self._test_request_with_wrong_secrets_parameters(secrets)

        secrets = {SECRET_USERNAME_KEY: SECRET_USERNAME_VALUE, SECRET_PASSWORD_KEY: SECRET_PASSWORD_VALUE}
        self._test_request_with_wrong_secrets_parameters(secrets)

        secrets = utils.get_fake_secret_config(system_id="u-")
        self._test_request_with_wrong_secrets_parameters(secrets, message="system id")

        self.request.secrets = []

    def _test_request_already_processing(self, request_attribute, object_id):
        with SyncLock(request_attribute, object_id, "test_request_already_processing"):
            response = self.tested_method(self.request, self.context)
        self.assertEqual(grpc.StatusCode.ABORTED, self.context.code)
        self.assertEqual(self.tested_method_response_class, type(response))

    def _test_request_with_array_connection_exception(self):
        self.get_agent.side_effect = [Exception("error")]
        context = utils.FakeContext()
        self.tested_method(self.request, context)
        self.assertEqual(grpc.StatusCode.INTERNAL, context.code)
        self.assertIn("error", context.details)

    def _test_request_with_get_array_type_exception(self):
        context = utils.FakeContext()
        self.detect_array_type.side_effect = [array_errors.FailedToFindStorageSystemType("endpoint")]
        self.tested_method(self.request, context)
        self.assertEqual(grpc.StatusCode.INTERNAL, context.code)
        msg = array_errors.FailedToFindStorageSystemType("endpoint").message
        self.assertIn(msg, context.details)

    def _test_request_with_wrong_parameters(self):
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

        self.mediator.create_snapshot = Mock()

        self.request.name = SNAPSHOT_NAME
        self.request.source_volume_id = "{}:{};{}".format("A9000", OBJECT_INTERNAL_ID, SNAPSHOT_VOLUME_UID)
        self.mediator.get_object_by_id = Mock()
        self.mediator.get_object_by_id.return_value = utils.get_mock_mediator_response_volume(10, SNAPSHOT_VOLUME_NAME,
                                                                                              VOLUME_UID, "xiv")
        self.context = utils.FakeContext()

    def test_create_snapshot_with_empty_name(self):
        self._test_create_object_with_empty_name()

    def _prepare_create_snapshot_mocks(self, ):
        self.mediator.get_snapshot = Mock()
        self.mediator.get_snapshot.return_value = None
        self.mediator.create_snapshot = Mock()
        self.mediator.create_snapshot.return_value = utils.get_mock_mediator_response_snapshot(10, SNAPSHOT_NAME,
                                                                                               SNAPSHOT_VOLUME_UID,
                                                                                               SNAPSHOT_VOLUME_NAME,
                                                                                               "xiv")

    def _test_create_snapshot_succeeds(self, expected_space_efficiency=None, expected_pool=None,
                                       system_id=None):
        self._prepare_create_snapshot_mocks()

        response_snapshot = self.servicer.CreateSnapshot(self.request, self.context)

        self.assertEqual(grpc.StatusCode.OK, self.context.code)
        self.mediator.get_snapshot.assert_called_once_with(SNAPSHOT_VOLUME_UID, SNAPSHOT_NAME, expected_pool, False)
        self.mediator.create_snapshot.assert_called_once_with(SNAPSHOT_VOLUME_UID, SNAPSHOT_NAME,
                                                              expected_space_efficiency, expected_pool, False)
        system_id_part = ':{}'.format(system_id) if system_id else ''
        snapshot_id = 'xiv{}:0;{}'.format(system_id_part, SNAPSHOT_VOLUME_UID)
        self.assertEqual(snapshot_id, response_snapshot.snapshot.snapshot_id)

    def test_create_snapshot_succeeds(self, ):
        self._test_create_snapshot_succeeds()

    def test_create_snapshot_with_pool_parameter_succeeds(self, ):
        self.request.parameters = {servers_settings.PARAMETERS_POOL: DUMMY_POOL1}
        self._test_create_snapshot_succeeds(expected_pool=DUMMY_POOL1)

    def test_create_snapshot_with_space_efficiency_parameter_succeeds(self):
        self.mediator.validate_supported_space_efficiency = Mock()
        self.request.parameters = {servers_settings.PARAMETERS_SPACE_EFFICIENCY: SPACE_EFFICIENCY}
        self._test_create_snapshot_succeeds(expected_space_efficiency=SPACE_EFFICIENCY)

    def test_create_snapshot_with_space_efficiency_and_virt_snap_func_enabled_fail(self):
        self.request.parameters = {servers_settings.PARAMETERS_SPACE_EFFICIENCY: SPACE_EFFICIENCY,
                                   servers_settings.PARAMETERS_VIRT_SNAP_FUNC: VIRT_SNAP_FUNC_TRUE}

        self.servicer.CreateSnapshot(self.request, self.context)

        self.assertEqual(grpc.StatusCode.INVALID_ARGUMENT, self.context.code)

    def test_create_snapshot_already_processing(self):
        self._test_request_already_processing("name", self.request.name)

    def _test_create_snapshot_with_by_system_id_parameter(self, system_id, expected_pool):
        system_id_part = ':{}'.format(system_id) if system_id else ''
        self.request.source_volume_id = "{}{}:{}".format("A9000", system_id_part, SNAPSHOT_VOLUME_UID)
        self.request.parameters = {servers_settings.PARAMETERS_BY_SYSTEM: json.dumps(
            {"u1": {servers_settings.PARAMETERS_POOL: DUMMY_POOL1},
             "u2": {servers_settings.PARAMETERS_POOL: DUMMY_POOL2}})}
        self._test_create_snapshot_succeeds(expected_pool=expected_pool, system_id=system_id)

    def test_create_snapshot_with_by_system_id_parameter_succeeds(self):
        self._test_create_snapshot_with_by_system_id_parameter("u1", DUMMY_POOL1)
        self._test_create_snapshot_with_by_system_id_parameter("u2", DUMMY_POOL2)
        self._test_create_snapshot_with_by_system_id_parameter(None, None)

    def test_create_snapshot_belongs_to_wrong_volume(self):
        self.mediator.create_snapshot = Mock()
        self.mediator.get_snapshot.return_value = utils.get_mock_mediator_response_snapshot(10, SNAPSHOT_NAME,
                                                                                            VOLUME_UID,
                                                                                            "wrong_volume_name", "xiv")

        self.servicer.CreateSnapshot(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.ALREADY_EXISTS)

    def test_create_snapshot_no_source_volume(self):
        self.request.source_volume_id = None

        self.servicer.CreateSnapshot(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.INVALID_ARGUMENT)

    def test_create_snapshot_with_wrong_secrets(self, ):
        self._test_request_with_wrong_secrets()

    def test_create_snapshot_with_array_connection_exception(self):
        self._test_request_with_array_connection_exception()

    def _test_create_snapshot_get_snapshot_raise_error(self, exception, grpc_status):
        self.mediator.get_snapshot.side_effect = [exception]

        self.servicer.CreateSnapshot(self.request, self.context)

        self.assertEqual(self.context.code, grpc_status)
        self.assertIn(str(exception), self.context.details)
        self.mediator.get_snapshot.assert_called_once_with(SNAPSHOT_VOLUME_UID, SNAPSHOT_NAME, None, False)

    def test_create_snapshot_get_snapshot_exception(self):
        self._test_create_snapshot_get_snapshot_raise_error(exception=Exception("error"),
                                                            grpc_status=grpc.StatusCode.INTERNAL)

    def test_create_snapshot_with_get_snapshot_illegal_object_name_exception(self):
        self._test_create_snapshot_get_snapshot_raise_error(exception=array_errors.InvalidArgumentError("snapshot"),
                                                            grpc_status=grpc.StatusCode.INVALID_ARGUMENT)

    def test_create_snapshot_with_get_snapshot_illegal_object_id_exception(self):
        self._test_create_snapshot_get_snapshot_raise_error(exception=array_errors.InvalidArgumentError("volume-id"),
                                                            grpc_status=grpc.StatusCode.INVALID_ARGUMENT)

    def test_create_snapshot_with_prefix_too_long_exception(self):
        self.request.parameters.update({"snapshot_name_prefix": "a" * 128})
        self.servicer.CreateSnapshot(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.INVALID_ARGUMENT)

    def test_create_snapshot_with_get_snapshot_name_too_long_success(self):
        self._prepare_create_snapshot_mocks()
        self.mediator.max_object_name_length = 63
        self.request.name = "a" * 128

        self.servicer.CreateSnapshot(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.OK)

    def create_snapshot_returns_error(self, return_code, err):
        self.mediator.create_snapshot.side_effect = [err]
        msg = str(err)

        self.servicer.CreateSnapshot(self.request, self.context)

        self.assertEqual(self.context.code, return_code)
        self.assertIn(msg, self.context.details)
        self.mediator.get_snapshot.assert_called_once_with(SNAPSHOT_VOLUME_UID, SNAPSHOT_NAME, None, False)
        self.mediator.create_snapshot.assert_called_once_with(SNAPSHOT_VOLUME_UID, SNAPSHOT_NAME, None, None, False)

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

    def test_create_snapshot_with_name_prefix(self):
        self.request.name = VOLUME_NAME
        self.request.parameters[servers_settings.PARAMETERS_SNAPSHOT_NAME_PREFIX] = NAME_PREFIX
        self.mediator.create_snapshot = Mock()
        self.mediator.create_snapshot.return_value = utils.get_mock_mediator_response_snapshot(10, SNAPSHOT_NAME,
                                                                                               VOLUME_UID,
                                                                                               SNAPSHOT_VOLUME_NAME,
                                                                                               "xiv")

        self.servicer.CreateSnapshot(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.OK)
        full_name = "{}_{}".format(NAME_PREFIX, VOLUME_NAME)
        self.mediator.create_snapshot.assert_called_once_with(SNAPSHOT_VOLUME_UID, full_name, None, None,
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
        self.request.snapshot_id = "A9000:{};{}".format(INTERNAL_SNAPSHOT_ID, SNAPSHOT_VOLUME_UID)

    @patch("controllers.array_action.array_mediator_xiv.XIVArrayMediator.delete_snapshot", Mock())
    def _test_delete_snapshot_succeeds(self, snapshot_id):
        self.request.snapshot_id = snapshot_id

        self.servicer.DeleteSnapshot(self.request, self.context)
        self.assertEqual(self.context.code, grpc.StatusCode.OK)

    def test_delete_snapshot_with_internal_id_succeeds(self):
        self._test_delete_snapshot_succeeds("xiv:{};{}".format(INTERNAL_SNAPSHOT_ID, SNAPSHOT_VOLUME_UID))
        self.mediator.delete_snapshot.assert_called_once()

    def test_delete_snapshot_with_system_id_succeeds(self):
        self._test_delete_snapshot_succeeds("xiv:system_id:{}".format(SNAPSHOT_VOLUME_UID))
        self.mediator.delete_snapshot.assert_called_once()

    def test_delete_snapshot_with_system_id_internal_id_succeeds(self):
        self._test_delete_snapshot_succeeds("xiv:system_id:{};{}".format(INTERNAL_SNAPSHOT_ID, SNAPSHOT_VOLUME_UID))
        self.mediator.delete_snapshot.assert_called_once()

    def test_delete_snapshot_no_internal_id_succeeds(self):
        self._test_delete_snapshot_succeeds("xiv:{}".format(SNAPSHOT_VOLUME_UID))
        self.mediator.delete_snapshot.assert_called_once()

    def test_delete_snapshot_bad_id_succeeds(self):
        self._test_delete_snapshot_succeeds("xiv:a:a:volume-id")
        self.mediator.delete_snapshot.assert_not_called()

    def test_delete_snapshot_already_processing(self):
        self._test_request_already_processing("snapshot_id", self.request.snapshot_id)

    def test_delete_snapshot_with_wrong_secrets(self):
        self._test_request_with_wrong_secrets()

    def test_delete_snapshot_with_array_connection_exception(self):
        self._test_request_with_array_connection_exception()

    def test_delete_snapshot_invalid_snapshot_id(self):
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

        self.mediator.create_volume = Mock()
        self.mediator.get_volume = Mock()
        self.mediator.get_volume.side_effect = array_errors.ObjectNotFoundError("vol")
        self.mediator.get_object_by_id = Mock()
        self.mediator.copy_to_existing_volume_from_source = Mock()

        self.request.parameters = {servers_settings.PARAMETERS_POOL: DUMMY_POOL1,
                                   servers_settings.PARAMETERS_IO_GROUP: DUMMY_IO_GROUP,
                                   servers_settings.PARAMETERS_VOLUME_GROUP: DUMMY_VOLUME_GROUP}
        self.request.volume_capabilities = [self.volume_capability]
        self.request.name = VOLUME_NAME
        self.request.volume_content_source = None

    def test_create_volume_with_empty_name(self):
        self._test_create_object_with_empty_name()

    def _prepare_create_volume_mocks(self):
        self.mediator.create_volume = Mock()
        self.mediator.create_volume.return_value = utils.get_mock_mediator_response_volume(10, "volume", VOLUME_UID,
                                                                                           "xiv")

    def _test_create_volume_succeeds(self, expected_volume_id, expected_pool=DUMMY_POOL1):
        self._prepare_create_volume_mocks()

        response_volume = self.servicer.CreateVolume(self.request, self.context)
        self.assertEqual(self.context.code, grpc.StatusCode.OK)
        self.mediator.get_volume.assert_called_once_with(VOLUME_NAME, expected_pool, False)
        self.mediator.create_volume.assert_called_once_with(VOLUME_NAME, 10, None, expected_pool, DUMMY_IO_GROUP,
                                                            DUMMY_VOLUME_GROUP,
                                                            ObjectIds(internal_id='', uid=''), None, False, None, None)
        self.assertEqual(response_volume.volume.content_source.volume.volume_id, '')
        self.assertEqual(response_volume.volume.content_source.snapshot.snapshot_id, '')
        self.assertEqual(response_volume.volume.volume_id, expected_volume_id)

    def test_create_volume_already_processing(self):
        self._test_request_already_processing("name", self.request.name)

    def test_create_volume_succeeds(self):
        self._test_create_volume_succeeds('xiv:{};{}'.format(INTERNAL_VOLUME_ID, VOLUME_UID))
        self.mediator.register_plugin.not_called()

    def test_create_volume_with_topologies_succeeds(self):
        self._test_create_volume_with_topologies_succeeds()

    def _test_create_volume_with_topologies_succeeds(self):
        self.request.secrets = utils.get_fake_secret_config(system_id="u2", supported_topologies=[
            {"topology.block.csi.ibm.com/test": "topology_value"}])
        self.request.accessibility_requirements.preferred = [
            ProtoBufMock(segments={"topology.block.csi.ibm.com/test": "topology_value",
                                   "topology.block.csi.ibm.com/test2": "topology_value2"})]
        second_system_parameters = self.request.parameters.copy()
        second_system_parameters[servers_settings.PARAMETERS_POOL] = DUMMY_POOL2
        self.request.parameters = {servers_settings.PARAMETERS_BY_SYSTEM: json.dumps(
            {"u1": self.request.parameters, "u2": second_system_parameters})}
        self._test_create_volume_succeeds('xiv:u2:{};{}'.format(INTERNAL_VOLUME_ID, VOLUME_UID),
                                          expected_pool=DUMMY_POOL2)
        self.mediator.register_plugin.assert_called_once_with('topology', '')

    def test_create_volume_with_space_efficiency_succeeds(self):
        self._prepare_create_volume_mocks()
        self.request.parameters.update({servers_settings.PARAMETERS_SPACE_EFFICIENCY: "not_none"})
        self.mediator.validate_supported_space_efficiency = Mock()

        self.servicer.CreateVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.OK)
        self.mediator.get_volume.assert_called_once_with(VOLUME_NAME, DUMMY_POOL1, False)
        self.mediator.create_volume.assert_called_once_with(VOLUME_NAME, 10, "not_none", DUMMY_POOL1, DUMMY_IO_GROUP,
                                                            DUMMY_VOLUME_GROUP,
                                                            ObjectIds(internal_id='', uid=''), None, False, None, None)

    def test_create_volume_idempotent_no_source_succeeds(self):
        self._prepare_create_volume_mocks()
        self.mediator.get_volume = Mock()
        self.mediator.get_volume.return_value = utils.get_mock_mediator_response_volume(10, VOLUME_NAME, VOLUME_UID,
                                                                                        "xiv")

        response_volume = self.servicer.CreateVolume(self.request, self.context)
        self.assertEqual(self.context.code, grpc.StatusCode.OK)
        self.mediator.get_volume.assert_called_once_with(VOLUME_NAME, DUMMY_POOL1, False)
        self.mediator.create_volume.assert_not_called()
        self.assertEqual(response_volume.volume.content_source.volume.volume_id, '')
        self.assertEqual(response_volume.volume.content_source.snapshot.snapshot_id, '')

    def test_create_volume_with_wrong_secrets(self):
        self._test_request_with_wrong_secrets()

    def test_create_volume_no_pool(self):
        self._prepare_create_volume_mocks()
        self.request.parameters = {"by_system_id": json.dumps({"u1": DUMMY_POOL1, "u2": DUMMY_POOL2})}
        self.servicer.CreateVolume(self.request, self.context)
        self.assertEqual(self.context.code, grpc.StatusCode.INVALID_ARGUMENT)

    def test_create_volume_with_wrong_parameters(self):
        self._test_request_with_wrong_parameters()

    def test_create_volume_with_wrong_volume_capabilities(self):

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

    def test_create_volume_with_array_connection_exception(self):
        self._test_request_with_array_connection_exception()

    def test_create_volume_with_get_array_type_exception(self):
        self._test_request_with_get_array_type_exception()

    def test_create_volume_get_volume_exception(self):
        self.mediator.get_volume.side_effect = [Exception("error")]

        self.servicer.CreateVolume(self.request, self.context)
        self.assertEqual(self.context.code, grpc.StatusCode.INTERNAL)
        self.assertIn("error", self.context.details)
        self.mediator.get_volume.assert_called_once_with(VOLUME_NAME, DUMMY_POOL1, False)

    def test_create_volume_with_get_volume_illegal_object_name_exception(self):
        self.mediator.get_volume.side_effect = [array_errors.InvalidArgumentError("volume")]

        self.servicer.CreateVolume(self.request, self.context)
        msg = array_errors.InvalidArgumentError("volume").message

        self.assertEqual(self.context.code, grpc.StatusCode.INVALID_ARGUMENT)
        self.assertIn(msg, self.context.details)
        self.mediator.get_volume.assert_called_once_with(VOLUME_NAME, DUMMY_POOL1, False)

    def test_create_volume_with_prefix_too_long_exception(self):
        self.request.parameters.update({"volume_name_prefix": "a" * 128})
        self.servicer.CreateVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.INVALID_ARGUMENT)

    def test_create_volume_with_get_volume_name_too_long_success(self):
        self._prepare_create_volume_mocks()
        self.mediator.max_object_name_length = 63

        self.request.name = "a" * 128
        self.servicer.CreateVolume(self.request, self.context)
        self.assertEqual(self.context.code, grpc.StatusCode.OK)

    def create_volume_returns_error(self, return_code, err):
        self.mediator.create_volume = Mock()
        self.mediator.create_volume.side_effect = [err]

        self.servicer.CreateVolume(self.request, self.context)
        msg = str(err)

        self.assertEqual(self.context.code, return_code)
        self.assertIn(msg, self.context.details)
        self.mediator.get_volume.assert_called_once_with(VOLUME_NAME, DUMMY_POOL1, False)
        self.mediator.create_volume.assert_called_once_with(VOLUME_NAME, self.capacity_bytes, None, DUMMY_POOL1,
                                                            DUMMY_IO_GROUP,
                                                            DUMMY_VOLUME_GROUP, ObjectIds(internal_id='', uid=''),
                                                            None, False, None, None)

    def test_create_volume_with_illegal_object_name_exception(self):
        self.create_volume_returns_error(return_code=grpc.StatusCode.INVALID_ARGUMENT,
                                         err=array_errors.InvalidArgumentError("volume"))

    def test_create_volume_with_volume_exists_exception(self):
        self.create_volume_returns_error(return_code=grpc.StatusCode.ALREADY_EXISTS,
                                         err=array_errors.VolumeAlreadyExists(VOLUME_NAME, "endpoint"))

    def test_create_volume_with_pool_does_not_exist_exception(self):
        self.create_volume_returns_error(return_code=grpc.StatusCode.INVALID_ARGUMENT,
                                         err=array_errors.PoolDoesNotExist(DUMMY_POOL1, "endpoint"))

    def test_create_volume_with_pool_does_not_match_space_efficiency_exception(self):
        self.create_volume_returns_error(return_code=grpc.StatusCode.INVALID_ARGUMENT,
                                         err=array_errors.PoolDoesNotMatchSpaceEfficiency(DUMMY_POOL1, "", "endpoint"))

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
        self.mediator.create_volume.return_value = utils.get_mock_mediator_response_volume(10, VOLUME_NAME, VOLUME_UID,
                                                                                           "xiv")
        self.mediator.validate_supported_space_efficiency = Mock()
        self.servicer.CreateVolume(self.request, self.context)
        self.assertEqual(self.context.code, grpc.StatusCode.OK)
        self.mediator.create_volume.assert_called_once_with(final_name, 10, space_efficiency, DUMMY_POOL1,
                                                            DUMMY_IO_GROUP,
                                                            DUMMY_VOLUME_GROUP, ObjectIds(internal_id='', uid=''),
                                                            None, False, None, None)

    def test_create_volume_with_name_prefix(self):
        self.request.parameters[servers_settings.PARAMETERS_VOLUME_NAME_PREFIX] = NAME_PREFIX
        self._test_create_volume_parameters("prefix_some_name")

    def test_create_volume_with_no_name_prefix(self):
        self.request.parameters[servers_settings.PARAMETERS_VOLUME_NAME_PREFIX] = ""
        self._test_create_volume_parameters()

    def _test_create_volume_with_parameters_by_system_prefix(self, get_array_connection_info_from_secrets, prefix,
                                                             final_name="default_some_name",
                                                             space_efficiency=None):
        get_array_connection_info_from_secrets.side_effect = [utils.get_fake_array_connection_info()]
        system_parameters = self.request.parameters
        system_parameters.update({servers_settings.PARAMETERS_VOLUME_NAME_PREFIX: prefix,
                                  servers_settings.PARAMETERS_SPACE_EFFICIENCY: space_efficiency})
        self.request.parameters = {servers_settings.PARAMETERS_BY_SYSTEM: json.dumps({"u1": system_parameters})}
        self._test_create_volume_parameters(final_name, space_efficiency)

    @patch("controllers.servers.utils.get_array_connection_info_from_secrets")
    def test_create_volume_with_parameters_by_system_no_name_prefix(self, get_array_connection_info_from_secrets):
        self._test_create_volume_with_parameters_by_system_prefix(get_array_connection_info_from_secrets, "")

    @patch("controllers.servers.utils.get_array_connection_info_from_secrets")
    def test_create_volume_with_parameters_by_system_name_prefix(self, get_array_connection_info_from_secrets):
        self._test_create_volume_with_parameters_by_system_prefix(get_array_connection_info_from_secrets, NAME_PREFIX,
                                                                  "prefix_some_name")

    @patch("controllers.servers.utils.get_array_connection_info_from_secrets")
    def test_create_volume_with_parameters_by_system_space_efficiency(self, get_array_connection_info_from_secrets):
        self._test_create_volume_with_parameters_by_system_prefix(get_array_connection_info_from_secrets, "",
                                                                  space_efficiency="not_none")

    def test_create_volume_with_required_bytes_zero(self):
        self._prepare_create_volume_mocks()
        self.request.capacity_range.required_bytes = 0

        self.servicer.CreateVolume(self.request, self.context)
        self.assertEqual(self.context.code, grpc.StatusCode.OK)
        self.mediator.create_volume.assert_called_once_with(self.request.name, 2, None, DUMMY_POOL1, DUMMY_IO_GROUP,
                                                            DUMMY_VOLUME_GROUP,
                                                            ObjectIds(internal_id='', uid=''), None, False, None, None)

    def test_create_volume_with_required_bytes_too_large_fail(self):
        self._prepare_create_volume_mocks()
        self.request.capacity_range.required_bytes = 11

        self.servicer.CreateVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.OUT_OF_RANGE)
        self.mediator.create_volume.assert_not_called()

    def test_create_volume_with_no_space_in_pool(self):
        self.create_volume_returns_error(return_code=grpc.StatusCode.INTERNAL,
                                         err=array_errors.NotEnoughSpaceInPool(DUMMY_POOL1))

    def _prepare_snapshot_request_volume_content_source(self):
        self.request.volume_content_source = self._get_source_snapshot(SNAPSHOT_VOLUME_UID)

    def _prepare_idempotent_tests(self):
        self.mediator.get_volume = Mock()
        self.mediator.copy_to_existing_volume = Mock()
        self._prepare_snapshot_request_volume_content_source()

    def test_create_volume_idempotent_with_source_succeed(self):
        self._prepare_idempotent_tests()
        snapshot_id = SNAPSHOT_VOLUME_UID
        self.mediator.get_volume.return_value = utils.get_mock_mediator_response_volume(10, VOLUME_NAME, VOLUME_UID,
                                                                                        "a9k",
                                                                                        source_id=snapshot_id)

        response = self.servicer.CreateVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.OK)
        self.assertEqual(response.volume.content_source.snapshot.snapshot_id, snapshot_id)
        self.mediator.copy_to_existing_volume.assert_not_called()

    def test_create_volume_idempotent_with_source_volume_have_no_source(self):
        self._prepare_idempotent_tests()
        self.mediator.get_volume.return_value = utils.get_mock_mediator_response_volume(10, VOLUME_NAME, VOLUME_UID,
                                                                                        "a9k")
        response = self.servicer.CreateVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.ALREADY_EXISTS)
        self.assertFalse(response.HasField("volume"))
        self.mediator.copy_to_existing_volume.assert_not_called()

    def test_create_volume_idempotent_source_not_requested_but_found_in_volume(self):
        self._prepare_idempotent_tests()
        snapshot_id = SNAPSHOT_VOLUME_UID
        self.request.volume_content_source = None
        self.mediator.get_volume.return_value = utils.get_mock_mediator_response_volume(10, VOLUME_NAME, VOLUME_UID,
                                                                                        "a9k",
                                                                                        source_id=snapshot_id)
        response = self.servicer.CreateVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.ALREADY_EXISTS)
        self.assertFalse(response.HasField("volume"))
        self.mediator.copy_to_existing_volume.assert_not_called()

    def _prepare_idempotent_test_with_other_source(self):
        self._prepare_idempotent_tests()
        volume_source_id = SOURCE_VOLUME_ID
        self.mediator.get_volume.return_value = utils.get_mock_mediator_response_volume(10, VOLUME_NAME,
                                                                                        SNAPSHOT_VOLUME_UID, "a9k",
                                                                                        source_id=volume_source_id)
        self.servicer.CreateVolume(self.request, self.context)
        self.mediator.copy_to_existing_volume.assert_not_called()

    def test_create_volume_idempotent_with_source_volume_got_other_source(self):
        self._prepare_idempotent_test_with_other_source()
        self.assertEqual(self.context.code, grpc.StatusCode.ALREADY_EXISTS)

    def _enable_virt_snap_func(self):
        self.request.parameters[servers_settings.PARAMETERS_VIRT_SNAP_FUNC] = "true"

    def test_create_volume_idempotent_with_other_source_and_virt_snap_func_enabled(self):
        self._enable_virt_snap_func()
        self.mediator.get_object_by_id.return_value = utils.get_mock_mediator_response_volume()
        self._prepare_idempotent_test_with_other_source()
        self.assertEqual(self.context.code, grpc.StatusCode.OK)

    def test_create_volume_virt_snap_func_enabled_no_source(self):
        self._enable_virt_snap_func()
        self._prepare_snapshot_request_volume_content_source()
        self.mediator.get_object_by_id.return_value = None
        self.servicer.CreateVolume(self.request, self.context)
        self.assertEqual(self.context.code, grpc.StatusCode.NOT_FOUND)

    def test_create_volume_virt_snap_func_enabled_no_snapshot_source(self):
        self._enable_virt_snap_func()
        self._prepare_snapshot_request_volume_content_source()
        self.mediator.get_object_by_id.side_effect = [utils.get_mock_mediator_response_snapshot(), None]
        self.servicer.CreateVolume(self.request, self.context)
        self.assertEqual(self.context.code, grpc.StatusCode.INVALID_ARGUMENT)

    def test_create_volume_idempotent_with_size_not_matched(self):
        self.mediator.get_volume = Mock()
        self.mediator.get_volume.return_value = utils.get_mock_mediator_response_volume(9, VOLUME_NAME, VOLUME_UID,
                                                                                        "a9k")

        self.servicer.CreateVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.ALREADY_EXISTS)

    def _prepare_mocks_for_copy_from_source(self):
        self.mediator.create_volume = Mock()
        self.mediator.create_volume.return_value = utils.get_mock_mediator_response_volume(10, VOLUME_NAME, VOLUME_UID,
                                                                                           "a9k")

    def test_create_volume_from_snapshot_success(self):
        self._prepare_mocks_for_copy_from_source()
        snapshot_id = SNAPSHOT_VOLUME_UID
        snapshot_capacity_bytes = 100
        self.request.volume_content_source = self._get_source_snapshot(snapshot_id)
        self.mediator.get_object_by_id.return_value = utils.get_mock_mediator_response_snapshot(snapshot_capacity_bytes,
                                                                                                SNAPSHOT_NAME,
                                                                                                snapshot_id,
                                                                                                VOLUME_NAME,
                                                                                                "a9k")

        response_volume = self.servicer.CreateVolume(self.request, self.context)
        self.assertEqual(self.context.code, grpc.StatusCode.OK)
        self.mediator.copy_to_existing_volume_from_source.assert_called_once()
        self.assertEqual(response_volume.volume.content_source.volume.volume_id, '')
        self.assertEqual(response_volume.volume.content_source.snapshot.snapshot_id, snapshot_id)

    def test_create_volume_from_source_source_or_target_not_found(self):
        array_exception = array_errors.ObjectNotFoundError("")
        self._test_create_volume_from_snapshot_error(array_exception, grpc.StatusCode.NOT_FOUND)

    def test_create_volume_from_source_source_snapshot_invalid(self):
        volume_content_source = self._get_source_snapshot(SNAPSHOT_VOLUME_UID)
        volume_content_source.snapshot.snapshot_id = 'invalid_snapshot_id'
        self.request.volume_content_source = volume_content_source

        self.servicer.CreateVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.NOT_FOUND)
        self.assertIn("invalid_snapshot_id", self.context.details)

    def test_create_volume_from_source_illegal_object_id(self):
        array_exception = array_errors.InvalidArgumentError("")
        self._test_create_volume_from_snapshot_error(array_exception, grpc.StatusCode.INVALID_ARGUMENT)

    def test_create_volume_from_source_permission_denied(self):
        array_exception = array_errors.PermissionDeniedError("")
        self._test_create_volume_from_snapshot_error(array_exception, grpc.StatusCode.PERMISSION_DENIED)

    def test_create_volume_from_source_pool_missing(self):
        array_exception = array_errors.PoolParameterIsMissing("")
        self._test_create_volume_from_snapshot_error(array_exception, grpc.StatusCode.INVALID_ARGUMENT)

    def test_create_volume_from_source_general_error(self):
        array_exception = Exception("")
        self._test_create_volume_from_snapshot_error(array_exception,
                                                     grpc.StatusCode.INTERNAL)

    def test_create_volume_from_source_get_object_general_error(self):
        array_exception = Exception("")
        self._test_create_volume_from_snapshot_error(None,
                                                     grpc.StatusCode.INTERNAL, get_exception=array_exception)

    def test_create_volume_from_source_get_object_error(self):
        array_exception = array_errors.ExpectedSnapshotButFoundVolumeError("", "")
        self._test_create_volume_from_snapshot_error(None,
                                                     grpc.StatusCode.INVALID_ARGUMENT, get_exception=array_exception)

    def test_create_volume_from_source_get_object_none(self):
        self._test_create_volume_from_snapshot_error(None,
                                                     grpc.StatusCode.NOT_FOUND,
                                                     array_errors.ObjectNotFoundError(""))

    def _test_create_volume_from_snapshot_error(self, copy_exception, return_code,
                                                get_exception=None):
        self._prepare_mocks_for_copy_from_source()
        source_id = SNAPSHOT_VOLUME_UID
        self.request.volume_content_source = self._get_source_snapshot(source_id)
        if not copy_exception:
            self.mediator.copy_to_existing_volume_from_source.side_effect = [get_exception]
            self.storage_agent.get_mediator.return_value.__exit__.side_effect = [get_exception]
        else:
            self.mediator.copy_to_existing_volume_from_source.side_effect = [copy_exception]
            self.storage_agent.get_mediator.return_value.__exit__.side_effect = [copy_exception]

        response = self.servicer.CreateVolume(self.request, self.context)
        self.assertEqual(self.context.code, return_code)
        self.assertIsInstance(response, csi_pb2.CreateVolumeResponse)

    def test_clone_volume_success(self):
        self._prepare_mocks_for_copy_from_source()
        volume_id = SOURCE_VOLUME_ID
        volume_capacity_bytes = 100
        self.request.volume_content_source = self._get_source_volume(volume_id)
        self.mediator.get_object_by_id.return_value = utils.get_mock_mediator_response_volume(volume_capacity_bytes,
                                                                                              CLONE_VOLUME_NAME,
                                                                                              volume_id, "a9k")
        response_volume = self.servicer.CreateVolume(self.request, self.context)
        self.assertEqual(self.context.code, grpc.StatusCode.OK)
        self.mediator.copy_to_existing_volume_from_source.assert_called_once()
        self.assertEqual(response_volume.volume.content_source.volume.volume_id, volume_id)
        self.assertEqual(response_volume.volume.content_source.snapshot.snapshot_id, '')

    def _get_source_volume(self, object_id):
        return self._get_source(object_id, servers_settings.VOLUME_TYPE_NAME)

    def _get_source_snapshot(self, object_id):
        return self._get_source(object_id, servers_settings.SNAPSHOT_TYPE_NAME)

    @staticmethod
    def _get_source(object_id, object_type):
        source = ProtoBufMock(spec=[object_type])
        id_field_name = servers_settings.VOLUME_SOURCE_ID_FIELDS[object_type]
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
        self.mediator.delete_volume = Mock()
        self.mediator.is_volume_has_snapshots = Mock()
        self.mediator.is_volume_has_snapshots.return_value = False

        self.request.volume_id = "xiv:0;volume-id"

    def test_delete_volume_already_processing(self):
        self._test_request_already_processing("volume_id", self.request.volume_id)

    def test_delete_volume_with_wrong_secrets(self):
        self._test_request_with_wrong_secrets()

    def test_delete_volume_invalid_volume_id(self):
        self.request.volume_id = "wrong_id"

        self.servicer.DeleteVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.OK)

    @patch("controllers.servers.csi.csi_controller_server.get_agent")
    def test_delete_volume_with_array_connection_exception(self, storage_agent):
        storage_agent.side_effect = [Exception("a_enter error")]

        self.servicer.DeleteVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.INTERNAL)
        self.assertTrue("a_enter error" in self.context.details)

    def delete_volume_returns_error(self, error, return_code):
        self.mediator.delete_volume.side_effect = [error]

        self.servicer.DeleteVolume(self.request, self.context)

        self.assertEqual(self.context.code, return_code)
        if return_code != grpc.StatusCode.OK:
            msg = str(error)
            self.assertIn(msg, self.context.details, "msg : {0} is not in : {1}".format(msg, self.context.details))

    def test_delete_volume_with_volume_not_found_error(self):
        self.delete_volume_returns_error(error=array_errors.ObjectNotFoundError("volume"),
                                         return_code=grpc.StatusCode.OK)

    def test_delete_volume_with_delete_volume_other_exception(self):
        self.delete_volume_returns_error(error=Exception("error"), return_code=grpc.StatusCode.INTERNAL)

    def test_delete_volume_has_snapshots(self):
        self.delete_volume_returns_error(error=array_errors.ObjectIsStillInUseError("a", ["b"]),
                                         return_code=grpc.StatusCode.FAILED_PRECONDITION)

    @patch("controllers.array_action.array_mediator_xiv.XIVArrayMediator.delete_volume")
    def _test_delete_volume_succeeds(self, volume_id, delete_volume):
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

        self.mediator.map_volume_by_initiators = Mock()
        self.mediator.map_volume_by_initiators.return_value = "2", "iscsi", {"iqn1": ["1.1.1.1", "2.2.2.2"],
                                                                             "iqn2": ["[::1]"]}

        arr_type = XIVArrayMediator.array_type
        self.request.volume_id = "{}:wwn1".format(arr_type)
        self.iqn = "iqn.1994-05.com.redhat:686358c930fe"
        self.fc_port = "500143802426baf4"
        self.request.node_id = "{};;{};{}".format(self.hostname, self.fc_port, self.iqn)
        self.request.readonly = False

        self.request.volume_capability = utils.get_mock_volume_capability()

    def test_publish_volume_success(self):
        self.servicer.ControllerPublishVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.OK)

    def test_publish_volume_already_processing(self):
        self._test_request_already_processing("volume_id", self.request.volume_id)

    @patch("controllers.servers.utils.validate_publish_volume_request")
    def test_publish_volume_validateion_exception(self, publish_validation):
        publish_validation.side_effect = [controller_errors.ValidationException("msg")]

        self.servicer.ControllerPublishVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.INVALID_ARGUMENT)
        self.assertIn("msg", self.context.details)

    def test_publish_volume_with_wrong_secrets(self):
        self._test_request_with_wrong_secrets()

    def test_publish_volume_wrong_volume_id(self):
        self.request.volume_id = "some-wrong-id-format"

        self.servicer.ControllerPublishVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.NOT_FOUND)

    def test_publish_volume_wrong_node_id(self):
        self.request.node_id = "some-wrong-id-format"

        self.servicer.ControllerPublishVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.NOT_FOUND)

    def test_publish_volume_get_host_by_host_identifiers_exception(self):
        self.mediator.map_volume_by_initiators = Mock()
        self.mediator.map_volume_by_initiators.side_effect = [array_errors.MultipleHostsFoundError("", "")]

        self.servicer.ControllerPublishVolume(self.request, self.context)
        self.assertTrue("Multiple hosts" in self.context.details)
        self.assertEqual(self.context.code, grpc.StatusCode.INTERNAL)

        self.mediator.map_volume_by_initiators.side_effect = [array_errors.HostNotFoundError("")]

        self.servicer.ControllerPublishVolume(self.request, self.context)
        self.assertEqual(self.context.code, grpc.StatusCode.NOT_FOUND)

    def test_publish_volume_with_connectivity_type_fc(self):
        self.mediator.map_volume_by_initiators.return_value = "1", "fc", ["500143802426baf4"]

        response = self.servicer.ControllerPublishVolume(self.request, self.context)
        self.assertEqual(self.context.code, grpc.StatusCode.OK)

        self.assertEqual(response.publish_context["PUBLISH_CONTEXT_LUN"], '1')
        self.assertEqual(response.publish_context["PUBLISH_CONTEXT_CONNECTIVITY"], "fc")
        self.assertEqual(response.publish_context["PUBLISH_CONTEXT_ARRAY_FC_INITIATORS"], "500143802426baf4")

    def test_publish_volume_with_connectivity_type_iscsi(self):
        response = self.servicer.ControllerPublishVolume(self.request, self.context)
        self.assertEqual(self.context.code, grpc.StatusCode.OK)

        self.assertEqual(response.publish_context["PUBLISH_CONTEXT_LUN"], '2')
        self.assertEqual(response.publish_context["PUBLISH_CONTEXT_CONNECTIVITY"],
                         "iscsi")
        self.assertEqual(response.publish_context["PUBLISH_CONTEXT_ARRAY_IQN"],
                         "iqn1,iqn2")
        self.assertEqual(response.publish_context["iqn1"],
                         "1.1.1.1,2.2.2.2")
        self.assertEqual(response.publish_context["iqn2"],
                         "[::1]")

    def test_publish_volume_get_volume_mappings_more_then_one_mapping(self):
        self.mediator.map_volume_by_initiators.side_effect = [array_errors.VolumeAlreadyMappedToDifferentHostsError("")]
        self.servicer.ControllerPublishVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.FAILED_PRECONDITION)
        self.assertTrue("Volume is already mapped" in self.context.details)

    def test_publish_volume_map_volume_excpetions(self):
        self.mediator.map_volume_by_initiators.side_effect = [array_errors.PermissionDeniedError("msg")]

        self.servicer.ControllerPublishVolume(self.request, self.context)
        self.assertEqual(self.context.code, grpc.StatusCode.PERMISSION_DENIED)

        self.mediator.map_volume_by_initiators.side_effect = [array_errors.ObjectNotFoundError("volume")]

        self.servicer.ControllerPublishVolume(self.request, self.context)
        self.assertEqual(self.context.code, grpc.StatusCode.NOT_FOUND)

        self.mediator.map_volume_by_initiators.side_effect = [array_errors.HostNotFoundError("host")]

        self.servicer.ControllerPublishVolume(self.request, self.context)
        self.assertEqual(self.context.code, grpc.StatusCode.NOT_FOUND)

        self.mediator.map_volume_by_initiators.side_effect = [array_errors.MappingError("", "", "")]

        self.servicer.ControllerPublishVolume(self.request, self.context)
        self.assertEqual(self.context.code, grpc.StatusCode.INTERNAL)

    def test_publish_volume_map_volume_lun_already_in_use(self):
        self.mediator.map_volume_by_initiators.side_effect = [array_errors.NoAvailableLunError("")]

        self.servicer.ControllerPublishVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.RESOURCE_EXHAUSTED)

    def test_publish_volume_get_iscsi_targets_by_iqn_excpetions(self):
        self.mediator.map_volume_by_initiators.side_effect = [array_errors.NoIscsiTargetsFoundError("some_endpoint")]

        self.servicer.ControllerPublishVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.NOT_FOUND)

    def test_map_volume_by_initiators_exceptions(self):
        self.mediator.map_volume_by_initiators.side_effect = [
            array_errors.UnsupportedConnectivityTypeError("usb")]

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

        self.mediator.unmap_volume_by_initiators = Mock()
        self.mediator.unmap_volume_by_initiators.return_value = None

        arr_type = XIVArrayMediator.array_type
        self.request.volume_id = "{}:wwn1".format(arr_type)
        self.request.node_id = "hostname;iqn1;500143802426baf4"

    def test_unpublish_volume_success(self):
        self.servicer.ControllerUnpublishVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.OK)

    def test_unpublish_volume_already_processing(self):
        self._test_request_already_processing("volume_id", self.request.volume_id)

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

    def test_unpublish_volume_with_wrong_secrets(self):
        self._test_request_with_wrong_secrets()

    def test_unpublish_volume_with_too_much_delimiters_in_volume_id(self):
        self.request.volume_id = "too:much:delimiters:in:id"

        self.servicer.ControllerUnpublishVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.INVALID_ARGUMENT)

    def test_unpublish_volume_wrong_node_id(self):
        self.request.node_id = "some-wrong-id-format"

        self.servicer.ControllerUnpublishVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.OK)

    def test_unpublish_volume_get_host_by_host_identifiers_multiple_hosts_found_error(self):
        self.mediator.unmap_volume_by_initiators.side_effect = [array_errors.MultipleHostsFoundError("", "")]

        self.servicer.ControllerUnpublishVolume(self.request, self.context)
        self.assertTrue("Multiple hosts" in self.context.details)
        self.assertEqual(self.context.code, grpc.StatusCode.INTERNAL)

    def test_unpublish_volume_get_host_by_host_identifiers_host_not_found_error(self):
        self.mediator.get_host_by_host_identifiers = Mock()
        self.mediator.get_host_by_host_identifiers.side_effect = [array_errors.HostNotFoundError("")]

        self.servicer.ControllerUnpublishVolume(self.request, self.context)
        self.assertEqual(self.context.code, grpc.StatusCode.OK)

    def _test_unpublish_volume_unmap_volume_by_initiators_with_error(self, array_error, status_code):
        self.mediator.unmap_volume_by_initiators.side_effect = [array_error]

        self.servicer.ControllerUnpublishVolume(self.request, self.context)
        self.assertEqual(self.context.code, status_code)

    def test_unpublish_volume_unmap_volume_by_initiators_object_not_found_error(self):
        self._test_unpublish_volume_unmap_volume_by_initiators_with_error(array_errors.ObjectNotFoundError("volume"),
                                                                          grpc.StatusCode.OK)

    def test_unpublish_volume_unmap_volume_by_initiators_volume_already_unmapped_error(self):
        self._test_unpublish_volume_unmap_volume_by_initiators_with_error(array_errors.VolumeAlreadyUnmappedError(""),
                                                                          grpc.StatusCode.OK)

    def test_unpublish_volume_unmap_volume_by_initiators_volume_not_mapped_to_host_error(self):
        self._test_unpublish_volume_unmap_volume_by_initiators_with_error(
            array_errors.VolumeNotMappedToHostError("volume", "host"),
            grpc.StatusCode.OK)

    def test_unpublish_volume_unmap_volume_by_initiators_permission_denied_error(self):
        self._test_unpublish_volume_unmap_volume_by_initiators_with_error(array_errors.PermissionDeniedError("msg"),
                                                                          grpc.StatusCode.PERMISSION_DENIED)

    def test_unpublish_volume_unmap_volume_by_initiators_host_not_found_error(self):
        self._test_unpublish_volume_unmap_volume_by_initiators_with_error(array_errors.HostNotFoundError("host"),
                                                                          grpc.StatusCode.OK)

    def test_unpublish_volume_unmap_volume_by_initiators_unmapping_error(self):
        self._test_unpublish_volume_unmap_volume_by_initiators_with_error(array_errors.UnmappingError("", "", ""),
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

        self.mediator.expand_volume = Mock()

        self.request.parameters = {}
        self.volume_id = "vol-id"
        self.request.volume_id = "{}:{}".format("xiv", self.volume_id)
        self.request.volume_content_source = None
        self.mediator.get_object_by_id = Mock()
        self.volume_before_expand = utils.get_mock_mediator_response_volume(2, VOLUME_NAME, self.volume_id, "a9k")
        self.volume_after_expand = utils.get_mock_mediator_response_volume(self.capacity_bytes, VOLUME_NAME,
                                                                           self.volume_id, "a9k")
        self.mediator.get_object_by_id.side_effect = [self.volume_before_expand, self.volume_after_expand]
        self.request.volume_capability = self.volume_capability

    def _prepare_expand_volume_mocks(self):
        self.mediator.expand_volume = Mock()

    def test_expand_volume_already_processing(self):
        self._test_request_already_processing("volume_id", self.request.volume_id)

    def test_expand_volume_with_required_bytes_too_large_fail(self):
        self._prepare_expand_volume_mocks()
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

    def test_expand_volume_with_required_bytes_below_minimal(self):
        self._prepare_expand_volume_mocks()
        self.request.capacity_range.required_bytes = 1
        self._test_no_expand_needed()

    def test_expand_volume_with_required_bytes_zero(self):
        self._prepare_expand_volume_mocks()
        self.request.capacity_range.required_bytes = 0
        self._test_no_expand_needed()

    def test_expand_volume_with_volume_size_already_in_range(self):
        self._prepare_expand_volume_mocks()
        self.request.capacity_range.required_bytes = 2
        self._test_no_expand_needed()

    def test_expand_volume_succeeds(self):
        self._prepare_expand_volume_mocks()

        response = self.servicer.ControllerExpandVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.OK)
        self.assertTrue(response.node_expansion_required)
        self.assertEqual(response.capacity_bytes, self.volume_after_expand.capacity_bytes)
        self.mediator.expand_volume.assert_called_once_with(volume_id=self.volume_id,
                                                            required_bytes=self.capacity_bytes)

    def test_expand_volume_with_bad_id(self):
        self._prepare_expand_volume_mocks()
        self.request.volume_id = "123"

        self.servicer.ControllerExpandVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.INVALID_ARGUMENT)
        self.mediator.expand_volume.assert_not_called()

    def test_expand_volume_not_found_before_expansion(self):
        self._prepare_expand_volume_mocks()
        self.mediator.get_object_by_id.side_effect = [None, None]

        self.servicer.ControllerExpandVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.NOT_FOUND)

    def test_expand_volume_not_found_after_expansion(self):
        self._prepare_expand_volume_mocks()
        self.mediator.get_object_by_id.side_effect = [self.volume_before_expand, None]

        self.servicer.ControllerExpandVolume(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.NOT_FOUND)

    def test_expand_volume_with_wrong_secrets(self):
        self._test_request_with_wrong_secrets()

    def test_expand_volume_with_array_connection_exception(self):
        self._test_request_with_array_connection_exception()

    def _expand_volume_returns_error(self, return_code, err):
        self.mediator.expand_volume.side_effect = [err]
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
                                          err=array_errors.NotEnoughSpaceInPool(DUMMY_POOL1))


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
        self.request.volume_id = "{}:{}".format(arr_type, VOLUME_UID)
        self.request.parameters = {servers_settings.PARAMETERS_POOL: DUMMY_POOL1}

        self.mediator.get_object_by_id = Mock()
        self.mediator.get_object_by_id.return_value = utils.get_mock_mediator_response_volume(10, "vol", VOLUME_UID,
                                                                                              "a9k")
        self.request.volume_capabilities = [self.volume_capability]

    def _assert_response(self, expected_status_code, expected_details_substring):
        self.assertEqual(self.context.code, expected_status_code)
        self.assertTrue(expected_details_substring in self.context.details)

    def test_validate_volume_capabilities_already_processing(self):
        self._test_request_already_processing("volume_id", self.request.volume_id)

    def test_validate_volume_capabilities_success(self):
        self.servicer.ValidateVolumeCapabilities(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.OK)

    @patch("controllers.servers.utils.get_volume_id_info")
    def test_validate_volume_capabilities_object_id_error(self, get_volume_id_info):
        get_volume_id_info.side_effect = [controller_errors.ObjectIdError("object_type", "object_id")]

        self.servicer.ValidateVolumeCapabilities(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.NOT_FOUND)
        self.assertIn("object_type", self.context.details)
        self.assertIn("object_id", self.context.details)

    def test_validate_volume_capabilities_with_empty_id(self):
        self.request.volume_id = ""

        self.servicer.ValidateVolumeCapabilities(self.request, self.context)

        self._assert_response(grpc.StatusCode.INVALID_ARGUMENT, "volume_id")

    def test_validate_volume_capabilities_with_wrong_secrets(self):
        self._test_request_with_wrong_secrets()

    def test_validate_volume_capabilities_with_unsupported_access_mode(self):
        self.request.volume_capabilities[0].access_mode.mode = 999

        self.servicer.ValidateVolumeCapabilities(self.request, self.context)

        self._assert_response(grpc.StatusCode.INVALID_ARGUMENT, "unsupported access mode")

    def test_validate_volume_capabilities_with_unsupported_fs_type(self):
        volume_capability = utils.get_mock_volume_capability(fs_type="ext3")
        self.request.volume_capabilities = [volume_capability]

        self.servicer.ValidateVolumeCapabilities(self.request, self.context)

        self._assert_response(grpc.StatusCode.INVALID_ARGUMENT, "fs_type")

    def test_validate_volume_capabilities_with_no_capabilities(self):
        self.request.volume_capabilities = {}

        self.servicer.ValidateVolumeCapabilities(self.request, self.context)

        self._assert_response(grpc.StatusCode.INVALID_ARGUMENT, "not set")

    def test_validate_volume_capabilities_with_bad_id(self):
        self.request.volume_id = VOLUME_UID

        self.servicer.ValidateVolumeCapabilities(self.request, self.context)

        self._assert_response(grpc.StatusCode.NOT_FOUND, "id format")

    def test_validate_volume_capabilities_with_volume_not_found(self):
        self.mediator.get_object_by_id.return_value = None

        self.servicer.ValidateVolumeCapabilities(self.request, self.context)

        self._assert_response(grpc.StatusCode.NOT_FOUND, VOLUME_UID)

    def test_validate_volume_capabilities_with_volume_context_not_match(self):
        self.request.volume_context = {servers_settings.VOLUME_CONTEXT_VOLUME_NAME: "fake"}

        self.servicer.ValidateVolumeCapabilities(self.request, self.context)

        self._assert_response(grpc.StatusCode.INVALID_ARGUMENT, "volume context")

    def test_validate_volume_capabilities_with_space_efficiency_not_match(self):
        self.request.parameters.update({servers_settings.PARAMETERS_SPACE_EFFICIENCY: "not_none"})
        self.mediator.validate_supported_space_efficiency = Mock()

        self.servicer.ValidateVolumeCapabilities(self.request, self.context)

        self._assert_response(grpc.StatusCode.INVALID_ARGUMENT, "space efficiency")

    def test_validate_volume_capabilities_with_pool_not_match(self):
        self.request.parameters.update({servers_settings.PARAMETERS_POOL: "other pool"})

        self.servicer.ValidateVolumeCapabilities(self.request, self.context)

        self._assert_response(grpc.StatusCode.INVALID_ARGUMENT, DUMMY_POOL1)

    def test_validate_volume_capabilities_with_prefix_not_match(self):
        self.request.parameters.update({servers_settings.PARAMETERS_VOLUME_NAME_PREFIX: NAME_PREFIX})

        self.servicer.ValidateVolumeCapabilities(self.request, self.context)

        self._assert_response(grpc.StatusCode.INVALID_ARGUMENT, NAME_PREFIX)

    def test_validate_volume_capabilities_parameters_success(self):
        self.request.parameters = {servers_settings.PARAMETERS_VOLUME_NAME_PREFIX: NAME_PREFIX,
                                   servers_settings.PARAMETERS_POOL: "pool2",
                                   servers_settings.PARAMETERS_SPACE_EFFICIENCY: "not_none"}
        volume_response = utils.get_mock_mediator_response_volume(10, "prefix_vol", VOLUME_UID, "a9k",
                                                                  space_efficiency="not_none")
        volume_response.pool = "pool2"
        self.mediator.get_object_by_id.return_value = volume_response
        self.mediator.validate_supported_space_efficiency = Mock()

        self.servicer.ValidateVolumeCapabilities(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.OK)
