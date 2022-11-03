NO_CONNECTION_AVAILABLE_EXCEPTION_MESSAGE = "Currently no connection is available to endpoint: {0}"

CREDENTIALS_ERROR_MESSAGE = "Credential error has occurred while connecting to endpoint : {0} "

STORAGE_MANAGEMENT_IPS_NOT_SUPPORT_ERROR_MESSAGE = "Invalid Management IP for SVC : {0} "

OBJECT_NOT_FOUND_ERROR_MESSAGE = "Object was not found : {0} "

VOLUME_NAME_BELONGS_TO_SNAPSHOT_ERROR_MESSAGE = "Volume not found. Snapshot with the same id exists. \
                                                         Name : {0} , array : {1}"

POOL_DOES_NOT_MATCH_SPACE_EFFICIENCY_MESSAGE = "Pool : {0} does not match the following space efficiency : {1} . " \
                                               "error : {2}"

SPACE_EFFICIENCY_NOT_SUPPORTED_MESSAGE = "space efficiency is not supported : {0} "

SPACE_EFFICIENCY_MISMATCH_MESSAGE = "space efficiency : {0}" \
                                    " does not match the source volume space efficiency : {1}"

VIRT_SNAPSHOT_FUNCTION_NOT_SUPPORTED_MESSAGE = "Snapshot function is enabled but not supported with object : {0} "

VOLUME_ALREADY_EXISTS_MESSAGE = "Volume already exists : {0} , array : {1}"

POOL_DOES_NOT_EXIST_MESSAGE = "Pool does not exist: {0} , array : {1}"

POOL_PARAMETER_IS_MISSING = "Pool parameter is mandatory in {0}"

FAILED_TO_FIND_STORAGE_SYSTEM_TYPE_MESSAGE = "Could not identify the type for endpoint: {0} "

PERMISSION_DENIED_ERROR_MESSAGE = "Permission was denied to operation : {0}"

MULTIPLE_HOSTS_FOUND_ERROR_MESSAGE = "Multiple hosts found for port(s): {0}. hosts are : {1}"

HOST_NOT_FOUND_ERROR_MESSAGE = "Host for node: {0} was not found, ensure all host ports are configured on storage"

NO_AVAILABLE_LUN_ERROR_MESSAGE = "No available lun was found for host : {0}"

LUN_ALREADY_IN_USE_MESSAGE = "Lun : {0} is already mapped for host : {1}"

MAPPING_ERROR_MESSAGE = "Mapping error has occurred while mapping volume : {0} to host : {1}. error : {2}"

VOLUME_ALREADY_UNMAPPED_MESSAGE = "Volume: {0} is already unmapped."

UNMAPPING_ERROR_MESSAGE = "Unmapping error has occurred for volume : {0} and host : {1}. error : {2}"

VOLUME_ALREADY_MAPPED_TO_DIFFERENT_HOSTS_ERROR_MESSAGE = "Volume is already mapped to different hosts {0}"

UNSUPPORTED_STORAGE_VERSION_ERROR_MESSAGE = ('Unsupported storage system microcode version {}, '
                                             'the version should not be lower than {}')

VOLUME_CREATION_ERROR_MESSAGE = 'Failed to create volume {}'

VOLUME_DELETION_ERROR_MESSAGE = 'Failed to delete volume {}'

NO_ISCSI_TARGETS_FOUND_ERROR_MESSAGE = "Could not find iSCSI targets for endpoint: {0}"

UNSUPPORTED_CONNECTIVITY_TYPE_ERROR_MESSAGE = "Unsupported connectivity type: {0}"

SNAPSHOT_NOT_FOUND_ERROR_MESSAGE = "Snapshot was not found : {0} "

SNAPSHOT_ALREADY_EXISTS_ERROR_MESSAGE = "Snapshot already exists : {0} , array : {1}"

HOST_ALREADY_EXISTS_ERROR_MESSAGE = "Host already exists : {0} , array : {1}"

EXPECTED_SNAPSHOT_BUT_FOUND_VOLUME_ERROR_MESSAGE = "Could not find info about the source of: {0}, array: {1}"

SNAPSHOT_WRONG_VOLUME_ERROR_MESSAGE = "Snapshot {0} exists but it is of Volume {1} and not {2}"

OBJECT_IS_STILL_IN_USE_ERROR_MESSAGE = "Object {0} is still in use by {1}"

INVALID_CLI_RESPONSE_ERROR_MESSAGE = "Invalid CLI response. Details : {0}"

NOT_ENOUGH_SPACE_IN_POOL_ERROR_MESSAGE = "Not enough space in pool {0}"

SIZE_OUT_OF_RANGE_ERROR_MESSAGE = "requested size is out of limits. requested: {0}," \
                                  " max_in_byte: {1}"

SNAPSHOT_SOURCE_POOL_MISMATCH_ERROR_MESSAGE = "Snapshot pool : {0} does not match the source volume pool : {1}"

NO_PORT_FOUND_BY_CONNECTIVITY_TYPE_ERROR_MESSAGE = "no port in : {} found by connectivity type : {}"

ALL_THE_PORTS_FOR_HOST_ARE_INVALID = "All the ports for host {} are already assigned or not valid"
