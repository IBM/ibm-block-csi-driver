from csi_general import fence_pb2_grpc, fence_pb2

from controllers.common.csi_logger import get_stdout_logger
from controllers.servers.csi.decorators import csi_method

logger = get_stdout_logger()

class FenceControllerServicer(fence_pb2_grpc.FenceControllerServicer):
    @csi_method(error_response_type=fence_pb2.FenceClusterNetworkResponse, lock_request_attribute="cidrs")
    def FenceClusterNetwork(self, request, context):
        logger.debug("FenceClusterNetwork parameters : {}".format(request.parameters))
        logger.debug("FenceClusterNetwork cidrs : {}".format(request.cidrs))

    @csi_method(error_response_type=fence_pb2.UnfenceClusterNetworkResponse, lock_request_attribute="cidrs")
    def UnfenceClusterNetwork(self, request, context):
        logger.debug("UnfenceClusterNetwork parameters : {}".format(request.parameters))
        logger.debug("UnfenceClusterNetwork cidrs : {}".format(request.cidrs))