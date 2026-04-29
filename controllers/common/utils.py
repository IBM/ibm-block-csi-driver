import threading

# needed for getting k8s and ocp versions
#
import json
import os
import re
import base64
import zlib
import platform

import urllib3

from controllers.common.csi_logger import get_stdout_logger

logger = get_stdout_logger()


def set_current_thread_name(name):
    """
    Sets current thread name if ame not None or empty string

    Args:
        name : name to set
    """
    if name:
        current_thread = threading.current_thread()
        current_thread.name = name


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


def _get_config_map_info():
    result = {}

    # Config map for node
    #
    cfgmap = os.environ.get('CSI_NODE_CONFIG')
    if not cfgmap or cfgmap == 'null':
        result['csi_node_config'] = {}
    else:
        result['csi_node_config'] = json.loads(cfgmap)

    # Config map for hostdefiner
    #
    cfgmap = os.environ.get('CSI_HOSTDEFINER_CONFIG')
    if not cfgmap or cfgmap == 'null':
        result['csi_hd_config'] = {}
    else:
        result['csi_hd_config'] = json.loads(cfgmap)

    return result


def _encode_to_base64(data, max_size=1024):
    # Convert to Compact JSON
    #
    json_bytes = json.dumps(data, separators=(',', ':')).encode('utf-8')

    # Base64 encode
    #
    b64_encoded = base64.b64encode(json_bytes)

    # If we are within the limit - just return
    #
    if len(b64_encoded) <= max_size:
        return b64_encoded.decode('utf-8')

    # We are too large, compress with zlib first with highest compression
    #
    compressed = zlib.compress(json_bytes, level=9)
    b64_compressed = base64.b64encode(compressed)

    # If now we are within limit, just return
    #
    if len(b64_compressed) <= max_size:
        return b64_compressed.decode('utf-8')

    # OK, nothing helps log error
    #
    logger.error("Encoded data too large: {} bytes max allowed: {} bytes "
                 "(even after zlib compression)".format(len(b64_compressed), max_size))

    # Replace user config map with error information and re-encode
    #
    del data["config_map"]
    data["config_map"] = {'error': 'CONFIG_MAP_TOO_LARGE'}
    return _encode_to_base64(data)


def _default_callhome_metadata_aux():
    ch_info = {}

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
        ch_info["k8s"] = k8s_version

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
    if ocp_match:
        ocp_version = ocp_match.group(1)

    if ocp_version:
        ch_info["ocp"] = ocp_version

    # Processor arch: one of 'x86_64', 's390x', 'ppc64'
    #
    ch_info["arch"] = platform.machine()

    # Any user changes for config map
    #
    ch_info["config_map"] = _get_config_map_info()

    callhome_metadata = _encode_to_base64(ch_info)
    return callhome_metadata


# just a wrapper around real code above
#
def default_callhome_metadata():
    ch_metadata = ''

    try:
        ch_metadata = _default_callhome_metadata_aux()
    except Exception as e:
        logger.error("Could not fetch metadata: {}".format(e))

    logger.info("CH Metadata: \"{}\"".format(ch_metadata))
    return ch_metadata
