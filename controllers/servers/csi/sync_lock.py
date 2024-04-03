import threading
from collections import defaultdict

from controllers.common.csi_logger import get_stdout_logger
from controllers.servers.errors import ObjectAlreadyProcessingError

logger = get_stdout_logger()

ids_in_use = defaultdict(set)
ids_in_use_lock = threading.Lock()


def _add_to_ids_in_use(lock_key, object_id):
    ids_in_use[lock_key].add(object_id)


def _remove_from_ids_in_use(lock_key, object_id):
    if object_id in ids_in_use[lock_key]:
        ids_in_use[lock_key].remove(object_id)
    else:
        logger.error("could not find lock to release for {}: {}".format(lock_key, object_id))


class SyncLock:
    def __init__(self, lock_key, object_id, action_name):
        self.lock_key = lock_key
        self.object_id = object_id
        self.action_name = action_name

    def __enter__(self):
        if self.lock_key:
            self._add_object_lock()

    def __exit__(self, exc_type, exc_val, exc_tb):
        if self.lock_key:
            self._remove_object_lock()

    def _add_object_lock(self):
        logger.debug(
            ("trying to acquire lock for action {} with {}: {}".format(self.action_name, self.lock_key,
                                                                       self.object_id)))
        with ids_in_use_lock:
            if self.object_id in ids_in_use[self.lock_key]:
                logger.error(
                    "lock for action {} with {}: {} is already in use by another thread".format(self.action_name,
                                                                                                self.lock_key,
                                                                                                self.object_id))
                raise ObjectAlreadyProcessingError(self.object_id)
            _add_to_ids_in_use(self.lock_key, self.object_id)
            logger.debug(
                "succeed to acquire lock for action {} with {}: {}".format(self.action_name,
                                                                           self.lock_key,
                                                                           self.object_id))

    def _remove_object_lock(self):
        logger.debug("release lock for action {} with {}: {}".format(self.action_name, self.lock_key, self.object_id))
        with ids_in_use_lock:
            _remove_from_ids_in_use(self.lock_key, self.object_id)
