import grpc

from controller.array_action import errors as array_errors
from controller.array_action.config import REPLICATION_DEFAULT_COPY_TYPE
from controller.array_action.storage_agent import get_agent
from controller.common.csi_logger import get_stdout_logger
from controller.common.utils import set_current_thread_name
from controller.controller_server import config, utils
from controller.controller_server.exception_handler import handle_common_exceptions, build_error_response
from controller.csi_general import replication_pb2 as pb2
from controller.csi_general import replication_pb2_grpc as pb2_grpc

logger = get_stdout_logger()


class ReplicationControllerServicer(pb2_grpc.ControllerServicer):
    @handle_common_exceptions(pb2.EnableVolumeReplicationResponse)
    def EnableVolumeReplication(self, request, context):
        set_current_thread_name(request.volume_id)
        logger.info("EnableVolumeReplication")
        utils.validate_addons_request(request)

        volume_id_info = utils.get_volume_id_info(request.volume_id)
        volume_id = volume_id_info.object_id
        volume_internal_id = volume_id_info.internal_id

        other_volume_id_info = utils.get_volume_id_info(request.target_volume_id)
        other_volume_internal_id = other_volume_id_info.internal_id

        other_system_id = request.parameters.get(config.PARAMETERS_SYSTEM_ID)
        copy_type = request.parameters.get(config.PARAMETERS_COPY_TYPE, REPLICATION_DEFAULT_COPY_TYPE)

        connection_info = utils.get_array_connection_info_from_secrets(request.secrets)
        with get_agent(connection_info, volume_id_info.array_type).get_mediator() as mediator:
            volume = mediator.get_object_by_id(volume_id, config.VOLUME_TYPE_NAME)
            if not volume:
                raise array_errors.ObjectNotFoundError(volume_id)
            replication = mediator.get_replication(volume_internal_id, other_volume_internal_id, other_system_id)
            if replication:
                if replication.copy_type != copy_type:
                    message = "replication already exists " \
                              "but has copy type of {} and not {}".format(replication.copy_type, copy_type)
                    return build_error_response(message, context, grpc.StatusCode.ALREADY_EXISTS,
                                                pb2.EnableVolumeReplicationResponse)
                logger.info("idempotent case. replication already exists "
                            "for volume {} with system: {}".format(volume.name,
                                                                   other_system_id))
                return pb2.EnableVolumeReplicationResponse()

            logger.info("creating replication for volume {} with system: {}".format(volume.name,
                                                                                    other_system_id))
            mediator.create_replication(volume_internal_id, other_volume_internal_id, other_system_id, copy_type)

        return pb2.EnableVolumeReplicationResponse()

    @handle_common_exceptions(pb2.DisableVolumeReplicationResponse)
    def DisableVolumeReplication(self, request, context):
        set_current_thread_name(request.volume_id)
        logger.info("DisableVolumeReplication")
        utils.validate_addons_request(request)

        volume_id_info = utils.get_volume_id_info(request.volume_id)
        volume_internal_id = volume_id_info.internal_id

        other_volume_id_info = utils.get_volume_id_info(request.target_volume_id)
        other_volume_internal_id = other_volume_id_info.internal_id

        other_system_id = request.parameters.get(config.PARAMETERS_SYSTEM_ID)

        connection_info = utils.get_array_connection_info_from_secrets(request.secrets)
        with get_agent(connection_info, volume_id_info.array_type).get_mediator() as mediator:
            replication = mediator.get_replication(volume_internal_id, other_volume_internal_id, other_system_id)
            if replication:
                logger.info("deleting replication {} with system {}".format(replication.name,
                                                                            other_system_id))
                mediator.delete_replication(replication.name)
            else:
                logger.info("idempotent case. replication is already deleted with system {}".format(other_system_id))

        return pb2.DisableVolumeReplicationResponse()

    @staticmethod
    def _ensure_volume_role_for_replication(mediator, replication, is_to_promote):
        if is_to_promote:
            if replication.is_primary:
                logger.info("idempotent case. volume is already primary")
            else:
                logger.info("promoting volume for replication {}".format(replication.name))
                mediator.promote_replication_volume(replication.name)
        else:
            if replication.is_primary or replication.is_primary is None:
                logger.info("demoting volume for replication {}".format(replication.name))
                mediator.demote_replication_volume(replication.name)
            else:
                logger.info("idempotent case. volume is already secondary")

    def _ensure_volume_role(self, request, context, is_to_promote, response_type):
        set_current_thread_name(request.volume_id)
        method_name = "PromoteVolume" if is_to_promote else "DemoteVolume"
        logger.info(method_name)
        utils.validate_addons_request(request)

        volume_id_info = utils.get_volume_id_info(request.volume_id)
        volume_internal_id = volume_id_info.internal_id

        other_volume_id_info = utils.get_volume_id_info(request.target_volume_id)
        other_volume_internal_id = other_volume_id_info.internal_id

        other_system_id = request.parameters.get(config.PARAMETERS_SYSTEM_ID)

        connection_info = utils.get_array_connection_info_from_secrets(request.secrets)
        with get_agent(connection_info, volume_id_info.array_type).get_mediator() as mediator:
            replication = mediator.get_replication(volume_internal_id, other_volume_internal_id, other_system_id)
            if not replication:
                message = "could not find replication for volume internal id: {} " \
                          "with volume internal id: {} of system: {}".format(volume_internal_id,
                                                                             other_volume_internal_id,
                                                                             other_system_id)
                return build_error_response(message, context, grpc.StatusCode.FAILED_PRECONDITION, response_type)
            logger.info("found replication {} on system {}".format(replication.name, mediator.identifier))

            self._ensure_volume_role_for_replication(mediator, replication, is_to_promote)

        logger.info("finished {}".format(method_name))
        return response_type()

    @handle_common_exceptions(pb2.PromoteVolumeResponse)
    def PromoteVolume(self, request, context):
        return self._ensure_volume_role(request, context, is_to_promote=True, response_type=pb2.PromoteVolumeResponse)

    @handle_common_exceptions(pb2.PromoteVolumeResponse)
    def DemoteVolume(self, request, context):
        return self._ensure_volume_role(request, context, is_to_promote=False, response_type=pb2.DemoteVolumeResponse)

    @handle_common_exceptions(pb2.ResyncVolumeResponse)
    def ResyncVolume(self, request, context):
        set_current_thread_name(request.volume_id)
        logger.info("ResyncVolume")
        utils.validate_addons_request(request)

        volume_id_info = utils.get_volume_id_info(request.volume_id)
        volume_internal_id = volume_id_info.internal_id

        other_volume_id_info = utils.get_volume_id_info(request.target_volume_id)
        other_volume_internal_id = other_volume_id_info.internal_id

        other_system_id = request.parameters.get(config.PARAMETERS_SYSTEM_ID)

        connection_info = utils.get_array_connection_info_from_secrets(request.secrets)
        with get_agent(connection_info, volume_id_info.array_type).get_mediator() as mediator:
            replication = mediator.get_replication(volume_internal_id, other_volume_internal_id, other_system_id)
            if not replication:
                message = "could not find replication for volume internal id: {} " \
                          "with volume internal id: {} of system: {}".format(volume_internal_id,
                                                                             other_volume_internal_id,
                                                                             other_system_id)
                return build_error_response(message, context, grpc.StatusCode.FAILED_PRECONDITION,
                                            pb2.ResyncVolumeResponse)

        logger.info("is replication {} ready: {}".format(replication.name, replication.is_ready))
        return pb2.ResyncVolumeResponse(ready=replication.is_ready)
