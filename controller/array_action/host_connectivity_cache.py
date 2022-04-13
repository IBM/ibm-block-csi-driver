from collections import defaultdict
from dataclasses import dataclass
from threading import RLock
from controller.common.csi_logger import get_stdout_logger

logger = get_stdout_logger()


@dataclass()
class HostConnectivityInfo:
    host_name: str
    connectivity_type: str
    array_initiators: list = None


class HostConnectivityInfoCacheByInitiators:
    def __init__(self):
        logger.debug("creating a new cache")
        self._volume_cache_by_address = defaultdict(dict)
        self._cache_lock = RLock()

    def add(self, address, initiators, host_connectivity_info):
        logger.debug("adding {} to cache".format(host_connectivity_info))
        with self._cache_lock:
            self._volume_cache_by_address[address][initiators] = host_connectivity_info

    def remove(self, address, initiators):
        logger.debug("removing {} from cache".format(initiators))
        with self._cache_lock:
            if self._volume_cache_by_address[initiators] is not None:
                del self._volume_cache_by_address[address][initiators]

    def get(self, address, initiators):
        logger.debug("getting {} from cache".format(initiators))
        with self._cache_lock:
            return self._volume_cache_by_address[address][initiators]


host_connectivity_cache = HostConnectivityInfoCacheByInitiators()
