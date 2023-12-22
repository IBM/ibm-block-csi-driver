import time

from controllers.common.config import config
from controllers.common.csi_logger import get_stdout_logger

logger = get_stdout_logger()


class ServerManager:
    def __init__(self, array_endpoint, server_type, grpc_server):
        self.endpoint = array_endpoint
        self.server_type = server_type
        self.grpc_server = grpc_server

    def start_server(self):
        # bind the server to the port defined above
        # grpc_server.add_insecure_port('[::]:{}'.format(self.server_port))
        # grpc_server.add_insecure_port('unix://{}'.format(self.server_port))
        self.grpc_server.add_insecure_port(self.endpoint)

        logger.info("{} version: {}".format(self.server_type, config.identity.version))

        # start the server
        logger.debug("Listening for connections on endpoint address: {}".format(self.endpoint))

        self.grpc_server.start()
        logger.debug('{} Server running ...'.format(self.server_type))

        try:
            while True:
                time.sleep(60 * 60 * 60)
        except KeyboardInterrupt:
            self.grpc_server.stop(0)
            logger.debug('Controller Server Stopped ...')
