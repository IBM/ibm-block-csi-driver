import encodings

from controller.common.csi_logger import get_stdout_logger

UTF_8 = encodings.utf_8.getregentry().name

logger = get_stdout_logger()


def bytes_to_string(input_as_bytes):
    return input_as_bytes.decode(UTF_8) if input_as_bytes else ""


def convert_scsi_uuid_to_nguid(vol_id):
    logger.debug("converting scsi uuid to nguid")
    indices = [1, 7, 16]
    splited_text = list()
    for index, _ in enumerate(indices):
        last = len(vol_id)
        if index + 1 < len(indices):
            last = indices[index + 1]
        splited_text.append(vol_id[indices[index]:last])
    final_nguid = splited_text[2] + splited_text[0] + '0' + splited_text[1]
    logger.debug("nguid is : {}".format(final_nguid))
    return final_nguid


class classproperty:

    def __init__(self, function):
        self._function = function

    def __get__(self, instance, owner):
        return self._function(owner)
