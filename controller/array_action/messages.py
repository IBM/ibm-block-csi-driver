NoConnectionAvailableException_message = "Currently no connection is available to endpoint: {0}"

CredentialsError_message = "Credential error has occurred while connecting to endpoint : {0} "

StorageManagementIPsNotSupportError_message = "Invalid Management IP for SVC : {0} "

ObjectNotFoundError_message = "Object was not found : {0} "

VolumeNameBelongsToSnapshotError_message = "Volume not found. Snapshot with the same id exists. \
                                                         Name : {0} , array : {1}"

PoolDoesNotMatchSpaceEfficiency_message = "Pool : {0} does not match the following space efficiency : {1} . error : {2}"

SpaceEfficiencyNotSupported_message = "space efficiency is not supported : {0} "

VolumeAlreadyExists_message = "Volume already exists : {0} , array : {1}"

PoolDoesNotExist_message = "Pool does not exist: {0} , array : {1}"

PoolParameterIsMissing = "Pool parameter is mandatory in {0}"

FailedToFindStorageSystemType_message = "Could not identify the type for endpoint: {0} "

PermissionDeniedError_message = "Permission was denied to operation : {0}"

MultipleHostsFoundError_message = "Multiple hosts found for port(s): {0}. hosts are : {1}"

HostNotFoundError_message = "Host for node: {0} was not found, ensure all host ports are configured on storage"

NoAvailableLunError_message = "No available lun was found for host : {0}"

LunAlreadyInUse_message = "Lun : {0} is already mapped for host : {1}"

MappingError_message = "Mapping error has occurred while mapping volume : {0} to host : {1}. error : {2}"

VolumeAlreadyUnmapped_message = "Volume: {0} is already unmapped."

UnMappingError_message = "Unmapping error has occurred for volume : {0} and host : {1}. error : {2}"

VolumeMappedToMultipleHostsError_message = "Volume is already mapped to different hosts {0}"

UnsupportedStorageVersionError_message = ('Unsupported storage system microcode version {}, '
                                          'the version should not be lower than {}')

VolumeCreationError_message = 'Failed to create volume {}'

VolumeDeletionError_message = 'Failed to delete volume {}'

NoIscsiTargetsFoundError_message = "Could not find iSCSI targets for endpoint: {0}"

UnsupportedConnectivityTypeError_message = "Unsupported connectivity type: {0}"

SnapshotNotFoundError_message = "Snapshot was not found : {0} "

SnapshotAlreadyExistsError_message = "Snapshot already exists : {0} , array : {1}"

ExpectedSnapshotButFoundVolumeError_message = "Could not find info about the source of: {0}, array: {1}"

SnapshotWrongVolumeError_message = "Snapshot {0} exists but it is of Volume {1} and not {2}"

ObjectIsStillInUseError_message = "Object {0} is still in use by {1}"

InvalidCliResponseError_message = "Invalid CLI response. Details : {0}"

NotEnoughSpaceInPoolError_message = "Not enough space in pool {0}"

SizeOutOfRangeError_message = "requested size is out of limits. requested: {0}," \
                              " max_in_byte: {1}"

SnapshotSourcePoolMismatchError_message = "Snapshot pool : {0} does not match the source volume pool : {1}"
