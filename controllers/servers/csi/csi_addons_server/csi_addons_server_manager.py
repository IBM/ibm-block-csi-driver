import os
import time
from concurrent import futures

import grpc
from csi_general import identity_pb2_grpc, replication_pb2_grpc

from controllers.common.csi_logger import get_stdout_logger
from controllers.common.settings import CSI_CONTROLLER_SERVER_WORKERS
from controllers.servers.csi.csi_addons_server.replication_controller_servicer import ReplicationControllerServicer
from controllers.servers.csi.csi_addons_server.identity_controller_servicer import IdentityControllerServicer

logger = get_stdout_logger()


def get_max_workers_count():
    cpu_count = (os.cpu_count() or 1) + 4
    return CSI_CONTROLLER_SERVER_WORKERS if cpu_count < CSI_CONTROLLER_SERVER_WORKERS else None


class CSIAddonsServerManager:
    def __init__(self, csi_addons_endpoint):
        self.endpoint = csi_addons_endpoint
        self.replication_servicer = ReplicationControllerServicer()
        self.identity_servicer = IdentityControllerServicer()

    def start_server(self):
        max_workers = get_max_workers_count()
        csi_addons_server = grpc.server(futures.ThreadPoolExecutor(max_workers=max_workers))

        replication_pb2_grpc.add_ControllerServicer_to_server(self.replication_servicer, csi_addons_server)
        identity_pb2_grpc.add_IdentityServicer_to_server(self.identity_servicer, csi_addons_server)

        # bind the server to the port defined above
        # controller_server.add_insecure_port('[::]:{}'.format(self.server_port))
        # controller_server.add_insecure_port('unix://{}'.format(self.server_port))
        csi_addons_server.add_insecure_port(self.endpoint)

        # start the server
        logger.debug("Listening for connections on endpoint address: {}".format(self.endpoint))

        csi_addons_server.start()
        logger.debug('CSI Addons Server running ...')

        try:
            while True:
                time.sleep(60 * 60 * 60)
        except KeyboardInterrupt:
            csi_addons_server.stop(0)
            logger.debug('CSI Addons Server Stopped ...')
