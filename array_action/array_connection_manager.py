from array_mediator_xiv import XIVArrayMediator
from array_mediator_svc import SVCArrayMediator
from concurrent.futures import ThreadPoolExecutor
from threading import Thread, Lock, BoundedSemaphore
from time import sleep
from csi_logger import get_stdout_logger
from errors import NoConnctionAvailableException

connection_lock = Lock()
array_connections_dict = {}

xiv_type = "a9k"
svc_type = "svc"

logger = get_stdout_logger()
MAX_TRIES = 3
SLEEP_TIME = 10


class ArrayConnectionManager(object):

    def __init__(self, **kwargs):  # array_type, user, password, endpoint):
        self.array_mediator_class_dict = {xiv_type : XIVArrayMediator, svc_type : SVCArrayMediator}
        self.array_type = kwargs["array_type"]
#         self.user = user
#         self.password = password
        self.endpoint = kwargs["endpoint"]
        self.med_class_params = kwargs
        self.med_class = None
    
    def __enter__(self):
        logger.debug("in enter")
        
        counter = 0
        while counter < MAX_TRIES:
            try:
                arr_conncetion = self.get_array_connection()
                break
            except NoConnctionAvailableException as ex:
                logger.debug("sleeping : {}".format(counter))
                sleep(SLEEP_TIME)
                logger.debug("done sleeping : {} ".format(counter))
                counter += 1
                logger.debug("retrying. counter :  {}".format(counter))
                continue
        else:
            # get here if the while loop ran its course and no connection was established
            logger.debug("failed to get connection raising error. counter :  {}".format(counter))
            raise NoConnctionAvailableException(self.endpoint) 
        
        return arr_conncetion
        
    def __exit__(self, type, value, traceback):
        logger.debug("closing the connection")
        self.med_class.close()
        logger.debug("reducing the connection count")
        connection_lock.acquire(True)
        array_connections_dict[self.endpoint] -= 1
        logger.debug("removing the connection  : {}".format(array_connections_dict))
        connection_lock.release() 
    
    def get_array_connection(self):  # , array_type, user, password, endpoint):
        logger.debug("get array connection")
        med_class = self.array_mediator_class_dict[self.array_type]
        
        try:
            logger.debug('getting conncetion')
            try:
                # TODO:  when moving to python 3 there is probably an option to add timeout
                connection_lock.acquire(True)  # this is blocking forever 
            except Exception as ex :
                logger.exception(ex)

            logger.debug("got connection lock. array connection dict is: {}".format(array_connections_dict))
                
            if self.endpoint in  array_connections_dict:
                if array_connections_dict[self.endpoint] < med_class.CONNECTION_LIMIT:
                    logger.debug("adding new connection ")
                    array_connections_dict[self.endpoint] += 1
                    
                else:
                    logger.debug("failed to get connection. current connections: {}".format(array_connections_dict))
                    raise NoConnctionAvailableException(self.endpoint) 
            else:
                logger.debug("adding new connection to new endpoint : {}".format(self.endpoint))
                array_connections_dict[self.endpoint] = 1
                
            self.med_class = med_class(**self.med_class_params) 
            return self.med_class
        
        finally:
            logger.debug("Released adding connection lock")
            connection_lock.release()
        
        
