VALIDATION_EXCEPTION_MESSAGE = "Validation error has occurred : {0}"

WRONG_ID_FORMAT_MESSAGE = "Wrong {0} id format : {1}"

OBJECT_ALREADY_PROCESSING_MESSAGE = "object {0} is already processing. request cannot be completed."

# validation error messages
INVALID_SECRET_CONFIG_MESSAGE = "got an invalid secret config"
INSUFFICIENT_DATA_TO_CHOOSE_A_STORAGE_SYSTEM_MESSAGE = "insufficient data to choose a storage system"
NO_SYSTEM_MATCH_REQUESTED_TOPOLOGIES = "no system match requested topologies: {}"
SECRET_MISSING_CONNECTION_INFO_MESSAGE = "secret is missing connection info"
SECRET_MISSING_TOPOLOGIES_MESSAGE = "secret is missing topologies"
INVALID_SYSTEM_ID_MESSAGE = "got an invalid system id: {}, validation regex: {}"
INVALID_JSON_PARAMETER_MESSAGE = "got an invalid json parameter: {}, error: {}."
INVALID_REPLICATION_COPY_TYPE_MESSAGE = "got an invalid copy type: {}"
SECRET_MISSING_MESSAGE = 'secret is missing'
CAPABILITIES_NOT_SET_MESSAGE = "capabilities were not set"
UNSUPPORTED_FS_TYPE_MESSAGE = "unsupported fs_type : {}"
UNSUPPORTED_MOUNT_FLAGS_MESSAGE = "mount_flags is unsupported"
UNSUPPORTED_VOLUME_ACCESS_TYPE_MESSAGE = "unsupported volume access type"
UNSUPPORTED_ACCESS_MODE_MESSAGE = "unsupported access mode : {}"
NAME_SHOULD_NOT_BE_EMPTY_MESSAGE = 'name should not be empty'
VOLUME_ID_SHOULD_NOT_BE_EMPTY_MESSAGE = 'volume id should not be empty'
SNAPSHOT_ID_SHOULD_NOT_BE_EMPTY_MESSAGE = 'snapshot id should not be empty'
SIZE_SHOULD_NOT_BE_NEGATIVE_MESSAGE = 'size should not be negative'
NO_CAPACITY_RANGE_MESSAGE = 'no capacity range set'
POOL_IS_MISSING_MESSAGE = 'pool parameter is missing.'
POOL_SHOULD_NOT_BE_EMPTY_MESSAGE = 'pool should not be empty'
WRONG_FORMAT_MESSAGE = '{} has wrong format'
READONLY_NOT_SUPPORTED_MESSAGE = 'readonly parameter is not supported'
VOLUME_SOURCE_ID_IS_MISSING = 'volume source {0} id is missing'
SNAPSHOT_SOURCE_VOLUME_ID_IS_MISSING = 'snapshot source volume id is missing'
PARAMETER_LENGTH_IS_TOO_LONG = '{} parameter: {} is too long, max length is: {}'
VOLUME_CLONING_NOT_SUPPORTED_MESSAGE = 'volume cloning is not supported'
VOLUME_CONTEXT_NOT_MATCH_VOLUME_MESSAGE = 'volume context: {0} does not match existing volume context: {1}'
SPACE_EFFICIENCY_NOT_MATCH_VOLUME_MESSAGE = 'space efficiency: {0}' \
                                            ' does not match existing volume space efficiency: {1}'
POOL_NOT_MATCH_VOLUME_MESSAGE = 'pool name: {0} does not match existing volume pool name: {1}'
PREFIX_NOT_MATCH_VOLUME_MESSAGE = 'prefix: {0} does not match existing volume name: {1}'
REQUIRED_BYTES_MISMATCH_MESSAGE = "required bytes : {0} does not match the source volume required bytes : {1}"

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
VERIFY_HOST_DEFINITION_USING_EXPONENTIAL_BACKOFF = 'Verifying host definition {}, using exponential backoff'
SET_HOST_DEFINITION_PHASE_TO_ERROR = 'Set host definition {} phase to error'
SECRET_HAS_BEEN_MODIFIED = 'Secret {} in namespace {}, has been modified'
NEW_STORAGE_CLASS = 'New storageClass {}'
HOST_ALREADY_ON_STORAGE_HOST_DEFINITION_READY = "Host {} is already on storage of {} secret in {} namespace,"\
    " detected host definition {} in Ready phase"
CREATING_NEW_HOST_DEFINITION = 'Creating host Definition: {}'
VERIFY_HOST_IS_UNDEFINED = 'Verifying that host {} is undefined from {} secret in {} namespace'
CREATE_EVENT_FOR_HOST_DEFINITION = 'Creating event : [{}] for host definition: {}'
NEW_KUBERNETES_NODE = 'New Kubernetes node {}, has csi IBM block'
ADD_LABEL_TO_NODE = 'Add {} label to node {}'
REMOVE_LABEL_FROM_NODE = 'Remove {} label from node {}'
FAILED_TO_LIST_DAEMON_SETS = 'Failed to list csi IBM block daemon set, got: {}'
FAILED_TO_LIST_PODS = 'Failed to list csi IBM block pods, got: {}'
