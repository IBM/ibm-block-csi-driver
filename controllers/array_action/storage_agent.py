import socket
from collections import OrderedDict
from contextlib import contextmanager
from queue import Empty
from threading import RLock

import controllers.array_action.errors as array_errors
from controllers.array_action.array_connection_pool import ConnectionPool
from controllers.array_action.array_mediator_ds8k import DS8KArrayMediator
from controllers.array_action.array_mediator_svc import SVCArrayMediator
from controllers.array_action.array_mediator_xiv import XIVArrayMediator
from controllers.array_action.errors import FailedToFindStorageSystemType
from controllers.common import settings
from controllers.common.csi_logger import get_stdout_logger

logger = get_stdout_logger()
_array_agents = {}
lock = RLock()

array_type_to_port = OrderedDict()
# Don't change the order here since svc port (22) is also opened in ds8k.
array_type_to_port[XIVArrayMediator.array_type] = XIVArrayMediator.port
array_type_to_port[DS8KArrayMediator.array_type] = DS8KArrayMediator.port
array_type_to_port[SVCArrayMediator.array_type] = SVCArrayMediator.port

array_type_to_mediator = {
    XIVArrayMediator.array_type: XIVArrayMediator,
    SVCArrayMediator.array_type: SVCArrayMediator,
    DS8KArrayMediator.array_type: DS8KArrayMediator,
}

array_type_cache = {}


def _get_array_type_from_cache(endpoints):
    for endpoint in endpoints:
        storage_type = array_type_cache.get(endpoint)
        if storage_type:
            logger.debug(
                "found in cache, for endpoint : {}, storage array type is : {}".format(endpoint, storage_type))
            return storage_type
    return None


def detect_array_type(endpoints):
    logger.debug("detecting array connection type")
    storage_type = _get_array_type_from_cache(endpoints)
    if storage_type:
        return storage_type
    for storage_type, port in array_type_to_port.items():
        for endpoint in endpoints:
            if _socket_connect_test(endpoint, port) == 0:
                logger.debug("storage array type is : {0}".format(storage_type))
                array_type_cache[endpoint] = storage_type
                return storage_type

    raise FailedToFindStorageSystemType(endpoints)


def _socket_connect_test(host, port, timeout=1):
    """
    function to test socket connection to host:port.

    :param host: ip address or host name
    :param port: port
    :param timeout: connection timeout

    :return:  0 - on successful connection
             -1 - on any exception, or specific connection errors with that error number
             other values - on other connection errors
    """
    try:
        sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        sock.settimeout(timeout)
        ret = sock.connect_ex((host, port))
        sock.close()
        return ret
    except socket.gaierror as e:
        logger.debug('could not resolve hostname "{HOST}": {ERROR}'.format(HOST=host, ERROR=e))
        return -1
    except Exception as e:
        logger.debug('socket_connect {}'.format(e))
        return -1


def get_agent(array_connection_info, array_type=None):
    endpoints = array_connection_info.array_addresses
    username = array_connection_info.user
    password = array_connection_info.password
    endpoint_key = settings.ENDPOINTS_SEPARATOR.join(endpoints)
    partition_name = array_connection_info.partition_name
    if partition_name is None:
        partition_name = ""
    with lock:
        found = _array_agents.get((username, endpoint_key, partition_name), None)
        if found:
            # delete the agent and clear all the connections if password is changed.
            if found.password != password:
                logger.debug(
                    "The password is changed for endpoint {}, "
                    "remove the cached connection".format(endpoint_key)
                )
                del _array_agents[(username, endpoint_key, partition_name)]
                del found
            else:
                logger.debug("Found a cached agent for endpoint {}, reuse it".format(endpoint_key))
                return found

        logger.debug("Creating a new agent for endpoint {}".format(endpoint_key))
        agent = StorageAgent(endpoints, username, password, array_type, array_connection_info.partition_name)
        _array_agents[(username, endpoint_key)] = agent
        return agent


def get_agents():
    return _array_agents


def clear_agents():
    with lock:
        agents = list(_array_agents.values())
        _array_agents.clear()
        try:
            while True:
                agent = agents.pop()
                # close all the connections
                del agent
        except IndexError:
            pass


class StorageAgent:
    """
    StorageAgent is an agent which caches several mediators of the same storage for reuse cross threads.
    """

    def __init__(self, endpoints, username, password, array_type=None, partition_name=None):
        self.username = username
        self.password = password
        self.partition_name = partition_name
        self.endpoints = endpoints
        self.endpoint_key = settings.ENDPOINTS_SEPARATOR.join(endpoints)
        self.conn_pool = None

        if not array_type:
            array_type = detect_array_type(self.endpoints)

        med_class = array_type_to_mediator[array_type]

        self.conn_pool = ConnectionPool(
            endpoints=self.endpoints,
            username=self.username,
            password=self.password,
            partition_name=self.partition_name,
            med_class=med_class,
            # Specifying a non-zero min_size pre-populates the pool with min_size items
            min_size=1,
            max_size=min(med_class.max_connections, settings.CSI_CONTROLLER_SERVER_WORKERS)
        )

    def __del__(self):
        if self.conn_pool:
            # close all the connections
            pool = self.conn_pool
            self.conn_pool = None
            del pool

    @contextmanager
    def get_mediator(self, timeout=None):
        """
        Get an object out of the pool, for use with with-statement.
        """
        try:
            med = self.conn_pool.get(timeout=timeout)
        except Empty:
            raise array_errors.NoConnectionAvailableException(", ".join(self.endpoint_key))

        try:
            yield med
        finally:
            self.conn_pool.put(med)
