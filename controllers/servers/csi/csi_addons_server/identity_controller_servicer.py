import grpc

from csi_general import identity_pb2 as pb2
from csi_general import identity_pb2_grpc as pb2_grpc

from controllers.common.config import config as common_config
from controllers.servers.csi.decorators import csi_method
from controllers.servers.csi.exception_handler import build_error_response
from controllers.common.csi_logger import get_stdout_logger

logger = get_stdout_logger()


class IdentityControllerServicer(pb2_grpc.IdentityServicer):

    @csi_method(error_response_type=pb2.GetIdentityResponse)
    def GetIdentity(self, request, context):
        name = common_config.identity.name
        version = common_config.identity.version

        if not name or not version:
            message = "plugin name or version cannot be empty"
            return build_error_response(message, context, grpc.StatusCode.INTERNAL, pb2.GetIdentityResponse)

        return pb2.GetIdentityResponse(name=name, vendor_version=version)

    def GetCapabilities(self, request, context):
        logger.info("GetCapabilities")
        response = pb2.GetCapabilitiesResponse(
            capabilities=[self._get_replication_capability(),
                          self._get_controller_capability(),
                          self._get_network_fence_capability()])

        logger.info("finished GetCapabilities")
        return response

    def _get_replication_capability(self):
        types = pb2.Capability.VolumeReplication.Type
        capability_enum_value = types.Value("VOLUME_REPLICATION")
        return pb2.Capability(
            volume_replication=pb2.Capability.VolumeReplication(type=capability_enum_value))

    def _get_controller_capability(self):
        types = pb2.Capability.Service.Type
        capability_enum_value = types.Value("CONTROLLER_SERVICE")
        return pb2.Capability(
            service=pb2.Capability.Service(type=capability_enum_value))

    def _get_network_fence_capability(self):
        types = pb2.Capability.NetworkFence.Type
        capability_enum_value = types.Value("NETWORK_FENCE")
        return pb2.Capability(
            network_fence=pb2.Capability.NetworkFence(type=capability_enum_value))

    def Probe(self, request, context):
        context.set_code(grpc.StatusCode.OK)
        return pb2.ProbeResponse()
