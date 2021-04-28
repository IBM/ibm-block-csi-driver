import socket
from threading import RLock
from queue import Empty
from collections import OrderedDict
from contextlib import contextmanager
from controller.array_action.array_connection_pool import ConnectionPool
from controller.common.csi_logger import get_stdout_logger
from controller.common import settings
from controller.array_action.errors import FailedToFindStorageSystemType
from controller.array_action.array_mediator_xiv import XIVArrayMediator
from controller.array_action.array_mediator_svc import SVCArrayMediator
from controller.array_action.array_mediator_ds8k import DS8KArrayMediator
import controller.array_action.errors as array_errors

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


def detect_array_type(endpoints):
    logger.debug("detecting array connection type")

    for storage_type, port in array_type_to_port.items():
        for endpoint in endpoints:
            if _socket_connect_test(endpoint, port) == 0:
                logger.debug("storage array type is : {0}".format(storage_type))
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


def get_agent(username, password, endpoints, array_type=None):
    endpoint_key = settings.ENDPOINTS_SEPARATOR.join(endpoints)
    with lock:
        found = _array_agents.get((username, endpoint_key), None)
        if found:
            # delete the agent and clear all the connections if password is changed.
            if found.password != password:
                logger.debug(
                    "The password is changed for endpoint {}, "
                    "remove the cached connection".format(endpoint_key)
                )
                del _array_agents[(username, endpoint_key)]
                del found
            else:
                logger.debug("Found a cached agent for endpoint {}, reuse it".format(endpoint_key))
                return found

        logger.debug("Creating a new agent for endpoint {}".format(endpoint_key))
        agent = StorageAgent(endpoints, username, password, array_type)
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

    def __init__(self, endpoints, username, password, array_type=None):
        self.username = username
        self.password = password
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
