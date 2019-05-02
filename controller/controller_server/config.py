from controller.csi_general import csi_pb2

SUPPORTED_FS_TYPES = ["ext4", "xsf"]
 
access_types = csi_pb2.VolumeCapability.AccessMode
SUPPORTED_ACCESS_MODE = [access_types.SINGLE_NODE_WRITER]

SECRET_USERNAME_PARAMETER = "username"
SECRET_PASSWORD_PARAMETER = "password"
SECRET_ARRAY_PARAMETER = "management_address"

PARAMETERS_CAPACITY = "capacity"
PARAMETERS_CAPABILITIES = "capabilities"
PARAMETERS_PREFIX = "volume_name_prefix"

