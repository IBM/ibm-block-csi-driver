import threading
from collections import defaultdict

from controller.common.csi_logger import get_stdout_logger
from controller.controller_server.errors import ObjectAlreadyProcessingError

logger = get_stdout_logger()

ids_in_use = defaultdict(set)
ids_in_use_lock = threading.Lock()


def _add_to_ids_in_use(lock_key, object_id):
    ids_in_use[lock_key].add(object_id)


def _remove_from_ids_in_use(lock_key, object_id):
    ids_in_use[lock_key].remove(object_id)


def _add_object_lock(lock_key, object_id, action_name):
    logger.debug(
        ("trying to acquire lock for action {} with {}: {}".format(action_name, lock_key, object_id)))
    ids_in_use_lock.acquire()
    if object_id in ids_in_use[lock_key]:
        ids_in_use_lock.release()
        logger.error(
            "lock for action {} with {}: {} is already in use by another thread".format(action_name, lock_key,
                                                                                        object_id))
        raise ObjectAlreadyProcessingError(object_id)
    _add_to_ids_in_use(lock_key, object_id)
    logger.debug(
        "succeed to acquire lock for action {} with {}: {}".format(action_name, lock_key, object_id))
    ids_in_use_lock.release()


def _remove_object_lock(lock_key, lock_id, action_name):
    logger.debug("release lock for action {} with {}: {}".format(action_name, lock_key, lock_id))
    ids_in_use_lock.acquire()
    _remove_from_ids_in_use(lock_key, lock_id)
    ids_in_use_lock.release()


class SyncLock:
    def __init__(self, lock_key, object_id, action_name):
        self.lock_key = lock_key
        self.object_id = object_id
        self.action_name = action_name

    def __enter__(self):
        if self.lock_key:
            _add_object_lock(self.lock_key, self.object_id, self.action_name)

    def __exit__(self, exc_type, exc_val, exc_tb):
        if self.lock_key:
            _remove_object_lock(self.lock_key, self.object_id, self.action_name)
