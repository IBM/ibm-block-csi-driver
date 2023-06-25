from csi_general import fence_pb2_grpc, fence_pb2

from controllers.array_action.storage_agent import get_agent, detect_array_type
from controllers.common.csi_logger import get_stdout_logger
from controllers.servers import utils
from controllers.servers.csi.decorators import csi_fence_method

logger = get_stdout_logger()


def _is_already_handled(mediator, fence_ownership_group):
    return mediator.is_fenced(fence_ownership_group)


def _fence_cluster_network(mediator, fence_ownership_group, unfence_ownership_group):
    logger.info("fencing {}".format(fence_ownership_group))
    mediator.fence(fence_ownership_group, unfence_ownership_group)


def handle_fencing(request):
    utils.validate_fencing_request(request)
    fence_ownership_group = request.parameters["fenceToken"]
    unfence_ownership_group = request.parameters["unfenceToken"]

    connection_info = utils.get_array_connection_info_from_secrets(request.secrets)
    array_type = detect_array_type(connection_info.array_addresses)
    with get_agent(connection_info, array_type).get_mediator() as mediator:
        # idempotence - check if the fence_ownership_group is already fenced (no pools in the og)
        if _is_already_handled(mediator, fence_ownership_group):
            logger.info("{} is fenced".format(fence_ownership_group))
            return fence_pb2.FenceClusterNetworkResponse()
        _fence_cluster_network(mediator, fence_ownership_group, unfence_ownership_group)
        return fence_pb2.FenceClusterNetworkResponse()


class FenceControllerServicer(fence_pb2_grpc.FenceControllerServicer):
    @csi_fence_method(error_response_type=fence_pb2.FenceClusterNetworkResponse)
    def FenceClusterNetwork(self, request, context):
        return handle_fencing(request)

    @csi_fence_method(error_response_type=fence_pb2.UnfenceClusterNetworkResponse)
    def UnfenceClusterNetwork(self, request, context):
        return handle_fencing(request)

    @csi_fence_method(error_response_type=fence_pb2.ListClusterFenceResponse)
    def ListClusterFence(self, request, context):
        raise NotImplementedError()
