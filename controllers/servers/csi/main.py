from argparse import ArgumentParser
from threading import Thread

from controllers.common.csi_logger import set_log_level
from controllers.servers.csi.controller_server_manager import ControllerServerManager
from controllers.servers.csi.csi_addons_server.csi_addons_server_manager import CSIAddonsServerManager


def main():
    parser = ArgumentParser()
    parser.add_argument("-e", "--csi-endpoint", dest="endpoint", help="grpc endpoint")
    parser.add_argument("-a", "--csi-addons-endpoint", dest="addonsendpoint", help="CSI-Addons grpc endpoint")
    parser.add_argument("-l", "--loglevel", dest="loglevel", help="log level")
    arguments = parser.parse_args()

    set_log_level(arguments.loglevel)

    csi_controller_server_manager = ControllerServerManager(arguments.endpoint)
    csi_addons_server_manager = CSIAddonsServerManager(arguments.addonsendpoint)
    _start_servers(csi_controller_server_manager, csi_addons_server_manager)


def _start_servers(csi_controller_server_manager, csi_addons_server_manager):
    servers = (
        csi_controller_server_manager.start_server,
        csi_addons_server_manager.start_server)
    for server_function in servers:
        thread = Thread(target=server_function,)
        thread.start()


if __name__ == '__main__':
    main()
