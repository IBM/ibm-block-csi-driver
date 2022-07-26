from abc import ABC

from retry import retry

import controllers.array_action.errors as array_errors
from controllers.array_action.array_mediator_interface import ArrayMediator
from controllers.array_action.config import (NVME_OVER_FC_CONNECTIVITY_TYPE,
                                             FC_CONNECTIVITY_TYPE,
                                             ISCSI_CONNECTIVITY_TYPE)
from controllers.array_action.errors import NoConnectionAvailableException, UnsupportedConnectivityTypeError
from controllers.array_action.utils import convert_scsi_id_to_nguid
from controllers.common.csi_logger import get_stdout_logger
import controllers.servers.utils as utils

logger = get_stdout_logger()


class ArrayMediatorAbstract(ArrayMediator, ABC):
    # https://github.com/PyCQA/pylint/issues/3975
    def __init__(self, user, password, endpoint):  # pylint: disable=super-init-not-called
        self.user = user
        self.password = password
        self.endpoint = endpoint

    @retry(NoConnectionAvailableException, tries=11, delay=1)
    def map_volume_by_initiators(self, vol_id, initiators):
        mappings = self.get_volume_mappings(vol_id)
        if len(mappings) >= 1:
            logger.debug(
                "{0} mappings have been found for volume. the mappings are: {1}".format(len(mappings), mappings))
            if len(mappings) == 1:
                mapping_host_name = list(mappings)[0]
                host = self.get_host_by_name(mapping_host_name)
                if host.initiators in initiators:
                    logger.debug("idempotent case - volume is already mapped to host.")
                    lun = mappings[mapping_host_name]
                    logger.debug(
                        "hostname : {}, connectivity_types  : {}".format(host.name, host.connectivity_types))
                    connectivity_type = utils.choose_connectivity_type(host.connectivity_types)
                    array_initiators = self._get_array_initiators(host.name, connectivity_type)
                    return lun, connectivity_type, array_initiators
                logger.error("volume is already mapped to a host but doesn't match initiators."
                             " host initiators: {} request initiators: {}.".format(host.initiators, initiators))
            raise array_errors.VolumeAlreadyMappedToDifferentHostsError(mappings)

        logger.debug("no mappings were found for volume. mapping volume : {0}".format(vol_id))

        host_name, connectivity_types = self.get_host_by_host_identifiers(initiators)

        logger.debug("hostname : {}, connectivity_types  : {}".format(host_name, connectivity_types))

        connectivity_type = utils.choose_connectivity_type(connectivity_types)
        array_initiators = self._get_array_initiators(host_name, connectivity_type)

        try:
            lun = self.map_volume(vol_id, host_name, connectivity_type)
            logger.debug("lun : {}".format(lun))
        except array_errors.LunAlreadyInUseError as ex:
            logger.warning("Lun was already in use. re-trying the operation. {0}".format(ex))
            for i in range(self.max_lun_retries - 1):
                try:
                    lun = self.map_volume(vol_id, host_name, connectivity_type)
                    break
                except array_errors.LunAlreadyInUseError as inner_ex:
                    logger.warning(
                        "re-trying map volume. try #{0}. {1}".format(i, inner_ex))
            else:
                raise ex

        return lun, connectivity_type, array_initiators

    @retry(NoConnectionAvailableException, tries=11, delay=1)
    def unmap_volume_by_initiators(self, vol_id, initiators):
        host_name, connectivity_types = self.get_host_by_host_identifiers(initiators)
        connectivity_type = utils.choose_connectivity_type(connectivity_types)
        if connectivity_type == NVME_OVER_FC_CONNECTIVITY_TYPE:
            vol_id = convert_scsi_id_to_nguid(vol_id)
        self.unmap_volume(vol_id, host_name)

    def copy_to_existing_volume_from_source(self, volume, source_id, source_type, minimum_volume_size):
        volume_id = volume.id
        try:
            source_object = self.get_object_by_id(source_id, source_type)
            if not source_object:
                self._rollback_create_volume_from_source(volume.id)
                raise array_errors.ObjectNotFoundError(source_id)
            source_capacity = source_object.capacity_bytes
            logger.debug("Copy {0} {1} data to volume {2}.".format(source_type, source_id, volume_id))
            self.copy_to_existing_volume(volume_id, source_id,
                                         source_capacity, minimum_volume_size)
            logger.debug("Copy volume from {0} finished".format(source_type))
        except array_errors.ObjectNotFoundError as ex:
            logger.error("Volume not found while copying {0} data to volume".format(source_type))
            logger.exception(ex)
            self._rollback_create_volume_from_source(volume.id)
            raise ex
        except Exception as ex:
            logger.error("Exception raised while copying {0} data to volume".format(source_type))
            self._rollback_create_volume_from_source(volume.id)
            raise ex

    @retry(Exception, tries=5, delay=1)
    def _rollback_create_volume_from_source(self, volume_id):
        logger.debug("Rollback copy volume from source. Deleting volume {0}".format(volume_id))
        self.delete_volume(volume_id)

    def _get_array_initiators(self, host_name, connectivity_type):
        if NVME_OVER_FC_CONNECTIVITY_TYPE == connectivity_type:
            array_initiators = []
        elif FC_CONNECTIVITY_TYPE == connectivity_type:
            array_initiators = self.get_array_fc_wwns(host_name)
        elif ISCSI_CONNECTIVITY_TYPE == connectivity_type:
            array_initiators = self.get_iscsi_targets_by_iqn(host_name)
        else:
            raise UnsupportedConnectivityTypeError(connectivity_type)
        return array_initiators
