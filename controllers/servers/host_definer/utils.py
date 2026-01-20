"""docstring"""
from kubernetes import client
import json
import controllers.servers.host_definer.settings as host_definer_settings
import controllers.common.settings as common_settings
from controllers.common.node_info import Initiators
from controllers.common.csi_logger import get_stdout_logger

logger = get_stdout_logger()


def get_node_initiators_from_node(node_name):
    """docstring"""
    kubernetes_manager = client.CoreV1Api()
    try:
        k8s_node = kubernetes_manager.core_api.read_node(name=node_name)
    except Exception as ex:
        logger.warning(
            f"Failed to read node {node_name}, treating as no initiators",
            exc_info=ex,
        )
        return Initiators([], [], [])

    if k8s_node:
        return generate_node_initiators_from_k8s_node(k8s_node)

    return Initiators([], [], [])

def generate_node_initiators_from_k8s_node(k8s_node):
    "docstring"
    initiators_data = k8s_node.metadata.annotations.get('block.csi.ibm.com/node-initiators', "{}")
    return generate_node_initiators_from_string_data(initiators_data)


def generate_node_initiators_from_string_data(initiators_data):
    "docstring"
    initiators_data = json.loads(initiators_data)
    nvme_nqns = initiators_data.get("nvme", [])
    fc_wwns = initiators_data.get("fc", [])
    iscsi_iqns = initiators_data.get("iscsi", [])
    return Initiators(nvme_nqns, fc_wwns, iscsi_iqns)

def generate_io_group_from_labels(labels):
    """docstring"""
    io_group = ''
    for io_group_index in range(host_definer_settings.POSSIBLE_NUMBER_OF_IO_GROUP):
        label_content = labels.get(common_settings.IO_GROUP_LABEL_PREFIX + str(io_group_index))
        if label_content == host_definer_settings.TRUE_STRING:
            if io_group:
                io_group += common_settings.IO_GROUP_DELIMITER
            io_group += str(io_group_index)
    return io_group
