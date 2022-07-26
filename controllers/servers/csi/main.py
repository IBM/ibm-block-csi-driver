from argparse import ArgumentParser

from controllers.common.csi_logger import set_log_level
from controllers.servers.csi.controller_server_manager import ControllerServerManager


def main():
    parser = ArgumentParser()
    parser.add_argument("-e", "--csi-endpoint", dest="endpoint", help="grpc endpoint")
    parser.add_argument("-l", "--loglevel", dest="loglevel", help="log level")
    arguments = parser.parse_args()

    set_log_level(arguments.loglevel)

    server_manager = ControllerServerManager(arguments.endpoint)
    server_manager.start_server()


if __name__ == '__main__':
    main()
