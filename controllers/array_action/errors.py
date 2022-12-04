import controllers.array_action.messages as messages


class BaseArrayActionException(Exception):

    def __str__(self, *args, **kwargs):
        return self.message


# =============================================================================
# System errors
# =============================================================================
class NoConnectionAvailableException(BaseArrayActionException):

    def __init__(self, endpoint):
        super().__init__()
        self.message = messages.NO_CONNECTION_AVAILABLE_EXCEPTION_MESSAGE.format(endpoint)


class StorageManagementIPsNotSupportError(BaseArrayActionException):

    def __init__(self, endpoint):
        super().__init__()
        self.message = messages.STORAGE_MANAGEMENT_IPS_NOT_SUPPORT_ERROR_MESSAGE.format(endpoint)


class CredentialsError(BaseArrayActionException):

    def __init__(self, endpoint):
        super().__init__()
        self.message = messages.CREDENTIALS_ERROR_MESSAGE.format(endpoint)


class UnsupportedStorageVersionError(BaseArrayActionException):

    def __init__(self, version, supported_version):
        super().__init__()
        self.message = messages.UNSUPPORTED_STORAGE_VERSION_ERROR_MESSAGE.format(version,
                                                                                 supported_version)  # noqa


# =============================================================================
# Volume errors
# =============================================================================
class InvalidArgumentError(BaseArrayActionException):

    def __init__(self, message):
        super().__init__()
        self.message = message


class ObjectNotFoundError(BaseArrayActionException):

    def __init__(self, name):
        super().__init__()
        self.message = messages.OBJECT_NOT_FOUND_ERROR_MESSAGE.format(name)


class VolumeNameBelongsToSnapshotError(BaseArrayActionException):

    def __init__(self, volume, array):
        super().__init__()
        self.message = messages.VOLUME_NAME_BELONGS_TO_SNAPSHOT_ERROR_MESSAGE.format(volume, array)


class VolumeCreationError(BaseArrayActionException):

    def __init__(self, name):
        super().__init__()
        self.message = messages.VOLUME_CREATION_ERROR_MESSAGE.format(name)


class VolumeDeletionError(BaseArrayActionException):

    def __init__(self, volume_id):
        super().__init__()
        self.message = messages.VOLUME_DELETION_ERROR_MESSAGE.format(volume_id)


class PoolDoesNotMatchSpaceEfficiency(InvalidArgumentError):

    def __init__(self, pool, space_efficiency, error):
        message = messages.POOL_DOES_NOT_MATCH_SPACE_EFFICIENCY_MESSAGE.format(pool, space_efficiency,
                                                                               error)
        super().__init__(message)


class SpaceEfficiencyNotSupported(InvalidArgumentError):

    def __init__(self, space_efficiency):
        message = messages.SPACE_EFFICIENCY_NOT_SUPPORTED_MESSAGE.format(space_efficiency)
        super().__init__(message)


class SpaceEfficiencyMismatch(BaseArrayActionException):

    def __init__(self, target_space_efficiency, source_space_efficiency):
        super().__init__()
        self.message = messages.SPACE_EFFICIENCY_MISMATCH_MESSAGE.format(target_space_efficiency,
                                                                         source_space_efficiency)


class VolumeAlreadyExists(BaseArrayActionException):

    def __init__(self, volume_name, array):
        super().__init__()
        self.message = messages.VOLUME_ALREADY_EXISTS_MESSAGE.format(volume_name, array)


class PoolDoesNotExist(InvalidArgumentError):

    def __init__(self, pool, array):
        message = messages.POOL_DOES_NOT_EXIST_MESSAGE.format(pool, array)
        super().__init__(message)


class PoolParameterIsMissing(InvalidArgumentError):

    def __init__(self, array_type):
        message = messages.POOL_PARAMETER_IS_MISSING.format(array_type)
        super().__init__(message)


class FailedToFindStorageSystemType(BaseArrayActionException):

    def __init__(self, endpoint):
        super().__init__()
        self.message = messages.FAILED_TO_FIND_STORAGE_SYSTEM_TYPE_MESSAGE.format(endpoint)


class PermissionDeniedError(BaseArrayActionException):

    def __init__(self, operation):
        super().__init__()
        self.message = messages.PERMISSION_DENIED_ERROR_MESSAGE.format(operation)


class MultipleHostsFoundError(BaseArrayActionException):

    def __init__(self, initiators, hosts):
        super().__init__()
        self.message = messages.MULTIPLE_HOSTS_FOUND_ERROR_MESSAGE.format(initiators, hosts)


class HostNotFoundError(BaseArrayActionException):

    def __init__(self, host_identifier):
        super().__init__()
        self.message = messages.HOST_NOT_FOUND_ERROR_MESSAGE.format(host_identifier)


class NoAvailableLunError(BaseArrayActionException):

    def __init__(self, host):
        super().__init__()
        self.message = messages.NO_AVAILABLE_LUN_ERROR_MESSAGE.format(host)


class LunAlreadyInUseError(BaseArrayActionException):

    def __init__(self, lun, host):
        super().__init__()
        self.message = messages.LUN_ALREADY_IN_USE_MESSAGE.format(lun, host)


class MappingError(BaseArrayActionException):

    def __init__(self, volume_id_or_name, host, err):
        super().__init__()
        self.message = messages.MAPPING_ERROR_MESSAGE.format(volume_id_or_name, host, err)


class VolumeAlreadyUnmappedError(BaseArrayActionException):

    def __init__(self, volume_id_or_name):
        super().__init__()
        self.message = messages.VOLUME_ALREADY_UNMAPPED_MESSAGE.format(volume_id_or_name)


class UnmappingError(BaseArrayActionException):

    def __init__(self, volume_id_or_name, host, err):
        super().__init__()
        self.message = messages.UNMAPPING_ERROR_MESSAGE.format(volume_id_or_name, host, err)


class VolumeAlreadyMappedToDifferentHostsError(BaseArrayActionException):

    def __init__(self, hosts):
        super().__init__()
        self.message = messages.VOLUME_ALREADY_MAPPED_TO_DIFFERENT_HOSTS_ERROR_MESSAGE.format(hosts)


class NoIscsiTargetsFoundError(BaseArrayActionException):

    def __init__(self, endpoint):
        super().__init__()
        self.message = messages.NO_ISCSI_TARGETS_FOUND_ERROR_MESSAGE.format(endpoint)


class UnsupportedConnectivityTypeError(InvalidArgumentError):

    def __init__(self, connectivity_type):
        message = messages.UNSUPPORTED_CONNECTIVITY_TYPE_ERROR_MESSAGE.format(connectivity_type)
        super().__init__(message)


class ExpectedSnapshotButFoundVolumeError(InvalidArgumentError):

    def __init__(self, id_or_name, array):
        message = messages.EXPECTED_SNAPSHOT_BUT_FOUND_VOLUME_ERROR_MESSAGE.format(id_or_name, array)
        super().__init__(message)


class SnapshotAlreadyExists(BaseArrayActionException):

    def __init__(self, snapshot_id_or_name, array):
        super().__init__()
        self.message = messages.SNAPSHOT_ALREADY_EXISTS_ERROR_MESSAGE.format(snapshot_id_or_name, array)


class HostAlreadyExists(BaseArrayActionException):

    def __init__(self, host_name, array):
        super().__init__()
        self.message = messages.HOST_ALREADY_EXISTS_ERROR_MESSAGE.format(host_name, array)


class NoPortFoundByConnectivityType(BaseArrayActionException):

    def __init__(self, initiators, connectivity_type):
        super().__init__()
        self.message = messages.NO_PORT_FOUND_BY_CONNECTIVITY_TYPE_ERROR_MESSAGE.format(initiators, connectivity_type)


class SnapshotSourcePoolMismatch(BaseArrayActionException):

    def __init__(self, snapshot_pool, source_pool):
        super().__init__()
        self.message = messages.SNAPSHOT_SOURCE_POOL_MISMATCH_ERROR_MESSAGE.format(snapshot_pool, source_pool)


class ObjectIsStillInUseError(BaseArrayActionException):

    def __init__(self, id_or_name, used_by: list):
        super().__init__()
        self.message = messages.OBJECT_IS_STILL_IN_USE_ERROR_MESSAGE.format(id_or_name, used_by[0])
        self.message += ' {0} more were truncated.'.format(len(used_by) - 1) if len(used_by) > 1 else ''


class InvalidCliResponseError(BaseArrayActionException):

    def __init__(self, details):
        super().__init__()
        self.message = messages.INVALID_CLI_RESPONSE_ERROR_MESSAGE.format(details)


class NotEnoughSpaceInPool(BaseArrayActionException):

    def __init__(self, id_or_name):
        super().__init__()
        self.message = messages.NOT_ENOUGH_SPACE_IN_POOL_ERROR_MESSAGE.format(id_or_name)


class VirtSnapshotFunctionNotSupportedMessage(BaseArrayActionException):
    def __init__(self, id_or_name):
        super().__init__()
        self.message = messages.VIRT_SNAPSHOT_FUNCTION_NOT_SUPPORTED_MESSAGE.format(id_or_name)


class NoPortIsValid(BaseArrayActionException):

    def __init__(self, host_name):
        super().__init__()
        self.message = messages.ALL_THE_PORTS_FOR_HOST_ARE_INVALID.format(host_name)


class IoGroupIsInValid(BaseArrayActionException):

    def __init__(self, io_group):
        super().__init__()
        self.message = messages.IO_GROUP_IS_NOT_VALID.format(io_group)


class UnSupportedParameter(BaseArrayActionException):

    def __init__(self, parameter):
        super().__init__()
        self.message = messages.UNSUPPORTED_PARAMETER.format(parameter)


class CannotChangeHostProtocolBecauseOfMappedPorts(BaseArrayActionException):

    def __init__(self, host_name):
        super().__init__()
        self.message = messages.CANNOT_CHANGE_HOST_PROTOCOL_BECAUSE_OF_MAPPED_PORTS.format(host_name)
