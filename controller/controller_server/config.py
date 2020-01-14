from controller.csi_general import csi_pb2

SUPPORTED_FS_TYPES = ["ext4", "xfs"]

access_types = csi_pb2.VolumeCapability.AccessMode
SUPPORTED_ACCESS_MODE = [access_types.SINGLE_NODE_WRITER]

SECRET_USERNAME_PARAMETER = "username"
SECRET_PASSWORD_PARAMETER = "password"
SECRET_ARRAY_PARAMETER = "management_address"

PARAMETERS_POOL = "pool"
PARAMETERS_CAPABILITIES_SPACEEFFICIENCY = "SpaceEfficiency"
PARAMETERS_VOLUME_NAME_PREFIX = "volume_name_prefix"
PARAMETERS_SNAPSHOT_NAME_PREFIX = "snapshot_name_prefix"
PARAMETERS_CAPACITY_DELIMITER = "="
PARAMETERS_CAPABILITIES_DELIMITER = "="
PARAMETERS_OBJECT_ID_DELIMITER = ":"
PARAMETERS_NODE_ID_DELIMITER = ";"
PARAMETERS_FC_WWN_DELIMITER = ":"

SUPPORTED_CONNECTIVITY_TYPES = 2
