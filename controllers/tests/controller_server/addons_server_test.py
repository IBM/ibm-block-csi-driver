import unittest

import grpc
from csi_general import replication_pb2 as pb2
from mock import Mock, MagicMock

from controllers.servers.settings import PARAMETERS_SYSTEM_ID, PARAMETERS_COPY_TYPE, PARAMETERS_REPLICATION_POLICY
from controllers.array_action.settings import REPLICATION_TYPE_MIRROR, REPLICATION_TYPE_EAR, REPLICATION_COPY_TYPE_SYNC
from controllers.array_action.array_action_types import ReplicationRequest
from controllers.servers.csi.addons_server import ReplicationControllerServicer
from controllers.tests import utils
from controllers.tests.common.test_settings import VOLUME_NAME, VOLUME_UID, OBJECT_INTERNAL_ID, \
    OTHER_OBJECT_INTERNAL_ID, REPLICATION_NAME, SYSTEM_ID, COPY_TYPE, SECRET_USERNAME_VALUE, SECRET_PASSWORD_VALUE, \
    SECRET_MANAGEMENT_ADDRESS_VALUE, DUMMY_VOLUME_GROUP
from controllers.tests.controller_server.common import mock_get_agent
from controllers.tests.controller_server.csi_controller_server_test import (CommonControllerTest)
from controllers.tests.utils import ProtoBufMock

ADDON_SERVER_PATH = "controllers.servers.csi.addons_server"


class BaseReplicationSetUp(unittest.TestCase):

    def setUp(self):
        self.servicer = ReplicationControllerServicer()
        self.mediator = Mock()
        self.mediator.client = Mock()

        self.storage_agent = MagicMock()
        mock_get_agent(self, ADDON_SERVER_PATH)

        self.request = ProtoBufMock()
        self.request.secrets = {"username": SECRET_USERNAME_VALUE, "password": SECRET_PASSWORD_VALUE,
                                "management_address": SECRET_MANAGEMENT_ADDRESS_VALUE}
        self.request.volume_id = "{}:{};{}".format("A9000", OBJECT_INTERNAL_ID, VOLUME_UID)
        self.request.replication_id = "{}:{};{}".format("A9000", OTHER_OBJECT_INTERNAL_ID, VOLUME_UID)
        self.context = utils.FakeContext()

    def _prepare_replication_mocks(self, replication_type=None, copy_type=COPY_TYPE, is_primary=False,
                                   volume_group_id=None):
        if replication_type:
            replication = utils.get_mock_mediator_response_replication(name=REPLICATION_NAME,
                                                                       replication_type=replication_type,
                                                                       copy_type=copy_type,
                                                                       is_primary=is_primary)
        else:
            replication = None
        self.mediator.get_replication.return_value = replication

    def _prepare_request_params(self, replication_type, replication_name=REPLICATION_NAME,
                                replication_id=""):
        if replication_type == REPLICATION_TYPE_MIRROR:
            self.request.parameters = {PARAMETERS_SYSTEM_ID: SYSTEM_ID,
                                       PARAMETERS_COPY_TYPE: COPY_TYPE}
            replication_request = ReplicationRequest(OBJECT_INTERNAL_ID, OTHER_OBJECT_INTERNAL_ID, SYSTEM_ID, COPY_TYPE,
                                                     REPLICATION_TYPE_MIRROR)
        else:
            self.request.replication_id = replication_id
            self.request.parameters = {PARAMETERS_REPLICATION_POLICY: replication_name}
            replication_request = ReplicationRequest(OBJECT_INTERNAL_ID, None, None, REPLICATION_COPY_TYPE_SYNC,
                                                     REPLICATION_TYPE_EAR, replication_name)
        return replication_request


class TestEnableVolumeReplication(BaseReplicationSetUp, CommonControllerTest):
    @property
    def tested_method(self):
        return self.servicer.EnableVolumeReplication

    @property
    def tested_method_response_class(self):
        return pb2.EnableVolumeReplicationResponse

    def setUp(self):
        super().setUp()
        self.mediator.get_object_by_id = Mock()
        self.mediator.get_object_by_id.return_value = utils.get_mock_mediator_response_volume(10, VOLUME_NAME,
                                                                                              VOLUME_UID, "xiv")

    def _test_enable_replication_succeeds(self, replication_type):
        replication_request = self._prepare_request_params(replication_type)
        self._prepare_replication_mocks()

        self.servicer.EnableVolumeReplication(self.request, self.context)

        self.assertEqual(grpc.StatusCode.OK, self.context.code)
        self.mediator.get_replication.assert_called_once_with(replication_request)
        self.mediator.create_replication.assert_called_once_with(replication_request)

    def _test_enable_replication_fails(self, replication_type, replication_id, grpc_status):
        self._prepare_request_params(replication_type=replication_type, replication_id=replication_id)
        self._prepare_replication_mocks(replication_type)

        self.servicer.EnableVolumeReplication(self.request, self.context)

        self.assertEqual(grpc_status, self.context.code)
        self.mediator.get_replication.assert_not_called()
        self.mediator.create_replication.assert_not_called()

    def _test_enable_replication_idempotency(self, replication_type, replication_name=REPLICATION_NAME,
                                             copy_type=COPY_TYPE, grpc_status=grpc.StatusCode.OK):
        replication_request = self._prepare_request_params(replication_type, replication_name)
        self._prepare_replication_mocks(replication_type=replication_type, copy_type=copy_type,
                                        volume_group_id=DUMMY_VOLUME_GROUP)

        self.servicer.EnableVolumeReplication(self.request, self.context)

        self.assertEqual(grpc_status, self.context.code)
        self.mediator.get_replication.assert_called_once_with(replication_request)
        self.mediator.create_replication.assert_not_called()

    def test_enable_replication_succeeds(self):
        self._test_enable_replication_succeeds(REPLICATION_TYPE_MIRROR)

    def test_enable_replication_idempotency_succeeds(self):
        self._test_enable_replication_idempotency(replication_type=REPLICATION_TYPE_MIRROR)

    def test_enable_replication_idempotency_fails(self):
        self._test_enable_replication_idempotency(replication_type=REPLICATION_TYPE_MIRROR,
                                                  copy_type=REPLICATION_COPY_TYPE_SYNC,
                                                  grpc_status=grpc.StatusCode.ALREADY_EXISTS)

    def test_enable_replication_no_volume_fails(self):
        self.mediator.get_object_by_id.return_value = None
        self._test_enable_replication_fails(grpc.StatusCode.NOT_FOUND)

    def test_enable_replication_volume_in_group_fails(self):
        self.mediator.get_object_by_id.return_value = utils.get_mock_mediator_response_volume(
            volume_group_id=DUMMY_VOLUME_GROUP)
        self._test_enable_replication_fails(grpc.StatusCode.FAILED_PRECONDITION)

    def test_enable_replication_already_processing(self):
        self._test_request_already_processing("volume_id", self.request.volume_id)

    def test_enable_replication_with_wrong_secrets(self):
        self._test_request_with_wrong_secrets()

    def test_enable_replication_with_array_connection_exception(self):
        self._test_request_with_array_connection_exception()

    def test_enable_ear_replication_obsolete_request_parameters_fails(self):
        replication_id = "{}:{};{}".format("A9000", OTHER_OBJECT_INTERNAL_ID, VOLUME_UID)
        self._test_enable_replication_fails(REPLICATION_TYPE_EAR, replication_id, grpc.StatusCode.INVALID_ARGUMENT)

    def test_enable_ear_replication_succeeds(self):
        self._test_enable_replication_succeeds(REPLICATION_TYPE_EAR)

    def test_enable_ear_replication_idempotency_volume_belongs_to_another_group_fails(self):
        self._test_enable_replication_idempotency(replication_type=REPLICATION_TYPE_EAR,
                                                  copy_type=REPLICATION_COPY_TYPE_SYNC,
                                                  grpc_status=grpc.StatusCode.ALREADY_EXISTS)

    def test_enable_ear_replication_idempotency_volume_has_another_policy_fails(self):
        self._test_enable_replication_idempotency(replication_type=REPLICATION_TYPE_EAR,
                                                  replication_name="",
                                                  copy_type=REPLICATION_COPY_TYPE_SYNC,
                                                  grpc_status=grpc.StatusCode.ALREADY_EXISTS)


class TestDisableVolumeReplication(BaseReplicationSetUp, CommonControllerTest):
    @property
    def tested_method(self):
        return self.servicer.DisableVolumeReplication

    @property
    def tested_method_response_class(self):
        return pb2.DisableVolumeReplicationResponse

    def _test_disable_replication_succeeds(self, replication_type):
        replication_request = self._prepare_request_params(replication_type)
        self._prepare_replication_mocks(replication_type=replication_type)

        self.servicer.DisableVolumeReplication(self.request, self.context)

        self.assertEqual(grpc.StatusCode.OK, self.context.code)
        replication = utils.get_mock_mediator_response_replication(name=REPLICATION_NAME,
                                                                   replication_type=replication_type)
        self.mediator.get_replication.assert_called_once_with(replication_request)
        self.mediator.delete_replication.assert_called_once_with(replication)

    def _test_disable_replication_idempotency_succeeds(self, replication_type):
        replication_request = self._prepare_request_params(replication_type)
        self._prepare_replication_mocks()

        self.servicer.DisableVolumeReplication(self.request, self.context)

        self.assertEqual(grpc.StatusCode.OK, self.context.code)
        self.mediator.get_replication.assert_called_once_with(replication_request)
        self.mediator.delete_replication.assert_not_called()

    def test_disable_replication_succeeds(self):
        self._test_disable_replication_succeeds(replication_type=REPLICATION_TYPE_MIRROR)

    def test_disable_replication_idempotency_succeeds(self):
        self._test_disable_replication_idempotency_succeeds(REPLICATION_TYPE_MIRROR)

    def test_disable_replication_already_processing(self):
        self._test_request_already_processing("volume_id", self.request.volume_id)

    def test_disable_replication_with_wrong_secrets(self):
        self._test_request_with_wrong_secrets()

    def test_disable_replication_with_array_connection_exception(self):
        self._test_request_with_array_connection_exception()

    def test_disable_ear_replication_succeeds(self):
        self._test_disable_replication_succeeds(REPLICATION_TYPE_EAR)


class TestPromoteVolume(BaseReplicationSetUp, CommonControllerTest):
    @property
    def tested_method(self):
        return self.servicer.PromoteVolume

    @property
    def tested_method_response_class(self):
        return pb2.PromoteVolumeResponse

    def _test_promote_replication_succeeds(self, replication_type, is_primary=False):
        replication_request = self._prepare_request_params(replication_type)
        self._prepare_replication_mocks(replication_type=replication_type, is_primary=is_primary)

        self.servicer.PromoteVolume(self.request, self.context)

        self.assertEqual(grpc.StatusCode.OK, self.context.code)
        self.mediator.get_replication.assert_called_once_with(replication_request)

    def _test_promote_replication_fails(self, replication_type, grpc_status=grpc.StatusCode.FAILED_PRECONDITION):
        replication_request = self._prepare_request_params(replication_type)
        self._prepare_replication_mocks()

        self.servicer.PromoteVolume(self.request, self.context)

        self.assertEqual(grpc_status, self.context.code)
        self.mediator.get_replication.assert_called_once_with(replication_request)
        self.mediator.promote_replication_volume.assert_not_called()

    def test_promote_replication_succeeds(self):
        replication = utils.get_mock_mediator_response_replication(name=REPLICATION_NAME,
                                                                   replication_type=REPLICATION_TYPE_MIRROR)
        self._test_promote_replication_succeeds(REPLICATION_TYPE_MIRROR)
        self.mediator.promote_replication_volume.assert_called_once_with(replication)

    def test_promote_replication_idempotency_succeeds(self):
        self._test_promote_replication_succeeds(REPLICATION_TYPE_MIRROR, True)
        self.mediator.promote_replication_volume.assert_not_called()

    def test_promote_replication_fails(self):
        self._test_promote_replication_fails(REPLICATION_TYPE_MIRROR)

    def test_promote_replication_already_processing(self):
        self._test_request_already_processing("volume_id", self.request.volume_id)

    def test_promote_replication_with_wrong_secrets(self):
        self._test_request_with_wrong_secrets()

    def test_promote_replication_with_array_connection_exception(self):
        self._test_request_with_array_connection_exception()

    def test_promote_ear_replication_succeeds(self):
        self._test_promote_replication_succeeds(REPLICATION_TYPE_EAR)

    def test_promote_ear_replication_fails(self):
        self._test_promote_replication_fails(REPLICATION_TYPE_EAR)


class TestDemoteVolume(BaseReplicationSetUp, CommonControllerTest):
    @property
    def tested_method(self):
        return self.servicer.DemoteVolume

    @property
    def tested_method_response_class(self):
        return pb2.DemoteVolumeResponse

    def _test_demote_replication_succeeds(self, replication_type, is_primary=False):
        replication_request = self._prepare_request_params(replication_type)
        self._prepare_replication_mocks(replication_type=replication_type, is_primary=is_primary)

        self.servicer.DemoteVolume(self.request, self.context)

        self.assertEqual(grpc.StatusCode.OK, self.context.code)
        self.mediator.get_replication.assert_called_once_with(replication_request)

    def _test_demote_replication_fails(self, replication_type, grpc_status=grpc.StatusCode.FAILED_PRECONDITION):
        replication_request = self._prepare_request_params(replication_type)
        self._prepare_replication_mocks()

        self.servicer.DemoteVolume(self.request, self.context)

        self.assertEqual(grpc_status, self.context.code)
        self.mediator.get_replication.assert_called_once_with(replication_request)
        self.mediator.promote_replication_volume.assert_not_called()

    def test_demote_replication_succeeds(self):
        replication = utils.get_mock_mediator_response_replication(name=REPLICATION_NAME,
                                                                   replication_type=REPLICATION_TYPE_MIRROR,
                                                                   is_primary=True)
        self._test_demote_replication_succeeds(REPLICATION_TYPE_MIRROR, is_primary=True)
        self.mediator.demote_replication_volume.assert_called_once_with(replication)

    def test_demote_replication_fails(self):
        self._test_demote_replication_fails(REPLICATION_TYPE_MIRROR)

    def test_demote_replication_idempotency_succeeds(self):
        self._test_demote_replication_succeeds(REPLICATION_TYPE_MIRROR)
        self.mediator.demote_replication_volume.assert_not_called()

    def test_demote_replication_already_processing(self):
        self._test_request_already_processing("volume_id", self.request.volume_id)

    def test_demote_replication_with_wrong_secrets(self):
        self._test_request_with_wrong_secrets()

    def test_demote_replication_with_array_connection_exception(self):
        self._test_request_with_array_connection_exception()

    def test_demote_ear_replication_succeeds(self):
        self._test_demote_replication_succeeds(REPLICATION_TYPE_EAR)

    def test_demote_ear_replication_fails(self):
        self._test_demote_replication_fails(REPLICATION_TYPE_EAR)


class TestResyncVolume(BaseReplicationSetUp, CommonControllerTest):
    @property
    def tested_method(self):
        return self.servicer.ResyncVolume

    @property
    def tested_method_response_class(self):
        return pb2.ResyncVolumeResponse

    def _test_resync_replication(self, replication_type, grpc_status=grpc.StatusCode.OK):
        replication_request = self._prepare_request_params(replication_type)

        self.servicer.ResyncVolume(self.request, self.context)

        self.assertEqual(grpc_status, self.context.code)
        self.mediator.get_replication.assert_called_once_with(replication_request)

    def test_resync_replication_succeeds(self):
        self._prepare_replication_mocks(replication_type=REPLICATION_TYPE_MIRROR)
        self._test_resync_replication(REPLICATION_TYPE_MIRROR)

    def test_resync_replication_fails(self):
        self._prepare_replication_mocks()
        self._test_resync_replication(REPLICATION_TYPE_MIRROR, grpc.StatusCode.FAILED_PRECONDITION)
