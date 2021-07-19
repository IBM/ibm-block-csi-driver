import os.path
import time
from argparse import ArgumentParser
from concurrent import futures

import grpc
import yaml
from retry import retry

import controller.array_action.errors as array_errors
import controller.controller_server.config as config
import controller.controller_server.utils as utils
from controller.array_action import messages
from controller.array_action.storage_agent import get_agent, detect_array_type
from controller.common import settings
from controller.common.csi_logger import get_stdout_logger, set_log_level
from controller.common.node_info import NodeIdInfo
from controller.common.utils import set_current_thread_name
from controller.controller_server import messages as controller_messages
from controller.controller_server.errors import ObjectIdError, ValidationException
from controller.controller_server.exception_handler import handle_common_exceptions, handle_exception, \
    build_error_response
from controller.csi_general import csi_pb2
from controller.csi_general import csi_pb2_grpc

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
            self.cfg = yaml.safe_load(yamlfile)  # TODO: add the following when possible : Loader=yaml.FullLoader)

    @handle_common_exceptions(csi_pb2.CreateVolumeResponse)
    def CreateVolume(self, request, context):
        set_current_thread_name(request.name)
        logger.info("create volume")
        try:
            utils.validate_create_volume_request(request)
        except ObjectIdError as ex:
            return handle_exception(ex, context, grpc.StatusCode.NOT_FOUND,
                                    csi_pb2.CreateVolumeResponse)

        logger.debug("volume name : {}".format(request.name))

        source_type, source_id = self._get_source_type_and_id(request)

        logger.debug("Source {0} id : {1}".format(source_type, source_id))

        topologies = utils.get_volume_topologies(request)

        secrets = request.secrets

        try:
            array_connection_info = utils.get_array_connection_info_from_secrets(
                secrets=secrets,
                topologies=topologies)
            volume_parameters = utils.get_volume_parameters(parameters=request.parameters,
                                                            system_id=array_connection_info.system_id)
            pool = volume_parameters.pool
            if not pool:
                raise ValidationException(controller_messages.pool_should_not_be_empty_message)
            space_efficiency = volume_parameters.space_efficiency
            # TODO : pass multiple array addresses
            array_type = detect_array_type(array_connection_info.array_addresses)
            system_id = array_connection_info.system_id
            with get_agent(array_connection_info, array_type).get_mediator() as array_mediator:
                logger.debug(array_mediator)

                if system_id and system_id != array_mediator.identifier:
                    raise ValidationException(
                        controller_messages.system_id_mismatch_message.format(system_id, array_mediator.identifier))

                volume_final_name = self._get_volume_final_name(volume_parameters, request.name, array_mediator)

                required_bytes = request.capacity_range.required_bytes
                max_size = array_mediator.maximal_volume_size_in_bytes
                min_size = array_mediator.minimal_volume_size_in_bytes

                if required_bytes > max_size:
                    message = messages.SizeOutOfRangeError_message.format(required_bytes, max_size)
                    return build_error_response(message, context, grpc.StatusCode.OUT_OF_RANGE,
                                                csi_pb2.CreateVolumeResponse)

                if required_bytes == 0:
                    required_bytes = min_size
                    logger.debug("requested size is 0 so the default size will be used : {0} ".format(
                        required_bytes))

                try:
                    volume = array_mediator.get_volume(
                        volume_final_name,
                        pool=pool,
                    )
                except array_errors.ObjectNotFoundError:
                    logger.debug(
                        "volume was not found. creating a new volume with parameters: {0}".format(request.parameters))

                    array_mediator.validate_supported_space_efficiency(space_efficiency)
                    volume = array_mediator.create_volume(volume_final_name, required_bytes, space_efficiency,
                                                          pool)
                else:
                    logger.debug("volume found : {}".format(volume))

                    if not source_id and volume.capacity_bytes != request.capacity_range.required_bytes:
                        message = "Volume was already created with different size."
                        return build_error_response(message, context, grpc.StatusCode.ALREADY_EXISTS,
                                                    csi_pb2.CreateVolumeResponse)

                    copy_source_res = self._handle_existing_volume_source(volume, source_id, source_type,
                                                                          array_mediator.identifier,
                                                                          context)
                    if copy_source_res:
                        return copy_source_res

                if source_id:
                    self._copy_to_existing_volume_from_source(volume, source_id,
                                                              source_type, required_bytes,
                                                              array_mediator)
                    volume.copy_source_id = source_id

                res = utils.generate_csi_create_volume_response(volume, array_mediator.identifier, source_type)
                logger.info("finished create volume")
                return res
        except array_errors.InvalidArgumentError as ex:
            return handle_exception(ex, context, grpc.StatusCode.INVALID_ARGUMENT, csi_pb2.CreateVolumeResponse)
        except array_errors.VolumeAlreadyExists as ex:
            return handle_exception(ex, context, grpc.StatusCode.ALREADY_EXISTS, csi_pb2.CreateVolumeResponse)

    def _copy_to_existing_volume_from_source(self, volume, source_id, source_type,
                                             minimum_volume_size, array_mediator):
        volume_id = volume.id
        try:
            source_object = array_mediator.get_object_by_id(source_id, source_type)
            if not source_object:
                self._rollback_create_volume_from_source(array_mediator, volume.id)
                raise array_errors.ObjectNotFoundError(source_id)
            source_capacity = source_object.capacity_bytes
            logger.debug("Copy {0} {1} data to volume {2}.".format(source_type, source_id, volume_id))
            array_mediator.copy_to_existing_volume_from_source(volume_id, source_id,
                                                               source_capacity, minimum_volume_size)
            logger.debug("Copy volume from {0} finished".format(source_type))
        except array_errors.ObjectNotFoundError as ex:
            logger.error("Volume not found while copying {0} data to volume".format(source_type))
            logger.exception(ex)
            self._rollback_create_volume_from_source(array_mediator, volume.id)
            raise ex
        except Exception as ex:
            logger.error("Exception raised while copying {0} data to volume".format(source_type))
            self._rollback_create_volume_from_source(array_mediator, volume.id)
            raise ex

    @retry(Exception, tries=5, delay=1)
    def _rollback_create_volume_from_source(self, array_mediator, volume_id):
        logger.debug("Rollback copy volume from source. Deleting volume {0}".format(volume_id))
        array_mediator.delete_volume(volume_id)

    def _handle_existing_volume_source(self, volume, source_id, source_type, system_id, context):
        """
        Args:
            volume              : volume fetched or created in CreateVolume
            source_id           : id of object we should copy to volume or None if volume should not be copied
            source_type:        : the object type of the source - volume or snapshot
            context             : CreateVolume response context
        Returns:
            If volume exists and is a copy of specified object - set context status to OK
            and return CreateVolumeResponse.
            If volume is a copy of another source - set context status to INTERNAL and return CreateVolumeResponse.
            In any other case return None.
        """
        volume_name = volume.name
        volume_copy_source_id = volume.copy_source_id
        if not source_id and not volume_copy_source_id:
            return None
        if volume_copy_source_id == source_id:
            return self._handle_volume_exists_with_same_source(context, source_id, source_type, volume_name, volume,
                                                               system_id)
        return self._handle_volume_exists_with_different_source(context, source_id, source_type, volume_name)

    def _handle_volume_exists_with_same_source(self, context, source_id, source_type, volume_name, volume, system_id):
        logger.debug(
            "Volume {0} exists and it is a copy of {1} {2}.".format(volume_name, source_type, source_id))
        context.set_code(grpc.StatusCode.OK)
        return utils.generate_csi_create_volume_response(volume, system_id, source_type)

    def _handle_volume_exists_with_different_source(self, context, source_id, source_type, volume_name):
        logger.debug(
            "Volume {0} exists but it is not a copy of {1} {2}.".format(volume_name, source_type, source_id))
        message = "Volume already exists but it was created from a different source."
        return build_error_response(message, context, grpc.StatusCode.ALREADY_EXISTS, csi_pb2.CreateVolumeResponse)

    @handle_common_exceptions(csi_pb2.DeleteVolumeResponse)
    def DeleteVolume(self, request, context):
        set_current_thread_name(request.volume_id)
        logger.info("DeleteVolume")
        secrets = request.secrets
        utils.validate_delete_volume_request(request)

        try:
            volume_id_info = utils.get_volume_id_info(request.volume_id)
        except ObjectIdError as ex:
            logger.warning("volume id is invalid. error : {}".format(ex))
            return csi_pb2.DeleteVolumeResponse()

        system_id = volume_id_info.system_id
        array_type = volume_id_info.array_type
        volume_id = volume_id_info.object_id
        array_connection_info = utils.get_array_connection_info_from_secrets(secrets, system_id=system_id)

        with get_agent(array_connection_info, array_type).get_mediator() as array_mediator:
            logger.debug(array_mediator)

            try:
                logger.debug("Deleting volume {0}".format(volume_id))
                array_mediator.delete_volume(volume_id)

            except array_errors.ObjectNotFoundError as ex:
                logger.debug("volume was not found during deletion: {0}".format(ex))
            except array_errors.PermissionDeniedError as ex:
                return handle_exception(ex, context, grpc.StatusCode.PERMISSION_DENIED,
                                        csi_pb2.DeleteVolumeResponse)

        logger.debug("generating delete volume response")
        res = csi_pb2.DeleteVolumeResponse()
        logger.info("finished DeleteVolume")
        return res

    @handle_common_exceptions(csi_pb2.ControllerPublishVolumeResponse)
    def ControllerPublishVolume(self, request, context):
        set_current_thread_name(request.volume_id)
        logger.info("ControllerPublishVolume")
        utils.validate_publish_volume_request(request)
        try:
            volume_id_info = utils.get_volume_id_info(request.volume_id)
            system_id = volume_id_info.system_id
            array_type = volume_id_info.array_type
            volume_id = volume_id_info.object_id
            node_id_info = NodeIdInfo(request.node_id)
            node_name = node_id_info.node_name
            initiators = node_id_info.initiators

            logger.debug("node name for this publish operation is : {0}".format(node_name))

            array_connection_info = utils.get_array_connection_info_from_secrets(request.secrets, system_id=system_id)
            with get_agent(array_connection_info, array_type).get_mediator() as array_mediator:
                lun, connectivity_type, array_initiators = array_mediator.map_volume_by_initiators(volume_id,
                                                                                                   initiators)
            logger.info("finished ControllerPublishVolume")
            res = utils.generate_csi_publish_volume_response(lun,
                                                             connectivity_type,
                                                             self.cfg,
                                                             array_initiators)
            return res

        except array_errors.VolumeMappedToMultipleHostsError as ex:
            return handle_exception(ex, context, grpc.StatusCode.FAILED_PRECONDITION,
                                    csi_pb2.ControllerPublishVolumeResponse)
        except (array_errors.LunAlreadyInUseError, array_errors.NoAvailableLunError) as ex:
            return handle_exception(ex, context, grpc.StatusCode.RESOURCE_EXHAUSTED,
                                    csi_pb2.ControllerPublishVolumeResponse)
        except (array_errors.NoIscsiTargetsFoundError, ObjectIdError) as ex:
            return handle_exception(ex, context, grpc.StatusCode.NOT_FOUND, csi_pb2.ControllerPublishVolumeResponse)
        except array_errors.UnsupportedConnectivityTypeError as ex:
            return handle_exception(ex, context, grpc.StatusCode.INVALID_ARGUMENT,
                                    csi_pb2.ControllerPublishVolumeResponse)

    @handle_common_exceptions(csi_pb2.ControllerUnpublishVolumeResponse)
    def ControllerUnpublishVolume(self, request, context):
        set_current_thread_name(request.volume_id)
        logger.info("ControllerUnpublishVolume")
        utils.validate_unpublish_volume_request(request)
        try:
            volume_id_info = utils.get_volume_id_info(request.volume_id)
            system_id = volume_id_info.system_id
            array_type = volume_id_info.array_type
            volume_id = volume_id_info.object_id
            node_id_info = NodeIdInfo(request.node_id)
            node_name = node_id_info.node_name
            initiators = node_id_info.initiators
            logger.debug("node name for this unpublish operation is : {0}".format(node_name))

            array_connection_info = utils.get_array_connection_info_from_secrets(request.secrets,
                                                                                 system_id=system_id)

            with get_agent(array_connection_info, array_type).get_mediator() as array_mediator:
                array_mediator.unmap_volume_by_initiators(volume_id, initiators)

            logger.info("finished ControllerUnpublishVolume")
            return csi_pb2.ControllerUnpublishVolumeResponse()
        except ObjectIdError as ex:
            return handle_exception(ex, context, grpc.StatusCode.INVALID_ARGUMENT,
                                    array_errors.VolumeAlreadyUnmappedError)
        except array_errors.VolumeAlreadyUnmappedError:
            logger.debug("Idempotent case. volume is already unmapped.")
            return csi_pb2.ControllerUnpublishVolumeResponse()
        except array_errors.ObjectNotFoundError:
            logger.debug("Idempotent case. volume is already deleted.")
            return csi_pb2.ControllerUnpublishVolumeResponse()

    @handle_common_exceptions(csi_pb2.ValidateVolumeCapabilitiesResponse)
    def ValidateVolumeCapabilities(self, request, context):
        logger.info("ValidateVolumeCapabilities")
        raise NotImplementedError()

    @handle_common_exceptions(csi_pb2.ListVolumesResponse)
    def ListVolumes(self, request, context):
        logger.info("ListVolumes")
        raise NotImplementedError()

    @handle_common_exceptions(csi_pb2.CreateSnapshotResponse)
    def CreateSnapshot(self, request, context):
        set_current_thread_name(request.name)
        logger.info("Create snapshot")
        utils.validate_create_snapshot_request(request)
        source_volume_id = request.source_volume_id
        logger.info("Snapshot base name : {}. Source volume id : {}".format(request.name, source_volume_id))
        secrets = request.secrets
        try:
            volume_id_info = utils.get_volume_id_info(source_volume_id)
            system_id = volume_id_info.system_id
            array_type = volume_id_info.array_type
            volume_id = volume_id_info.object_id
            array_connection_info = utils.get_array_connection_info_from_secrets(secrets, system_id=system_id)
            snapshot_parameters = utils.get_snapshot_parameters(parameters=request.parameters,
                                                                system_id=array_connection_info.system_id)
            pool = snapshot_parameters.pool
            with get_agent(array_connection_info, array_type).get_mediator() as array_mediator:
                logger.debug(array_mediator)
                snapshot_final_name = self._get_snapshot_final_name(snapshot_parameters, request.name, array_mediator)

                logger.info("Snapshot name : {}. Volume id : {}".format(snapshot_final_name, volume_id))
                snapshot = array_mediator.get_snapshot(
                    volume_id,
                    snapshot_final_name,
                    pool=pool
                )

                if snapshot:
                    if snapshot.source_volume_id != volume_id:
                        message = messages.SnapshotWrongVolumeError_message.format(snapshot_final_name,
                                                                                   snapshot.source_volume_id,
                                                                                   volume_id)
                        return build_error_response(message, context, grpc.StatusCode.ALREADY_EXISTS,
                                                    csi_pb2.CreateSnapshotResponse)
                else:
                    logger.debug(
                        "Snapshot doesn't exist. Creating a new snapshot {0} from volume {1}".format(
                            snapshot_final_name,
                            volume_id))
                    snapshot = array_mediator.create_snapshot(volume_id, snapshot_final_name, pool)

                logger.debug("generating create snapshot response")
                res = utils.generate_csi_create_snapshot_response(snapshot, source_volume_id)
                logger.info("finished create snapshot")
                return res
        except (ObjectIdError, array_errors.SnapshotSourcePoolMismatch) as ex:
            return handle_exception(ex, context, grpc.StatusCode.INVALID_ARGUMENT,
                                    csi_pb2.CreateSnapshotResponse)
        except array_errors.SnapshotAlreadyExists as ex:
            return handle_exception(ex, context, grpc.StatusCode.ALREADY_EXISTS,
                                    csi_pb2.CreateSnapshotResponse)
        except array_errors.NotEnoughSpaceInPool as ex:
            return handle_exception(ex, context, grpc.StatusCode.RESOURCE_EXHAUSTED,
                                    csi_pb2.CreateSnapshotResponse)

    @handle_common_exceptions(csi_pb2.DeleteSnapshotResponse)
    def DeleteSnapshot(self, request, context):
        set_current_thread_name(request.snapshot_id)
        logger.info("Delete snapshot")
        secrets = request.secrets
        utils.validate_delete_snapshot_request(request)
        try:
            try:
                snapshot_id_info = utils.get_snapshot_id_info(request.snapshot_id)
            except ObjectIdError as ex:
                logger.warning("Snapshot id is invalid. error : {}".format(ex))
                return csi_pb2.DeleteSnapshotResponse()

            system_id = snapshot_id_info.system_id
            array_type = snapshot_id_info.array_type
            snapshot_id = snapshot_id_info.object_id
            array_connection_info = utils.get_array_connection_info_from_secrets(secrets, system_id=system_id)
            with get_agent(array_connection_info, array_type).get_mediator() as array_mediator:
                logger.debug(array_mediator)
                try:
                    array_mediator.delete_snapshot(snapshot_id)
                except array_errors.ObjectNotFoundError as ex:
                    logger.debug("Snapshot was not found during deletion: {0}".format(ex))

        except array_errors.ObjectNotFoundError as ex:
            logger.debug("snapshot was not found during deletion: {0}".format(ex.message))
            context.set_code(grpc.StatusCode.OK)
            return csi_pb2.DeleteSnapshotResponse()

        logger.debug("generating delete snapshot response")
        res = csi_pb2.DeleteSnapshotResponse()
        logger.info("finished DeleteSnapshot")
        return res

    @handle_common_exceptions(csi_pb2.GetCapacityResponse)
    def GetCapacity(self, request, context):
        logger.info("GetCapacity")
        raise NotImplementedError()

    @handle_common_exceptions(csi_pb2.ControllerExpandVolumeResponse)
    def ControllerExpandVolume(self, request, context):
        set_current_thread_name(request.volume_id)
        logger.info("ControllerExpandVolume")
        secrets = request.secrets
        utils.validate_expand_volume_request(request)
        try:
            volume_id_info = utils.get_volume_id_info(request.volume_id)
        except ObjectIdError as ex:
            return handle_exception(ex, context, grpc.StatusCode.INVALID_ARGUMENT,
                                    csi_pb2.ControllerExpandVolumeResponse)
        try:
            system_id = volume_id_info.system_id
            array_type = volume_id_info.array_type
            volume_id = volume_id_info.object_id
            array_connection_info = utils.get_array_connection_info_from_secrets(secrets, system_id=system_id)
            with get_agent(array_connection_info, array_type).get_mediator() as array_mediator:
                logger.debug(array_mediator)

                required_bytes = request.capacity_range.required_bytes
                max_size = array_mediator.maximal_volume_size_in_bytes

                volume_before_expand = array_mediator.get_object_by_id(volume_id, config.VOLUME_TYPE_NAME)
                if not volume_before_expand:
                    raise array_errors.ObjectNotFoundError(volume_id)

                if volume_before_expand.capacity_bytes >= required_bytes:
                    context.set_code(grpc.StatusCode.OK)
                    return utils.generate_csi_expand_volume_response(volume_before_expand.capacity_bytes,
                                                                     node_expansion_required=False)

                if required_bytes > max_size:
                    message = messages.SizeOutOfRangeError_message.format(required_bytes, max_size)
                    return build_error_response(message, context, grpc.StatusCode.OUT_OF_RANGE,
                                                csi_pb2.ControllerExpandVolumeResponse)

                logger.debug("expanding volume {0}".format(volume_id))
                array_mediator.expand_volume(
                    volume_id=volume_id,
                    required_bytes=required_bytes)

                volume_after_expand = array_mediator.get_object_by_id(volume_id, config.VOLUME_TYPE_NAME)
                if not volume_after_expand:
                    raise array_errors.ObjectNotFoundError(volume_id)

            res = utils.generate_csi_expand_volume_response(volume_after_expand.capacity_bytes)
            logger.info("finished expanding volume")
            return res

        except array_errors.NotEnoughSpaceInPool as ex:
            return handle_exception(ex, context, grpc.StatusCode.RESOURCE_EXHAUSTED,
                                    csi_pb2.ControllerExpandVolumeResponse)

    def ControllerGetCapabilities(self, request, context):
        logger.info("ControllerGetCapabilities")
        types = csi_pb2.ControllerServiceCapability.RPC.Type

        res = csi_pb2.ControllerGetCapabilitiesResponse(
            capabilities=[csi_pb2.ControllerServiceCapability(
                rpc=csi_pb2.ControllerServiceCapability.RPC(type=types.Value("CREATE_DELETE_VOLUME"))),
                csi_pb2.ControllerServiceCapability(
                    rpc=csi_pb2.ControllerServiceCapability.RPC(type=types.Value("CREATE_DELETE_SNAPSHOT"))),
                csi_pb2.ControllerServiceCapability(
                    rpc=csi_pb2.ControllerServiceCapability.RPC(type=types.Value("PUBLISH_UNPUBLISH_VOLUME"))),
                csi_pb2.ControllerServiceCapability(
                    rpc=csi_pb2.ControllerServiceCapability.RPC(type=types.Value("CLONE_VOLUME"))),
                csi_pb2.ControllerServiceCapability(
                    rpc=csi_pb2.ControllerServiceCapability.RPC(type=types.Value("EXPAND_VOLUME")))])

        logger.info("finished ControllerGetCapabilities")
        return res

    def __get_identity_config(self, attribute_name):
        return self.cfg['identity'][attribute_name]

    @handle_common_exceptions(csi_pb2.GetPluginInfoResponse)
    def GetPluginInfo(self, _, context):
        logger.info("GetPluginInfo")
        name = self.__get_identity_config("name")
        version = self.__get_identity_config("version")

        if not name or not version:
            message = "plugin name or version cannot be empty"
            return build_error_response(message, context, grpc.StatusCode.INTERNAL, csi_pb2.GetPluginInfoResponse)

        logger.info("finished GetPluginInfo")
        return csi_pb2.GetPluginInfoResponse(name=name, vendor_version=version)

    def _get_volume_final_name(self, volume_parameters, name, array_mediator):
        return self._get_object_final_name(volume_parameters, name, array_mediator,
                                           config.VOLUME_TYPE_NAME)

    def _get_snapshot_final_name(self, volume_parameters, name, array_mediator):
        name = self._get_object_final_name(volume_parameters, name, array_mediator,
                                           config.SNAPSHOT_TYPE_NAME)
        return name

    def _get_object_final_name(self, volume_parameters, name, array_mediator, object_type):
        prefix = ""
        if volume_parameters.prefix:
            prefix = volume_parameters.prefix
            if len(prefix) > array_mediator.max_object_prefix_length:
                raise array_errors.IllegalObjectName(
                    "The {} name prefix '{}' is too long, max allowed length is {}".format(
                        object_type,
                        prefix,
                        array_mediator.max_object_prefix_length
                    )
                )
        if not prefix:
            prefix = array_mediator.default_object_prefix
        full_name = self._join_object_prefix_with_name(prefix, name)
        if len(full_name) > array_mediator.max_object_name_length:
            hashed_name = utils.hash_string(name)
            full_name = self._join_object_prefix_with_name(prefix, hashed_name)
        return full_name[:array_mediator.max_object_name_length]

    def _join_object_prefix_with_name(self, prefix, name):
        if prefix:
            return settings.NAME_PREFIX_SEPARATOR.join((prefix, name))
        return name

    def GetPluginCapabilities(self, _, __):
        logger.info("GetPluginCapabilities")
        service_type = csi_pb2.PluginCapability.Service.Type
        volume_expansion_type = csi_pb2.PluginCapability.VolumeExpansion.Type
        capabilities = self.__get_identity_config("capabilities")
        capability_list = []
        service_capabilities = capabilities.get('Service')
        volume_expansion_capability = capabilities.get('VolumeExpansion')
        if service_capabilities:
            for service_capability in service_capabilities:
                capability_list.append(
                    csi_pb2.PluginCapability(
                        service=csi_pb2.PluginCapability.Service(type=service_type.Value(service_capability))))
        if volume_expansion_capability:
            capability_list.append(
                csi_pb2.PluginCapability(
                    volume_expansion=csi_pb2.PluginCapability.VolumeExpansion(
                        type=volume_expansion_type.Value(volume_expansion_capability))))
        logger.info("finished GetPluginCapabilities")
        return csi_pb2.GetPluginCapabilitiesResponse(
            capabilities=capability_list
        )

    def Probe(self, _, context):
        context.set_code(grpc.StatusCode.OK)
        return csi_pb2.ProbeResponse()

    def start_server(self):
        controller_server = grpc.server(futures.ThreadPoolExecutor(max_workers=settings.CSI_CONTROLLER_SERVER_WORKERS))

        csi_pb2_grpc.add_ControllerServicer_to_server(self, controller_server)
        csi_pb2_grpc.add_IdentityServicer_to_server(self, controller_server)

        # bind the server to the port defined above
        # controller_server.add_insecure_port('[::]:{}'.format(self.server_port))
        # controller_server.add_insecure_port('unix://{}'.format(self.server_port))
        controller_server.add_insecure_port(self.endpoint)

        logger.info("Controller version: {}".format(self.__get_identity_config("version")))

        # start the server
        logger.debug("Listening for connections on endpoint address: {}".format(self.endpoint))

        controller_server.start()
        logger.debug('Controller Server running ...')

        try:
            while True:
                time.sleep(60 * 60 * 60)
        except KeyboardInterrupt:
            controller_server.stop(0)
            logger.debug('Controller Server Stopped ...')

    def _get_source_type_and_id(self, request):
        source = request.volume_content_source
        object_id = None
        source_type = None
        if source:
            logger.info(source)
            if source.HasField(config.SNAPSHOT_TYPE_NAME):
                source_id = source.snapshot.snapshot_id
                source_type = config.SNAPSHOT_TYPE_NAME
            elif source.HasField(config.VOLUME_TYPE_NAME):
                source_id = source.volume.volume_id
                source_type = config.VOLUME_TYPE_NAME
            else:
                return None, None
            object_id_info = utils.get_object_id_info(source_id, source_type)
            object_id = object_id_info.object_id
        return source_type, object_id


def main():
    parser = ArgumentParser()
    parser.add_argument("-e", "--csi-endpoint", dest="endpoint", help="grpc endpoint")
    parser.add_argument("-l", "--loglevel", dest="loglevel", help="log level")
    arguments = parser.parse_args()

    set_log_level(arguments.loglevel)

    controller_servicer = ControllerServicer(arguments.endpoint)
    controller_servicer.start_server()


if __name__ == '__main__':
    main()
