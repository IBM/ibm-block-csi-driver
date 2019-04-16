import grpc
import time
from optparse import OptionParser
import yaml

import os.path

from concurrent import futures
from controller.csi_general import csi_pb2
from controller.csi_general import csi_pb2_grpc
from controller.array_action.array_connection_manager import  ArrayConnectionManager, NoConnctionAvailableException, xiv_type
from controller.common.csi_logger import get_stdout_logger
from test_settings import  user, password, array, vol_name

logger = get_stdout_logger()


class ControllerServicer(csi_pb2_grpc.ControllerServicer):
    """
    gRPC server for Digestor Service
    """

    def __init__(self, endpoint):
        self.endpoint = endpoint
        
        my_path = os.path.abspath(os.path.dirname(__file__))
        path = os.path.join(my_path, "../../common/config.yaml")

        with open(path, 'r') as ymlfile:
            self.cfg = yaml.load(ymlfile, Loader=yaml.FullLoader)
                
    def CreateVolume(self, request, context):
        logger.debug("create volume")
        if request.name == '':
            context.set_code(grpc.StatusCode.INVALID_ARGUMENT)
            context.set_details('name should not be empty')
            return csi_pb2.CreateVolumeResponse()
         
        try:
            with ArrayConnectionManager(xiv_type, user, password, array) as array_mediator:
                logger.debug(array_mediator)
                # TODO: add create volume logic
                vol = array_mediator.get_volume(vol_name)

                logger.debug(vol)
 
                context.set_code(grpc.StatusCode.OK)
                return csi_pb2.CreateVolumeResponse()
            
        except Exception as ex :
            logger.exception(ex)
            context.set_code(grpc.StatusCode.INTERNAL)
            context.set_details('an error ocurred while connecting to the storage endpoint')
            return csi_pb2.CreateVolumeResponse()
    
    def DeleteVolume(self, request, context):
        logger.debug("DeleteVolume")
        context.set_code(grpc.StatusCode.UNIMPLEMENTED)
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
                             rpc=csi_pb2.ControllerServiceCapability.RPC(type=types.Value("PUBLISH_UNPUBLISH_VOLUME")))  ])
 
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
