import os
import time
from concurrent import futures

import grpc
from csi_general import csi_pb2_grpc, replication_pb2_grpc, volumegroup_pb2_grpc, fence_pb2_grpc

from controllers.common.config import config
from controllers.common.csi_logger import get_stdout_logger
from controllers.common.settings import CSI_CONTROLLER_SERVER_WORKERS
from controllers.servers.csi.addons_server import ReplicationControllerServicer
from controllers.servers.csi.csi_controller_server import CSIControllerServicer
from controllers.servers.csi.volume_group_server import VolumeGroupControllerServicer
from controllers.servers.csi.fence import FenceControllerServicer

logger = get_stdout_logger()


def get_max_workers_count():
    cpu_count = (os.cpu_count() or 1) + 4
    return CSI_CONTROLLER_SERVER_WORKERS if cpu_count < CSI_CONTROLLER_SERVER_WORKERS else None


class ControllerServerManager:
    def __init__(self, array_endpoint):
        self.endpoint = array_endpoint
        self.csi_servicer = CSIControllerServicer()
        self.replication_servicer = ReplicationControllerServicer()
        self.volume_group_servicer = VolumeGroupControllerServicer()
        self.fence_servicer = FenceControllerServicer()

    def start_server(self):
        max_workers = get_max_workers_count()
        controller_server = grpc.server(futures.ThreadPoolExecutor(max_workers=max_workers))

        csi_pb2_grpc.add_ControllerServicer_to_server(self.csi_servicer, controller_server)
        csi_pb2_grpc.add_IdentityServicer_to_server(self.csi_servicer, controller_server)
        replication_pb2_grpc.add_ControllerServicer_to_server(self.replication_servicer, controller_server)
        volumegroup_pb2_grpc.add_ControllerServicer_to_server(self.volume_group_servicer, controller_server)
        fence_pb2_grpc.add_FenceControllerServicer_to_server(self.fence_servicer, controller_server)

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
