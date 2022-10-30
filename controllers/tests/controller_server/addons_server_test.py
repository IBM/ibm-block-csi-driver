import unittest

import grpc
from csi_general import replication_pb2 as pb2
from mock import Mock, MagicMock

from controllers.servers.settings import PARAMETERS_SYSTEM_ID, PARAMETERS_COPY_TYPE, PARAMETERS_REPLICATION_POLICY
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
        self.request.parameters = {PARAMETERS_SYSTEM_ID: SYSTEM_ID,
                                   PARAMETERS_COPY_TYPE: COPY_TYPE}
        self.mediator.get_object_by_id = Mock()
        self.mediator.get_object_by_id.return_value = utils.get_mock_mediator_response_volume(10, VOLUME_NAME,
                                                                                              VOLUME_UID, "xiv")
        self.mediator.get_replication = Mock()
        replication_mock = utils.get_mock_mediator_response_replication(REPLICATION_NAME,
                                                                        OBJECT_INTERNAL_ID,
                                                                        OTHER_OBJECT_INTERNAL_ID)
        self.mediator.get_replication.return_value = replication_mock
        self.context = utils.FakeContext()

    def _prepare_enable_replication_mocks(self):
        self.mediator.get_replication = Mock()
        self.mediator.get_replication.return_value = None
        self.mediator.create_replication = Mock()

    def test_enable_replication_succeeds(self):
        self._prepare_enable_replication_mocks()

        self.servicer.EnableVolumeReplication(self.request, self.context)

        self.assertEqual(grpc.StatusCode.OK, self.context.code)
        self.mediator.get_replication.assert_called_once_with(OBJECT_INTERNAL_ID, OTHER_OBJECT_INTERNAL_ID,
                                                                     SYSTEM_ID)
        self.mediator.create_replication.assert_called_once_with(OBJECT_INTERNAL_ID, OTHER_OBJECT_INTERNAL_ID,
                                                                        SYSTEM_ID, COPY_TYPE)

    def test_enable_replication_already_processing(self):
        self._test_request_already_processing("volume_id", self.request.volume_id)

    def test_enable_replication_with_wrong_secrets(self):
        self._test_request_with_wrong_secrets()

    def test_enable_replication_with_array_connection_exception(self):
        self._test_request_with_array_connection_exception()


class TestControllerServicerEnableEarReplication(unittest.TestCase, CommonControllerTest):
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
        self.request.parameters = {PARAMETERS_REPLICATION_POLICY: REPLICATION_NAME}
        self.mediator.get_object_by_id = Mock()
        self.mediator.get_object_by_id.return_value = utils.get_mock_mediator_response_volume(10, VOLUME_NAME,
                                                                                              VOLUME_UID, "xiv")
        self.mediator.get_ear_replication = Mock()
        replication_mock = utils.get_mock_mediator_response_replication(REPLICATION_NAME,
                                                                        OBJECT_INTERNAL_ID,
                                                                        OTHER_OBJECT_INTERNAL_ID)
        self.mediator.get_ear_replication.return_value = replication_mock
        self.context = utils.FakeContext()

    def _prepare_enable_ear_replication_mocks(self):
        self.mediator.get_ear_replication = Mock()
        self.mediator.get_ear_replication.return_value = None
        self.mediator.create_replication = Mock()

    def test_enable_ear_replication_succeeds(self):
        self._prepare_enable_ear_replication_mocks()

        self.servicer.EnableVolumeReplication(self.request, self.context)

        self.assertEqual(grpc.StatusCode.OK, self.context.code)
        self.mediator.get_ear_replication.assert_called_once_with(OBJECT_INTERNAL_ID)
        self.mediator.create_ear_replication.assert_called_once_with(OBJECT_INTERNAL_ID, REPLICATION_NAME)
