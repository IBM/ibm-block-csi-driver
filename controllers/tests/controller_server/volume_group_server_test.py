import unittest

import grpc
from csi_general import volumegroup_pb2
from mock import MagicMock, Mock

from controllers.array_action import errors as array_errors
from controllers.servers import settings as servers_settings
from controllers.servers.csi.volume_group_server import VolumeGroupControllerServicer
from controllers.tests import utils
from controllers.tests.common.test_settings import SECRET, VOLUME_GROUP_NAME, NAME_PREFIX, REQUEST_VOLUME_GROUP_ID, \
    VOLUME_GROUP_UID, REQUEST_VOLUME_ID, VOLUME_UID
from controllers.tests.controller_server.common import mock_array_type, mock_mediator, mock_get_agent
from controllers.tests.controller_server.csi_controller_server_test import CommonControllerTest
from controllers.tests.utils import ProtoBufMock

VG_CONTROLLER_SERVER_PATH = "controllers.servers.csi.volume_group_server"


class BaseVgControllerSetUp(unittest.TestCase):

    def setUp(self):
        self.servicer = VolumeGroupControllerServicer()

        mock_array_type(self, VG_CONTROLLER_SERVER_PATH)

        self.mediator = mock_mediator()

        self.storage_agent = MagicMock()
        mock_get_agent(self, VG_CONTROLLER_SERVER_PATH)

        self.request = ProtoBufMock()
        self.request.secrets = SECRET

        self.request.parameters = {}
        self.request.volume_context = {}
        self.volume_capability = utils.get_mock_volume_capability()
        self.capacity_bytes = 10
        self.request.capacity_range = Mock()
        self.request.capacity_range.required_bytes = self.capacity_bytes
        self.context = utils.FakeContext()


class TestCreateVolumeGroup(BaseVgControllerSetUp, CommonControllerTest):

    @property
    def tested_method(self):
        return self.servicer.CreateVolumeGroup

    @property
    def tested_method_response_class(self):
        return volumegroup_pb2.CreateVolumeGroupResponse

    def setUp(self):
        super().setUp()
        self.request.name = VOLUME_GROUP_NAME

    def test_create_volume_group_with_empty_name(self):
        self._test_create_object_with_empty_name()

    def test_create_volume_group_with_wrong_secrets(self, ):
        self._test_request_with_wrong_secrets()

    def test_create_volume_group_already_processing(self):
        self._test_request_already_processing("name", self.request.name)

    def _prepare_create_volume_without_get(self):
        self.mediator.get_volume_group = Mock(side_effect=array_errors.ObjectNotFoundError(""))
        self.mediator.create_volume_group = Mock()
        self.mediator.create_volume_group.return_value = utils.get_mock_mediator_response_volume_group()

    def test_create_volume_group_success(self):
        self._prepare_create_volume_without_get()

        response = self.servicer.CreateVolumeGroup(self.request, self.context)

        self.mediator.create_volume_group.assert_called_once_with(VOLUME_GROUP_NAME)
        self.assertEqual(type(response), volumegroup_pb2.CreateVolumeGroupResponse)
        self.assertEqual(self.context.code, grpc.StatusCode.OK)

    def test_create_volume_group_with_prefix_success(self):
        self._prepare_create_volume_without_get()
        self.request.parameters = {servers_settings.PARAMETERS_VOLUME_GROUP_NAME_PREFIX: NAME_PREFIX}

        self.servicer.CreateVolumeGroup(self.request, self.context)

        self.mediator.create_volume_group.assert_called_once_with('prefix_volume_group_name')
        self.assertEqual(self.context.code, grpc.StatusCode.OK)

    def test_create_volume_group_already_exist_fail(self):
        self.mediator.get_volume_group = Mock(side_effect=array_errors.ObjectNotFoundError(""))
        self.mediator.create_volume_group = Mock(side_effect=array_errors.VolumeGroupAlreadyExists("", ""))

        response = self.servicer.CreateVolumeGroup(self.request, self.context)

        self.mediator.create_volume_group.assert_called_once_with(VOLUME_GROUP_NAME)
        self.assertEqual(type(response), volumegroup_pb2.CreateVolumeGroupResponse)
        self.assertEqual(self.context.code, grpc.StatusCode.ALREADY_EXISTS)

    def test_get_volume_success(self):
        self.mediator.get_volume_group = Mock(return_value=utils.get_mock_mediator_response_volume_group())

        response = self.servicer.CreateVolumeGroup(self.request, self.context)

        self.mediator.get_volume_group.assert_called_once_with(VOLUME_GROUP_NAME)
        self.mediator.create_volume_group.assert_not_called()
        self.assertEqual(type(response), volumegroup_pb2.CreateVolumeGroupResponse)
        self.assertEqual(self.context.code, grpc.StatusCode.OK)

    def test_group_get_volume_not_empty_fail(self):
        volumes = [utils.get_mock_mediator_response_volume()]
        response_volume_group = utils.get_mock_mediator_response_volume_group(volumes=volumes)
        self.mediator.get_volume_group = Mock(return_value=response_volume_group)

        response = self.servicer.CreateVolumeGroup(self.request, self.context)

        self.mediator.get_volume_group.assert_called_once_with(VOLUME_GROUP_NAME)
        self.assertEqual(type(response), volumegroup_pb2.CreateVolumeGroupResponse)
        self.assertEqual(self.context.code, grpc.StatusCode.ALREADY_EXISTS)


class TestDeleteVolumeGroup(BaseVgControllerSetUp, CommonControllerTest):

    @property
    def tested_method(self):
        return self.servicer.DeleteVolumeGroup

    @property
    def tested_method_response_class(self):
        return volumegroup_pb2.DeleteVolumeGroupResponse

    def setUp(self):
        super().setUp()
        self.request.volume_group_id = REQUEST_VOLUME_GROUP_ID

    def test_delete_volume_group_success(self):
        self.servicer.DeleteVolumeGroup(self.request, self.context)
        self.assertEqual(self.context.code, grpc.StatusCode.OK)

    def _delete_volume_group_returns_error(self, error, return_code):
        self.mediator.delete_volume_group.side_effect = [error]

        self.servicer.DeleteVolumeGroup(self.request, self.context)

        self.assertEqual(self.context.code, return_code)
        if return_code != grpc.StatusCode.OK:
            msg = str(error)
            self.assertIn(msg, self.context.details, "msg : {0} is not in : {1}".format(msg, self.context.details))

    def test_delete_volume_group_with_volume_not_found_error(self):
        self._delete_volume_group_returns_error(error=array_errors.ObjectNotFoundError("volume"),
                                                return_code=grpc.StatusCode.OK)

    def test_delete_volume_group_with_delete_volume_other_exception(self):
        self._delete_volume_group_returns_error(error=Exception("error"), return_code=grpc.StatusCode.INTERNAL)

    def test_delete_volume_group_with_wrong_secrets(self):
        self._test_request_with_wrong_secrets()

    def test_delete_volume_group_with_array_connection_exception(self):
        self._test_request_with_array_connection_exception()

    def test_delete_volume_group_bad_id(self):
        self.request.volume_group_id = VOLUME_GROUP_UID
        self.servicer.DeleteVolumeGroup(self.request, self.context)
        self.assertEqual(self.context.code, grpc.StatusCode.OK)


class TestModifyVolumeGroupMembership(BaseVgControllerSetUp, CommonControllerTest):

    @property
    def tested_method(self):
        return self.servicer.ModifyVolumeGroupMembership

    @property
    def tested_method_response_class(self):
        return volumegroup_pb2.ModifyVolumeGroupMembershipResponse

    def setUp(self):
        super().setUp()
        self.request.volume_group_id = REQUEST_VOLUME_GROUP_ID

    def test_modify_volume_group_success(self):
        self.mediator.get_volume_group.return_value = utils.get_mock_mediator_response_volume_group()
        self.servicer.ModifyVolumeGroupMembership(self.request, self.context)
        self.assertEqual(self.context.code, grpc.StatusCode.OK)

    def _prepare_modify_volume_group_volumes(self, volume_ids_in_request=None, volumes_in_volume_group=None,
                                             volumes_in_volume_group_after=None):
        if volume_ids_in_request is None:
            volume_ids_in_request = []
        self.request.volume_ids = volume_ids_in_request
        self.mediator.get_volume_group.side_effect = [
            utils.get_mock_mediator_response_volume_group(volumes=volumes_in_volume_group),
            utils.get_mock_mediator_response_volume_group(volumes=volumes_in_volume_group_after)]

    def test_modify_volume_group_add_success(self):
        volume_in_volume_group = utils.get_mock_mediator_response_volume()
        self._prepare_modify_volume_group_volumes(volume_ids_in_request=[REQUEST_VOLUME_ID],
                                                  volumes_in_volume_group_after=[volume_in_volume_group])

        volume_group_response = self.servicer.ModifyVolumeGroupMembership(self.request, self.context)
        self.assertEqual(self.context.code, grpc.StatusCode.OK)
        self.mediator.get_volume_group.assert_called_with(VOLUME_GROUP_NAME)
        self.mediator.add_volume_to_volume_group.assert_called_once_with(VOLUME_GROUP_NAME, VOLUME_UID)
        self.mediator.remove_volume_from_volume_group.assert_not_called()
        self.assertEqual(volume_group_response.volume_group.volume_group_id, REQUEST_VOLUME_GROUP_ID)
        self.assertEqual(len(volume_group_response.volume_group.volumes), 1)

    def test_modify_volume_group_remove_success(self):
        volume_in_volume_group = utils.get_mock_mediator_response_volume()
        self._prepare_modify_volume_group_volumes(volumes_in_volume_group=[volume_in_volume_group])

        volume_group_response = self.servicer.ModifyVolumeGroupMembership(self.request, self.context)
        self.assertEqual(self.context.code, grpc.StatusCode.OK)
        self.mediator.remove_volume_from_volume_group.assert_called_once_with(VOLUME_UID)
        self.mediator.add_volume_to_volume_group.assert_not_called()
        self.assertEqual(volume_group_response.volume_group.volume_group_id, REQUEST_VOLUME_GROUP_ID)
        self.assertEqual(len(volume_group_response.volume_group.volumes), 0)

    def test_modify_volume_group_with_wrong_secrets(self):
        self._test_request_with_wrong_secrets()

    def test_modify_volume_group_with_array_connection_exception(self):
        self._test_request_with_array_connection_exception()

    def test_modify_volume_group_with_bad_id(self):
        self.request.volume_group_id = "bad_id"

        response = self.servicer.ModifyVolumeGroupMembership(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.INVALID_ARGUMENT)
        self.mediator.remove_volume_from_volume_group.assert_not_called()
        self.mediator.add_volume_to_volume_group.assert_not_called()
        self.assertEqual(type(response), volumegroup_pb2.ModifyVolumeGroupMembershipResponse)

    def test_modify_volume_group_already_exist_fail(self):
        self.mediator.get_volume_group = Mock(side_effect=array_errors.ObjectNotFoundError(""))

        response = self.servicer.ModifyVolumeGroupMembership(self.request, self.context)

        self.mediator.remove_volume_from_volume_group.assert_not_called()
        self.mediator.add_volume_to_volume_group.assert_not_called()
        self.assertEqual(type(response), volumegroup_pb2.ModifyVolumeGroupMembershipResponse)
        self.assertEqual(self.context.code, grpc.StatusCode.NOT_FOUND)
