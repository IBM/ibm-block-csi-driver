import time
from argparse import ArgumentParser
from concurrent import futures

import grpc

from controller.common import settings
from controller.common.csi_logger import get_stdout_logger, set_log_level
from controller.controller_server.csi_controller_server import CSIControllerServicer
from controller.csi_general import csi_pb2_grpc

logger = get_stdout_logger()


class ControllerServicer(CSIControllerServicer):
    """
    gRPC server for Digestor Service
    """

    def __init__(self, array_endpoint):
        super().__init__()
        self.endpoint = array_endpoint

    def start_server(self):
        controller_server = grpc.server(futures.ThreadPoolExecutor(max_workers=settings.CSI_CONTROLLER_SERVER_WORKERS))

        csi_pb2_grpc.add_ControllerServicer_to_server(self, controller_server)
        csi_pb2_grpc.add_IdentityServicer_to_server(self, controller_server)

        # bind the server to the port defined above
        # controller_server.add_insecure_port('[::]:{}'.format(self.server_port))
        # controller_server.add_insecure_port('unix://{}'.format(self.server_port))
        controller_server.add_insecure_port(self.endpoint)

        logger.info("Controller version: {}".format(self._get_identity_config("version")))

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


def main():
    parser = ArgumentParser()
    parser.add_argument("-e", "--csi-endpoint", dest="endpoint", help="grpc endpoint")
    parser.add_argument("-l", "--loglevel", dest="loglevel", help="log level")
    arguments = parser.parse_args()

    set_log_level(arguments.loglevel)

    controller_servicer = ControllerServicer(arguments.endpoint)
    controller_servicer.start_server()


if __name__ == '__main__':
    main()
