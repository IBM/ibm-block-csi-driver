import threading
from collections import defaultdict

from controller.common.csi_logger import get_stdout_logger
from controller.controller_server.errors import ObjectAlreadyProcessingError

logger = get_stdout_logger()

ids_in_use = defaultdict(set)


def add_to_ids_in_use(lock_key, object_id):
    ids_in_use[lock_key].add(object_id)


class SyncLock:
    def __init__(self, lock_key, object_id, action_name):
        self._lock = threading.Lock()
        self.lock_key = lock_key
        self.object_id = object_id
        self.action_name = action_name

    def __enter__(self):
        if self.lock_key:
            self.add_object_lock(self.lock_key, self.object_id, self.action_name)

    def __exit__(self, exc_type, exc_val, exc_tb):
        if self.lock_key:
            self.remove_object_lock(self.lock_key, self.object_id, self.action_name)

    def add_object_lock(self, lock_key, object_id, action_name):
        logger.debug(
            ("Trying to acquire lock for action {} with {}: {}".format(action_name, lock_key, object_id)))
        self._lock.acquire()
        if object_id in ids_in_use[lock_key]:
            self._lock.release()
            logger.error(
                "Lock for action {}, with {}: {} is already in use by another thread".format(action_name, lock_key,
                                                                                             object_id))
            raise ObjectAlreadyProcessingError(object_id)
        add_to_ids_in_use(lock_key, object_id)
        logger.debug(
            "Succeed to acquire lock for action {} with {}: {}".format(action_name, lock_key, object_id))
        self._lock.release()

    def remove_object_lock(self, lock_key, lock_id, action_name):
        logger.debug("Release lock for action {} with {}: {}".format(action_name, lock_key, lock_id))
        self._lock.acquire()
        ids_in_use[lock_key].remove(lock_id)
        self._lock.release()
