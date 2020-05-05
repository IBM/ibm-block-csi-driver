def bytes_to_string(input_as_bytes):
    return input_as_bytes.decode() if input_as_bytes else ""


class classproperty(object):

    def __init__(self, function):
        self._function = function

    def __get__(self, instance, owner):
        return self._function(owner)
