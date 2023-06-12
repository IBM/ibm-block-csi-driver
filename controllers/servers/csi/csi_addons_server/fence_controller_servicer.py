from csi_general import fence_pb2_grpc, fence_pb2

from controllers.array_action.storage_agent import get_agent, detect_array_type
from controllers.common.csi_logger import get_stdout_logger
from controllers.servers import utils
from controllers.servers.csi.decorators import csi_method

logger = get_stdout_logger()


def _is_already_handled(mediator, fence_ownership_group):
    return mediator.is_fenced(fence_ownership_group)


def _fence_cluster_network(mediator, fence_ownership_group, unfence_ownership_group):
    mediator.fence(fence_ownership_group, unfence_ownership_group)


def handle_fencing(request):
    fence_ownership_group = request.parameters["fenceToken"]
    unfence_ownership_group = request.parameters["unfenceToken"]

    connection_info = utils.get_array_connection_info_from_secrets(request.secrets)
    array_type = detect_array_type(connection_info.array_addresses)
    with get_agent(connection_info, array_type).get_mediator() as mediator:
        # idempotence - check if the fence_ownership_group is already fenced (no pools in the og)
        if _is_already_handled(mediator, fence_ownership_group):
            return fence_pb2.FenceClusterNetworkResponse()
        _fence_cluster_network(mediator, fence_ownership_group, unfence_ownership_group)
        return fence_pb2.FenceClusterNetworkResponse()


class FenceControllerServicer(fence_pb2_grpc.FenceControllerServicer):
    @csi_method(error_response_type=fence_pb2.FenceClusterNetworkResponse, lock_request_attribute="parameters")
    def FenceClusterNetwork(self, request, context):
        logger.debug("FenceClusterNetwork parameters : {}".format(request.parameters))
        logger.debug("FenceClusterNetwork cidrs : {}".format(request.cidrs))
        return handle_fencing(request)

    @csi_method(error_response_type=fence_pb2.UnfenceClusterNetworkResponse, lock_request_attribute="parameters")
    def UnfenceClusterNetwork(self, request, context):
        logger.debug("UnfenceClusterNetwork parameters : {}".format(request.parameters))
        logger.debug("UnfenceClusterNetwork cidrs : {}".format(request.cidrs))
        return handle_fencing(request)
