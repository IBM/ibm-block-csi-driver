class WatcherException(Exception):
    def __str__(self, *args, **kwargs):
        return self.message
    
class SecretDoesNotExist(WatcherException):
    def __init__(self, secret_name, secret_namespace):
        super().__init__()
        self.message = 'Secret {} in namespace {} does not exist'.format(
            secret_name, secret_namespace)

class FailedToGetSecret(WatcherException):
    def __init__(self, secret_name, secret_namespace, message):
        super().__init__()
        self.message = 'Failed to get Secret {} in namespace {}, go this error: {}'.format(
            secret_name, secret_namespace, message)

class FailedToCreateHostDefinitionObject(WatcherException):
    def __init__(self, host_definition_name, message):
        super().__init__()
        self.message = 'Failed to create host definition {}, go this error: {}'.format(
            host_definition_name, message)
        
class FailedToPatchHostDefinitionObject(WatcherException):
    def __init__(self, host_definition_name, message):
        super().__init__()
        self.message = 'Failed to patch host definition {}, go this error: {}'.format(
            host_definition_name, message)

class FailedToSetHostDefinitionStatus(WatcherException):
    def __init__(self, host_definition_name, message):
        super().__init__()
        self.message = 'Failed to set host definition {} status, go this error: {}'.format(
            host_definition_name, message)
        
class FailedToGetHostDefinitionObject(WatcherException):
    def __init__(self, host_definition_name, message):
        super().__init__()
        self.message = 'Failed to get host definition {}, go this error: {}'.format(
            host_definition_name, message)

class HostDefinitionDoesNotExist(WatcherException):
    def __init__(self, host_definition_name):
        super().__init__()
        self.message = 'host definition {}does not exist'.format(
            host_definition_name)