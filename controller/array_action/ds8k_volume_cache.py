from collections import defaultdict
from threading import RLock

from controller.common.csi_logger import get_stdout_logger

logger = get_stdout_logger()


class volumeCacheByAddress:
    def __init__(self):
        logger.debug("creating a new cache")
        self.volume_cache_by_address = defaultdict(dict)
        self.cache_lock = RLock()

    def add(self, address, key, value):
        logger.debug("adding {} to cache".format(key))
        with self.cache_lock:
            self.volume_cache_by_address[address][key] = value

    def remove(self, address, key):
        logger.debug("removing {} from cache".format(key))
        with self.cache_lock:
            if self.volume_cache_by_address[address].get(key) is not None:
                del self.volume_cache_by_address[address][key]

    def get(self, address, key):
        logger.debug("getting {} from cache".format(key))
        with self.cache_lock:
            return self.volume_cache_by_address[address].get(key)

    def get_all(self, address):
        logger.debug("getting cache for address {}".format(address))
        with self.cache_lock:
            return self.volume_cache_by_address.get(address)

    def add_or_delete(self, address, key, value):
        with self.cache_lock:
            if self.volume_cache_by_address[address].get(key) is None:
                logger.debug("adding {} to cache".format(key))
                self.volume_cache_by_address[address][key] = value
            else:
                logger.debug("removing {} from cache".format(key))
                del self.volume_cache_by_address[address][key]


volume_cache_by_address = volumeCacheByAddress()


class volumeCache:
    def __init__(self, service_address):
        self.service_address = service_address

    def add(self, key, value):
        volume_cache_by_address.add(self.service_address, key, value)

    def remove(self, key):
        volume_cache_by_address.remove(self.service_address, key)

    def get(self, key):
        return volume_cache_by_address.get(self.service_address, key)

    def get_all(self):
        return volume_cache_by_address.get_all(self.service_address)

    def add_or_delete(self, key, value):
        volume_cache_by_address.add_or_delete(self.service_address, key, value)
