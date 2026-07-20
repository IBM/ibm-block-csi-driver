import grpc
from csi_general import replication_pb2 as pb2
from csi_general import replication_pb2_grpc as pb2_grpc
from google.protobuf.timestamp_pb2 import Timestamp
from datetime import datetime, timezone

import controllers.servers.settings as servers_settings
import controllers.array_action.settings as array_settings
from controllers.array_action import errors as array_errors
from controllers.array_action.storage_agent import get_agent
from controllers.common.csi_logger import get_stdout_logger
from controllers.servers import utils
from controllers.servers.csi.decorators import csi_method, csi_replication_method
from controllers.servers.csi.exception_handler import build_error_response

logger = get_stdout_logger()


class ReplicationControllerServicer(pb2_grpc.ControllerServicer):

    @csi_replication_method(error_response_type=pb2.EnableVolumeReplicationResponse)
    def EnableVolumeReplication(self, request, context):
        replication_type = utils.get_addons_replication_type(request)
        utils.validate_addons_request(request, replication_type)

        object_type, object_id_info = utils.get_replication_object_type_and_id_info(request)
        connection_info = utils.get_array_connection_info_from_secrets(request.secrets)

        with get_agent(connection_info, object_id_info.array_type).get_mediator() as mediator:
            object_type, object_id_info = utils.resolve_ramen_ear_volume_to_volume_group(
                object_type, object_id_info, replication_type, mediator)

            object_id = object_id_info.ids.internal_id

            error_message = self._validate_replication_object(object_type, replication_type)
            if error_message:
                return build_error_response(error_message, context, grpc.StatusCode.FAILED_PRECONDITION,
                                            pb2.EnableVolumeReplicationResponse)

            replication_request = utils.generate_addons_replication_request(request, replication_type, object_id)

            replication_object = self._get_replication_object(object_id_info, object_type, connection_info, mediator)
            replication = mediator.get_replication(replication_request)
            if replication:
                error_message = self._ensure_replication_idempotency(replication_request, replication)
                if error_message:
                    return build_error_response(error_message, context, grpc.StatusCode.ALREADY_EXISTS,
                                                pb2.EnableVolumeReplicationResponse)
                logger.info("idempotent case. replication already exists "
                            "for volume {} with system: {}".format(replication_object.name,
                                                                   replication_request.other_system_id))
                return pb2.EnableVolumeReplicationResponse()

            logger.info("creating replication for volume {} with system: {}"
                        .format(replication_object.name, replication_request.other_system_id))
            mediator.create_replication(replication_request)

        return pb2.EnableVolumeReplicationResponse()

    @csi_replication_method(error_response_type=pb2.DisableVolumeReplicationResponse)
    def DisableVolumeReplication(self, request, context):
        replication_type = utils.get_addons_replication_type(request)
        utils.validate_addons_request(request, replication_type)

        object_type, object_id_info = utils.get_replication_object_type_and_id_info(request)
        connection_info = utils.get_array_connection_info_from_secrets(request.secrets)

        with get_agent(connection_info, object_id_info.array_type).get_mediator() as mediator:
            object_type, object_id_info = utils.resolve_ramen_ear_volume_to_volume_group(
                object_type, object_id_info, replication_type, mediator)

            object_id = object_id_info.ids.internal_id

            error_message = self._validate_replication_object(object_type, replication_type)
            if error_message:
                return build_error_response(error_message, context, grpc.StatusCode.FAILED_PRECONDITION,
                                            pb2.EnableVolumeReplicationResponse)

            replication_request = utils.generate_addons_replication_request(request, replication_type, object_id)

            self._get_replication_object(object_id_info, object_type, connection_info, mediator)
            replication = mediator.get_replication(replication_request)
            if replication:
                logger.info("deleting replication {} with system {}".format(replication.name,
                                                                            replication_request.other_system_id))
                mediator.delete_replication(replication)
            else:
                logger.info("idempotent case. replication is already deleted with system {}"
                            .format(replication_request.other_system_id))

        return pb2.DisableVolumeReplicationResponse()

    @staticmethod
    def _ensure_volume_role_for_replication(mediator, replication, is_to_promote, force=False):
        if is_to_promote:
            if replication.is_primary:
                logger.info("idempotent case. volume is already primary")
            else:
                logger.info("promoting volume for replication {}".format(replication.name))
                mediator.promote_replication_volume(replication, force=force)
        else:
            if replication.is_primary or replication.is_primary is None:
                logger.info("demoting volume for replication {}".format(replication.name))
                mediator.demote_replication_volume(replication)
            else:
                logger.info("idempotent case. volume is already secondary")

    def _ensure_volume_role(self, request, context, is_to_promote, response_type):
        method_name = "PromoteVolume" if is_to_promote else "DemoteVolume"
        logger.info(method_name)
        replication_type = utils.get_addons_replication_type(request)
        utils.validate_addons_request(request, replication_type)

        object_type, object_id_info = utils.get_replication_object_type_and_id_info(request)

        connection_info = utils.get_array_connection_info_from_secrets(request.secrets)
        with get_agent(connection_info, object_id_info.array_type).get_mediator() as mediator:
            object_type, object_id_info = utils.resolve_ramen_ear_volume_to_volume_group(
                object_type, object_id_info, replication_type, mediator)

            object_id = object_id_info.ids.internal_id

            error_message = self._validate_replication_object(object_type, replication_type)
            if error_message:
                return build_error_response(error_message, context, grpc.StatusCode.FAILED_PRECONDITION,
                                            pb2.EnableVolumeReplicationResponse)

            replication_request = utils.generate_addons_replication_request(request, replication_type, object_id)

            self._get_replication_object(object_id_info, object_type, connection_info, mediator)
            replication = mediator.get_replication(replication_request)
            if not replication:
                message = "could not find replication for volume internal id: {} with " \
                          "volume internal id: {} of system: {}".format(replication_request.volume_internal_id,
                                                                        replication_request.other_volume_internal_id,
                                                                        replication_request.other_system_id)
                return build_error_response(message, context, grpc.StatusCode.FAILED_PRECONDITION, response_type)
            logger.info("found replication {} on system {}".format(replication.name, mediator.identifier))

            self._ensure_volume_role_for_replication(mediator, replication, is_to_promote, force=request.force)

        logger.info("finished {}".format(method_name))
        return response_type()

    @csi_replication_method(error_response_type=pb2.PromoteVolumeResponse)
    def PromoteVolume(self, request, context):
        return self._ensure_volume_role(request, context, is_to_promote=True, response_type=pb2.PromoteVolumeResponse)

    @csi_replication_method(error_response_type=pb2.DemoteVolumeResponse)
    def DemoteVolume(self, request, context):
        return self._ensure_volume_role(request, context, is_to_promote=False, response_type=pb2.DemoteVolumeResponse)

    @csi_replication_method(error_response_type=pb2.ResyncVolumeResponse)
    def ResyncVolume(self, request, context):
        replication_type = utils.get_addons_replication_type(request)
        utils.validate_addons_request(request, replication_type)

        object_type, object_id_info = utils.get_replication_object_type_and_id_info(request)

        connection_info = utils.get_array_connection_info_from_secrets(request.secrets)
        with get_agent(connection_info, object_id_info.array_type).get_mediator() as mediator:
            object_type, object_id_info = utils.resolve_ramen_ear_volume_to_volume_group(
                object_type, object_id_info, replication_type, mediator)

            object_id = object_id_info.ids.internal_id

            error_message = self._validate_replication_object(object_type, replication_type)
            if error_message:
                return build_error_response(error_message, context, grpc.StatusCode.FAILED_PRECONDITION,
                                            pb2.EnableVolumeReplicationResponse)

            replication_request = utils.generate_addons_replication_request(request, replication_type, object_id)

            self._get_replication_object(object_id_info, object_type, connection_info, mediator)
            replication = mediator.get_replication(replication_request)
            if not replication:
                message = "could not find replication for volume internal id: {} with " \
                          "volume internal id: {} of system: {}".format(replication_request.volume_internal_id,
                                                                        replication_request.other_volume_internal_id,
                                                                        replication_request.other_system_id)
                return build_error_response(message, context, grpc.StatusCode.FAILED_PRECONDITION,
                                            pb2.ResyncVolumeResponse)

        logger.info("is replication {} ready: {}".format(replication.name, replication.is_ready))
        return pb2.ResyncVolumeResponse(ready=replication.is_ready)

    @staticmethod
    def _ensure_replication_idempotency(replication_request, replication):
        if replication_request.replication_type == array_settings.REPLICATION_TYPE_MIRROR and \
                replication.copy_type != replication_request.copy_type:
            error_message = "replication already exists " \
                            "but has copy type of {} and not {}".format(replication.copy_type,
                                                                        replication_request.copy_type)
            return error_message
        return None

    @staticmethod
    def _validate_replication_object(object_type, replication_type):
        if object_type == servers_settings.VOLUME_TYPE_NAME and \
                replication_type == array_settings.REPLICATION_TYPE_EAR:
            error_message = "EAR replication is supported only on volume group level"
            return error_message
        return None

    @staticmethod
    def _get_replication_object(object_id_info, object_type, array_connection_info, mediator):
        object_uid = object_id_info.ids.uid if object_type == servers_settings.VOLUME_TYPE_NAME else \
            object_id_info.ids.internal_id
        replication_object = mediator.get_object_by_id(object_uid, object_type)

        if not replication_object:
            raise array_errors.ObjectNotFoundError(object_uid)
        # TODO function name misleading - checks partition_name attribute, not necessarily volume
        mediator.verify_volume_partition(replication_object, array_connection_info.partition_name)
        return replication_object

    @csi_replication_method(error_response_type=pb2.GetVolumeReplicationInfoResponse)
    def GetVolumeReplicationInfo(self, request, context):
        logger.info("GetVolumeReplicationInfo: called with replication_source='{}'".format(request.replication_source))

        object_type, object_id_info = utils.get_replication_object_type_and_id_info(request)

        utils.validate_secrets(request.secrets)
        response = pb2.GetVolumeReplicationInfoResponse()

        connection_info = utils.get_array_connection_info_from_secrets(request.secrets)

        with get_agent(connection_info, object_id_info.array_type).get_mediator() as mediator:
            if object_type != servers_settings.VOLUME_GROUP_TYPE_NAME:
                try:
                    object_type, object_id_info = utils.resolve_ramen_ear_volume_to_volume_group(
                        object_type, object_id_info, array_settings.REPLICATION_TYPE_EAR, mediator)
                except array_errors.ObjectNotFoundError:
                    logger.warning(
                        "GetVolumeReplicationInfo: volume not part of any VolumeGroup, "
                        "returning current time as last_sync_time")
                    ts_seconds = int(datetime.now(timezone.utc).timestamp())
                    response.last_sync_time.CopyFrom(Timestamp(seconds=ts_seconds, nanos=0))
                    return response

            object_id = object_id_info.ids.internal_id
            replication_info = mediator.get_last_async_snapshot_info(object_id)

        if replication_info.last_sync_time is not None:
            ts_seconds = int(replication_info.last_sync_time.timestamp())
            response.last_sync_time.CopyFrom(Timestamp(seconds=ts_seconds, nanos=0))
        else:
            response.last_sync_time.CopyFrom(Timestamp(seconds=0, nanos=0))
            logger.warning("last_sync_time not available at storage, setting to default timestamp (0).")

        response.status = replication_info.replication_status
        response.status_message = replication_info.status_message or ''

        logger.info(
            "GetVolumeReplicationInfo: returning response last_sync_time.seconds={}, "
            "status={}, status_message='{}'".format(
                response.last_sync_time.seconds,
                response.status,
                response.status_message
            )
        )
        return response

    @csi_replication_method(error_response_type=pb2.GetReplicationDestinationInfoResponse)
    def GetReplicationDestinationInfo(self, request, context):
        object_type, object_id_info = utils.get_replication_object_type_and_id_info(
            request, require_replication_source=True)
        if object_type != servers_settings.VOLUME_TYPE_NAME:
            logger.warning("object type '{}' is not volume, not supported".format(object_type))
            return pb2.GetReplicationDestinationInfoResponse()

        object_id = object_id_info.ids.internal_id
        object_uid = object_id_info.ids.uid
        utils.validate_secrets(request.secrets)

        connection_info = utils.get_array_connection_info_from_secrets(request.secrets)
        dr_connection_info = utils.get_dr_array_connection_info_from_secrets(request.secrets)

        with get_agent(connection_info, object_id_info.array_type).get_mediator() as mediator:
            if dr_connection_info is not None:
                with get_agent(dr_connection_info, object_id_info.array_type).get_mediator() as dr_mediator:
                    destination_volume_id = mediator.get_replication_destination_info(
                        object_id, object_type, object_uid=object_uid, dr_mediator=dr_mediator)
            else:
                destination_volume_id = mediator.get_replication_destination_info(
                    object_id, object_type, object_uid=object_uid)

        response = pb2.GetReplicationDestinationInfoResponse()
        if destination_volume_id is not None:
            response.replication_destination.volume.volume_id = destination_volume_id
            logger.info("destination volume id '{}' for source uid '{}'".format(
                destination_volume_id, object_uid))
        else:
            logger.warning("destination volume not yet available for source uid '{}'".format(object_uid))

        return response
