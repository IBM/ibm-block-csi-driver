from threading import Lock
import socket

from controller.common.csi_logger import get_stdout_logger
from controller.array_action.errors import NoConnectionAvailableException, FailedToFindStorageSystemType
from controller.array_action.array_mediator_xiv import XIVArrayMediator
from controller.array_action.array_mediator_svc import SVCArrayMediator
from controller.array_action.array_mediator_ds8k import DS8KArrayMediator

connection_lock_dict = {}
array_connections_dict = {}

logger = get_stdout_logger()


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


class ArrayConnectionManager(object):

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
        logger.debug("detecting array connection type")

        # Don't change the order here since svc port (22) is also opened in ds8k.
        for storage_type, port in [(XIVArrayMediator.array_type, XIVArrayMediator.port),
                                   (DS8KArrayMediator.array_type, DS8KArrayMediator.port),
                                   (SVCArrayMediator.array_type, SVCArrayMediator.port),
                                   ]:

            for endpoint in self.endpoints:
                if _socket_connect_test(endpoint, port) == 0:
                    logger.debug("storage array type is : {0}".format(storage_type))
                    return storage_type

        raise FailedToFindStorageSystemType(self.endpoints)
