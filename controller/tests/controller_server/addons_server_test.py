import grpc
from mock import patch, Mock

from controller.controller_server.addons_server import ReplicationControllerServicer
from controller.csi_general import replication_pb2 as pb2
from controller.tests import utils
from controller.tests.controller_server.csi_controller_server_test import BaseControllerSetUp, CommonControllerTest
from controller.controller_server.config import PARAMETERS_SYSTEM_ID, PARAMETERS_COPY_TYPE
from controller.controller_server.test_settings import volume_name, volume_wwn, object_internal_id, \
    other_object_internal_id, replication_name, system_id, copy_type


class TestControllerServicerEnableVolumeReplication(BaseControllerSetUp, CommonControllerTest):
    def get_tested_method(self):
        return self.servicer.EnableVolumeReplication

    def get_tested_method_response_class(self):
        return pb2.EnableVolumeReplicationResponse

    def setUp(self):
        super().setUp()
        self.servicer = ReplicationControllerServicer()
        self.request.volume_id = "{}:{};{}".format("A9000", object_internal_id, volume_wwn)
        self.request.replication_id = "{}:{};{}".format("A9000", other_object_internal_id, volume_wwn)
        self.request.parameters.update({PARAMETERS_SYSTEM_ID: system_id,
                                        PARAMETERS_COPY_TYPE: copy_type})
        self.mediator.get_object_by_id = Mock()
        self.mediator.get_object_by_id.return_value = utils.get_mock_mediator_response_volume(10, volume_name,
                                                                                              volume_wwn, "xiv")
        self.mediator.get_replication = Mock()
        replication_mock = utils.get_mock_mediator_response_replication(replication_name,
                                                                        object_internal_id,
                                                                        other_object_internal_id)
        self.mediator.get_replication.return_value = replication_mock

    def _prepare_enable_replication_mocks(self, storage_agent):
        storage_agent.return_value = self.storage_agent
        self.mediator.get_replication = Mock()
        self.mediator.get_replication.return_value = None
        self.mediator.create_replication = Mock()

    @patch("controller.controller_server.addons_server.get_agent")
    def test_enable_replication_succeeds(self, storage_agent):
        self._prepare_enable_replication_mocks(storage_agent)

        self.servicer.EnableVolumeReplication(self.request, self.context)

        self.assertEqual(self.context.code, grpc.StatusCode.OK)
        self.mediator.get_replication.assert_called_once_with(object_internal_id, other_object_internal_id, system_id)
        self.mediator.create_replication.assert_called_once_with(object_internal_id, other_object_internal_id,
                                                                 system_id, copy_type)

    @patch("controller.controller_server.addons_server.get_agent")
    def test_enable_replication_already_processing(self, storage_agent):
        self._test_request_already_processing(storage_agent, "volume_id", self.request.volume_id)

    @patch("controller.controller_server.addons_server.get_agent")
    def test_enable_replication_with_wrong_secrets(self, storage_agent):
        self._test_request_with_wrong_secrets(storage_agent)

    @patch("controller.controller_server.addons_server.get_agent")
    def test_enable_replication_with_array_connection_exception(self, storage_agent):
        self._test_request_with_array_connection_exception(storage_agent)
