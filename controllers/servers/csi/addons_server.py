import grpc
from csi_general import replication_pb2 as pb2
from csi_general import replication_pb2_grpc as pb2_grpc

import controllers.servers.settings as servers_settings
import controllers.array_action.settings as array_settings
from controllers.array_action import errors as array_errors
from controllers.array_action.settings import REPLICATION_DEFAULT_COPY_TYPE
from controllers.array_action.storage_agent import get_agent
from controllers.common.csi_logger import get_stdout_logger
from controllers.servers import utils
from controllers.servers.csi.decorators import csi_method
from controllers.servers.csi.exception_handler import build_error_response

logger = get_stdout_logger()


class ReplicationControllerServicer(pb2_grpc.ControllerServicer):

    @csi_method(error_response_type=pb2.EnableVolumeReplicationResponse, lock_request_attribute="volume_id")
    def EnableVolumeReplication(self, request, context):
        replication_type = self._get_replication_type(request)
        utils.validate_addons_request(request, replication_type)

        volume_id_info = utils.get_volume_id_info(request.volume_id)
        volume_id = volume_id_info.ids.uid
        volume_internal_id = volume_id_info.ids.internal_id
        copy_type = request.parameters.get(servers_settings.PARAMETERS_COPY_TYPE, REPLICATION_DEFAULT_COPY_TYPE)

        connection_info = utils.get_array_connection_info_from_secrets(request.secrets)
        with get_agent(connection_info, volume_id_info.array_type).get_mediator() as mediator:
            volume = mediator.get_object_by_id(volume_id, servers_settings.VOLUME_TYPE_NAME)
            if not volume:
                raise array_errors.ObjectNotFoundError(volume_id)
            replication = self._get_replication(mediator, request, volume_internal_id, replication_type)
            if replication:
                if replication_type == array_settings.REPLICATION_TYPE_MIRROR and replication.copy_type != copy_type:
                    message = "replication already exists " \
                              "but has copy type of {} and not {}".format(replication.copy_type, copy_type)
                    return build_error_response(message, context, grpc.StatusCode.ALREADY_EXISTS,
                                                pb2.EnableVolumeReplicationResponse)
                elif replication_type == array_settings.REPLICATION_TYPE_EAR and \
                    replication.volume_group_id != volume.group_id:
                    message = "replication already exists " \
                              "but volume {} belongs to another group {}".format(volume.name, volume.volume_group_name)
                    return build_error_response(message, context, grpc.StatusCode.ALREADY_EXISTS,
                                                pb2.EnableVolumeReplicationResponse)

                logger.info("idempotent case. replication already exists for volume {}".format(volume.name))
                return pb2.EnableVolumeReplicationResponse()

            self._create_replication(mediator, request, volume_internal_id, volume, replication_type)

        return pb2.EnableVolumeReplicationResponse()

    @csi_method(error_response_type=pb2.DisableVolumeReplicationResponse, lock_request_attribute="volume_id")
    def DisableVolumeReplication(self, request, context):
        replication_type = self._get_replication_type(request)
        utils.validate_addons_request(request, replication_type)

        volume_id_info = utils.get_volume_id_info(request.volume_id)
        volume_internal_id = volume_id_info.ids.internal_id

        connection_info = utils.get_array_connection_info_from_secrets(request.secrets)
        with get_agent(connection_info, volume_id_info.array_type).get_mediator() as mediator:
            replication = self._get_replication(mediator, request, volume_internal_id, replication_type)
            if replication:
                logger.info("deleting replication {}".format(replication.name))
                self._delete_replication(mediator, volume_internal_id, replication, replication_type)
            else:
                logger.info("idempotent case. replication is already deleted for volume {}".format(volume_internal_id))

        return pb2.DisableVolumeReplicationResponse()

    def _ensure_volume_role_for_replication(self, mediator, replication, is_to_promote, replication_type):
        if replication_type == array_settings.REPLICATION_TYPE_MIRROR:
            return self._ensure_volume_role_for_mirror_replication(mediator, replication, is_to_promote)
        elif replication_type == array_settings.REPLICATION_TYPE_EAR:
            return self._ensure_volume_role_for_ear_replication(mediator, replication, is_to_promote)

    @staticmethod
    def _ensure_volume_role_for_mirror_replication(mediator, replication, is_to_promote):
        if is_to_promote:
            if replication.is_primary:
                logger.info("idempotent case. volume is already primary")
            else:
                logger.info("promoting volume for replication {}".format(replication.name))
                mediator.promote_mirror_replication_volume(replication.name)
        else:
            if replication.is_primary or replication.is_primary is None:
                logger.info("demoting volume for replication {}".format(replication.name))
                mediator.demote_mirror_replication_volume(replication.name)
            else:
                logger.info("idempotent case. volume is already secondary")

    @staticmethod
    def _ensure_volume_role_for_ear_replication(mediator, replication, is_to_promote):
        if is_to_promote:
            if replication.is_primary:
                logger.info("idempotent case. volume is already primary")
            else:
                logger.info("promoting volume for replication {}".format(replication.name))
                mediator.promote_ear_replication_volume(replication.volume_group_id)
        else:
            logger.info("demoting volume for replication {}".format(replication.name))
            mediator.demote_ear_replication_volume(replication.volume_group_id)

    def _ensure_volume_role(self, request, context, is_to_promote, response_type):
        method_name = "PromoteVolume" if is_to_promote else "DemoteVolume"
        logger.info(method_name)
        replication_type = self._get_replication_type(request)
        if method_name == "DemoteVolume" and replication_type == array_settings.REPLICATION_TYPE_EAR:
            logger.info("Demote volume is not supported in the EAR replication")
            return response_type()

        utils.validate_addons_request(request, replication_type)

        volume_id_info = utils.get_volume_id_info(request.volume_id)
        volume_internal_id = volume_id_info.ids.internal_id

        connection_info = utils.get_array_connection_info_from_secrets(request.secrets)
        with get_agent(connection_info, volume_id_info.array_type).get_mediator() as mediator:
            replication = self._get_replication(mediator, request, volume_internal_id, replication_type)
            if not replication:
                message = "could not find replication for volume internal id: {} ".format(volume_internal_id)
                return build_error_response(message, context, grpc.StatusCode.FAILED_PRECONDITION, response_type)
            logger.info("found replication {} on system {}".format(replication.name, mediator.identifier))

            self._ensure_volume_role_for_replication(mediator, replication, is_to_promote, replication_type)

        logger.info("finished {}".format(method_name))
        return response_type()

    @csi_method(error_response_type=pb2.PromoteVolumeResponse, lock_request_attribute="volume_id")
    def PromoteVolume(self, request, context):
        return self._ensure_volume_role(request, context, is_to_promote=True, response_type=pb2.PromoteVolumeResponse)

    @csi_method(error_response_type=pb2.DemoteVolumeResponse, lock_request_attribute="volume_id")
    def DemoteVolume(self, request, context):
        return self._ensure_volume_role(request, context, is_to_promote=False, response_type=pb2.DemoteVolumeResponse)

    @csi_method(error_response_type=pb2.ResyncVolumeResponse, lock_request_attribute="volume_id")
    def ResyncVolume(self, request, context):
        replication_type = self._get_replication_type(request)
        if replication_type == array_settings.REPLICATION_TYPE_EAR:
            logger.info("Resync volume is not supported in the EAR replication")
            return pb2.ResyncVolumeResponse(ready=True)

        utils.validate_addons_request(request, replication_type)

        volume_id_info = utils.get_volume_id_info(request.volume_id)
        volume_internal_id = volume_id_info.ids.internal_id

        other_volume_id_info = utils.get_volume_id_info(request.replication_id)
        other_volume_internal_id = other_volume_id_info.ids.internal_id

        other_system_id = request.parameters.get(servers_settings.PARAMETERS_SYSTEM_ID)

        connection_info = utils.get_array_connection_info_from_secrets(request.secrets)
        with get_agent(connection_info, volume_id_info.array_type).get_mediator() as mediator:
            replication = mediator.get_mirror_replication(volume_internal_id, other_volume_internal_id, other_system_id)
            if not replication:
                message = "could not find replication for volume internal id: {} " \
                          "with volume internal id: {} of system: {}".format(volume_internal_id,
                                                                             other_volume_internal_id,
                                                                             other_system_id)
                return build_error_response(message, context, grpc.StatusCode.FAILED_PRECONDITION,
                                            pb2.ResyncVolumeResponse)

        logger.info("is replication {} ready: {}".format(replication.name, replication.is_ready))
        return pb2.ResyncVolumeResponse(ready=replication.is_ready)

    @staticmethod
    def _get_replication_type(request):
        if servers_settings.PARAMETERS_REPLICATION_POLICY in request.parameters:
            replication_type = array_settings.REPLICATION_TYPE_EAR
        else:
            replication_type = array_settings.REPLICATION_TYPE_MIRROR

        logger.info("replication type is {}".format(replication_type))
        return replication_type

    @staticmethod
    def _get_mirror_replication(mediator, request, volume_internal_id):
        other_volume_id_info = utils.get_volume_id_info(request.replication_id)
        other_volume_internal_id = other_volume_id_info.ids.internal_id
        other_system_id = request.parameters.get(servers_settings.PARAMETERS_SYSTEM_ID)

        return mediator.get_mirror_replication(volume_internal_id, other_volume_internal_id, other_system_id)

    @staticmethod
    def _get_ear_replication(mediator, volume_internal_id):
        return mediator.get_ear_replication(volume_internal_id)

    def _get_replication(self, mediator, request, volume_internal_id, replication_type):
        if replication_type == array_settings.REPLICATION_TYPE_MIRROR:
            return self._get_mirror_replication(mediator, request, volume_internal_id)
        elif replication_type == array_settings.REPLICATION_TYPE_EAR:
            return self._get_ear_replication(mediator, volume_internal_id)

        return None

    @staticmethod
    def _create_replication(mediator, request, volume_internal_id, volume, replication_type):
        if replication_type == array_settings.REPLICATION_TYPE_MIRROR:
            other_volume_id_info = utils.get_volume_id_info(request.replication_id)
            other_volume_internal_id = other_volume_id_info.ids.internal_id
            other_system_id = request.parameters.get(servers_settings.PARAMETERS_SYSTEM_ID)
            copy_type = request.parameters.get(servers_settings.PARAMETERS_COPY_TYPE, REPLICATION_DEFAULT_COPY_TYPE)

            logger.info("creating replication for volume {} with system: {}".format(volume.name, other_system_id))
            mediator.create_mirror_replication(volume_internal_id, other_volume_internal_id, other_system_id, copy_type)

        else:
            replication_policy = request.parameters.get(servers_settings.PARAMETERS_REPLICATION_POLICY)

            logger.info("creating replication for volume {} with policy: {}".format(volume.name, replication_policy))
            mediator.create_ear_replication(volume_internal_id, replication_policy)

    @staticmethod
    def _delete_replication(mediator, volume_internal_id, replication, replication_type):
        if replication_type == array_settings.REPLICATION_TYPE_MIRROR:
            mediator.delete_mirror_replication(replication.name)
        else:
            mediator.delete_ear_replication(volume_internal_id)
