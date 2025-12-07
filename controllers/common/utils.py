import threading

# needed for getting k8s and ocp versions
#
import json
import os
import re
import urllib3

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


def _get_config_map_info():
    pass


def _default_callhome_metadata_aux():
    ch_info = []

    # Disable wanings for insecure https
    #
    urllib3.disable_warnings(urllib3.exceptions.InsecureRequestWarning)

    # Get token from a local file
    #
    token_path = "/var/run/secrets/kubernetes.io/serviceaccount/token"
    if not os.path.exists(token_path):
        logger.info("Not running inside a Kubernetes/OpenShift pod")
        return ''

    with open(token_path, encoding='utf-8') as token_file:
        token = token_file.read().strip()

    # Collect environment info and create URL and http opject
    #
    k8s_service_host = os.environ['KUBERNETES_SERVICE_HOST']
    k8s_service_port = os.environ['KUBERNETES_SERVICE_PORT']
    api_server = f"https://{k8s_service_host}:{k8s_service_port}"
    headers = {"Authorization": f"Bearer {token}"}
    http_client = urllib3.PoolManager(cert_reqs='NONE')

    # Get k8s version (works for k8s AND ocp)
    #
    req = http_client.request('GET', f"{api_server}/version", headers=headers)
    version_info = json.loads(req.data.decode('utf-8'))
    k8s_version = version_info.get("gitVersion", '')
    if k8s_version:
        ch_info.append(f"k8s:{k8s_version}")

    # To Getting OCP version is more complicated
    #
    req = http_client.request('GET', f"{api_server}/api/v1/nodes", headers=headers)
    result_str = req.data.decode('utf-8')

    # assumption OCP version is vX.YZ
    #
    ocp_match = re.search('redhat-operator-index:(v\\d{1}\\.\\d{2})', result_str)

    # for k8s only 'ocp_version' would be None
    #
    ocp_version = None
    if ocp_match and ocp_match.groups():
        ocp_version = ocp_match.groups[0]

    if ocp_version:
        ch_info.append(f"ocp:{ocp_version}")

    callhome_metadata = ", ".join(ch_info)
    return callhome_metadata


# just a wrapper around real code above
#
def default_callhome_metadata():
    try:
        return _default_callhome_metadata_aux()
    except Exception as e:
        logger.error("Could not fetch metadata: {}".format(e))
        return ''
