import logging
import sys


def get_stdout_logger():
    csi_logger = logging.getLogger("csi_logger")

    if not getattr(csi_logger, 'handler_set', None):
        csi_logger.setLevel(logging.DEBUG)
        handler = logging.StreamHandler(sys.stdout)
        handler.setLevel(logging.DEBUG)
        formatter = logging.Formatter(
            '%(asctime)s %(levelname)s\t[%(thread)d] (%(filename)s:%(funcName)s:%(lineno)d) - %(message)s')
        handler.setFormatter(formatter)
        csi_logger.addHandler(handler)

        csi_logger.handler_set = True

    return csi_logger
