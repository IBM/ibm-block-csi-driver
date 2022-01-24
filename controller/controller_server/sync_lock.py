import threading
from collections import defaultdict

from decorator import decorator

from controller.controller_server.errors import VolumeAlreadyProcessingError


class SyncLock:
    def __init__(self):
        self._lock = threading.Lock()
        self._lock_ids = defaultdict(set)

    def add_volume_lock(self, lock_key, lock_id):
        self._lock.acquire()
        if lock_id in self._lock_ids[lock_key]:
            self._lock.release()
            raise VolumeAlreadyProcessingError(lock_id)
        self._lock_ids[lock_key].add(lock_id)
        self._lock.release()

    def remove_volume_lock(self, lock_key, lock_id):
        self._lock.acquire()
        self._lock_ids[lock_key].remove(lock_id)
        self._lock.release()


def handle_volume_lock(lock_key):
    @decorator
    def handle_handle_volume_lock_with_response(controller_method, servicer, request, context):
        lock_id = getattr(request, lock_key)
        servicer.sync_lock.add_volume_lock(lock_key, lock_id)
        response = controller_method(servicer, request, context)
        servicer.sync_lock.remove_volume_lock(lock_key, lock_id)
        return response

    return handle_handle_volume_lock_with_response
