from collections import defaultdict
from threading import RLock

from controllers.common.csi_logger import get_stdout_logger

logger = get_stdout_logger()


class VolumeCacheByAddress:
    def __init__(self):
        logger.debug("creating a new cache")
        self._volume_cache_by_address = defaultdict(dict)
        self._cache_lock = RLock()

    def add(self, address, key, value):
        logger.debug("adding {} to cache".format(key))
        with self._cache_lock:
            self._volume_cache_by_address[address][key] = value

    def remove(self, address, key):
        logger.debug("removing {} from cache".format(key))
        with self._cache_lock:
            if self._volume_cache_by_address[address].get(key) is not None:
                del self._volume_cache_by_address[address][key]

    def get(self, address, key):
        logger.debug("getting {} from cache".format(key))
        with self._cache_lock:
            return self._volume_cache_by_address[address].get(key)

    def add_or_delete(self, address, key, value):
        with self._cache_lock:
            if self._volume_cache_by_address[address].get(key) is None:
                logger.debug("adding {} to cache".format(key))
                self._volume_cache_by_address[address][key] = value
            else:
                logger.debug("removing {} from cache".format(key))
                del self._volume_cache_by_address[address][key]


volume_cache_by_address = VolumeCacheByAddress()


class VolumeCache:
    def __init__(self, service_address):
        self._service_address = service_address

    def add(self, key, value):
        volume_cache_by_address.add(self._service_address, key, value)

    def remove(self, key):
        volume_cache_by_address.remove(self._service_address, key)

    def get(self, key):
        return volume_cache_by_address.get(self._service_address, key)

    def add_or_delete(self, key, value):
        volume_cache_by_address.add_or_delete(self._service_address, key, value)
