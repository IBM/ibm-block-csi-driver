import encodings

from controllers.array_action.config import WWN_OUI_END, WWN_VENDOR_IDENTIFIER_END
from controllers.common.csi_logger import get_stdout_logger

UTF_8 = encodings.utf_8.getregentry().name

logger = get_stdout_logger()


def convert_scsi_id_to_nguid(volume_id):
    logger.debug("Converting scsi uuid : {} to nguid".format(volume_id))
    oui = volume_id[1:WWN_OUI_END]
    vendor_identifier = volume_id[WWN_OUI_END:WWN_VENDOR_IDENTIFIER_END]
    vendor_identifier_extension = volume_id[WWN_VENDOR_IDENTIFIER_END:]
    final_nguid = ''.join((vendor_identifier_extension, oui, '0', vendor_identifier))
    logger.debug("Nguid is : {}".format(final_nguid))
    return final_nguid


class ClassProperty:

    def __init__(self, function):
        self._function = function

    def __get__(self, instance, owner):
        return self._function(owner)
