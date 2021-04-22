import logging
ENTRY = '%(asctime)s %(levelname)s\t[%(thread)d] [%(threadName)s] (%(filename)s:%(funcName)s:%(lineno)d) - %(message)s'


def initialize_logger(log_level="DEBUG"):
    logging.basicConfig(format=ENTRY, level=log_level)
