from pyxcli.client import XCLIClient
from pyxcli import errors as xcli_errors
from controller.common.csi_logger import get_stdout_logger
from array_mediator_interface import ArrayMediator
from array_action_types import Volume
from controller.array_action.errors import CredentialsError, VolumeNotFoundError, IllegalObjectName,\
    StorageClassCapabilityNotSupported, \
    VolumeAlreadyExists, PoolDoesNotExist, PermissionDeniedError

array_connections_dict = {}
logger = get_stdout_logger()


class XIVArrayMediator(ArrayMediator):
    ARRAY_ACTIONS = {}
    BLOCK_SIZE_IN_BYTES = 512

    @property
    def array_type(self):
       return 'XIV'

    @property
    def port(self):
       return 7778

    @property
    def max_vol_name_length(self):
       return 63

    @property
    def max_connections(self):
       return 2

    @property
    def minimal_volume_size_in_bytes(self):
        return 1 * 1024 * 1024 * 1024  # 1 GiB

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
            raise CredentialsError(self.endpoint)
        except xcli_errors.XCLIError:
            raise CredentialsError(self.endpoint)

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
            raise IllegalObjectName(ex.status)

        logger.debug("cli volume returned : {}".format(cli_volume))
        if not cli_volume:
            raise VolumeNotFoundError(vol_name)

        array_vol = self._generate_volume_response(cli_volume)
        return array_vol

    def validate_supported_capabilities(self, capabilities):
        logger.info("validate_supported_capabilities for capabilities : {0}".format(capabilities))
        # for a9k there should be no capabilities
        if capabilities or len(capabilities) > 0:
            raise StorageClassCapabilityNotSupported(capabilities)

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
            raise IllegalObjectName(ex.status)
        except xcli_errors.VolumeExistsError as ex:
            logger.exception(ex)
            raise VolumeAlreadyExists(name, self.endpoint)
        except xcli_errors.PoolDoesNotExistError as ex:
            logger.exception(ex)
            raise PoolDoesNotExist(pool, self.endpoint)
        except xcli_errors.OperationForbiddenForUserCategoryError as ex:
            logger.exception(ex)
            raise PermissionDeniedError("create vol : {0}".format(name))

    def delete_volume(self, volume_id):
        logger.info("Deleting volume with id : {0}".format(volume_id))
        vol_by_wwn = self.client.cmd.vol_list(vol=volume_id).as_single_element
        if not vol_by_wwn:
            raise VolumeNotFoundError(volume_id)

        vol_name = vol_by_wwn.name
        logger.debug("found volume name : {0}".format(vol_name))

        try:
            self.client.cmd.vol_delete(vol=vol_name)
        except xcli_errors.VolumeBadNameError as ex:
            logger.exception(ex)
            raise VolumeNotFoundError(vol_name)

        except xcli_errors.OperationForbiddenForUserCategoryError as ex:
            logger.exception(ex)
            raise PermissionDeniedError("delete vol : {0}".format(vol_name))

        logger.info("Finished volume deletion. id : {0}".format(volume_id))
