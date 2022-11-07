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
    SECRET_MANAGEMENT_ADDRESS_VALUE
from controllers.tests.controller_server.common import mock_get_agent
from controllers.tests.controller_server.csi_controller_server_test import (CommonControllerTest)
from controllers.tests.utils import ProtoBufMock

ADDON_SERVER_PATH = "controllers.servers.csi.addons_server"


class TestControllerServicerEnableVolumeReplication(unittest.TestCase, CommonControllerTest):
    @property
    def tested_method(self):
        return self.servicer.EnableVolumeReplication

    @property
    def tested_method_response_class(self):
        return pb2.EnableVolumeReplicationResponse

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
        self.mediator.get_object_by_id = Mock()
        self.mediator.get_object_by_id.return_value = utils.get_mock_mediator_response_volume(10, VOLUME_NAME,
                                                                                              VOLUME_UID, "xiv")
        self.context = utils.FakeContext()

    def _prepare_replication_mocks(self, replication_type=None):
        self.mediator.get_replication = Mock()
        if replication_type:
            replication = utils.get_mock_mediator_response_replication(name=REPLICATION_NAME,
                                                                       replication_type=replication_type)
        else:
            replication = None
        self.mediator.get_replication.return_value = replication

    def _prepare_request_params(self, replication_type):
        if replication_type == REPLICATION_TYPE_MIRROR:
            self.request.parameters = {PARAMETERS_SYSTEM_ID: SYSTEM_ID,
                                       PARAMETERS_COPY_TYPE: COPY_TYPE}
            replication_request = ReplicationRequest(OBJECT_INTERNAL_ID, OTHER_OBJECT_INTERNAL_ID, SYSTEM_ID, COPY_TYPE,
                                                     REPLICATION_TYPE_MIRROR)
        elif replication_type == REPLICATION_TYPE_EAR:
            self.request.parameters = {PARAMETERS_REPLICATION_POLICY: REPLICATION_NAME}
            replication_request = ReplicationRequest(OBJECT_INTERNAL_ID, None, None, REPLICATION_COPY_TYPE_SYNC,
                                                     REPLICATION_TYPE_EAR, REPLICATION_NAME)
        return replication_request

    def _prepare_enable_replication_mocks(self):
        self._prepare_replication_mocks()
        self.mediator.create_replication = Mock()

    def _test_enable_replication_succeeds(self, replication_type):
        replication_request = self._prepare_request_params(replication_type)
        self._prepare_enable_replication_mocks()

        self.servicer.EnableVolumeReplication(self.request, self.context)

        self.assertEqual(grpc.StatusCode.OK, self.context.code)
        self.mediator.get_replication.assert_called_once_with(replication_request)
        self.mediator.create_replication.assert_called_once_with(replication_request)

    def test_enable_replication_succeeds(self):
        self._test_enable_replication_succeeds(REPLICATION_TYPE_MIRROR)

    def test_enable_ear_replication_succeeds(self):
        self._test_enable_replication_succeeds(REPLICATION_TYPE_EAR)

    def _prepare_disable_replication_mocks(self, replication_type):
        self._prepare_replication_mocks(replication_type)
        self.mediator.delete_replication = Mock()

    def _test_disable_replication_succeeds(self, replication_type):
        replication_request = self._prepare_request_params(replication_type)
        self._prepare_disable_replication_mocks(replication_type)

        self.servicer.DisableVolumeReplication(self.request, self.context)

        self.assertEqual(grpc.StatusCode.OK, self.context.code)
        replication = utils.get_mock_mediator_response_replication(name=REPLICATION_NAME,
                                                                   replication_type=replication_type)
        self.mediator.get_replication.assert_called_once_with(replication_request)
        self.mediator.delete_replication.assert_called_once_with(replication)

    def test_disable_replication_succeeds(self):
        self._test_disable_replication_succeeds(REPLICATION_TYPE_MIRROR)

    def test_disable_ear_replication_succeeds(self):
        self._test_disable_replication_succeeds(REPLICATION_TYPE_EAR)

    def _prepare_promote_replication_mocks(self, replication_type):
        self._prepare_replication_mocks(replication_type)
        self.mediator.promote_replication_volume = Mock()

    def _test_promote_replication_succeeds(self, replication_type):
        replication_request = self._prepare_request_params(replication_type)
        self._prepare_promote_replication_mocks(replication_type)

        self.servicer.PromoteVolume(self.request, self.context)

        self.assertEqual(grpc.StatusCode.OK, self.context.code)
        replication = utils.get_mock_mediator_response_replication(name=REPLICATION_NAME,
                                                                   replication_type=replication_type)
        self.mediator.get_replication.assert_called_once_with(replication_request)
        self.mediator.promote_replication_volume.assert_called_once_with(replication)

    def test_promote_replication_succeeds(self):
        self._test_promote_replication_succeeds(REPLICATION_TYPE_MIRROR)

    def test_promote_ear_replication_succeeds(self):
        self._test_promote_replication_succeeds(REPLICATION_TYPE_EAR)

    def test_enable_replication_already_processing(self):
        self._test_request_already_processing("volume_id", self.request.volume_id)

    def test_enable_replication_with_wrong_secrets(self):
        self._test_request_with_wrong_secrets()

    def test_enable_replication_with_array_connection_exception(self):
        self._test_request_with_array_connection_exception()
