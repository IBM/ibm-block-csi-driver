import os
from argparse import ArgumentParser
from concurrent import futures
from threading import Thread

import grpc
from csi_general import csi_pb2_grpc, volumegroup_pb2_grpc, identity_pb2_grpc, replication_pb2_grpc, fence_pb2_grpc

from controllers.common.csi_logger import set_log_level
from controllers.common.settings import CSI_CONTROLLER_SERVER_WORKERS
from controllers.servers.csi.controller_server.csi_controller_server import CSIControllerServicer
from controllers.servers.csi.common_servers.volume_group_server import VolumeGroupControllerServicer
from controllers.servers.csi.csi_addons_server.fence_controller_servicer import FenceControllerServicer
from controllers.servers.csi.csi_addons_server.identity_controller_servicer import IdentityControllerServicer
from controllers.servers.csi.csi_addons_server.replication_controller_servicer import ReplicationControllerServicer
from controllers.servers.csi.server_manager import ServerManager


def main():
    parser = ArgumentParser()
    parser.add_argument("-e", "--csi-endpoint", dest="endpoint", help="grpc endpoint")
    parser.add_argument("-a", "--csi-addons-endpoint", dest="addonsendpoint", help="CSI-Addons grpc endpoint")
    parser.add_argument("-l", "--loglevel", dest="loglevel", help="log level")
    arguments = parser.parse_args()

    set_log_level(arguments.loglevel)
    controller_server = _create_grpc_server()
    csi_addons_server = _create_grpc_server()

    csi_controller_server_manager = ServerManager(arguments.endpoint, "Controller",
                                                  _add_csi_controller_servicers(controller_server))
    csi_addons_server_manager = ServerManager(arguments.addonsendpoint, "CSI Addons",
                                              _add_csi_addons_servicers(csi_addons_server))
    _start_servers(csi_controller_server_manager, csi_addons_server_manager)


def _create_grpc_server():
    max_workers = _get_max_workers_count()
    return grpc.server(futures.ThreadPoolExecutor(max_workers=max_workers))


def _get_max_workers_count():
    cpu_count = (os.cpu_count() or 1) + 4
    return CSI_CONTROLLER_SERVER_WORKERS if cpu_count < CSI_CONTROLLER_SERVER_WORKERS else None


def _add_csi_controller_servicers(controller_server):
    csi_servicer = CSIControllerServicer()
    volume_group_servicer = VolumeGroupControllerServicer()
    csi_pb2_grpc.add_ControllerServicer_to_server(csi_servicer, controller_server)
    csi_pb2_grpc.add_IdentityServicer_to_server(csi_servicer, controller_server)
    volumegroup_pb2_grpc.add_ControllerServicer_to_server(volume_group_servicer, controller_server)
    return controller_server


def _add_csi_addons_servicers(csi_addons_server):
    replication_servicer = ReplicationControllerServicer()
    identity_servicer = IdentityControllerServicer()
    fence_servicer = FenceControllerServicer()
    volume_group_servicer = VolumeGroupControllerServicer()
    replication_pb2_grpc.add_ControllerServicer_to_server(replication_servicer, csi_addons_server)
    identity_pb2_grpc.add_IdentityServicer_to_server(identity_servicer, csi_addons_server)
    fence_pb2_grpc.add_FenceControllerServicer_to_server(fence_servicer, csi_addons_server)
    volumegroup_pb2_grpc.add_ControllerServicer_to_server(volume_group_servicer, csi_addons_server)
    return csi_addons_server


def _start_servers(csi_controller_server_manager, csi_addons_server_manager):
    servers = (
        csi_controller_server_manager.start_server,
        csi_addons_server_manager.start_server)
    for server_function in servers:
        thread = Thread(target=server_function, )
        thread.start()


if __name__ == '__main__':
    main()
