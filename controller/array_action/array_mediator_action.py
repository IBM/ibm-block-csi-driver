from controller.array_action.array_connection_manager import ArrayConnectionManager
from controller.common.csi_logger import get_stdout_logger
from controller.array_action.errors import NoConnectionAvailableException
from controller.controller_server import utils
import controller.array_action.errors as controller_errors
from controller.array_action.config import FC_CONNECTIVITY_TYPE

from retry import retry

logger = get_stdout_logger()


@retry(NoConnectionAvailableException, tries=11, delay=1)
def map_volume(user, password, array_addresses, array_type, vol_id, initiators):
    with ArrayConnectionManager(user, password, array_addresses, array_type) as array_mediator:
        host_name, connectivity_types = array_mediator.get_host_by_host_identifiers(initiators)

        logger.debug(
            "hostname : {}, connectivity_types  : {}".format(host_name,
                                                             connectivity_types))

        connectivity_type = utils.choose_connectivity_type(connectivity_types)

        if FC_CONNECTIVITY_TYPE == connectivity_type:
            array_initiators = array_mediator.get_array_fc_wwns(host_name)
        else:
            array_initiators = array_mediator.get_array_iqns()
        mappings = array_mediator.get_volume_mappings(vol_id)
        if len(mappings) >= 1:
            logger.debug(
                "{0} mappings have been found for volume. the mappings are: {1}".format(len(mappings), mappings))
            if len(mappings) == 1:
                mapping = list(mappings)[0]
                if mapping == host_name:
                    logger.debug("idempotent case - volume is already mapped to host.")
                    return mappings[mapping], connectivity_type, array_initiators
            raise controller_errors.VolumeMappedToMultipleHostsError(mappings)

        logger.debug(
            "no mappings were found for volume. mapping vol : {0} to host : {1}".format(
                vol_id, host_name))

        try:
            lun = array_mediator.map_volume(vol_id, host_name)
            logger.debug("lun : {}".format(lun))
        except controller_errors.LunAlreadyInUseError as ex:
            logger.warning(
                "Lun was already in use. re-trying the operation. {0}".format(
                    ex))
            for i in range(array_mediator.max_lun_retries - 1):
                try:
                    lun = array_mediator.map_volume(vol_id, host_name)
                    break
                except controller_errors.LunAlreadyInUseError as inner_ex:
                    logger.warning(
                        "re-trying map volume. try #{0}. {1}".format(i,
                                                                     inner_ex))
            else:  # will get here only if the for statement is false.
                raise ex

        return lun, connectivity_type, array_initiators


@retry(NoConnectionAvailableException, tries=11, delay=1)
def unmap_volume(user, password, array_addresses, array_type, vol_id, initiators):
    with ArrayConnectionManager(user, password, array_addresses, array_type) as array_mediator:
        host_name, _ = array_mediator.get_host_by_host_identifiers(initiators)

        array_mediator.unmap_volume(vol_id, host_name)
