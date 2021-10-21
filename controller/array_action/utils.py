import encodings

from controller.array_action.config import WWN_OUI_END, WWN_VENDOR_IDENTIFIER_END
from controller.common.csi_logger import get_stdout_logger

UTF_8 = encodings.utf_8.getregentry().name

logger = get_stdout_logger()


def bytes_to_string(input_as_bytes):
    return input_as_bytes.decode(UTF_8) if input_as_bytes else ""


def convert_scsi_id_to_nguid(volume_id):
    logger.debug("converting scsi uuid : {} to nguid".format(volume_id))
    oui = volume_id[1:WWN_OUI_END]
    vendor_identifier = volume_id[WWN_OUI_END:WWN_VENDOR_IDENTIFIER_END]
    vendor_identifier_extension = volume_id[WWN_VENDOR_IDENTIFIER_END:]
    final_nguid = vendor_identifier_extension + oui + '0' + vendor_identifier
    logger.debug("nguid is : {}".format(final_nguid))
    return final_nguid


class classproperty:

    def __init__(self, function):
        self._function = function

    def __get__(self, instance, owner):
        return self._function(owner)
