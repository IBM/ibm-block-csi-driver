import logging
import sys

log_level = "DEBUG"
ENTRY = '%(asctime)s %(levelname)s\t[%(thread)d] [%(threadName)s] (%(filename)s:%(funcName)s:%(lineno)d) - %(message)s'


def get_stdout_logger():
    csi_logger = logging.getLogger("csi_logger")

    if not getattr(csi_logger, 'handler_set', None):
        global log_level
        csi_logger.setLevel(log_level)
        handler = logging.StreamHandler(sys.stdout)
        handler.setLevel(logging.DEBUG)
        formatter = logging.Formatter(ENTRY)
        handler.setFormatter(formatter)
        csi_logger.addHandler(handler)

        csi_logger.handler_set = True

    return csi_logger


def set_log_level(log_level_to_set):
    """
    In order to set non-default log level this function should be called before first cal of get_stdout_logger
    :param log_level_to_set:
    """
    global log_level
    if log_level_to_set:
        log_level = log_level_to_set.upper()
