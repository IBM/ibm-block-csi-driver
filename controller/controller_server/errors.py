import controller.controller_server.messages as messages


class BaseControllerServerException(Exception):

    def __str__(self, *args, **kwargs):
        return self.message


class ValidationException(BaseControllerServerException):

    def __init__(self, msg):
        self.message = messages.ValidationException_message.format(msg)


class VolumeIdError(BaseControllerServerException):
    def __init__(self, id):
        self.message = messages.VolumeIdError_message.format(id)
