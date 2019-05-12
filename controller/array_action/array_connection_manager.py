from threading import Lock
import socket

from controller.common.csi_logger import get_stdout_logger
from controller.array_action.errors import NoConnectionAvailableException, FailedToFindStorageSystemType
from array_mediator_xiv import XIVArrayMediator
from array_mediator_svc import SVCArrayMediator

connection_lock_dict = {}
array_connections_dict = {}


logger = get_stdout_logger()


def _socket_connect_test(ipaddr, port, timeout=1):
    '''
    function to test socket connection to ip:port.

    :param ipaddr: ip address
    :param port: port
    :param timeout: connection timeout

    :return:  0 - on successful connection
             -1 for exception + other return codes for connection errors.

    '''
    try:
        sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        sock.settimeout(timeout)
        ret = sock.connect_ex((ipaddr, port))
        sock.close()
        return ret
    except Exception as e:
        logger.debug('socket_connect {}'.format(e))
        return -1


class ArrayConnectionManager(object):

    def __init__(self, user, password, endpoint, array_type=None):  # TODO return the params back.
        self.array_mediator_class_dict = {XIVArrayMediator.ARRAY_TYPE: XIVArrayMediator, SVCArrayMediator.ARRAY_TYPE: SVCArrayMediator}
        self.array_type = array_type
        self.user = user
        self.password = password
        self.endpoint = endpoint

        if self.array_type is None:
            self.array_type = self.detect_array_type()

        connection_lock_dict[endpoint] = Lock()
        self.med_class = None
        self.connected = False

    def __enter__(self):
        logger.debug("in enter")
        arr_connection = self.get_array_connection()
        return arr_connection

    def __exit__(self, type, value, traceback):
        logger.debug("closing the connection")
        with connection_lock_dict[self.endpoint]:  # TODO: when moving to python 3 add tiemout!
            if self.connected:
                self.med_class.disconnect()
                logger.debug("reducing the connection count")
                array_connections_dict[self.endpoint] -= 1
                logger.debug("removing the connection  : {}".format(array_connections_dict))
                self.connected = False

    def get_array_connection(self):
        logger.debug("get array connection")
        med_class = self.array_mediator_class_dict[self.array_type]

        with connection_lock_dict[self.endpoint]:  # TODO: when moving to python 3 - add timeout to the lock!
            if self.endpoint in array_connections_dict:

                if array_connections_dict[self.endpoint] < med_class.CONNECTION_LIMIT:
                    logger.debug("adding new connection ")
                    array_connections_dict[self.endpoint] += 1

                else:
                    logger.error("failed to get connection. current connections: {}".format(array_connections_dict))
                    raise NoConnectionAvailableException(self.endpoint)
            else:
                logger.debug("adding new connection to new endpoint : {}".format(self.endpoint))
                array_connections_dict[self.endpoint] = 1

            logger.debug("got connection lock. array connection dict is: {}".format(array_connections_dict))
            try:
                self.med_class = med_class(self.user, self.password, self.endpoint)
            except Exception as ex:
                array_connections_dict[self.endpoint] -= 1
                raise ex

            self.connected = True

            return self.med_class

    def detect_array_type(self):
        logger.debug("detecting array connection type")
        for storage_type, port in [(XIVArrayMediator.ARRAY_TYPE, XIVArrayMediator.PORT), (SVCArrayMediator.ARRAY_TYPE, XIVArrayMediator.PORT)]:  # ds8k : 8452
            if _socket_connect_test(self.endpoint, port) == 0:
                logger.debug("storage array type is : {0}".format(self.array_type))
                return storage_type
        raise FailedToFindStorageSystemType(self.endpoint)
