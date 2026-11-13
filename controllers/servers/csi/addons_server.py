import time

import grpc
from csi_general import replication_pb2 as pb2
from csi_general import replication_pb2_grpc as pb2_grpc
from google.protobuf.timestamp_pb2 import Timestamp

import controllers.servers.settings as servers_settings
import controllers.array_action.settings as array_settings
from controllers.array_action import errors as array_errors
from controllers.array_action.storage_agent import get_agent
from controllers.common.csi_logger import get_stdout_logger
from controllers.servers import utils
from controllers.servers.csi import ramen_volume_group_coordinator
from controllers.servers.csi.decorators import csi_method, csi_replication_method
from controllers.servers.csi.exception_handler import build_error_response
from controllers.servers.csi.ramen_volume_group_coordinator import VolumeGroupOperationNotCompletedError

logger = get_stdout_logger()


class _RamenReplicationNotFoundError(Exception):
    pass


def _get_rpc_time_remaining(context):
    time_remaining = context.time_remaining() if context else None
    if isinstance(time_remaining, (int, float)):
        return time_remaining
    return servers_settings.RAMEN_VG_REQUEST_DEFAULT_TIMEOUT_SECONDS


def _get_ramen_request_deadline(context):
    # time until which a Ramen request may wait for its volume group, leaving time to send the response
    time_remaining = _get_rpc_time_remaining(context)
    return time.monotonic() + max(time_remaining - servers_settings.RAMEN_VG_REQUEST_DEADLINE_MARGIN_SECONDS, 0)


def _get_ramen_promote_retry_deadline(context):
    # time until which the owner may retry a promote, so that its last attempt still ends before the RPC timeout
    time_remaining = _get_rpc_time_remaining(context)
    retry_seconds = min(servers_settings.RAMEN_VG_PROMOTE_RETRY_MAX_SECONDS,
                        time_remaining - servers_settings.RAMEN_VG_PROMOTE_RETRY_DEADLINE_MARGIN_SECONDS)
    return time.monotonic() + max(retry_seconds, 0)


def _get_replication_not_found_message(replication_request):
    return "could not find replication for volume internal id: {} with " \
           "volume internal id: {} of system: {}".format(replication_request.volume_internal_id,
                                                         replication_request.other_volume_internal_id,
                                                         replication_request.other_system_id)


class ReplicationControllerServicer(pb2_grpc.ControllerServicer):

    @csi_replication_method(error_response_type=pb2.EnableVolumeReplicationResponse)
    def EnableVolumeReplication(self, request, context):
        request_deadline = _get_ramen_request_deadline(context)
        replication_type = utils.get_addons_replication_type(request)
        utils.validate_addons_request(request, replication_type)

        object_type, object_id_info = utils.get_replication_object_type_and_id_info(request)
        is_ramen_ear = utils.is_ramen_ear_request(object_type, replication_type)
        connection_info = utils.get_array_connection_info_from_secrets(request.secrets)
        agent = get_agent(connection_info, object_id_info.array_type)

        with agent.get_mediator() as mediator:
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

            if not is_ramen_ear:
                logger.info("creating replication for volume {} with system: {}"
                            .format(replication_object.name, replication_request.other_system_id))
                mediator.create_replication(replication_request)
                return pb2.EnableVolumeReplicationResponse()

            volume_group_key = (mediator.identifier, object_id)
            logger.info("Ramen EAR: replication of volume group {} is missing, it must be created once".format(
                volume_group_key))

        return self._create_ramen_replication(agent, volume_group_key, replication_object.name, replication_request,
                                              request_deadline, context)

    @staticmethod
    def _create_ramen_replication(agent, volume_group_key, volume_group_name, replication_request, request_deadline,
                                  context):
        # creating the replication removes and then assigns the policy of the volume group, so only one request
        # of the volume group may do it, after reading again that the replication is still missing
        def create_replication_once():
            with agent.get_mediator() as mediator:
                if mediator.get_replication(replication_request):
                    logger.info("idempotent case. replication of volume group {} was created by another "
                                "request".format(volume_group_name))
                    return
                logger.info("creating replication for volume group {} with system: {}"
                            .format(volume_group_name, replication_request.other_system_id))
                mediator.create_replication(replication_request)
                ramen_volume_group_coordinator.set_demote_checkpoint_achieved(volume_group_key, False)

        try:
            ramen_volume_group_coordinator.run_as_volume_group_owner(
                volume_group_key, "EnableVolumeReplication", request_deadline, create_replication_once)
        except VolumeGroupOperationNotCompletedError as ex:
            return build_error_response(str(ex), context, grpc.StatusCode.UNAVAILABLE,
                                        pb2.EnableVolumeReplicationResponse)
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
            if force:
                logger.info("Ramen: force-promote requested for replication {}".format(replication.name))
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
        request_deadline = _get_ramen_request_deadline(context)
        replication_type = utils.get_addons_replication_type(request)
        utils.validate_addons_request(request, replication_type)

        object_type, object_id_info = utils.get_replication_object_type_and_id_info(request)
        is_ramen_ear = utils.is_ramen_ear_request(object_type, replication_type)
        requested_volume_uid = object_id_info.ids.uid

        connection_info = utils.get_array_connection_info_from_secrets(request.secrets)
        agent = get_agent(connection_info, object_id_info.array_type)
        with agent.get_mediator() as mediator:
            object_type, object_id_info = utils.resolve_ramen_ear_volume_to_volume_group(
                object_type, object_id_info, replication_type, mediator)

            object_id = object_id_info.ids.internal_id

            error_message = self._validate_replication_object(object_type, replication_type)
            if error_message:
                return build_error_response(error_message, context, grpc.StatusCode.FAILED_PRECONDITION,
                                            pb2.EnableVolumeReplicationResponse)

            replication_request = utils.generate_addons_replication_request(request, replication_type, object_id)

            self._get_replication_object(object_id_info, object_type, connection_info, mediator)
            if is_ramen_ear:
                volume_group_key = (mediator.identifier, object_id)
                logger.info("Ramen EAR {}: volume {} belongs to volume group {}, releasing the storage "
                            "connection before acting on the volume group".format(
                                method_name, requested_volume_uid, volume_group_key))
            else:
                replication = mediator.get_replication(replication_request)
                if not replication:
                    message = _get_replication_not_found_message(replication_request)
                    return build_error_response(message, context, grpc.StatusCode.FAILED_PRECONDITION,
                                                response_type)
                logger.info("found replication {} on system {}".format(replication.name, mediator.identifier))

                try:
                    self._ensure_volume_role_for_replication(mediator, replication, is_to_promote,
                                                             force=request.force)
                except array_errors.OperationNotReadyError as e:
                    # Checkpoint not yet achieved - return UNAVAILABLE to trigger retry
                    logger.info("Operation not ready, will be retried: {}".format(str(e)))
                    return build_error_response(str(e), context, grpc.StatusCode.UNAVAILABLE, response_type)

                logger.info("finished {}".format(method_name))
                return response_type()

        # Ramen EAR: the storage connection is released here, so a request waiting for its volume group holds none
        return self._ensure_ramen_volume_group_role(agent, volume_group_key, replication_request, is_to_promote,
                                                    request.force, request_deadline, context, response_type,
                                                    method_name)

    def _ensure_ramen_volume_group_role(self, agent, volume_group_key, replication_request, is_to_promote, force,
                                        request_deadline, context, response_type, method_name):
        promote_retry_deadline = _get_ramen_promote_retry_deadline(context)
        logger.debug("Ramen EAR {} of volume group {}: may wait {:.1f}s, may retry promote {:.1f}s".format(
            method_name, volume_group_key, request_deadline - time.monotonic(),
            promote_retry_deadline - time.monotonic()))

        def ensure_role_once():
            with agent.get_mediator() as mediator:
                replication = mediator.get_replication(replication_request)
                if not replication:
                    raise _RamenReplicationNotFoundError(_get_replication_not_found_message(replication_request))
                logger.info("found replication {} of volume group {} on system {}".format(
                    replication.name, replication.volume_group_name, mediator.identifier))
                if is_to_promote:
                    self._promote_ramen_volume_group(mediator, replication, volume_group_key, force,
                                                     promote_retry_deadline)
                else:
                    self._demote_ramen_volume_group(mediator, replication, volume_group_key)

        try:
            ramen_volume_group_coordinator.run_as_volume_group_owner(volume_group_key, method_name, request_deadline,
                                                                     ensure_role_once)
        except _RamenReplicationNotFoundError as ex:
            return build_error_response(str(ex), context, grpc.StatusCode.FAILED_PRECONDITION, response_type)
        except array_errors.OperationNotReadyError as ex:
            # Checkpoint not yet achieved - return UNAVAILABLE to trigger retry
            logger.info("Operation not ready, will be retried: {}".format(str(ex)))
            return build_error_response(str(ex), context, grpc.StatusCode.UNAVAILABLE, response_type)
        except VolumeGroupOperationNotCompletedError as ex:
            return build_error_response(str(ex), context, grpc.StatusCode.UNAVAILABLE, response_type)

        logger.info("finished {}".format(method_name))
        return response_type()

    @staticmethod
    def _promote_ramen_volume_group(mediator, replication, volume_group_key, force, promote_retry_deadline):
        if force:
            logger.info("Ramen: force-promote requested for replication {}".format(replication.name))
        # a promote ends any demote of the volume group, even when the demote did not complete
        ramen_volume_group_coordinator.set_demote_checkpoint_achieved(volume_group_key, False)
        mediator.clear_ear_demote_state(replication.volume_group_name)
        if replication.is_primary:
            logger.info("idempotent case. volume is already primary")
            return

        logger.info("promoting volume group {} for replication {}".format(replication.volume_group_name,
                                                                         replication.name))
        attempt = 0
        while True:
            attempt += 1
            logger.debug("promote attempt {} of volume group {}".format(attempt, replication.volume_group_name))
            try:
                mediator.promote_replication_volume(replication)
                logger.info("promote of volume group {} completed on attempt {}".format(
                    replication.volume_group_name, attempt))
                return
            except array_errors.SecondaryStorageTransitionToProductionNotReadyError:
                retry_interval = servers_settings.RAMEN_VG_PROMOTE_RETRY_INTERVAL_SECONDS
                if time.monotonic() + retry_interval >= promote_retry_deadline:
                    logger.info("promote of volume group {} did not complete after {} attempts, the replicator "
                                "will retry".format(replication.volume_group_name, attempt))
                    raise
                logger.info("remote volume group of {} is not independent yet, retrying the promote in {} "
                            "seconds".format(replication.volume_group_name, retry_interval))
                time.sleep(retry_interval)

    @staticmethod
    def _demote_ramen_volume_group(mediator, replication, volume_group_key):
        if not (replication.is_primary or replication.is_primary is None):
            ramen_volume_group_coordinator.set_demote_checkpoint_achieved(volume_group_key, False)
            logger.info("idempotent case. volume is already secondary")
            return
        # demote changes no role, it only waits for the checkpoint of the volume group,
        # so once the checkpoint is achieved, it is achieved for every volume of the volume group
        if ramen_volume_group_coordinator.is_demote_checkpoint_achieved(volume_group_key):
            logger.info("idempotent case. demote checkpoint of volume group {} is already achieved".format(
                replication.volume_group_name))
            return

        logger.info("demoting volume group {} for replication {}".format(replication.volume_group_name,
                                                                        replication.name))
        is_checkpoint_achieved = mediator.demote_replication_volume(replication)
        logger.debug("demote of volume group {} returned checkpoint achieved: {}".format(
            replication.volume_group_name, is_checkpoint_achieved))
        if is_checkpoint_achieved:
            ramen_volume_group_coordinator.set_demote_checkpoint_achieved(volume_group_key, True)

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
                        "leaving last_sync_time unset")
                    return response

            object_id = object_id_info.ids.internal_id
            replication_info = mediator.get_last_async_snapshot_info(object_id)

        if replication_info.last_sync_time is not None:
            ts_seconds = int(replication_info.last_sync_time.timestamp())
            response.last_sync_time.CopyFrom(Timestamp(seconds=ts_seconds, nanos=0))
        else:
            logger.warning("last_sync_time not available at storage (no recovery point yet), leaving it unset.")

        response.status = replication_info.replication_status
        response.status_message = replication_info.status_message or ''

        logger.info(
            "GetVolumeReplicationInfo: returning response last_sync_time.seconds={}, "
            "status={}, status_message='{}'".format(
                response.last_sync_time.seconds if response.HasField('last_sync_time') else 'unset',
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
        force_dr_mediator = utils.should_force_dr_mediator(request.secrets)

        with get_agent(connection_info, object_id_info.array_type).get_mediator() as mediator:
            if dr_connection_info is not None:
                with get_agent(dr_connection_info, object_id_info.array_type).get_mediator() as dr_mediator:
                    destination_volume_id = mediator.get_replication_destination_info(
                        object_id, object_uid, dr_mediator, force_dr_mediator)
            else:
                destination_volume_id = mediator.get_replication_destination_info(
                    object_id, object_uid, None, force_dr_mediator)

        response = pb2.GetReplicationDestinationInfoResponse()
        if destination_volume_id is not None:
            response.replication_destination.volume.volume_id = destination_volume_id
            logger.info("resolved destination volume id '{}' for source volume uid '{}'".format(
                destination_volume_id, object_uid))
        else:
            logger.warning("destination volume not yet available for source uid '{}'".format(object_uid))

        return response
