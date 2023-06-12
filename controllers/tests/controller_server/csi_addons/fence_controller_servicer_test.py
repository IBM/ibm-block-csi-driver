import unittest

import grpc
from mock import Mock, MagicMock

from controllers.servers.csi.csi_addons_server.fence_controller_servicer import FenceControllerServicer
from controllers.tests import utils
from controllers.tests.common.test_settings import SECRET
from controllers.tests.controller_server.common import mock_array_type, mock_get_agent, mock_mediator

FENCE_SERVER_PATH = "controllers.servers.csi.csi_addons_server.fence_controller_servicer"


class TestFenceControllerServicer(unittest.TestCase):

    def setUp(self):
        self.servicer = FenceControllerServicer()
        self.request = Mock()
        self.context = utils.FakeContext()
        mock_array_type(self, FENCE_SERVER_PATH)

        self.mediator = mock_mediator()

        self.storage_agent = MagicMock()
        mock_get_agent(self, FENCE_SERVER_PATH)

        self.request.secrets = SECRET
        self.request.parameters = {"fenceToken": "fenceToken", "unfenceToken": "unfenceToken"}
        self.request.cidrs = ["0.0.0.0/32"]
        self.mediator.is_fenced.return_value = False

    def test_fence_succeeds(self):
        self.servicer.FenceClusterNetwork(self.request, self.context)
        self.assertEqual(grpc.StatusCode.OK, self.context.code)
        self.mediator.fence.assert_called_once_with("fenceToken", "unfenceToken")

    def test_fence_fails(self):
        self.mediator.fence.side_effect = Exception("fence failed")
        self.servicer.FenceClusterNetwork(self.request, self.context)
        self.assertEqual(grpc.StatusCode.INTERNAL, self.context.code)
        self.mediator.fence.assert_called_once_with("fenceToken", "unfenceToken")

    def test_fence_already_fenced(self):
        self.mediator.is_fenced.return_value = True
        self.servicer.FenceClusterNetwork(self.request, self.context)
        self.assertEqual(grpc.StatusCode.OK, self.context.code)
        self.mediator.fence.assert_not_called()

    def test_unfence_succeeds(self):
        self.servicer.UnfenceClusterNetwork(self.request, self.context)
        self.assertEqual(grpc.StatusCode.OK, self.context.code)
        self.mediator.fence.assert_called_once_with("fenceToken", "unfenceToken")

    def test_unfence_already_fenced(self):
        self.mediator.is_fenced.return_value = True
        self.servicer.UnfenceClusterNetwork(self.request, self.context)
        self.assertEqual(grpc.StatusCode.OK, self.context.code)
        self.mediator.fence.assert_not_called()
