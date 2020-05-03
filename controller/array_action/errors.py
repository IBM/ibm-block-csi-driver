import controller.array_action.messages as messages


class BaseArrayActionException(Exception):

    def __str__(self, *args, **kwargs):
        return self.message


# =============================================================================
# System errors
# =============================================================================
class NoConnectionAvailableException(BaseArrayActionException):

    def __init__(self, endpoint):
        self.message = messages.NoConnectionAvailableException_message.format(endpoint)


class StorageManagementIPsNotSupportError(BaseArrayActionException):

    def __init__(self, endpoint):
        self.message = messages.StorageManagementIPsNotSupportError_message.format(endpoint)


class CredentialsError(BaseArrayActionException):

    def __init__(self, endpoint):
        self.message = messages.CredentialsError_message.format(endpoint)


class UnsupportedStorageVersionError(BaseArrayActionException):

    def __init__(self, version, supported_version):
        self.message = messages.UnsupportedStorageVersionError_message.format(version, supported_version)  # noqa


# =============================================================================
# Volume errors
# =============================================================================
class VolumeNotFoundError(BaseArrayActionException):

    def __init__(self, name):
        self.message = messages.VolumeNotFoundError_message.format(name)


class VolumeCreationError(BaseArrayActionException):

    def __init__(self, name):
        self.message = messages.VolumeCreationError_message.format(name)


class VolumeDeletionError(BaseArrayActionException):

    def __init__(self, volume_id):
        self.message = messages.VolumeDeletionError_message.format(volume_id)


class IllegalObjectName(BaseArrayActionException):

    def __init__(self, msg):
        self.message = "{0}".format(msg)


class PoolDoesNotMatchCapabilities(BaseArrayActionException):

    def __init__(self, pool, capabilities, error):
        self.message = messages.PoolDoesNotMatchCapabilities_message.format(pool, capabilities, error)


class StorageClassCapabilityNotSupported(BaseArrayActionException):

    def __init__(self, capabilities):
        self.message = messages.StorageClassCapabilityNotSupported_message.format(capabilities)


class VolumeAlreadyExists(BaseArrayActionException):

    def __init__(self, volume, array):
        self.message = messages.VolumeAlreadyExists_message.format(volume, array)


class PoolDoesNotExist(BaseArrayActionException):

    def __init__(self, pool, array):
        self.message = messages.PoolDoesNotExist_message.format(pool, array)


class FailedToFindStorageSystemType(BaseArrayActionException):

    def __init__(self, endpoint):
        self.message = messages.FailedToFindStorageSystemType_message.format(endpoint)


class PermissionDeniedError(BaseArrayActionException):

    def __init__(self, operation):
        self.message = messages.PermissionDeniedError_message.format(operation)


class MultipleHostsFoundError(BaseArrayActionException):

    def __init__(self, initiators, hosts):
        self.message = messages.MultipleHostsFoundError_message.format(initiators, hosts)


class HostNotFoundError(BaseArrayActionException):

    def __init__(self, iscsi_iqn):
        self.message = messages.HostNotFoundError_message.format(iscsi_iqn)


class NoAvailableLunError(BaseArrayActionException):

    def __init__(self, host):
        self.message = messages.NoAvailableLunError_message.format(host)


class LunAlreadyInUseError(BaseArrayActionException):

    def __init__(self, lun, host):
        self.message = messages.LunAlreadyInUse_message.format(lun, host)


class MappingError(BaseArrayActionException):

    def __init__(self, vol, host, err):
        self.message = messages.MappingError_message.format(vol, host, err)


class VolumeAlreadyUnmappedError(BaseArrayActionException):

    def __init__(self, vol):
        self.message = messages.VolumeAlreadyUnmapped_message.format(vol)


class UnMappingError(BaseArrayActionException):

    def __init__(self, vol, host, err):
        self.message = messages.UnMappingError_message.format(vol, host, err)


class BadNodeIdError(BaseArrayActionException):

    def __init__(self, name):
        self.message = messages.BadNodeIdError_message.format(name)


class VolumeMappedToMultipleHostsError(BaseArrayActionException):

    def __init__(self, hosts):
        self.message = messages.VolumeMappedToMultipleHostsError_message.format(hosts)


class NoIscsiTargetsFoundError(BaseArrayActionException):

    def __init__(self, endpoint):
        self.message = messages.NoIscsiTargetsFoundError_message.format(endpoint)


class UnsupportedConnectivityTypeError(BaseArrayActionException):

    def __init__(self, connectivity_type):
        self.message = messages.UnsupportedConnectivityTypeError_message.format(connectivity_type)


class SnapshotNameBelongsToVolumeError(BaseArrayActionException):

    def __init__(self, snapshot, array):
        self.message = messages.SnapshotNameBelongsToVolumeError_message.format(snapshot, array)


class SnapshotAlreadyExists(BaseArrayActionException):

    def __init__(self, snapshot, array):
        self.message = messages.SnapshotAlreadyExistsError_message.format(snapshot, array)
