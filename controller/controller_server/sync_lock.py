import threading
from collections import defaultdict

from decorator import decorator

from controller.controller_server.errors import VolumeAlreadyProcessingError

from controller.common.csi_logger import get_stdout_logger

logger = get_stdout_logger()


class SyncLock:
    def __init__(self):
        self._lock = threading.Lock()
        self._lock_ids = defaultdict(set)

    def add_volume_lock(self, lock_key, lock_id, action_name):
        logger.debug(("Lock for action: {}, Try to acquire lock for volume: {}".format(action_name, lock_id)))
        self._lock.acquire()
        if lock_id in self._lock_ids[lock_key]:
            self._lock.release()
            logger.debug(
                "Lock for action {}, Lock for volume: {} is already in use by other thread".format(action_name,
                                                                                                   lock_id))
            raise VolumeAlreadyProcessingError(lock_id)
        self._lock_ids[lock_key].add(lock_id)
        logger.debug("Lock for action: {}, Succeed to acquire lock for volume: {}".format(action_name, lock_id))
        self._lock.release()

    def remove_volume_lock(self, lock_key, lock_id, action_name):
        logger.debug("Lock for action: {}, release lock for volume: {}".format(action_name, lock_id))
        self._lock.acquire()
        self._lock_ids[lock_key].remove(lock_id)
        self._lock.release()


def handle_volume_lock(lock_key):
    @decorator
    def handle_volume_lock_with_response(controller_method, servicer, request, context):
        lock_id = getattr(request, lock_key)
        controller_method_name = controller_method.__name__
        servicer.sync_lock.add_volume_lock(lock_key, lock_id, controller_method_name)
        response = controller_method(servicer, request, context)
        servicer.sync_lock.remove_volume_lock(lock_key, lock_id, controller_method_name)
        return response

    return handle_volume_lock_with_response
