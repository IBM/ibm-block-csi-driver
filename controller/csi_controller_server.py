#!/bin/python
import grpc
import time
from csi_general import csi_pb2
from csi_general import csi_pb2_grpc
from concurrent import futures
from optparse import OptionParser
from array_action.array_connection_manager import  ArrayConnectionManager,NoConnctionAvailableException,  xiv_type
import threading
from csi_logger import get_stdout_logger

logger = get_stdout_logger()

TIMEOUT=3

class ControllerServicer(csi_pb2_grpc.ControllerServicer):
    """
    gRPC server for Digestor Service
    """

    def __init__(self, endpoint):
        self.endpoint = endpoint
    
    def CreateVolume(self, request, context):
        logger.debug( "create volume")
        #TODO: uncomment this! this is the test logic
#         if request.name == '':
#             context.set_code(grpc.StatusCode.INVALID_ARGUMENT)
#             context.set_details('name should not be empty')
#             return csi_pb2.CreateVolumeResponse()
        
        try:
            args = {"array_type" :xiv_type, "user": "olgas", "password" : "olgas123", "endpoint": "gen4d-67d.xiv.ibm.com"}
            with ArrayConnectionManager(**args) as array_mediator:
                logger.debug("here1")
                try:
                    logger.debug( array_mediator)
                    vol = array_mediator.get_volume("olga_new_vol_1231")
                    logger.debug(vol)
                    #TODO : remove this from here! it should be above
                    if request.name == '':
                        context.set_code(grpc.StatusCode.INVALID_ARGUMENT)
                        context.set_details('name should not be empty')
                        return csi_pb2.CreateVolumeResponse()
                except Exception as ex :
                    logger.debug(ex)
                  
                context.set_code(grpc.StatusCode.OK)
                return csi_pb2.CreateVolumeResponse()
#    
        except Exception as ex :
            logger.exception(ex)
            context.set_code(grpc.StatusCode.INTERNAL)
            context.set_details('an error ocurred while connecting to the storage endpoint')
            return csi_pb2.CreateVolumeResponse()
    
    def DeleteVolume(self, request, context):
        print("DeleteVolume")
        context.set_code(grpc.StatusCode.UNIMPLEMENTED)
        return csi_pb2.DeleteVolumeResponse()
    
    def ControllerPublishVolume(self, request, context):
        print("ControllerPublishVolume")
        context.set_code(grpc.StatusCode.UNIMPLEMENTED)
        return csi_pb2.ControllerPublishVolumeResponse()
    
    def ControllerUnpublishVolume(self, request, context):
        print("ControllerUnpublishVolume")
        context.set_code(grpc.StatusCode.UNIMPLEMENTED)
        return csi_pb2.ControllerUnpublishVolumeResponse()
    
    def ValidateVolumeCapabilities(self, request, context):
        print("ValidateVolumeCapabilities")
        context.set_code(grpc.StatusCode.UNIMPLEMENTED)
        return csi_pb2.ValidateVolumeCapabilitiesResponse()
    
    def ListVolumes(self, request, context):
        print("ListVolumes")
        context.set_code(grpc.StatusCode.UNIMPLEMENTED)
        return csi_pb2.ListVolumesResponse()
    
    def GetCapacity(self, request, context):
        print("GetCapacity")
        context.set_code(grpc.StatusCode.UNIMPLEMENTED)
        return csi_pb2.GetCapacityResponse()
        
    def ControllerGetCapabilities(self, request, context):
        print("ControllerGetCapabilities")
        types = csi_pb2.ControllerServiceCapability.RPC.Type
                    
        return csi_pb2.ControllerGetCapabilitiesResponse(
           capabilities=[csi_pb2.ControllerServiceCapability(
               rpc=csi_pb2.ControllerServiceCapability.RPC(type=types.Value("CREATE_DELETE_VOLUME"))),
                         csi_pb2.ControllerServiceCapability(
                             rpc=csi_pb2.ControllerServiceCapability.RPC(type=types.Value("PUBLISH_UNPUBLISH_VOLUME")))  ])
 
    def start_server(self):
        controller_server = grpc.server(futures.ThreadPoolExecutor(max_workers=10))
        
        csi_pb2_grpc.add_ControllerServicer_to_server(self, controller_server)
        
        # bind the server to the port defined above
        # controller_server.add_insecure_port('[::]:{}'.format(self.server_port))
        #controller_server.add_insecure_port('unix://{}'.format(self.server_port))
        controller_server.add_insecure_port(self.endpoint)
        
        # start the server
        controller_server.start()
        print('Controller Server running ...')
        
        try:
            while True:
                time.sleep(60 * 60 * 60)
        except KeyboardInterrupt:
            controller_server.stop(0)
            print('Controller Server Stopped ...')




parser = OptionParser()
parser.add_option("-e", "--csi.endpoint", dest="endpoint",
                  help="grpc endpoint")

(options, args) = parser.parse_args()
endpoint = options.endpoint


curr_server = ControllerServicer(endpoint)
curr_server.start_server()
