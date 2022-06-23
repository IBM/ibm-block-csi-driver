import time
from concurrent import futures

import grpc
from csi_general import csi_pb2_grpc
from csi_general import replication_pb2_grpc

from controllers.common.config import config
from controllers.common.csi_logger import get_stdout_logger
from controllers.servers.csi.addons_server import ReplicationControllerServicer
from controllers.servers.csi.csi_controller_server import CSIControllerServicer

logger = get_stdout_logger()


class ControllerServerManager:
    def __init__(self, array_endpoint):
        self.endpoint = array_endpoint
        self.csi_servicer = CSIControllerServicer()
        self.replication_servicer = ReplicationControllerServicer()

    def start_server(self):
        controller_server = grpc.server(futures.ThreadPoolExecutor())

        csi_pb2_grpc.add_ControllerServicer_to_server(self.csi_servicer, controller_server)
        csi_pb2_grpc.add_IdentityServicer_to_server(self.csi_servicer, controller_server)
        replication_pb2_grpc.add_ControllerServicer_to_server(self.replication_servicer, controller_server)

        # bind the server to the port defined above
        # controller_server.add_insecure_port('[::]:{}'.format(self.server_port))
        # controller_server.add_insecure_port('unix://{}'.format(self.server_port))
        controller_server.add_insecure_port(self.endpoint)

        logger.info("Controller version: {}".format(config.identity.version))

        # start the server
        logger.debug("Listening for connections on endpoint address: {}".format(self.endpoint))

        controller_server.start()
        logger.debug('Controller Server running ...')

        try:
            while True:
                time.sleep(60 * 60 * 60)
        except KeyboardInterrupt:
            controller_server.stop(0)
            logger.debug('Controller Server Stopped ...')
