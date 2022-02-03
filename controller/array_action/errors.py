import controller.array_action.messages as messages


class BaseArrayActionException(Exception):

    def __str__(self, *args, **kwargs):
        return self.message


# =============================================================================
# System errors
# =============================================================================
class NoConnectionAvailableException(BaseArrayActionException):

    def __init__(self, endpoint):
        super().__init__()
        self.message = messages.NoConnectionAvailableException_message.format(endpoint)


class StorageManagementIPsNotSupportError(BaseArrayActionException):

    def __init__(self, endpoint):
        super().__init__()
        self.message = messages.StorageManagementIPsNotSupportError_message.format(endpoint)


class CredentialsError(BaseArrayActionException):

    def __init__(self, endpoint):
        super().__init__()
        self.message = messages.CredentialsError_message.format(endpoint)


class UnsupportedStorageVersionError(BaseArrayActionException):

    def __init__(self, version, supported_version):
        super().__init__()
        self.message = messages.UnsupportedStorageVersionError_message.format(version,
                                                                              supported_version)  # noqa


# =============================================================================
# Volume errors
# =============================================================================
class InvalidArgumentError(BaseArrayActionException):
    pass


class ObjectNotFoundError(BaseArrayActionException):

    def __init__(self, name):
        super().__init__()
        self.message = messages.ObjectNotFoundError_message.format(name)


class VolumeNameBelongsToSnapshotError(BaseArrayActionException):

    def __init__(self, volume, array):
        super().__init__()
        self.message = messages.VolumeNameBelongsToSnapshotError_message.format(volume, array)


class VolumeCreationError(BaseArrayActionException):

    def __init__(self, name):
        super().__init__()
        self.message = messages.VolumeCreationError_message.format(name)


class VolumeDeletionError(BaseArrayActionException):

    def __init__(self, volume_id):
        super().__init__()
        self.message = messages.VolumeDeletionError_message.format(volume_id)


class IllegalObjectName(InvalidArgumentError):

    def __init__(self, msg):
        super().__init__()
        self.message = "{0}".format(msg)


class IllegalObjectID(InvalidArgumentError):

    def __init__(self, msg):
        super().__init__()
        self.message = "{0}".format(msg)


class PoolDoesNotMatchSpaceEfficiency(InvalidArgumentError):

    def __init__(self, pool, space_efficiency, error):
        super().__init__()
        self.message = messages.PoolDoesNotMatchSpaceEfficiency_message.format(pool, space_efficiency,
                                                                               error)


class SpaceEfficiencyNotSupported(InvalidArgumentError):

    def __init__(self, space_efficiency):
        super().__init__()
        self.message = messages.SpaceEfficiencyNotSupported_message.format(space_efficiency)


class VolumeAlreadyExists(BaseArrayActionException):

    def __init__(self, volume_name, array):
        super().__init__()
        self.message = messages.VolumeAlreadyExists_message.format(volume_name, array)


class PoolDoesNotExist(InvalidArgumentError):

    def __init__(self, pool, array):
        super().__init__()
        self.message = messages.PoolDoesNotExist_message.format(pool, array)


class PoolParameterIsMissing(InvalidArgumentError):

    def __init__(self, array_type):
        super().__init__()
        self.message = messages.PoolParameterIsMissing.format(array_type)


class FailedToFindStorageSystemType(BaseArrayActionException):

    def __init__(self, endpoint):
        super().__init__()
        self.message = messages.FailedToFindStorageSystemType_message.format(endpoint)


class PermissionDeniedError(BaseArrayActionException):

    def __init__(self, operation):
        super().__init__()
        self.message = messages.PermissionDeniedError_message.format(operation)


class MultipleHostsFoundError(BaseArrayActionException):

    def __init__(self, initiators, hosts):
        super().__init__()
        self.message = messages.MultipleHostsFoundError_message.format(initiators, hosts)


class HostNotFoundError(BaseArrayActionException):

    def __init__(self, iscsi_iqn):
        super().__init__()
        self.message = messages.HostNotFoundError_message.format(iscsi_iqn)


class NoAvailableLunError(BaseArrayActionException):

    def __init__(self, host):
        super().__init__()
        self.message = messages.NoAvailableLunError_message.format(host)


class LunAlreadyInUseError(BaseArrayActionException):

    def __init__(self, lun, host):
        super().__init__()
        self.message = messages.LunAlreadyInUse_message.format(lun, host)


class MappingError(BaseArrayActionException):

    def __init__(self, volume_id_or_name, host, err):
        super().__init__()
        self.message = messages.MappingError_message.format(volume_id_or_name, host, err)


class VolumeAlreadyUnmappedError(BaseArrayActionException):

    def __init__(self, volume_id_or_name):
        super().__init__()
        self.message = messages.VolumeAlreadyUnmapped_message.format(volume_id_or_name)


class UnmappingError(BaseArrayActionException):

    def __init__(self, volume_id_or_name, host, err):
        super().__init__()
        self.message = messages.UnMappingError_message.format(volume_id_or_name, host, err)


class VolumeMappedToMultipleHostsError(BaseArrayActionException):

    def __init__(self, hosts):
        super().__init__()
        self.message = messages.VolumeMappedToMultipleHostsError_message.format(hosts)


class NoIscsiTargetsFoundError(BaseArrayActionException):

    def __init__(self, endpoint):
        super().__init__()
        self.message = messages.NoIscsiTargetsFoundError_message.format(endpoint)


class UnsupportedConnectivityTypeError(InvalidArgumentError):

    def __init__(self, connectivity_type):
        super().__init__()
        self.message = messages.UnsupportedConnectivityTypeError_message.format(connectivity_type)


class ExpectedSnapshotButFoundVolumeError(InvalidArgumentError):

    def __init__(self, id_or_name, array):
        super().__init__()
        self.message = messages.ExpectedSnapshotButFoundVolumeError_message.format(id_or_name, array)


class SnapshotAlreadyExists(BaseArrayActionException):

    def __init__(self, snapshot_id_or_name, array):
        super().__init__()
        self.message = messages.SnapshotAlreadyExistsError_message.format(snapshot_id_or_name, array)


class SnapshotSourcePoolMismatch(BaseArrayActionException):

    def __init__(self, snapshot_pool, source_pool):
        super().__init__()
        self.message = messages.SnapshotSourcePoolMismatchError_message.format(snapshot_pool, source_pool)


class ObjectIsStillInUseError(BaseArrayActionException):

    def __init__(self, id_or_name, used_by: list):
        super().__init__()
        self.message = messages.ObjectIsStillInUseError_message.format(id_or_name, used_by[0])
        self.message += ' {0} more were truncated.'.format(len(used_by) - 1) if len(used_by) > 1 else ''


class InvalidCliResponseError(BaseArrayActionException):

    def __init__(self, details):
        super().__init__()
        self.message = messages.InvalidCliResponseError_message.format(details)


class NotEnoughSpaceInPool(BaseArrayActionException):

    def __init__(self, id_or_name):
        super().__init__()
        self.message = messages.NotEnoughSpaceInPoolError_message.format(id_or_name)
