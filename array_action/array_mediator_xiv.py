from pyxcli.client import XCLIClient
from pyxcli import errors as xcli_errors
from concurrent.futures import ThreadPoolExecutor
from threading import Thread, Lock, BoundedSemaphore
import threading
from csi_logger import get_stdout_logger
import time
from array_action_types import ArrayVolume

connection_lock = Lock()
array_connections_dict = {}
logger = get_stdout_logger()

class XIVArrayMediator():
    ARRAY_TYPE = 'XIV'
    ARRAY_ACTIONS = {}

    BLOCK_SIZE_IN_BYTES = 512
    CONNECTION_LIMIT = 3
    MAX_CONNECTION_RETRY=3

    def __init__(self, **kwargs):#user, password, endpoint):
        self.user = kwargs["user"]
        self.password = kwargs["password"]
        self.endpoint = kwargs["endpoint"]
        self.client = None
        
        logger.debug("in init")
        self._connect()
         
   
    def _connect(self):
        logger.debug("connecting to endpoint")
        #TODO: remove the sleep!!!
        time.sleep(2)
        logger.debug("after sleep")
        try:
            self.client = XCLIClient.connect_multiendpoint_ssl(
                self.user,
                self.password,
                self.endpoint
            )
        
        except xcli_errors.CredentialsError:
            logger.debug("err1")
            raise Exception("connection credentials error")
        except xcli_errors.XCLIError:
            logger.debug("err2")
            raise Exception("connection error")

     
    def close(self):
        if self.client and self.client.is_connected():
            self.client.close()
        
    def _convert_volume_size(self, size):
        return size*1024*1024*1024
        
    def get_volume(self, vol_name):
        cli_volume = self.client.cmd.vol_list(vol=vol_name).as_single_element
        logger.debug(cli_volume)
        array_vol = ArrayVolume(self._convert_volume_size(int(cli_volume.size)))
        logger.debug("array volume :  {}".format(array_vol.size))
        return array_vol
