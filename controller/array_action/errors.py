class BaseArrayActionException(Exception):

    def __str__(self, *args, **kwargs):
        return self.message


class NoConnectionAvailableException(BaseArrayActionException):

    def __init__(self, endpoint):
        self.message = "No connection available to endpoint : {0}".format(endpoint)


class CredentialsError(BaseArrayActionException):

    def __init__(self, endpoint):
        self.message = "Credential error has occurred while connecting to endpoint : {0} ".format(endpoint)


class VolumeNotFoundError(BaseArrayActionException):

    def __init__(self, name):
        self.message = "Volume was not found : {0} ".format(name)


class IllegalObjectName(BaseArrayActionException):

    def __init__(self, msg):
        self.message = "{0}".format(msg)


class PoolDoesNotMatchCapabilities(BaseArrayActionException):

    def __init__(self, pool, capabilities, error):
        self.message = "Pool : {0} does not match the following capabilities : {1} . error : {2}".format(pool,
                                                                                                         capabilities,
                                                                                                         error)


class StorageClassCapabilityNotSupported(BaseArrayActionException):

    def __init__(self, capabilities):
        self.message = "Capability is not supported : {0} ".format(capabilities)


class VolumeAlreadyExists(BaseArrayActionException):

    def __init__(self, volume, array):
        self.message = "Volume already exists : {0} , array : {1}".format(volume, array)


class PoolDoesNotExist(BaseArrayActionException):

    def __init__(self, pool, array):
        self.message = "Pool does not exist: {0} , array : {1}".format(pool, array)


class FailedToFindStorageSystemType(BaseArrayActionException):

    def __init__(self, endpoint):
        self.message = "Could not identify the type for endpoint: {} ".format(endpoint)
