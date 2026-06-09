import controllers.servers.messages as messages


class BaseControllerServerException(Exception):

    def __str__(self, *args, **kwargs):
        return self.message


class ValidationException(BaseControllerServerException):

    def __init__(self, msg):
        super().__init__()
        self.message = messages.VALIDATION_EXCEPTION_MESSAGE.format(msg)


class InvalidNodeId(BaseControllerServerException):

    def __init__(self, node_id):
        super().__init__()
        self.message = messages.WRONG_ID_FORMAT_MESSAGE.format("node", node_id)


class ObjectIdError(BaseControllerServerException):

    def __init__(self, object_type, object_id):
        super().__init__()
        self.message = messages.WRONG_ID_FORMAT_MESSAGE.format(object_type, object_id)


class ObjectAlreadyProcessingError(BaseControllerServerException):
    def __init__(self, object_id_or_name):
        super().__init__()
        self.message = messages.OBJECT_ALREADY_PROCESSING_MESSAGE.format(object_id_or_name)
