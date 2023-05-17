from controllers.common.settings import TOPOLOGY_IBM_BLOCK_PREFIX, CSI_PARAMETER_PREFIX, SECRET_NAME_SUFFIX
from controllers.tests.common.test_settings import HOST_NAME
import controllers.common.settings as common_settings

STORAGE_CLASS_DRIVERS_FIELD = 'drivers'
CSI_NODE_NODE_ID_FIELD = 'nodeID'
FAKE_SECRET = 'fake_secret'
FAKE_SECRET_NAMESPACE = 'fake_secret_namespace'
FAKE_NODE_NAME = 'fake_node_name'
FAKE_DAEMON_SET_NAME = 'fake_daemon_set_name'
FAKE_NODE_PODS_NAME = '{}-jdf5g'.format(FAKE_DAEMON_SET_NAME)
FAKE_SECRET_ARRAY = 'management_address'
FAKE_SECRET_PASSWORD = 'fake_password'
FAKE_SECRET_USER_NAME = 'fake_user_name'
FAKE_STORAGE_CLASS = 'fake_storage_class'
FAKE_CONNECTIVITY_TYPE = 'fake_connectivity_type'
FAKE_SYSTEM_ID = 'fake_system_id'
IQN = 'iqn.1994-05.com.redhat:686358c930fe'
WWPN = '34859340583048'
NQN = 'nqn.2014-08.org.nvmexpress:uuid:b57708c7-5bb6-46a0-b2af-9d824bf539e1'
FAKE_NODE_ID = '{};;;{}'.format(HOST_NAME, IQN)
FAKE_CSI_PROVISIONER = 'fake_csi_provisioner'
FAKE_LABEL = 'FAKE_LABEL'
FAKE_TOPOLOGY_LABEL = '{}/topology'.format(TOPOLOGY_IBM_BLOCK_PREFIX)
HOST_DEFINER_PATH = 'controllers.servers.host_definer'
HOST_DEFINER_WATCHER_PATH = '{}.watcher'.format(HOST_DEFINER_PATH)
HOST_DEFINER_RESOURCE_MANAGER_PATH = '{}.resource_manager'.format(HOST_DEFINER_PATH)
NODES_WATCHER_PATH = '{}.node_watcher'.format(HOST_DEFINER_WATCHER_PATH)
SECRET_WATCHER_PATH = '{}.secret_watcher'.format(HOST_DEFINER_WATCHER_PATH)
CSI_NODE_WATCHER_PATH = '{}.csi_node_watcher'.format(HOST_DEFINER_WATCHER_PATH)
STORAGE_CLASS_WATCHER_PATH = '{}.storage_class_watcher'.format(HOST_DEFINER_WATCHER_PATH)
HOST_DEFINITION_WATCHER_PATH = '{}.host_definition_watcher'.format(HOST_DEFINER_WATCHER_PATH)
UTILS_PATH = 'controllers.servers.host_definer.utils.utils'
SETTINGS_PATH = 'controllers.servers.host_definer.settings'
HOST_DEFINITION_MANAGER_PATH = '{}.host_definition'.format(HOST_DEFINER_RESOURCE_MANAGER_PATH)
SECRET_MANAGER_PATH = '{}.secret'.format(HOST_DEFINER_RESOURCE_MANAGER_PATH)
NODE_MANAGER_PATH = '{}.node'.format(HOST_DEFINER_RESOURCE_MANAGER_PATH)
RESOURCE_INFO_MANAGER_PATH = '{}.resource_info'.format(HOST_DEFINER_RESOURCE_MANAGER_PATH)
TYPES_PATH = 'controllers.servers.host_definer.types'
REQUEST_MANAGER_PATH = 'controllers.servers.host_definer.definition_manager.request'
DEFINITION_MANAGER_PATH = 'controllers.servers.host_definer.definition_manager.definition'
K8S_API_PATH = 'controllers.servers.host_definer.k8s.api'
FAKE_RESOURCE_VERSION = '495873498573'
FAKE_UID = '50345093486093'
EVENT_TYPE_FIELD = 'type'
EVENT_OBJECT_FIELD = 'object'
KUBERNETES_MANAGER_INIT_FUNCTIONS_TO_PATCH = ['_load_cluster_configuration', '_get_dynamic_client']
UPDATED_PODS = 'updated_number_scheduled'
POD_NODE_NAME_FIELD = 'node_name'
DESIRED_UPDATED_PODS = 'desired_number_scheduled'
METADATA_UID_FIELD = 'uid'
SECRET_DATA_FIELD = 'data'
FAIL_MESSAGE_FROM_STORAGE = 'fail_from_storage'
MESSAGE = 'Host defined successfully on the array'
HOST_DEFINITION_PENDING_VARS = {'HOST_DEFINITION_PENDING_RETRIES': 3,
                                'HOST_DEFINITION_PENDING_EXPONENTIAL_BACKOFF_IN_SECONDS': 0.2,
                                'HOST_DEFINITION_PENDING_DELAY_IN_SECONDS': 0.2}
STORAGE_CLASS_PROVISIONER_FIELD = 'provisioner'
STORAGE_CLASS_PARAMETERS_FIELD = 'parameters'
STORAGE_CLASS_SECRET_FIELD = '{}{}'.format(CSI_PARAMETER_PREFIX, SECRET_NAME_SUFFIX)
STORAGE_CLASS_SECRET_NAMESPACE_FIELD = '{}secret-namespace'.format(CSI_PARAMETER_PREFIX)
FAKE_PREFIX = 'fake-prefix'
IO_GROUP_ID_FIELD = 'id'
IO_GROUP_IDS = ['0', '2']
IO_GROUP_NAMES = ['io_grp0', 'io_grp2']
FAKE_STRING_IO_GROUP = common_settings.IO_GROUP_DELIMITER.join(IO_GROUP_IDS)
FAKE_STORAGE_CLASS_PARAMETERS = {
    STORAGE_CLASS_SECRET_FIELD: FAKE_SECRET,
    STORAGE_CLASS_SECRET_NAMESPACE_FIELD: FAKE_SECRET_NAMESPACE
}
CONNECTIVITY_TYPE_FIELD = 'connectivityType'
FAKE_FC_PORTS = ['532453845345', '532453845345']
IO_GROUP_LABEL_PREFIX = 'hostdefiner.block.csi.ibm.com/io-group-'
FAKE_SINGLE_IO_GROUP_STRING = '0'
FAKE_MULTIPLE_IO_GROUP_STRING = '0:1'
BASE64_STRING = 'eydmYWtlX2tleSc6ICdmYWtlX3ZhbHVlJ30K'
DECODED_BASE64_STRING = "{'fake_key': 'fake_value'}"
FAKE_ENCODED_CONFIG = {"config": BASE64_STRING}
FAKE_DECODED_CONFIG_STRING = {"config": DECODED_BASE64_STRING}
FAKE_DECODED_CONFIG = {"config": {'fake_key': 'fake_value'}}
ISCSI_CONNECTIVITY_TYPE = 'iscsi'
FAKE_TOPOLOGY_LABELS = {FAKE_TOPOLOGY_LABEL + '1': common_settings.TRUE_STRING,
                        FAKE_TOPOLOGY_LABEL + '2': common_settings.TRUE_STRING}
FAKE_SYSTEM_IDS_TOPOLOGIES = {FAKE_SYSTEM_ID: FAKE_TOPOLOGY_LABELS}
SECRET_SUPPORTED_TOPOLOGIES_PARAMETER = "supported_topologies"
