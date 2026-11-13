"""
One owner per volume group for Ramen EAR replication calls.

Ramen creates one VolumeReplication per PVC, but the driver acts on the whole volume group (VG).
When the replicator runs several VolumeReplications in parallel, several requests of the same VG
reach the driver at the same time. Only one of them (the owner) acts on the VG. The others wait
for it without holding a storage connection, and then take its result.

Requests of different VGs never wait for each other.
"""
import threading
import time

from controllers.common.csi_logger import get_stdout_logger

logger = get_stdout_logger()


class VolumeGroupOperationNotCompletedError(Exception):
    pass


class _VolumeGroupOperation:
    def __init__(self, name):
        self.name = name
        self.owner = threading.current_thread().name
        self.start_time = time.monotonic()
        self.error = None
        self._done = threading.Event()

    def finish(self, error):
        self.error = error
        self._done.set()

    def wait(self, timeout):
        return self._done.wait(timeout)


class _VolumeGroupState:
    def __init__(self):
        self.operation = None
        self.is_demote_checkpoint_achieved = False


_states = {}
_states_lock = threading.Lock()


def _start_or_join(volume_group_key, operation_name):
    with _states_lock:
        state = _states.setdefault(volume_group_key, _VolumeGroupState())
        if state.operation is None:
            state.operation = _VolumeGroupOperation(operation_name)
            logger.debug("volume group {}: no operation running, this request owns {}".format(
                volume_group_key, operation_name))
            return state.operation, True
        logger.debug("volume group {}: {} is running, owned by request {} for {:.1f}s".format(
            volume_group_key, state.operation.name, state.operation.owner,
            time.monotonic() - state.operation.start_time))
        return state.operation, False


def _finish(volume_group_key, operation, error):
    with _states_lock:
        state = _states.get(volume_group_key)
        if state and state.operation is operation:
            state.operation = None
    operation.finish(error)


def run_as_volume_group_owner(volume_group_key, operation_name, deadline, owner_function):
    """
    Runs owner_function only if no other request is running an operation on this VG.
    Otherwise waits for that operation, without calling owner_function:
      - same operation: returns when it succeeded, raises VolumeGroupOperationNotCompletedError when it failed
      - another operation: tries again to become the owner once it is done
    Raises VolumeGroupOperationNotCompletedError when the deadline passes while waiting.
    """
    while True:
        operation, is_owner = _start_or_join(volume_group_key, operation_name)
        if is_owner:
            logger.info("{} of volume group {}: this request is the owner, acting on the volume group".format(
                operation_name, volume_group_key))
            error = None
            try:
                return owner_function()
            except Exception as ex:
                error = ex
                raise
            finally:
                _finish(volume_group_key, operation, error)
                logger.info("{} of volume group {}: owner finished in {:.1f}s, result: {}".format(
                    operation_name, volume_group_key, time.monotonic() - operation.start_time,
                    "success" if error is None else "{}: {}".format(type(error).__name__, error)))

        remaining = deadline - time.monotonic()
        logger.info("{} of volume group {} is waiting for {} running on it by request {}, "
                    "for at most {:.1f}s".format(operation_name, volume_group_key, operation.name,
                                                 operation.owner, max(remaining, 0)))
        wait_start_time = time.monotonic()
        if remaining <= 0 or not operation.wait(remaining):
            raise VolumeGroupOperationNotCompletedError(
                "timed out waiting for {} of volume group {} running by request {}".format(
                    operation.name, volume_group_key, operation.owner))
        logger.debug("{} of volume group {}: waited {:.1f}s for {} of request {}".format(
            operation_name, volume_group_key, time.monotonic() - wait_start_time, operation.name, operation.owner))

        if operation.name != operation_name:
            logger.info("{} of volume group {}: {} of request {} finished, trying to own the volume group".format(
                operation_name, volume_group_key, operation.name, operation.owner))
            continue
        if operation.error is not None:
            raise VolumeGroupOperationNotCompletedError(
                "{} of volume group {} by request {} did not complete: {}".format(
                    operation_name, volume_group_key, operation.owner, operation.error))
        logger.info("{} of volume group {} was completed by request {}".format(
            operation_name, volume_group_key, operation.owner))
        return None


def is_demote_checkpoint_achieved(volume_group_key):
    with _states_lock:
        state = _states.get(volume_group_key)
        return bool(state and state.is_demote_checkpoint_achieved)


def set_demote_checkpoint_achieved(volume_group_key, is_achieved):
    with _states_lock:
        state = _states.setdefault(volume_group_key, _VolumeGroupState())
        was_achieved = state.is_demote_checkpoint_achieved
        state.is_demote_checkpoint_achieved = is_achieved
    if was_achieved != is_achieved:
        logger.info("volume group {}: demote checkpoint achieved changed from {} to {}".format(
            volume_group_key, was_achieved, is_achieved))
