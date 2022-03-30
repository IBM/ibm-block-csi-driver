from abc import ABC
from retry import retry

import controller.array_action.errors as array_errors
from controller.array_action.array_mediator_interface import ArrayMediator
from controller.array_action.config import NVME_OVER_FC_CONNECTIVITY_TYPE, FC_CONNECTIVITY_TYPE, ISCSI_CONNECTIVITY_TYPE
from controller.array_action.errors import NoConnectionAvailableException, UnsupportedConnectivityTypeError
from controller.array_action.utils import convert_scsi_id_to_nguid
from controller.common.csi_logger import get_stdout_logger
from controller.controller_server import utils

logger = get_stdout_logger()


class ArrayMediatorAbstract(ArrayMediator, ABC):
    # https://github.com/PyCQA/pylint/issues/3975
    def __init__(self, user, password, endpoint):  # pylint: disable=super-init-not-called
        self.user = user
        self.password = password
        self.endpoint = endpoint

    @retry(NoConnectionAvailableException, tries=11, delay=1)
    def map_volume_by_initiators(self, vol_id, initiators):
        host_name, connectivity_types = self.get_host_by_host_identifiers(initiators)

        logger.debug("hostname : {}, connectivity_types  : {}".format(host_name, connectivity_types))

        connectivity_type = utils.choose_connectivity_type(connectivity_types)
        if NVME_OVER_FC_CONNECTIVITY_TYPE == connectivity_type:
            array_initiators = []
        elif FC_CONNECTIVITY_TYPE == connectivity_type:
            array_initiators = self.get_array_fc_wwns(host_name)
        elif ISCSI_CONNECTIVITY_TYPE == connectivity_type:
            array_initiators = self.get_iscsi_targets_by_iqn(host_name)
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
            raise array_errors.VolumeMappedToMultipleHostsError(mappings)

        logger.debug("no mappings were found for volume. mapping volume : {0} to host : {1}".format(vol_id, host_name))

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
            else:  # will get here only if the for statement is false.
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
