import threading

from controller.controller_server.errors import VolumeAlreadyProcessingError


class SyncLock:
    def __init__(self):
        self._lock = threading.Lock()

    def add_volume_lock(self, lock_id):
        self._lock.acquire()
        current_thread = threading.current_thread()
        for thread in threading.enumerate():
            if thread is not current_thread:
                if lock_id == thread.getName():
                    raise VolumeAlreadyProcessingError(lock_id)
        current_thread.setName(lock_id)
        self._lock.release()
 