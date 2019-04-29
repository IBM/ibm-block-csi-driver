import grpc
import time
from optparse import OptionParser
import yaml
import os.path

from concurrent import futures
from controller.csi_general import csi_pb2
from controller.csi_general import csi_pb2_grpc
from controller.array_action.array_connection_manager import ArrayConnectionManager
from controller.common.csi_logger import get_stdout_logger
import config
import utils
from controller.array_action.errors import VolumeNotFoundError, IllegalObjectName, PoolDoesNotMatchCapabilities, \
    CapabilityNotSupported, \
    VolumeAlreadyExists, PoolDoesNotExist

logger = get_stdout_logger()


class ControllerServicer(csi_pb2_grpc.ControllerServicer):
    """
    gRPC server for Digestor Service
    """

    def __init__(self, array_endpoint):
        self.endpoint = array_endpoint

        my_path = os.path.abspath(os.path.dirname(__file__))
        path = os.path.join(my_path, "../../common/config.yaml")

        with open(path, 'r') as yamlfile:
            self.cfg = yaml.load(yamlfile)  # TODO: add the following when possible : Loader=yaml.FullLoader)

    def CreateVolume(self, request, context):
        logger.debug("create volume")

        res, msg = utils.validate_create_volume_request(request)
        if not res:
            logger.error("failed request validation. error : {}".format(msg))
            context.set_details(msg)
            context.set_code(grpc.StatusCode.INVALID_ARGUMENT)
            return csi_pb2.CreateVolumeResponse()

        volume_name = request.name
        logger.debug("volume name : {}".format(volume_name))

        secrets = request.secrets
        user, password, array_addresses = utils.get_array_connection_info_from_secret(secrets)

        pool = request.parameters[config.PARAMETERS_CAPACITY].split("=")[1]
        capabilities = request.parameters[config.PARAMETERS_CAPABILITIES]

        try:
            # TODO : pass multiple array addresses
            with ArrayConnectionManager(user, password, array_addresses[0]) as array_mediator:
                logger.debug(array_mediator)

                try:
                    vol = array_mediator.get_volume(volume_name)

                except VolumeNotFoundError as ex:
                    logger.debug(
                        "volume was not found. creating a new volume with parameters: {0}".format(request.parameters))

                    vol = array_mediator.create_volume(volume_name, request.capacity_range.required_bytes, capabilities,
                                                       pool)

                else:
                    logger.debug("volume found : {}".format(vol))

                    if not (vol.size == request.capacity_range.required_bytes):
                        context.set_details("Volume was already created with different size.")
                        context.set_code(grpc.StatusCode.ALREADY_EXISTS)
                        return csi_pb2.CreateVolumeResponse()

        except (IllegalObjectName, CapabilityNotSupported, PoolDoesNotExist, PoolDoesNotMatchCapabilities) as ex:
            context.set_details(ex.message)
            context.set_code(grpc.StatusCode.INVALID_ARGUMENT)
            return csi_pb2.CreateVolumeResponse()
        except VolumeAlreadyExists as ex:
            context.set_details(ex.message)
            context.set_code(grpc.StatusCode.ALREADY_EXISTS)
            return csi_pb2.CreateVolumeResponse()
        except Exception as ex:
            logger.debug("an internal exception occured")
            logger.exception(ex)
            context.set_code(grpc.StatusCode.INTERNAL)
            context.set_details('an internal exception occurred : {}'.format(ex))
            return csi_pb2.CreateVolumeResponse()

        logger.debug("generating create volume response")
        return utils.get_create_volume_response(vol)

    def DeleteVolume(self, request, context):
        logger.debug("DeleteVolume")
        volume_id = request.volume_id
        secrets = request.secrets

        res, msg = utils.validate_secret(secrets)
        if not res:
            context.set_details(msg)
            context.set_code(grpc.StatusCode.INVALID_ARGUMENT)
            return csi_pb2.DeleteVolumeResponse()

        user, password, array_addresses = utils.get_array_connection_info_from_secret(secrets)

        res, array_type, vol_id = utils.get_volume_id_info(volume_id)
        if not res:
            context.set_details("Wrong volume id format")
            context.set_code(grpc.StatusCode.INVALID_ARGUMENT)
            return csi_pb2.DeleteVolumeResponse()

        try:
            # TODO : pass multiple array addresses
            with ArrayConnectionManager(user, password, array_addresses[0], array_type) as array_mediator:
                logger.debug(array_mediator)

                try:
                    array_mediator.delete_volume(vol_id)

                except VolumeNotFoundError as ex:
                    logger.debug("volume was not found during deletion: {0}".format(ex))

        except Exception as ex:
            logger.debug("an internal exception occurred")
            logger.exception(ex)
            context.set_code(grpc.StatusCode.INTERNAL)
            context.set_details('an internal exception occurred : {}'.format(ex))
            return csi_pb2.DeleteVolumeResponse()

        logger.debug("generating delete volume response")
        return csi_pb2.DeleteVolumeResponse()

    def ControllerPublishVolume(self, request, context):
        logger.debug("ControllerPublishVolume")
        context.set_code(grpc.StatusCode.UNIMPLEMENTED)
        return csi_pb2.ControllerPublishVolumeResponse()

    def ControllerUnpublishVolume(self, request, context):
        logger.debug("ControllerUnpublishVolume")
        context.set_code(grpc.StatusCode.UNIMPLEMENTED)
        return csi_pb2.ControllerUnpublishVolumeResponse()

    def ValidateVolumeCapabilities(self, request, context):
        logger.debug("ValidateVolumeCapabilities")
        context.set_code(grpc.StatusCode.UNIMPLEMENTED)
        return csi_pb2.ValidateVolumeCapabilitiesResponse()

    def ListVolumes(self, request, context):
        logger.debug("ListVolumes")
        context.set_code(grpc.StatusCode.UNIMPLEMENTED)
        return csi_pb2.ListVolumesResponse()

    def GetCapacity(self, request, context):
        logger.debug("GetCapacity")
        context.set_code(grpc.StatusCode.UNIMPLEMENTED)
        return csi_pb2.GetCapacityResponse()

    def ControllerGetCapabilities(self, request, context):
        logger.debug("ControllerGetCapabilities")
        types = csi_pb2.ControllerServiceCapability.RPC.Type

        return csi_pb2.ControllerGetCapabilitiesResponse(
            capabilities=[csi_pb2.ControllerServiceCapability(
                rpc=csi_pb2.ControllerServiceCapability.RPC(type=types.Value("CREATE_DELETE_VOLUME"))),
                csi_pb2.ControllerServiceCapability(
                    rpc=csi_pb2.ControllerServiceCapability.RPC(type=types.Value("PUBLISH_UNPUBLISH_VOLUME")))])

    def __get_identity_config(self, attribute_name):
        return self.cfg['identity'][attribute_name]

    def GetPluginInfo(self, request, context):
        logger.debug("GetPluginInfo")
        try:
            name = self.__get_identity_config("name")
            version = self.__get_identity_config("version")
        except Exception as ex:
            logger.exception(ex)
            context.set_code(grpc.StatusCode.INTERNAL)
            context.set_details('an error occured while trying to get plugin name or version')
            return csi_pb2.GetPluginInfoResponse()

        if not name or not version:
            logger.error("plugin name or version cannot be empty")
            context.set_code(grpc.StatusCode.INTERNAL)
            context.set_details("plugin name or version cannot be empty")
            return csi_pb2.GetPluginInfoResponse()

        return csi_pb2.GetPluginInfoResponse(name=name, vendor_version=version)

    def GetPluginCapabilities(self, request, context):
        logger.debug("GetPluginCapabilities")
        types = csi_pb2.PluginCapability.Service.Type
        capabilities = self.__get_identity_config("capabilities")
        capability_list = []
        for cap in capabilities:
            capability_list.append(
                csi_pb2.PluginCapability(
                    service=csi_pb2.PluginCapability.Service(type=types.Value(cap))
                )
            )

        return csi_pb2.GetPluginCapabilitiesResponse(
            capabilities=capability_list

        )

    def Probe(self, request, context):
        logger.debug("Probe")
        # TODO: add future logic
        context.set_code(grpc.StatusCode.OK)
        return csi_pb2.ProbeResponse()

    def start_server(self):
        controller_server = grpc.server(futures.ThreadPoolExecutor(max_workers=10))

        csi_pb2_grpc.add_ControllerServicer_to_server(self, controller_server)
        csi_pb2_grpc.add_IdentityServicer_to_server(self, controller_server)

        # bind the server to the port defined above
        # controller_server.add_insecure_port('[::]:{}'.format(self.server_port))
        # controller_server.add_insecure_port('unix://{}'.format(self.server_port))
        controller_server.add_insecure_port(self.endpoint)

        # start the server
        controller_server.start()
        logger.debug('Controller Server running ...')

        try:
            while True:
                time.sleep(60 * 60 * 60)
        except KeyboardInterrupt:
            controller_server.stop(0)
            print('Controller Server Stopped ...')


if __name__ == '__main__':
    parser = OptionParser()
    parser.add_option("-e", "--csi.endpoint", dest="endpoint",
                      help="grpc endpoint")

    (options, args) = parser.parse_args()
    endpoint = options.endpoint

    curr_server = ControllerServicer(endpoint)
    curr_server.start_server()
