import controller.controller_server.messages as messages


class BaseControllerServerException(Exception):

    def __str__(self, *args, **kwargs):
        return self.message


class ValidationException(BaseControllerServerException):

    def __init__(self, msg):
        self.message = messages.ValidationException_message.format(msg)


class InvalidNodeId(BaseControllerServerException):

    def __init__(self, node_id):
        self.message = messages.wrong_id_format_message.format("node", node_id)


class ObjectIdError(BaseControllerServerException):

    def __init__(self, object_type, object_id):
        self.message = messages.wrong_id_format_message.format(object_type, object_id)
