import time
from contextlib import contextmanager

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


@contextmanager
def _get_ramen_mediator(agent, request_deadline):
    # a Ramen EAR request waits for a storage connection only until its deadline, and then returns UNAVAILABLE,
    # instead of getting a connection after the replicator already gave up on the request
    timeout = max(request_deadline - time.monotonic(), 0)
    try:
        with agent.get_mediator(timeout=timeout) as mediator:
            yield mediator
    except array_errors.NoConnectionAvailableException:
        raise array_errors.StorageConnectionWaitTimeoutError(agent.endpoint_key, timeout)


def _get_mediator(agent, is_ramen_ear, request_deadline):
    if is_ramen_ear:
        return _get_ramen_mediator(agent, request_deadline)
    return agent.get_mediator()


def _get_ramen_replication(mediator, replication_request, volume_group_name):
    # Ramen EAR only: the volume group name is already known from finding the volume group
    get_ramen_ear_replication = getattr(mediator, "get_ramen_ear_replication", None)
    if get_ramen_ear_replication:
        return get_ramen_ear_replication(replication_request, volume_group_name)
    return mediator.get_replication(replication_request)


def _get_replication_not_found_message(replication_request):
    return "could not find replication for volume internal id: {} with " \
           "volume internal id: {} of system: {}".format(replication_request.volume_internal_id,
                                                         replication_request.other_volume_internal_id,
                                                         replication_request.other_system_id)


class _StatusRecordingContext:
    """Passes everything to the gRPC context, and records the status code the request sets."""

    def __init__(self, context):
        self._context = context
        self.recorded_code = None

    def set_code(self, code):
        self.recorded_code = code
        return self._context.set_code(code)

    def __getattr__(self, name):
        return getattr(self._context, name)


class _RamenRequestSummary:
    """Times a Ramen EAR request and logs one summary line for it, to make slow requests easy to find."""

    def __init__(self, method_name):
        self.method_name = method_name
        self.start_time = time.monotonic()
        self.volume_uid = None
        self.volume_group_key = None
        self.volume_group_found_time = None

    def set_ramen_volume(self, volume_uid):
        self.volume_uid = volume_uid

    def set_volume_group_found(self, volume_group_key):
        self.volume_group_key = volume_group_key
        self.volume_group_found_time = time.monotonic()
        return self.volume_group_found_time - self.start_time

    def log(self, result):
        if self.volume_uid is None:
            return
        now = time.monotonic()
        if self.volume_group_found_time is None:
            find_seconds = now - self.start_time
            volume_group_step = "not reached"
        else:
            find_seconds = self.volume_group_found_time - self.start_time
            volume_group_step = "{:.2f}s".format(now - self.volume_group_found_time)
        logger.info("Ramen EAR summary: {} of volume {}, volume group {}, result {}, total {:.2f}s, "
                    "finding the volume group {:.2f}s, volume group step {}".format(
                        self.method_name, self.volume_uid, self.volume_group_key, result, now - self.start_time,
                        find_seconds, volume_group_step))


def _run_with_ramen_summary(method_name, context, run):
    summary = _RamenRequestSummary(method_name)
    recording_context = _StatusRecordingContext(context)
    try:
        response = run(recording_context, summary)
    except Exception as ex:
        summary.log(type(ex).__name__)
        raise
    summary.log(recording_context.recorded_code.name if recording_context.recorded_code else "OK")
    return response


class ReplicationControllerServicer(pb2_grpc.ControllerServicer):

    @csi_replication_method(error_response_type=pb2.EnableVolumeReplicationResponse)
    def EnableVolumeReplication(self, request, context):
        return _run_with_ramen_summary(
            "EnableVolumeReplication", context,
            lambda recording_context, summary: self._enable_volume_replication(request, recording_context, summary))

    def _enable_volume_replication(self, request, context, summary):
        request_deadline = _get_ramen_request_deadline(context)
        replication_type = utils.get_addons_replication_type(request)
        utils.validate_addons_request(request, replication_type)

        object_type, object_id_info = utils.get_replication_object_type_and_id_info(request)
        is_ramen_ear = utils.is_ramen_ear_request(object_type, replication_type)
        if is_ramen_ear:
            summary.set_ramen_volume(object_id_info.ids.uid)
        connection_info = utils.get_array_connection_info_from_secrets(request.secrets)
        agent = get_agent(connection_info, object_id_info.array_type)

        with _get_mediator(agent, is_ramen_ear, request_deadline) as mediator:
            object_type, object_id_info = utils.resolve_ramen_ear_volume_to_volume_group(
                object_type, object_id_info, replication_type, mediator)

            object_id = object_id_info.ids.internal_id

            error_message = self._validate_replication_object(object_type, replication_type)
            if error_message:
                return build_error_response(error_message, context, grpc.StatusCode.FAILED_PRECONDITION,
                                            pb2.EnableVolumeReplicationResponse)

            replication_request = utils.generate_addons_replication_request(request, replication_type, object_id)

            replication_object = self._get_replication_object(object_id_info, object_type, connection_info, mediator,
                                                              is_ramen_ear=is_ramen_ear)
            if is_ramen_ear:
                replication = _get_ramen_replication(mediator, replication_request, replication_object.name)
            else:
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
            find_seconds = summary.set_volume_group_found(volume_group_key)
            logger.info("Ramen EAR: replication of volume group {} is missing, it must be created once "
                        "(found the volume group in {:.2f}s)".format(volume_group_key, find_seconds))

        return self._create_ramen_replication(agent, volume_group_key, replication_object.name, replication_request,
                                              request_deadline, context)

    @staticmethod
    def _create_ramen_replication(agent, volume_group_key, volume_group_name, replication_request, request_deadline,
                                  context):
        # creating the replication removes and then assigns the policy of the volume group, so only one request
        # of the volume group may do it, after reading again that the replication is still missing
        def create_replication_once():
            with _get_ramen_mediator(agent, request_deadline) as mediator:
                if _get_ramen_replication(mediator, replication_request, volume_group_name):
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
        return _run_with_ramen_summary(
            method_name, context,
            lambda recording_context, summary: self._ensure_volume_role_with_summary(
                request, recording_context, is_to_promote, response_type, method_name, summary))

    def _ensure_volume_role_with_summary(self, request, context, is_to_promote, response_type, method_name,
                                         summary):
        logger.info(method_name)
        request_deadline = _get_ramen_request_deadline(context)
        replication_type = utils.get_addons_replication_type(request)
        utils.validate_addons_request(request, replication_type)

        object_type, object_id_info = utils.get_replication_object_type_and_id_info(request)
        is_ramen_ear = utils.is_ramen_ear_request(object_type, replication_type)
        requested_volume_uid = object_id_info.ids.uid
        if is_ramen_ear:
            summary.set_ramen_volume(requested_volume_uid)

        connection_info = utils.get_array_connection_info_from_secrets(request.secrets)
        agent = get_agent(connection_info, object_id_info.array_type)
        with _get_mediator(agent, is_ramen_ear, request_deadline) as mediator:
            object_type, object_id_info = utils.resolve_ramen_ear_volume_to_volume_group(
                object_type, object_id_info, replication_type, mediator)

            object_id = object_id_info.ids.internal_id

            error_message = self._validate_replication_object(object_type, replication_type)
            if error_message:
                return build_error_response(error_message, context, grpc.StatusCode.FAILED_PRECONDITION,
                                            pb2.EnableVolumeReplicationResponse)

            replication_request = utils.generate_addons_replication_request(request, replication_type, object_id)

            replication_object = self._get_replication_object(object_id_info, object_type, connection_info, mediator,
                                                              is_ramen_ear=is_ramen_ear)
            if is_ramen_ear:
                volume_group_key = (mediator.identifier, object_id)
                find_seconds = summary.set_volume_group_found(volume_group_key)
                logger.info("Ramen EAR {}: volume {} belongs to volume group {} (found in {:.2f}s), releasing the "
                            "storage connection before acting on the volume group".format(
                                method_name, requested_volume_uid, volume_group_key, find_seconds))
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
        return self._ensure_ramen_volume_group_role(agent, volume_group_key, replication_object.name,
                                                    replication_request, is_to_promote, request.force,
                                                    request_deadline, context, response_type, method_name)

    def _ensure_ramen_volume_group_role(self, agent, volume_group_key, volume_group_name, replication_request,
                                        is_to_promote, force, request_deadline, context, response_type, method_name):
        promote_retry_deadline = _get_ramen_promote_retry_deadline(context)
        logger.debug("Ramen EAR {} of volume group {}: may wait {:.1f}s, may retry promote {:.1f}s".format(
            method_name, volume_group_key, request_deadline - time.monotonic(),
            promote_retry_deadline - time.monotonic()))

        def ensure_role_once():
            with _get_ramen_mediator(agent, request_deadline) as mediator:
                replication = _get_ramen_replication(mediator, replication_request, volume_group_name)
                if not replication:
                    raise _RamenReplicationNotFoundError(_get_replication_not_found_message(replication_request))
                logger.info("found replication {} of volume group {} on system {}".format(
                    replication.name, replication.volume_group_name, mediator.identifier))
                if not is_to_promote:
                    self._demote_ramen_volume_group(mediator, replication, volume_group_key)
                    return
                if not self._prepare_ramen_promote(mediator, replication, volume_group_key, force):
                    return
            # the connection is released here, so the waits between promote attempts hold no connection
            self._promote_ramen_volume_group(agent, replication, request_deadline, promote_retry_deadline)

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
    def _prepare_ramen_promote(mediator, replication, volume_group_key, force):
        """Returns False when the volume group is already primary and there is nothing to promote."""
        if force:
            logger.info("Ramen: force-promote requested for replication {}".format(replication.name))
        # a promote ends any demote of the volume group, even when the demote did not complete
        ramen_volume_group_coordinator.set_demote_checkpoint_achieved(volume_group_key, False)
        mediator.clear_ear_demote_state(replication.volume_group_name)
        if replication.is_primary:
            logger.info("idempotent case. volume is already primary")
            return False
        return True

    @staticmethod
    def _promote_ramen_volume_group(agent, replication, request_deadline, promote_retry_deadline):
        logger.info("promoting volume group {} for replication {}".format(replication.volume_group_name,
                                                                         replication.name))
        attempt = 0
        while True:
            attempt += 1
            logger.debug("promote attempt {} of volume group {}".format(attempt, replication.volume_group_name))
            try:
                with _get_ramen_mediator(agent, request_deadline) as mediator:
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
    def _get_replication_object(object_id_info, object_type, array_connection_info, mediator, is_ramen_ear=False):
        object_uid = object_id_info.ids.uid if object_type == servers_settings.VOLUME_TYPE_NAME else \
            object_id_info.ids.internal_id
        if is_ramen_ear and object_type == servers_settings.VOLUME_GROUP_TYPE_NAME and \
                hasattr(mediator, "get_volume_group_without_volumes"):
            # Ramen uses only the volume group's name and partition, so its volumes are not listed
            replication_object = mediator.get_volume_group_without_volumes(object_uid)
        else:
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

        with get_agent(connection_info, object_id_info.array_type).get_mediator() as mediator:
            destination_volume_id = mediator.get_replication_destination_info(object_id, object_uid)

        response = pb2.GetReplicationDestinationInfoResponse()
        if destination_volume_id is not None:
            response.replication_destination.volume.volume_id = destination_volume_id
            logger.info("resolved destination volume id '{}' for source volume uid '{}'".format(
                destination_volume_id, object_uid))
        else:
            logger.warning("destination volume not yet available for source uid '{}'".format(object_uid))

        return response
