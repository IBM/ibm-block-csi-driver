import grpc
import time
from optparse import OptionParser
import yaml
import os.path

from concurrent import futures
from controller.csi_general import csi_pb2
from controller.csi_general import csi_pb2_grpc
from controller.array_action.array_connection_manager import  ArrayConnectionManager
from controller.common.csi_logger import get_stdout_logger
import config
from controller.array_action.errors import VolumeNotFoundError, IllegalObjectName, PoolDoesNotMatchCapabilities, CapabilityNotSupported, \
            VolumeAlreadyExists, PoolDoesNotExist

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
            self.cfg = yaml.load(ymlfile)  # TODO: add the following when possible : Loader=yaml.FullLoader)
               
    def _validateVolumeCapabilties(self, capabilities):
        logger.debug("all volume capabilies: {}".format(capabilities))
        if len(capabilities) == 0 :
            return False, "capbilities were not set"
        
        for cap in capabilities:
            if cap.mount:
                if cap.mount.fs_type :
                    if  cap.mount.fs_type not in config.SUPPORTED_FS_TYPES:
                        logger.error("unsupported fs_type : {}".format(cap.mount.fs_type))
                        return False, "unsupported fs_type"
                     
            else:
                logger.error("only mount volume capability is supported")
                return False, "only mount volume capability is supported"
                        
            if cap.access_mode.mode not in config.SUPPORTED_ACCESS_MODE:
                logger.error("unsupported access mode : {}".format(cap.access_mode))
                return False, "unsupported access mode"
        return True, ""
    
    def _validate_secret(self, secret):
        if secret:
            if not (config.SECRET_USERNAME_PARAMETER in secret and
                    config.SECRET_PASSWORD_PARAMETER in secret and 
                    config.SECRET_ARRAY_PARAMETER in secret):
                
                return False, 'invalid secret was passed'
            
        else:
            return False, 'secret is missing'
        
        return True, ""
    
    def validate_create_volume_request(self, request):
        logger.debug("validating request")
        
        logger.debug("validating volume name")
        if request.name == '':
            return False, 'name should not be empty'
        
        logger.debug("validating volume capacity")
        if request.capacity_range:
            print request.capacity_range.required_bytes
            if request.capacity_range.required_bytes <= 0 :
                return False, 'size should be bigger then 0'
        else:
            return False, 'no capacity range set'
        
        logger.debug("validating volume capabilities")
        res, msg = self._validateVolumeCapabilties(request.volume_capabilities)
        if not res:
            return False, msg
        
        logger.debug("validating secrets")
        if request.secrets:
            res, msg = self._validate_secret(request.secrets)
            if not res:
                return False, msg
        
        logger.debug("validating storage class parameters")
        if request.parameters:
            if not (config.PARAMETERS_CAPABILITIES in request.parameters and 
                    config.PARAMETERS_CAPACITY in request.parameters):
                return False, 'wrong parameters passed'
                
        else:
            return False, 'parameters are missing'
        
        logger.debug("request validation finished.")
        return True, ""
    
    def _get_vol_id(self, new_vol):
        return "{}:{}".format(new_vol.storage_type, new_vol.volume_name)
        
    def _get_create_volume_response(self, new_vol):
        vol_context = {"volume_name" : new_vol.volume_name,
                       "array_name" : new_vol.array_name,
                       "pool_name" : new_vol.pool_name,
                       "storage_type": new_vol.storage_type
                       }
        try:
            return csi_pb2.CreateVolumeResponse(volume=csi_pb2.Volume(
                capacity_bytes=new_vol.capacity_bytes,
                volume_id=self._get_vol_id(new_vol),
                volume_context=vol_context))
                
        except Exception as ex :
            logger.exception(ex)        
            
    def _get_array_connection_info_from_secret(self, secrets): 
        user = secrets[config.SECRET_USERNAME_PARAMETER]
        password = secrets[config.SECRET_PASSWORD_PARAMETER]
        array_addresses = secrets[config.SECRET_ARRAY_PARAMETER].split(",")
        return user, password, array_addresses
    
                
    def CreateVolume(self, request, context):
        logger.debug("create volume")

        res, msg = self.validate_create_volume_request(request)
        if not res:
            logger.error("failed request validation. error : {}".format(msg))
            context.set_details(msg)
            context.set_code(grpc.StatusCode.INVALID_ARGUMENT)
            return csi_pb2.CreateVolumeResponse() 
        
        volume_name = request.name
        logger.debug("volume name : {}".format(volume_name))
        
        secrets = request.secrets
        user, password, array_addresses = self._get_array_connection_info_from_secret(secrets)
        
        pool = request.parameters[config.PARAMETERS_CAPACITY].split("=")[1]
        capabilities = request.parameters[config.PARAMETERS_CAPABILITIES] 
        vol = None       
    
        try:
            # TODO : pass multiple array addresses
            # TODO: use connection detection instead of xiv_type
            with ArrayConnectionManager(user, password, array_addresses[0]) as array_mediator:
                logger.debug(array_mediator)
                
                try:
                    vol = array_mediator.get_volume(volume_name)
                
                except VolumeNotFoundError as ex:
                    logger.debug("volume was not found. creating a new volume")
                    print request.parameters
                    
                    vol = array_mediator.create_volume(volume_name, request.capacity_range.required_bytes, capabilities, pool)
                    
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
        except Exception as ex :
            logger.debug("an internal exception occured")
            logger.exception(ex)
            context.set_code(grpc.StatusCode.INTERNAL)
            context.set_details('an internal exception occurred : {}'.format(ex))
            return csi_pb2.CreateVolumeResponse()
        
        logger.debug("generating create volume response")
        return self._get_create_volume_response(vol)
    
    def _get_volume_id_info(self, volume_id):
        split_vol = volume_id.split(":")
        if len(split_vol) != 2 :
            return False, None, None
        array_type, vol_id = split_vol
        print "AAA : ", array_type, vol_id 
        return True, array_type, vol_id
    
    def DeleteVolume(self, request, context):
        logger.debug("DeleteVolume")
        volume_id = request.volume_id
        secrets = request.secrets
        
        res, msg = self._validate_secret(secrets)
        if not res:
            context.set_details(msg)
            context.set_code(grpc.StatusCode.INVALID_ARGUMENT)
            return csi_pb2.DeleteVolumeResponse() 
        
        user, password, array_addresses = self._get_array_connection_info_from_secret(secrets)
        
        res, array_type, vol_id = self._get_volume_id_info(volume_id)
        if not res:
            context.set_details("Wrong volume id format")
            context.set_code(grpc.StatusCode.INVALID_ARGUMENT)
            return csi_pb2.DeleteVolumeResponse()  
        
        try:
            # TODO : pass multiple array addresses
            with ArrayConnectionManager( user, password, array_addresses[0], array_type) as array_mediator:
                logger.debug(array_mediator)
                
                try:
                    array_mediator.delete_volume(vol_id)
                
                except VolumeNotFoundError as ex:
                    logger.debug("volume was not found during deletion: {0}".format(ex))
                
        except Exception as ex :
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
