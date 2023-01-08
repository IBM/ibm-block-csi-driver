import grpc
from csi_general import volumegroup_pb2_grpc, volumegroup_pb2

import controllers.array_action.errors as array_errors
import controllers.servers.settings as servers_settings
import controllers.servers.utils as utils
from controllers.array_action.storage_agent import get_agent, detect_array_type
from controllers.common.csi_logger import get_stdout_logger
from controllers.servers.csi.decorators import csi_method
from controllers.servers.csi.exception_handler import handle_exception, \
    build_error_response
from controllers.servers.errors import ObjectIdError

logger = get_stdout_logger()


class VolumeGroupControllerServicer(volumegroup_pb2_grpc.ControllerServicer):
    @csi_method(error_response_type=volumegroup_pb2.CreateVolumeGroupResponse, lock_request_attribute="name")
    def CreateVolumeGroup(self, request, context):
        utils.validate_create_volume_group_request(request)

        logger.debug("volume group name : {}".format(request.name))
        try:
            array_connection_info = utils.get_array_connection_info_from_secrets(request.secrets)
            volume_group_parameters = utils.get_volume_group_parameters(parameters=request.parameters)

            # TODO : pass multiple array addresses
            array_type = detect_array_type(array_connection_info.array_addresses)
            with get_agent(array_connection_info, array_type).get_mediator() as array_mediator:
                logger.debug(array_mediator)
                volume_group_final_name = self._get_volume_group_final_name(volume_group_parameters, request.name,
                                                                            array_mediator)

                try:
                    volume_group = array_mediator.get_volume_group(volume_group_final_name)
                except array_errors.ObjectNotFoundError:
                    logger.debug(
                        "volume group was not found. creating a new volume group")
                    volume_group = array_mediator.create_volume_group(volume_group_final_name)
                else:
                    logger.debug("volume group found : {}".format(volume_group))

                    if len(volume_group.volumes) > 0:
                        message = "Volume group {} is not empty".format(volume_group.name)
                        return build_error_response(message, context, grpc.StatusCode.ALREADY_EXISTS,
                                                    volumegroup_pb2.CreateVolumeGroupResponse)

                response = utils.generate_csi_create_volume_group_response(volume_group)
                return response
        except array_errors.VolumeGroupAlreadyExists as ex:
            return handle_exception(ex, context, grpc.StatusCode.ALREADY_EXISTS,
                                    volumegroup_pb2.CreateVolumeGroupResponse)

    @csi_method(error_response_type=volumegroup_pb2.DeleteVolumeGroupResponse, lock_request_attribute="volume_group_id")
    def DeleteVolumeGroup(self, request, _):
        secrets = request.secrets
        utils.validate_delete_volume_group_request(request)

        try:
            volume_group_id_info = utils.get_volume_group_id_info(request.volume_group_id)
        except ObjectIdError as ex:
            logger.warning("volume group id is invalid. error : {}".format(ex))
            return volumegroup_pb2.DeleteVolumeGroupResponse()

        array_type = volume_group_id_info.array_type
        volume_group_name = volume_group_id_info.ids.name
        array_connection_info = utils.get_array_connection_info_from_secrets(secrets)

        with get_agent(array_connection_info, array_type).get_mediator() as array_mediator:
            logger.debug(array_mediator)

            try:
                logger.debug("Deleting volume group {}".format(volume_group_name))
                array_mediator.delete_volume_group(volume_group_name)

            except array_errors.ObjectNotFoundError as ex:
                logger.debug("volume group was not found during deletion: {0}".format(ex))

        return volumegroup_pb2.DeleteVolumeGroupResponse()

    def _add_volumes_missing_from_group(self, array_mediator, volume_ids_in_request, volume_ids_in_volume_group,
                                        volume_group_id):
        for volume_id in volume_ids_in_request:
            if volume_id not in volume_ids_in_volume_group:
                array_mediator.add_volume_to_volume_group(volume_group_id, volume_id)

    def _remove_volumes_missing_from_request(self, array_mediator, volume_ids_in_request, volume_ids_in_volume_group):
        for volume_id in volume_ids_in_volume_group:
            if volume_id not in volume_ids_in_request:
                array_mediator.remove_volume_from_volume_group(volume_id)

    def _get_volume_group(self, array_mediator, volume_group_name):
        try:
            return array_mediator.get_volume_group(volume_group_name)
        except array_errors.ObjectNotFoundError:
            raise array_errors.ObjectNotFoundError(volume_group_name)

    def _get_volume_ids_from_request(self, volume_ids):
        volume_ids_in_request = []
        for volume_id in volume_ids:
            volume_id_info = utils.get_volume_id_info(volume_id)
            volume_ids_in_request.append(volume_id_info.ids.uid)
        return volume_ids_in_request

    def _get_volume_ids_from_volume_group(self, volumes):
        return [volume.id for volume in volumes]

    @csi_method(error_response_type=volumegroup_pb2.ModifyVolumeGroupMembershipResponse,
                lock_request_attribute="volume_group_id")
    def ModifyVolumeGroupMembership(self, request, context):
        secrets = request.secrets
        utils.validate_delete_volume_group_request(request)

        try:
            volume_group_id_info = utils.get_volume_group_id_info(request.volume_group_id)
        except ObjectIdError as ex:
            return handle_exception(ex, context, grpc.StatusCode.INVALID_ARGUMENT,
                                    volumegroup_pb2.ModifyVolumeGroupMembershipResponse)

        array_type = volume_group_id_info.array_type
        volume_group_name = volume_group_id_info.ids.name
        array_connection_info = utils.get_array_connection_info_from_secrets(secrets)

        with get_agent(array_connection_info, array_type).get_mediator() as array_mediator:
            logger.debug(array_mediator)

            volume_group = self._get_volume_group(array_mediator, volume_group_name)

            volume_ids_in_volume_group = self._get_volume_ids_from_volume_group(volume_group.volumes)
            volume_ids_in_request = self._get_volume_ids_from_request(request.volume_ids)

            self._add_volumes_missing_from_group(array_mediator, volume_ids_in_request, volume_ids_in_volume_group,
                                                 volume_group_name)
            self._remove_volumes_missing_from_request(array_mediator, volume_ids_in_request, volume_ids_in_volume_group)

            volume_group = self._get_volume_group(array_mediator, volume_group_name)

            response = utils.generate_csi_modify_volume_group_response(volume_group)
            return response

    def _get_volume_group_final_name(self, volume_parameters, name, array_mediator):
        return utils.get_object_final_name(volume_parameters, name, array_mediator,
                                           servers_settings.VOLUME_GROUP_TYPE_NAME)
