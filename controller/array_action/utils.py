import encodings
UTF_8 = encodings.utf_8.getregentry().name


def bytes_to_string(input_as_bytes):
    return input_as_bytes.decode(UTF_8) if input_as_bytes else ""


class classproperty:

    def __init__(self, function):
        self._function = function

    def __get__(self, instance, owner):
        return self._function(owner)
