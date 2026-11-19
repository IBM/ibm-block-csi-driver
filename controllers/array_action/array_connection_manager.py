import os
import socket
from threading import Lock

from controllers.common.csi_logger import get_stdout_logger
from controllers.array_action.errors import NoConnectionAvailableException, FailedToFindStorageSystemType
from controllers.array_action.array_mediator_xiv import XIVArrayMediator
from controllers.array_action.array_mediator_svc import SVCArrayMediator
from controllers.array_action.array_mediator_ds8k import DS8KArrayMediator

connection_lock_dict = {}
array_connections_dict = {}

logger = get_stdout_logger()


def _socket_connect_test(host, port, timeout=30):
    """
    function to test socket connection to host:port.

    :param host: ip address or host name
    :param port: port
    :param timeout: connection timeout (default increased to 30s)

    :return:  0 - on successful connection
             -1 - on any exception, or specific connection errors with that error number
             other values - on other connection errors
    """
    socket_timeout = int(os.environ.get("SOCKET_CONNECT_TIMEOUT", timeout))
    logger.info("Testing socket connection to {}:{} with timeout={}s".format(host, port, socket_timeout))
    try:
        sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        sock.settimeout(socket_timeout)
        ret = sock.connect_ex((host, port))
        sock.close()
        if ret == 0:
            logger.info("Socket connection to {}:{} SUCCESS (code: 0)".format(host, port))
        else:
            err_msg = os.strerror(ret) if hasattr(os, 'strerror') and ret > 0 else 'connection error'
            logger.warning("Socket connection to {}:{} FAILED (errno {}: {})".format(host, port, ret, err_msg))
        return ret
    except socket.gaierror as e:
        logger.warning('Could not resolve hostname "{HOST}": {ERROR}'.format(HOST=host, ERROR=e))
        return -1
    except Exception as e:
        logger.warning('Socket connect error for {}:{}: {}'.format(host, port, e))
        return -1


class ArrayConnectionManager:

    def __init__(self, user, password, endpoint, array_type=None):  # TODO return the params back.
        self.array_mediator_class_dict = {
            XIVArrayMediator.array_type: XIVArrayMediator,
            SVCArrayMediator.array_type: SVCArrayMediator,
            DS8KArrayMediator.array_type: DS8KArrayMediator,
        }

        self.array_type = array_type
        self.user = user
        self.password = password
        self.endpoints = endpoint
        self.endpoint_key = ",".join(self.endpoints)

        if self.array_type is None:
            self.array_type = self.detect_array_type()

        connection_lock_dict[self.endpoint_key] = Lock()
        self.med_class = None
        self.connected = False

    def __enter__(self):
        logger.debug("in enter")
        arr_connection = self.get_array_connection()
        return arr_connection

    def __exit__(self, type, value, traceback):
        logger.debug("closing the connection")
        with connection_lock_dict[self.endpoint_key]:  # TODO: when moving to python 3 add tiemout!
            if self.connected:
                self.med_class.disconnect()
                logger.debug("reducing the connection count")
                if array_connections_dict[self.endpoint_key] == 1:
                    del array_connections_dict[self.endpoint_key]
                else:
                    array_connections_dict[self.endpoint_key] -= 1
                logger.debug("removing the connection  : {}".format(array_connections_dict))
                self.connected = False

    def get_array_connection(self):
        logger.debug("get array connection")
        med_class = self.array_mediator_class_dict[self.array_type]

        with connection_lock_dict[self.endpoint_key]:  # TODO: when moving to python 3 - add timeout to the lock!
            if self.endpoint_key in array_connections_dict:

                if array_connections_dict[self.endpoint_key] < med_class.max_connections:
                    logger.debug("adding new connection ")
                    array_connections_dict[self.endpoint_key] += 1

                else:
                    logger.error("failed to get connection. current connections: {}".format(array_connections_dict))
                    raise NoConnectionAvailableException(self.endpoint_key)
            else:
                logger.debug("adding new connection to new endpoint : {}".format(self.endpoint_key))
                array_connections_dict[self.endpoint_key] = 1

            logger.debug("got connection lock. array connection dict is: {}".format(array_connections_dict))
            try:
                self.med_class = med_class(self.user, self.password, self.endpoints)
            except Exception as ex:
                if array_connections_dict[self.endpoint_key] == 1:
                    del array_connections_dict[self.endpoint_key]
                else:
                    array_connections_dict[self.endpoint_key] -= 1

                raise ex

            self.connected = True

            return self.med_class

    def detect_array_type(self):
        logger.info("Detecting array connection type for endpoints: {}".format(self.endpoints))

        # Don't change the order here since svc port (22) is also opened in ds8k.
        for storage_type, port in [(XIVArrayMediator.array_type, XIVArrayMediator.port),
                                   (DS8KArrayMediator.array_type, DS8KArrayMediator.port),
                                   (SVCArrayMediator.array_type, SVCArrayMediator.port),
                                   ]:

            logger.info("Testing storage array type '{}' on port {} for endpoints: {}".format(storage_type, port, self.endpoints))
            for endpoint in self.endpoints:
                if _socket_connect_test(endpoint, port) == 0:
                    logger.info("Detected storage array type: {0} for endpoint: {1}".format(storage_type, endpoint))
                    return storage_type

        logger.error("Failed to find storage system type for endpoints: {}".format(self.endpoints))
        raise FailedToFindStorageSystemType(self.endpoints)
