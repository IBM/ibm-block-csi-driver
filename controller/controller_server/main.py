from argparse import ArgumentParser

from controller.common.csi_logger import set_log_level
from controller.controller_server.controller_servicer import ControllerServicer


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
