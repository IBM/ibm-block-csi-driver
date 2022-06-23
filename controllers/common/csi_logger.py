import logging
import sys

logger_properties = {
    'log_level': 'DEBUG',
    'entry': '%(asctime)s %(levelname)s\t[%(thread)d] [%(threadName)s] '
             '(%(filename)s:%(funcName)s:%(lineno)d) - %(message)s'
}


def get_stdout_logger():
    csi_logger = logging.getLogger("csi_logger")

    if not getattr(csi_logger, 'handler_set', None):
        csi_logger.setLevel(logger_properties['log_level'])
        handler = logging.StreamHandler(sys.stdout)
        handler.setLevel(logging.DEBUG)
        formatter = logging.Formatter(logger_properties['entry'])
        handler.setFormatter(formatter)
        csi_logger.addHandler(handler)

        csi_logger.handler_set = True

    return csi_logger


def set_log_level(log_level_to_set):
    """
    In order to set non-default log level this function should be called before first cal of get_stdout_logger
    :param log_level_to_set:
    """
    if log_level_to_set:
        logger_properties['log_level'] = log_level_to_set.upper()
        csi_logger = logging.getLogger("csi_logger")
        csi_logger.setLevel(logger_properties['log_level'])
