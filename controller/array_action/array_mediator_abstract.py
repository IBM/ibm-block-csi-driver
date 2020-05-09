from abc import ABC
from retry import retry

import controller.array_action.errors as controller_errors
from controller.array_action.array_mediator_interface import ArrayMediator
from controller.array_action.config import FC_CONNECTIVITY_TYPE, ISCSI_CONNECTIVITY_TYPE
from controller.array_action.errors import NoConnectionAvailableException, UnsupportedConnectivityTypeError
from controller.common.csi_logger import get_stdout_logger
from controller.controller_server import utils

logger = get_stdout_logger()


class ArrayMediatorAbstract(ArrayMediator, ABC):

    @retry(NoConnectionAvailableException, tries=11, delay=1)
    def map_volume_by_initiators(self, vol_id, initiators):
        host_name, connectivity_types = self.get_host_by_host_identifiers(initiators)

        logger.debug(
            "hostname : {}, connectivity_types  : {}".format(host_name,
                                                             connectivity_types))

        connectivity_type = utils.choose_connectivity_type(connectivity_types)

        if FC_CONNECTIVITY_TYPE == connectivity_type:
            array_initiators = self.get_array_fc_wwns(host_name)
        elif ISCSI_CONNECTIVITY_TYPE == connectivity_type:
            array_initiators = self.get_iscsi_targets_by_iqn()
        else:
            raise UnsupportedConnectivityTypeError(connectivity_type)

        mappings = self.get_volume_mappings(vol_id)
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
            lun = self.map_volume(vol_id, host_name)
            logger.debug("lun : {}".format(lun))
        except controller_errors.LunAlreadyInUseError as ex:
            logger.warning(
                "Lun was already in use. re-trying the operation. {0}".format(
                    ex))
            for i in range(self.max_lun_retries - 1):
                try:
                    lun = self.map_volume(vol_id, host_name)
                    break
                except controller_errors.LunAlreadyInUseError as inner_ex:
                    logger.warning(
                        "re-trying map volume. try #{0}. {1}".format(i,
                                                                     inner_ex))
            else:  # will get here only if the for statement is false.
                raise ex

        return lun, connectivity_type, array_initiators

    @retry(NoConnectionAvailableException, tries=11, delay=1)
    def unmap_volume_by_initiators(self, vol_id, initiators):
        host_name, _ = self.get_host_by_host_identifiers(initiators)

        self.unmap_volume(vol_id, host_name)
