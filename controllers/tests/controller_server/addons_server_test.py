import unittest
from datetime import datetime, timezone

import grpc
from csi_general import replication_pb2 as pb2
from mock import Mock, MagicMock

from controllers.servers.settings import PARAMETERS_SYSTEM_ID, PARAMETERS_COPY_TYPE, PARAMETERS_REPLICATION_POLICY
from controllers.array_action import svc_messages
from controllers.array_action.settings import (REPLICATION_TYPE_MIRROR, REPLICATION_TYPE_EAR,
                                               REPLICATION_COPY_TYPE_SYNC, DR_LINK_STATUS_RUNNING)
from controllers.array_action.array_action_types import ReplicationRequest, ReplicationInfo, ReplicationStatus
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
        self.request.volume_id = "{0}:{1};{1}".format("A9000", OBJECT_INTERNAL_ID)
        self.request.replication_id = "{}:{};{}".format("A9000", OTHER_OBJECT_INTERNAL_ID, VOLUME_UID)
        self.request.replication_source.volumegroup.volume_group_id = self.request.volume_id
        self.context = utils.FakeContext()

    def _prepare_replication_mocks(self, replication_type=None, copy_type=COPY_TYPE, is_primary=False,
                                   volume_group_id=None):
        if replication_type:
            replication = utils.get_mock_mediator_response_replication(name=REPLICATION_NAME,
                                                                       replication_type=replication_type,
                                                                       copy_type=copy_type,
                                                                       is_primary=is_primary,
                                                                       volume_group_id=volume_group_id)
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
        self._test_enable_replication_fails(replication_type=REPLICATION_TYPE_MIRROR,
                                            replication_id=None,
                                            grpc_status=grpc.StatusCode.NOT_FOUND)

    def test_enable_ear_replication_idempotency_succeeds(self):
        self.mediator.get_object_by_id.return_value = utils.get_mock_mediator_response_volume(
            volume_group_id=DUMMY_VOLUME_GROUP)
        self._test_enable_replication_idempotency(replication_type=REPLICATION_TYPE_EAR,
                                                  copy_type=REPLICATION_COPY_TYPE_SYNC,
                                                  grpc_status=grpc.StatusCode.OK)

    def test_enable_replication_already_processing(self):
        self._test_request_already_processing(
            "replication_source",
            self.request.replication_source.volumegroup.volume_group_id
        )

    def test_enable_replication_with_wrong_secrets(self):
        self._test_request_with_wrong_secrets()

    def test_enable_replication_with_array_connection_exception(self):
        self._test_request_with_array_connection_exception()

    def test_enable_ear_replication_obsolete_request_parameters_fails(self):
        replication_id = "{}:{};{}".format("A9000", OTHER_OBJECT_INTERNAL_ID, VOLUME_UID)
        self._test_enable_replication_fails(replication_type=REPLICATION_TYPE_EAR,
                                            replication_id=replication_id,
                                            grpc_status=grpc.StatusCode.INVALID_ARGUMENT)

    def test_enable_ear_replication_succeeds(self):
        self._test_enable_replication_succeeds(REPLICATION_TYPE_EAR)


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
        self._test_request_already_processing(
            "replication_source",
            self.request.replication_source.volumegroup.volume_group_id
        )

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
        self._test_request_already_processing(
            "replication_source",
            self.request.replication_source.volumegroup.volume_group_id
        )

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
        self._test_request_already_processing(
            "replication_source",
            self.request.replication_source.volumegroup.volume_group_id
        )

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


class TestGetVolumeReplicationInfo(BaseReplicationSetUp, CommonControllerTest):
    @property
    def tested_method(self):
        return self.servicer.GetVolumeReplicationInfo

    @property
    def tested_method_response_class(self):
        return pb2.GetVolumeReplicationInfoResponse

    def _make_replication_info(self, last_sync_time=None,
                               replication_status=ReplicationStatus.UNKNOWN, status_message=None):
        return ReplicationInfo(
            last_sync_time=last_sync_time,
            replication_status=replication_status,
            status_message=status_message,
        )

    def _prepare_get_replication_info(self, replication_info):
        self.mediator.get_last_async_snapshot_info.return_value = replication_info

    def _make_volume_source_request(self):
        volume_request = ProtoBufMock()
        volume_request.secrets = self.request.secrets
        volume_request.volume_id = "{0}:{1};{1}".format("A9000", OBJECT_INTERNAL_ID)
        replication_source = ProtoBufMock(spec=['volume', 'ListFields'])
        replication_source.ListFields.return_value = [True]
        replication_source.volume.volume_id = volume_request.volume_id
        volume_request.replication_source = replication_source
        return volume_request

    def _make_status_message(self):
        return svc_messages.REPLICATION_STATUS_MESSAGE.format(
            DR_LINK_STATUS_RUNNING, "fab3p-118-c", "healthy", "yes"
        )

    def test_get_volume_replication_info_all_fields_populated_succeeds(self):
        replication_status_message = self._make_status_message()
        self._prepare_get_replication_info(self._make_replication_info(
            last_sync_time=datetime(2025, 4, 22, 4, 16, 47),
            replication_status=ReplicationStatus.HEALTHY,
            status_message=replication_status_message,
        ))

        response = self.servicer.GetVolumeReplicationInfo(self.request, self.context)

        self.assertEqual(grpc.StatusCode.OK, self.context.code)
        self.mediator.get_last_async_snapshot_info.assert_called_once_with(OBJECT_INTERNAL_ID)
        self.assertNotEqual(0, response.last_sync_time.seconds)
        self.assertEqual(ReplicationStatus.HEALTHY, response.status)
        self.assertEqual(replication_status_message, response.status_message)

    def test_get_volume_replication_info_all_fields_none_succeeds(self):
        self._prepare_get_replication_info(self._make_replication_info())

        response = self.servicer.GetVolumeReplicationInfo(self.request, self.context)

        self.assertEqual(grpc.StatusCode.OK, self.context.code)
        self.mediator.get_last_async_snapshot_info.assert_called_once_with(OBJECT_INTERNAL_ID)
        self.assertEqual(0, response.last_sync_time.seconds)
        self.assertEqual(ReplicationStatus.UNKNOWN, response.status)
        self.assertEqual('', response.status_message)

    def test_get_volume_replication_info_ramen_volume_source_resolves_to_vg_succeeds(self):
        self._prepare_get_replication_info(self._make_replication_info(
            last_sync_time=datetime(2025, 4, 22, 4, 16, 47),
            replication_status=ReplicationStatus.HEALTHY,
            status_message=self._make_status_message(),
        ))
        self.mediator.get_object_by_id.return_value = utils.get_mock_mediator_response_volume(
            volume_group_id=OBJECT_INTERNAL_ID)

        response = self.servicer.GetVolumeReplicationInfo(self._make_volume_source_request(), self.context)

        self.assertEqual(grpc.StatusCode.OK, self.context.code)
        self.mediator.get_last_async_snapshot_info.assert_called_once_with(OBJECT_INTERNAL_ID)
        self.assertNotEqual(0, response.last_sync_time.seconds)
        self.assertEqual(ReplicationStatus.HEALTHY, response.status)

    def test_get_volume_replication_info_volume_not_in_vg_returns_current_time(self):
        self.mediator.get_object_by_id.return_value = utils.get_mock_mediator_response_volume(volume_group_id=None)

        before = int(datetime.now(timezone.utc).timestamp())
        response = self.servicer.GetVolumeReplicationInfo(self._make_volume_source_request(), self.context)
        after = int(datetime.now(timezone.utc).timestamp())

        self.assertEqual(grpc.StatusCode.OK, self.context.code)
        self.mediator.get_last_async_snapshot_info.assert_not_called()
        self.assertGreaterEqual(response.last_sync_time.seconds, before)
        self.assertLessEqual(response.last_sync_time.seconds, after)

    def test_get_volume_replication_info_with_wrong_secrets(self):
        self._test_request_with_wrong_secrets()

    def test_get_volume_replication_info_with_array_connection_exception(self):
        self._test_request_with_array_connection_exception()


class TestGetReplicationDestinationInfo(BaseReplicationSetUp, CommonControllerTest):
    @property
    def tested_method(self):
        return self.servicer.GetReplicationDestinationInfo

    @property
    def tested_method_response_class(self):
        return pb2.GetReplicationDestinationInfoResponse

    def setUp(self):
        super().setUp()
        volume_id = "{0}:{1};{1}".format("A9000", OBJECT_INTERNAL_ID)
        replication_source = ProtoBufMock(spec=['volume', 'ListFields'])
        replication_source.ListFields.return_value = [True]
        replication_source.volume.volume_id = volume_id
        self.request.replication_source = replication_source

    def test_get_destination_info_with_dr_mediator_returns_destination_id(self):
        destination_id = "{0}:{1};{1}".format("A9000", OTHER_OBJECT_INTERNAL_ID)
        self.request.secrets["dr_management_address"] = SECRET_MANAGEMENT_ADDRESS_VALUE
        self.mediator.get_replication_destination_info.return_value = destination_id

        response = self.servicer.GetReplicationDestinationInfo(self.request, self.context)

        self.assertEqual(grpc.StatusCode.OK, self.context.code)
        self.mediator.get_replication_destination_info.assert_called_once()
        self.assertEqual(destination_id, response.replication_destination.volume.volume_id)

    def test_get_destination_info_destination_not_yet_available_returns_empty(self):
        self.request.secrets["dr_management_address"] = SECRET_MANAGEMENT_ADDRESS_VALUE
        self.mediator.get_replication_destination_info.return_value = None

        response = self.servicer.GetReplicationDestinationInfo(self.request, self.context)

        self.assertEqual(grpc.StatusCode.OK, self.context.code)
        self.mediator.get_replication_destination_info.assert_called_once()
        self.assertEqual("", response.replication_destination.volume.volume_id)

    def test_get_destination_info_volumegroup_source_not_supported_returns_empty(self):
        replication_source = ProtoBufMock(spec=['volumegroup', 'ListFields'])
        replication_source.ListFields.return_value = [True]
        replication_source.volumegroup.volume_group_id = self.request.volume_id
        self.request.replication_source = replication_source

        response = self.servicer.GetReplicationDestinationInfo(self.request, self.context)

        self.assertEqual(grpc.StatusCode.OK, self.context.code)
        self.mediator.get_replication_destination_info.assert_not_called()
        self.assertEqual("", response.replication_destination.volume.volume_id)

    def test_get_destination_info_without_dr_mediator_returns_source_handle(self):
        source_id = "{0}:{1};{1}".format("A9000", OBJECT_INTERNAL_ID)
        self.mediator.get_replication_destination_info.return_value = source_id

        response = self.servicer.GetReplicationDestinationInfo(self.request, self.context)

        self.assertEqual(grpc.StatusCode.OK, self.context.code)
        self.mediator.get_replication_destination_info.assert_called_once()
        self.assertEqual(source_id, response.replication_destination.volume.volume_id)

    def test_get_destination_info_with_wrong_secrets(self):
        self._test_request_with_wrong_secrets()

    def test_get_destination_info_with_array_connection_exception(self):
        self._test_request_with_array_connection_exception()
