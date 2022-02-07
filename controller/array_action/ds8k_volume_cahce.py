from threading import RLock

from controller.common.csi_logger import get_stdout_logger

logger = get_stdout_logger()


class volumeCache:
    def __init__(self):
        logger.debug("creating a new cache")
        self.cache = dict()
        self.cache_lock = RLock()

    def add(self, key, value):
        logger.debug("adding {} to cache".format(key))
        with self.cache_lock:
            self.cache[key] = value

    def remove(self, key):
        logger.debug("removing {} from cache".format(key))
        with self.cache_lock:
            if self.cache.get(key) is not None:
                del self.cache[key]

    def get(self, key):
        logger.debug("getting {} from cache".format(key))
        with self.cache_lock:
            return self.cache.get(key)

    def add_or_delete(self, key, value):
        with self.cache_lock:
            if self.cache.get(key) is None:
                logger.debug("adding {} to cache".format(key))
                self.cache[key] = value
            else:
                logger.debug("removing {} from cache".format(key))
                del self.cache[key]

    def __str__(self):
        return "volume cache: {}".format(self.cache)
