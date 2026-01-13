"""docstring"""
from kubernetes import client
import controllers.servers.host_definer.settings as host_definer_settings
import controllers.common.settings as common_settings


def get_node_initiators_from_node(node_name):
    """docstring"""
    core_api = client.CoreV1Api()
    k8s_node = core_api.read_node(name=node_name)
    node_annotations = k8s_node.metadata.annotations
    node_initiators = node_annotations.get(common_settings.NODE_INITIATORS_FIELD, "{}")
    return node_initiators


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
