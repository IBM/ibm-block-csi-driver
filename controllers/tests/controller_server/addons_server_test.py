import grpc
from csi_general import replication_pb2 as pb2
from mock import patch, Mock

from controllers.servers.csi.addons_server import ReplicationControllerServicer
from controllers.servers.config import PARAMETERS_SYSTEM_ID, PARAMETERS_COPY_TYPE
from controllers.tests.controller_server.test_settings import VOLUME_NAME, VOLUME_WWN, OBJECT_INTERNAL_ID, \
    OTHER_OBJECT_INTERNAL_ID, REPLICATION_NAME, SYSTEM_ID, COPY_TYPE
from controllers.tests import utils
from controllers.tests.controller_server.csi_controller_server_test import (BaseControllerSetUp,
                                                                            CommonControllerTest)


class TestControllerServicerEnableVolumeReplication(BaseControllerSetUp, CommonControllerTest):
    @property
    def tested_method(self):
        return self.servicer.EnableVolumeReplication

    @property
    def tested_method_response_class(self):
        return pb2.EnableVolumeReplicationResponse

    def setUp(self):
        super().setUp()
        self.servicer = ReplicationControllerServicer()
        self.request.volume_id = "{}:{};{}".format("A9000", OBJECT_INTERNAL_ID, VOLUME_WWN)
        self.request.replication_id = "{}:{};{}".format("A9000", OTHER_OBJECT_INTERNAL_ID, VOLUME_WWN)
        self.request.parameters.update({PARAMETERS_SYSTEM_ID: SYSTEM_ID,
                                        PARAMETERS_COPY_TYPE: COPY_TYPE})
        self.mediator.get_object_by_id = Mock()
        self.mediator.get_object_by_id.return_value = utils.get_mock_mediator_response_volume(10, VOLUME_NAME,
                                                                                              VOLUME_WWN, "xiv")
        self.mediator.get_replication = Mock()
        replication_mock = utils.get_mock_mediator_response_replication(REPLICATION_NAME,
                                                                        OBJECT_INTERNAL_ID,
                                                                        OTHER_OBJECT_INTERNAL_ID)
        self.mediator.get_replication.return_value = replication_mock

    def _prepare_enable_replication_mocks(self, storage_agent):
        storage_agent.return_value = self.storage_agent
        self.mediator.get_replication = Mock()
        self.mediator.get_replication.return_value = None
        self.mediator.create_replication = Mock()

    @patch("controllers.servers.csi.addons_server.get_agent")
    def test_enable_replication_succeeds(self, storage_agent):
        self._prepare_enable_replication_mocks(storage_agent)

        self.servicer.EnableVolumeReplication(self.request, self.context)

        self.assertEqual(grpc.StatusCode.OK, self.context.code)
        self.mediator.get_replication.assert_called_once_with(OBJECT_INTERNAL_ID, OTHER_OBJECT_INTERNAL_ID, SYSTEM_ID)
        self.mediator.create_replication.assert_called_once_with(OBJECT_INTERNAL_ID, OTHER_OBJECT_INTERNAL_ID,
                                                                 SYSTEM_ID, COPY_TYPE)

    @patch("controllers.servers.csi.addons_server.get_agent")
    def test_enable_replication_already_processing(self, storage_agent):
        self._test_request_already_processing(storage_agent, "volume_id", self.request.volume_id)

    @patch("controllers.servers.csi.addons_server.get_agent")
    def test_enable_replication_with_wrong_secrets(self, storage_agent):
        self._test_request_with_wrong_secrets(storage_agent)

    @patch("controllers.servers.csi.addons_server.get_agent")
    def test_enable_replication_with_array_connection_exception(self, storage_agent):
        self._test_request_with_array_connection_exception(storage_agent)
