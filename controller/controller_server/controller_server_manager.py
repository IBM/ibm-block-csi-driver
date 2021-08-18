import time
from concurrent import futures

import grpc

from controller.common import settings
from controller.common.csi_logger import get_stdout_logger
from controller.controller_server.csi_controller_server import ControllerServicer
from controller.csi_general import csi_pb2_grpc


logger = get_stdout_logger()


class ControllerServerManager:
    def __init__(self, array_endpoint):
        self.endpoint = array_endpoint
        self.csi_servicer = ControllerServicer()

    def start_server(self):
        controller_server = grpc.server(futures.ThreadPoolExecutor(max_workers=settings.CSI_CONTROLLER_SERVER_WORKERS))

        csi_pb2_grpc.add_ControllerServicer_to_server(self.csi_servicer, controller_server)
        csi_pb2_grpc.add_IdentityServicer_to_server(self.csi_servicer, controller_server)

        # bind the server to the port defined above
        # controller_server.add_insecure_port('[::]:{}'.format(self.server_port))
        # controller_server.add_insecure_port('unix://{}'.format(self.server_port))
        controller_server.add_insecure_port(self.endpoint)

        logger.info("Controller version: {}".format(self.csi_servicer.get_identity_config("version")))

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