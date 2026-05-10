import grpc
from csi_general import replication_pb2 as pb2
from csi_general import replication_pb2_grpc as pb2_grpc
from google.protobuf.timestamp_pb2 import Timestamp
from google.protobuf.duration_pb2 import Duration

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

    @csi_method(error_response_type=pb2.EnableVolumeReplicationResponse, lock_request_attribute="volume_id")
    def EnableVolumeReplication(self, request, context):
        replication_type = utils.get_addons_replication_type(request)
        utils.validate_addons_request(request, replication_type)

        object_type, object_id_info = utils.get_replication_object_type_and_id_info(request)
        object_id = object_id_info.ids.internal_id

        error_message = self._validate_replication_object(object_type, replication_type)
        if error_message:
            return build_error_response(error_message, context, grpc.StatusCode.FAILED_PRECONDITION,
                                        pb2.EnableVolumeReplicationResponse)

        replication_request = utils.generate_addons_replication_request(request, replication_type, object_id)

        connection_info = utils.get_array_connection_info_from_secrets(request.secrets)
        with get_agent(connection_info, object_id_info.array_type).get_mediator() as mediator:
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

    @csi_method(error_response_type=pb2.DisableVolumeReplicationResponse, lock_request_attribute="volume_id")
    def DisableVolumeReplication(self, request, context):
        replication_type = utils.get_addons_replication_type(request)
        utils.validate_addons_request(request, replication_type)

        object_type, object_id_info = utils.get_replication_object_type_and_id_info(request)
        object_id = object_id_info.ids.internal_id

        error_message = self._validate_replication_object(object_type, replication_type)
        if error_message:
            return build_error_response(error_message, context, grpc.StatusCode.FAILED_PRECONDITION,
                                        pb2.EnableVolumeReplicationResponse)

        replication_request = utils.generate_addons_replication_request(request, replication_type, object_id)

        connection_info = utils.get_array_connection_info_from_secrets(request.secrets)
        with get_agent(connection_info, object_id_info.array_type).get_mediator() as mediator:
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
    def _ensure_volume_role_for_replication(mediator, replication, is_to_promote):
        if is_to_promote:
            if replication.is_primary:
                logger.info("idempotent case. volume is already primary")
            else:
                logger.info("promoting volume for replication {}".format(replication.name))
                mediator.promote_replication_volume(replication)
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
        object_id = object_id_info.ids.internal_id

        error_message = self._validate_replication_object(object_type, replication_type)
        if error_message:
            return build_error_response(error_message, context, grpc.StatusCode.FAILED_PRECONDITION,
                                        pb2.EnableVolumeReplicationResponse)

        replication_request = utils.generate_addons_replication_request(request, replication_type, object_id)

        connection_info = utils.get_array_connection_info_from_secrets(request.secrets)
        with get_agent(connection_info, object_id_info.array_type).get_mediator() as mediator:
            self._get_replication_object(object_id_info, object_type, connection_info, mediator)
            replication = mediator.get_replication(replication_request)
            if not replication:
                message = "could not find replication for volume internal id: {} with " \
                          "volume internal id: {} of system: {}".format(replication_request.volume_internal_id,
                                                                        replication_request.other_volume_internal_id,
                                                                        replication_request.other_system_id)
                return build_error_response(message, context, grpc.StatusCode.FAILED_PRECONDITION, response_type)
            logger.info("found replication {} on system {}".format(replication.name, mediator.identifier))

            self._ensure_volume_role_for_replication(mediator, replication, is_to_promote)

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
        replication_type = utils.get_addons_replication_type(request)
        utils.validate_addons_request(request, replication_type)

        object_type, object_id_info = utils.get_replication_object_type_and_id_info(request)
        object_id = object_id_info.ids.internal_id

        error_message = self._validate_replication_object(object_type, replication_type)
        if error_message:
            return build_error_response(error_message, context, grpc.StatusCode.FAILED_PRECONDITION,
                                        pb2.EnableVolumeReplicationResponse)

        replication_request = utils.generate_addons_replication_request(request, replication_type, object_id)

        connection_info = utils.get_array_connection_info_from_secrets(request.secrets)
        with get_agent(connection_info, object_id_info.array_type).get_mediator() as mediator:
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
        object_id = object_id_info.ids.internal_id

        utils.validate_secrets(request.secrets)

        if object_type != servers_settings.VOLUME_GROUP_TYPE_NAME:
            logger.warning(
                "GetVolumeReplicationInfo is only supported for EAR (VolumeGroup level). "
                "Returning empty response for object_type='{}'".format(object_type)
            )
            return pb2.GetVolumeReplicationInfoResponse()

        connection_info = utils.get_array_connection_info_from_secrets(request.secrets)

        with get_agent(connection_info, object_id_info.array_type).get_mediator() as mediator:
            replication_info = mediator.get_replication_info(object_id)

        response = pb2.GetVolumeReplicationInfoResponse()

        if replication_info.last_sync_time is not None:
            ts_seconds = int(replication_info.last_sync_time.timestamp())
            response.last_sync_time.CopyFrom(Timestamp(seconds=ts_seconds, nanos=0))
        else:
            response.last_sync_time.CopyFrom(Timestamp(seconds=0, nanos=0))
            logger.warning(
                "last_sync_time not available (fixed_recovery_point was blank). "
                "Setting to default timestamp (0)."
            )

        bytes_val = replication_info.last_sync_bytes if replication_info.last_sync_bytes is not None else -1
        response.last_sync_bytes = bytes_val

        logger.info("Returning response last_sync_time.seconds={}, last_sync_bytes={}"
                    .format(response.last_sync_time.seconds, response.last_sync_bytes))
        return response

    @csi_replication_method(error_response_type=pb2.GetReplicationDestinationInfoResponse)
    def GetReplicationDestinationInfo(self, request, context):
        logger.info("GetReplicationDestinationInfo: called with replication_source='{}'".format(
            request.replication_source))

        object_type, object_id_info = utils.get_replication_object_type_and_id_info(request)
        object_id = object_id_info.ids.internal_id

        utils.validate_secrets(request.secrets)

        connection_info = utils.get_array_connection_info_from_secrets(request.secrets)

        with get_agent(connection_info, object_id_info.array_type).get_mediator() as mediator:
            destination_info = mediator.get_replication_destination_info(object_id, object_type)

        response = pb2.GetReplicationDestinationInfoResponse()

        if destination_info.destination_volume_group_id is not None:
            response.replication_destination.volumegroup.volume_group_id = (
                destination_info.destination_volume_group_id
            )
            if destination_info.destination_volume_ids:
                for source_vol_id, dest_vol_id in destination_info.destination_volume_ids.items():
                    response.replication_destination.volumegroup.volume_ids[source_vol_id] = dest_vol_id
            logger.info(
                "Returning destination_volume_group_id='{}' "
                "volume_ids='{}'".format(
                    destination_info.destination_volume_group_id,
                    destination_info.destination_volume_ids
                )
            )
        elif destination_info.destination_volume_id is not None:
            response.replication_destination.volume.volume_id = destination_info.destination_volume_id
            logger.info(
                "Returning destination_volume_id='{}'".format(destination_info.destination_volume_id)
            )
        else:
            logger.warning(
                "No destination info available for object_id='{}' "
                "object_type='{}'".format(object_id, object_type)
            )

        return response
