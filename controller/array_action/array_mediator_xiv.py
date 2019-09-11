from random import randint

from pyxcli.client import XCLIClient
from pyxcli import errors as xcli_errors
from controller.common.csi_logger import get_stdout_logger
from controller.array_action.array_mediator_interface import ArrayMediator
from controller.array_action.array_action_types import Volume
import controller.array_action.errors as controller_errors
from controller.array_action.config import ISCSI_CONNECTIVITY_TYPE
from controller.array_action.utils import classproperty

array_connections_dict = {}
logger = get_stdout_logger()


class XIVArrayMediator(ArrayMediator):
    ARRAY_ACTIONS = {}
    BLOCK_SIZE_IN_BYTES = 512
    MAX_LUN_NUMBER = 250
    MIN_LUN_NUMBER = 1

    @classproperty
    def array_type(self):
        return 'A9000'

    @classproperty
    def port(self):
        return 7778

    @classproperty
    def max_vol_name_length(self):
        return 63

    @classproperty
    def max_connections(self):
        return 2

    @classproperty
    def minimal_volume_size_in_bytes(self):
        return 1 * 1024 * 1024 * 1024  # 1 GiB

    @classproperty
    def max_lun_retries(self):
        return 10

    def __init__(self, user, password, endpoint):
        self.user = user
        self.password = password
        self.endpoint = endpoint
        self.client = None

        logger.debug("in init")
        self._connect()

    def _connect(self):
        logger.debug("connecting to endpoint")
        try:
            self.client = XCLIClient.connect_multiendpoint_ssl(
                self.user,
                self.password,
                self.endpoint
            )

        except xcli_errors.CredentialsError:
            raise controller_errors.CredentialsError(self.endpoint)
        except xcli_errors.XCLIError:
            raise controller_errors.CredentialsError(self.endpoint)

    def disconnect(self):
        if self.client and self.client.is_connected():
            self.client.close()

    def _convert_size_blocks_to_bytes(self, size_in_blocks):
        return size_in_blocks * self.BLOCK_SIZE_IN_BYTES

    def _generate_volume_response(self, cli_volume):
        return Volume(self._convert_size_blocks_to_bytes(int(cli_volume.capacity)),
                      cli_volume.wwn,
                      cli_volume.name,
                      self.endpoint,
                      cli_volume.pool_name,
                      self.array_type)

    def get_volume(self, vol_name):
        logger.debug("Get volume : {}".format(vol_name))
        try:
            cli_volume = self.client.cmd.vol_list(vol=vol_name).as_single_element
        except xcli_errors.IllegalNameForObjectError as ex:
            logger.exception(ex)
            raise controller_errors.IllegalObjectName(ex.status)

        logger.debug("cli volume returned : {}".format(cli_volume))
        if not cli_volume:
            raise controller_errors.VolumeNotFoundError(vol_name)

        array_vol = self._generate_volume_response(cli_volume)
        return array_vol

    def validate_supported_capabilities(self, capabilities):
        logger.info("validate_supported_capabilities for capabilities : {0}".format(capabilities))
        # for a9k there should be no capabilities
        if capabilities or len(capabilities) > 0:
            raise controller_errors.StorageClassCapabilityNotSupported(capabilities)

        logger.info("Finished validate_supported_capabilities")

    def _convert_size_bytes_to_blocks(self, size_in_bytes):
        """:rtype: float"""
        return float(size_in_bytes) / self.BLOCK_SIZE_IN_BYTES

    def create_volume(self, name, size_in_bytes, capabilities, pool):
        logger.info("creating volume with name : {}. size : {} . in pool : {} with capabilities : {}".format(
            name, size_in_bytes, pool, capabilities))

        size_in_blocks = int(self._convert_size_bytes_to_blocks(size_in_bytes))

        try:
            cli_volume = self.client.cmd.vol_create(vol=name, size_blocks=size_in_blocks,
                                                    pool=pool).as_single_element
            logger.info("finished creating cli volume : {}".format(cli_volume))
            return self._generate_volume_response(cli_volume)
        except xcli_errors.IllegalNameForObjectError as ex:
            logger.exception(ex)
            raise controller_errors.IllegalObjectName(ex.status)
        except xcli_errors.VolumeExistsError as ex:
            logger.exception(ex)
            raise controller_errors.VolumeAlreadyExists(name, self.endpoint)
        except xcli_errors.PoolDoesNotExistError as ex:
            logger.exception(ex)
            raise controller_errors.PoolDoesNotExist(pool, self.endpoint)
        except xcli_errors.OperationForbiddenForUserCategoryError as ex:
            logger.exception(ex)
            raise controller_errors.PermissionDeniedError("create vol : {0}".format(name))

    def _get_vol_by_wwn(self, volume_id):
        vol_by_wwn = self.client.cmd.vol_list(wwn=volume_id).as_single_element
        if not vol_by_wwn:
            raise controller_errors.VolumeNotFoundError(volume_id)

        vol_name = vol_by_wwn.name
        logger.debug("found volume name : {0}".format(vol_name))
        return vol_name

    def delete_volume(self, volume_id):
        logger.info("Deleting volume with id : {0}".format(volume_id))
        vol_name = self._get_vol_by_wwn(volume_id)

        try:
            self.client.cmd.vol_delete(vol=vol_name)
        except xcli_errors.VolumeBadNameError as ex:
            logger.exception(ex)
            raise controller_errors.VolumeNotFoundError(vol_name)

        except xcli_errors.OperationForbiddenForUserCategoryError as ex:
            logger.exception(ex)
            raise controller_errors.PermissionDeniedError("delete vol : {0}".format(vol_name))

        logger.info("Finished volume deletion. id : {0}".format(volume_id))

    def get_host_by_host_identifiers(self, iscsi_iqn, fc_wwns=None):
        logger.debug("Getting host id for initiators iscsi iqn : {0} and "
                     "fc wwns : {1}".format(iscsi_iqn, fc_wwns))
        host_list = self.client.cmd.host_list().as_list
        current_host = None
        for host in host_list:
            if iscsi_iqn.strip() == host.iscsi_ports.strip():
                logger.debug("found iscsi iqn in list : {0} for host : {1}".format(host.iscsi_ports, host.name))
                current_host = host.name
                break

        if not current_host:
            raise controller_errors.HostNotFoundError(iscsi_iqn)

        logger.debug("found host : {0}".format(current_host))
        return current_host, [ISCSI_CONNECTIVITY_TYPE]

    def get_volume_mappings(self, volume_id):
        logger.debug("Getting volume mappings for volume id : {0}".format(volume_id))
        vol_name = self._get_vol_by_wwn(volume_id)
        logger.debug("vol name : {0}".format(vol_name))
        mapping_list = self.client.cmd.vol_mapping_list(vol=vol_name).as_list
        res = {}
        for mapping in mapping_list:
            logger.debug("mapping for vol is :{0}".format(mapping))
            res[mapping.host] = mapping.lun

        return res

    def _get_next_available_lun(self, host_name):
        logger.debug("getting host mapping list for host :{0}".format(host_name))
        try:
            host_mapping_list = self.client.cmd.mapping_list(host=host_name).as_list
        except xcli_errors.HostBadNameError as ex:
            logger.exception(ex)
            raise controller_errors.HostNotFoundError(host_name)

        luns_in_use = set([host_mapping.lun for host_mapping in host_mapping_list])
        logger.debug("luns in use : {0}".format(luns_in_use))

        # try to use random lun number just in case there are many calls at the same time to reduce re-tries
        all_available_luns = [i for i in range(self.MIN_LUN_NUMBER, self.MAX_LUN_NUMBER + 1) if i not in luns_in_use]

        if len(all_available_luns) == 0:
            raise controller_errors.NoAvailableLunError(host_name)

        index = randint(0, len(all_available_luns) - 1)
        lun = all_available_luns[index]
        logger.debug("next random available lun is : {0}".format(lun))
        return lun

    def map_volume(self, volume_id, host_name):
        logger.debug("mapping volume : {0} to host : {1}".format(volume_id, host_name))
        vol_name = self._get_vol_by_wwn(volume_id)
        lun = self._get_next_available_lun(host_name)

        try:
            self.client.cmd.map_vol(host=host_name, vol=vol_name, lun=lun)
        except xcli_errors.OperationForbiddenForUserCategoryError as ex:
            logger.exception(ex)
            raise controller_errors.PermissionDeniedError("map volume : {0} to host : {1}".format(volume_id, host_name))
        except xcli_errors.VolumeBadNameError as ex:
            logger.exception(ex)
            raise controller_errors.VolumeNotFoundError(vol_name)
        except xcli_errors.HostBadNameError as ex:
            logger.exception(ex)
            raise controller_errors.HostNotFoundError(host_name)
        except xcli_errors.CommandFailedRuntimeError as ex:
            logger.exception(ex)
            if "LUN is already in use" in ex.status:
                raise controller_errors.LunAlreadyInUseError(lun, host_name)
            else:
                raise controller_errors.MappingError(vol_name, host_name, ex)

        return str(lun)

    def unmap_volume(self, volume_id, host_name):
        logger.debug("un-mapping volume : {0} from host : {1}".format(volume_id, host_name))

        vol_name = self._get_vol_by_wwn(volume_id)

        try:
            self.client.cmd.unmap_vol(host=host_name, vol=vol_name)
        except xcli_errors.VolumeBadNameError as ex:
            logger.exception(ex)
            raise controller_errors.VolumeNotFoundError(vol_name)
        except xcli_errors.HostBadNameError as ex:
            logger.exception(ex)
            raise controller_errors.HostNotFoundError(host_name)
        except xcli_errors.OperationForbiddenForUserCategoryError as ex:
            logger.exception(ex)
            raise controller_errors.PermissionDeniedError(
                "unmap volume : {0} from host : {1}".format(volume_id, host_name))
        except xcli_errors.CommandFailedRuntimeError as ex:
            logger.exception(ex)
            if "The requested mapping is not defined" in ex.status:
                raise controller_errors.VolumeAlreadyUnmappedError(vol_name)
            else:
                raise controller_errors.UnMappingError(vol_name, host_name, ex)

    def get_array_iqns(self):
        config_get_list = self.client.cmd.config_get().as_list
        array_iqn = [a for a in config_get_list if a["name"] == "iscsi_name"][0]["value"]
        return [array_iqn]

    def get_array_fc_wwns(self, host_name):
        # TODO need to be implemented
        return []
