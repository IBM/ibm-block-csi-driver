import logging
import sys

_logger = None


def init_logger(log_level):
    global _logger
    _logger = logging.getLogger("storage_agent")
    _logger.setLevel(log_level)

    handler = logging.StreamHandler(sys.stdout)
    handler.setLevel(log_level)
    formatter = logging.Formatter(
        '%(asctime)s %(name)s: %(levelname)s [%(processName)s][%(process)d] %(pathname)s:%(lineno)d %(message)s'
    )
    handler.setFormatter(formatter)
    _logger.addHandler(handler)


def get_logger():
    global _logger
    if not _logger:
        init_logger(logging.DEBUG)
    return _logger
