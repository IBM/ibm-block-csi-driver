
class BaseArrayActionException(Exception):

    def __str__(self, *args, **kwargs):
        return self.message 


class NoConnectionAvailableException(BaseArrayActionException):

    def __init__(self, endpoint):
        self.message = "no connection available to endpoint : {0}".format(endpoint)
 

class CredentialsError(BaseArrayActionException):

    def __init__(self, endpoint):
        self.message = "credential error has occurred while connecting to endpoint : {0} ".format(endpoint)
    

class VolumeNotFoundError(BaseArrayActionException):

    def __init__(self, name):
        self.message = "volume was not found : {0} ".format(name)
    
    
class IllegalObjectName(BaseArrayActionException):

    def __init__(self, msg):
        self.message = "{0}".format(msg)
    

class PoolDoesNotMatchCapabilities(BaseArrayActionException):

    def __init__(self, pool, capabilities):
        self.message = "pool : {0} does not match the following capabilities : {1} ".format(pool, capabilities)

    
class CapabilityNotSupported(BaseArrayActionException):

    def __init__(self, capabilities):
        self.message = "capability is not supported : {0} ".format(capabilities)

    
class VolumeAlreadyExists(BaseArrayActionException):

    def __init__(self, volume):
        self.message = "volume already exists : {0} ".format(volume)
    
    
class PoolDoesNotExist(BaseArrayActionException):

    def __init__(self, pool):
        self.message = "pool does not exist: {} ".format(pool)


class FailedToFindStorageSystemType(BaseArrayActionException):

    def __init__(self, endpoint):
        self.message = "could not identify the type for endpoint: {} ".format(endpoint)
