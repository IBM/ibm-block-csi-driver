import threading

from controllers.common import settings
from controllers.common.csi_logger import get_stdout_logger
from controllers.servers import messages

logger = get_stdout_logger()


def set_current_thread_name(name):
    """
    Sets current thread name if ame not None or empty string

    Args:
        name : name to set
    """
    if name:
        current_thread = threading.current_thread()
        current_thread.setName(name)


def string_to_array(str_val, separator):
    """
    Args
        str_val : string value
        separator : string separator
    Return
        List as splitted string by separator after stripping whitespaces from each element
    """
    if not str_val:
        return []
    res = str_val.split(separator)
    return res


def get_node_id_info(node_id):
    logger.debug("getting node info for node id : {0}".format(node_id))
    split_node = node_id.split(settings.PARAMETERS_NODE_ID_DELIMITER)
    hostname, nvme_nqn, fc_wwns, iscsi_iqn = "", "", "", ""
    if len(split_node) == 4:
        hostname, nvme_nqn, fc_wwns, iscsi_iqn = split_node
    elif len(split_node) == 3:
        hostname, nvme_nqn, fc_wwns = split_node
    elif len(split_node) == 2:
        hostname, nvme_nqn = split_node
    else:
        raise ValueError(messages.WRONG_FORMAT_MESSAGE.format("node id"))
    logger.debug("node name : {0}, nvme_nqn: {1}, fc_wwns : {2}, iscsi_iqn : {3} ".format(
        hostname, nvme_nqn, fc_wwns, iscsi_iqn))
    return hostname, nvme_nqn, fc_wwns, iscsi_iqn
