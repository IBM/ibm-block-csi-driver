from controller.csi_general import csi_pb2

SUPPORTED_FS_TYPES = ["ext4", "xfs"]

access_mode = csi_pb2.VolumeCapability.AccessMode
SUPPORTED_ACCESS_MODE = [access_mode.SINGLE_NODE_WRITER]

# VolumeCapabilities fields which specify if it is volume with fs or raw block volume
VOLUME_CAPABILITIES_FIELD_ACCESS_TYPE_MOUNT = 'mount'
VOLUME_CAPABILITIES_FIELD_ACCESS_TYPE_BLOCK = 'block'

SECRET_USERNAME_PARAMETER = "username"
SECRET_PASSWORD_PARAMETER = "password"
SECRET_ARRAY_PARAMETER = "management_address"
SECRET_CONFIG_PARAMETER = "config"
SECRET_SUPPORTED_TOPOLOGIES_PARAMETER = "supported_topologies"
# SECRET_UID_MAX_LENGTH = max string response - length of volume wwn - max length of array type key - delimiters
SECRET_UID_MAX_LENGTH = 128 - 32 - 4 - 2
SECRET_VALIDATION_REGEX = '^[a-zA-Z0-9][a-zA-Z0-9-_.]*[a-zA-Z0-9]$'

PARAMETERS_POOL = "pool"
PARAMETERS_BY_SYSTEM = "by_system"
PARAMETERS_SPACE_EFFICIENCY = "SpaceEfficiency"
PARAMETERS_VOLUME_NAME_PREFIX = "volume_name_prefix"
PARAMETERS_SNAPSHOT_NAME_PREFIX = "snapshot_name_prefix"
PARAMETERS_CAPACITY_DELIMITER = "="
PARAMETERS_CAPABILITIES_DELIMITER = "="
PARAMETERS_OBJECT_ID_DELIMITER = ":"
PARAMETERS_NODE_ID_DELIMITER = ";"
PARAMETERS_FC_WWN_DELIMITER = ":"
PARAMETERS_TOPOLOGY_DELIMITER = "/"
PARAMETERS_ARRAY_ADDRESSES_DELIMITER = ","

REQUEST_ACCESSIBILITY_REQUIREMENTS_FIELD = "accessibility_requirements"

SUPPORTED_CONNECTIVITY_TYPES = 2

SNAPSHOT_TYPE_NAME = "snapshot"
VOLUME_TYPE_NAME = "volume"
VOLUME_SOURCE_ID_FIELDS = {SNAPSHOT_TYPE_NAME: 'snapshot_id', VOLUME_TYPE_NAME: 'volume_id'}

TOPOLOGY_PREFIXES = ["topology.kubernetes.io", "topology.block.csi.ibm.com"]

MINIMUM_VOLUME_ID_PARTS = 2
MAXIMUM_VOLUME_ID_PARTS = 3
