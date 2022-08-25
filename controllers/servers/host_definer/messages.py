SECRET_DOES_NOT_EXIST = 'Secret {} in namespace {} does not exist'
FAILED_TO_GET_SECRET = 'Failed to get Secret {} in namespace {}, go this error: {}'
CSI_NODE_DOES_NOT_EXIST = 'node {}, do not have csi node'
HOST_DEFINITION_DOES_NOT_EXIST = 'Host definition {} does not exists'
FAILED_TO_GET_CSI_NODES = 'Failed to get csi nodes, got: {}'
FAILED_TO_GET_CSI_NODE = 'Failed to get csi node {}, got: {}'
FAILED_TO_GET_NODES = 'Failed to get nodes, got: {}'
FAILED_TO_GET_STORAGE_CLASSES = 'Failed to get storage classes, got: {}'
FAILED_TO_GET_HOST_DEFINITION = "Failed to get host definition for node {}," \
    " with {} secret in {} namespace, go this error: {}"
FAILED_TO_GET_LIST_OF_HOST_DEFINITIONS = 'Could not get list of hostDefinitions, got: {}'
FAILED_TO_PATCH_HOST_DEFINITION = 'Failed to patch host definition {}, go this error: {}'
FAILED_TO_CREATE_HOST_DEFINITION = 'Failed to create host definition {}, go this error: {}'
FAILED_TO_SET_HOST_DEFINITION_STATUS = 'Failed to set host definition {} status, go this error: {}'
FAILED_TO_CREATE_EVENT_FOR_HOST_DEFINITION = 'Failed to create event for host definition {}, go this error: {}'
FAILED_TO_DELETE_HOST_DEFINITION = 'Failed to delete hostDefinition {}, got: {}'
FAILED_TO_UPDATE_NODE_LABEL = 'Failed to update node {} {} label, got: {}'
FAILED_TO_GET_NODE = 'Failed to get node {}, got: {}'
PATCHING_HOST_DEFINITION = 'Patching host definition: {}'
SET_HOST_DEFINITION_STATUS = 'Set host definition {} status to: {}'
VERIFY_HOST_DEFINITION_USING_EXPONENTIAL_BACKOFF = "Verifying host definition {}, using exponential backoff."\
    " number of retries left [{}]"
SET_HOST_DEFINITION_PHASE_TO_ERROR = 'Set host definition {} phase to error'
SECRET_HAS_BEEN_MODIFIED = 'Secret {} in namespace {}, has been modified'
NEW_STORAGE_CLASS = 'New storageClass {}'
HOST_ALREADY_ON_STORAGE_HOST_DEFINITION_READY = "Host {} is already on storage of {} secret in {} namespace,"\
    " detected host definition {} in Ready phase"
CREATING_NEW_HOST_DEFINITION = 'Creating host Definition: {}'
UNDEFINED_HOST = 'Undefine host {} from {} secret in {} namespace'
CREATE_EVENT_FOR_HOST_DEFINITION = 'Creating event : [{}] for host definition: {}'
NEW_KUBERNETES_NODE = 'New Kubernetes node {}, has csi IBM block'
ADD_LABEL_TO_NODE = 'Add {} label to node {}'
REMOVE_LABEL_FROM_NODE = 'Remove {} label from node {}'
FAILED_TO_LIST_DAEMON_SETS = 'Failed to list csi IBM block daemon set, got: {}'
FAILED_TO_LIST_PODS = 'Failed to list csi IBM block pods, got: {}'
FAILED_TO_GET_SECRET_EVENT = 'Failed to get secret {} in namespace {}'
HOST_DEFINITION_IN_DESIRED_STATE = "Stopping verifying host definition {}, using exponential backoff,"\
    " because it is in his wanted state"
DELETE_HOST_DEFINITION = 'Deleting host definition {}'