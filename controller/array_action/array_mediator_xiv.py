from pyxcli.client import XCLIClient
from pyxcli import errors as xcli_errors
from controller.common.csi_logger import get_stdout_logger
from array_mediator_interface import ArrayMediator
from array_action_types import Volume
from errors import CredentialsError, VolumeNotFoundError, IllegalObjectName, PoolDoesNotMatchCapabilities, \
    CapabilityNotSupported, \
    VolumeAlreadyExists, PoolDoesNotExist

array_connections_dict = {}
logger = get_stdout_logger()


class XIVArrayMediator(ArrayMediator):
    ARRAY_TYPE = 'XIV'
    ARRAY_ACTIONS = {}

    BLOCK_SIZE_IN_BYTES = 512
    CONNECTION_LIMIT = 3
    MAX_CONNECTION_RETRY = 3

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
        """:rtype: int / long"""
        return size_in_blocks * self.BLOCK_SIZE_IN_BYTES

    def _generate_volume_response(self, cli_volume):
        return Volume(self._convert_size_blocks_to_bytes(int(cli_volume.capacity)),
                      cli_volume.wwn,
                      cli_volume.name,
                      self.endpoint,
                      cli_volume.pool_name,
                      self.ARRAY_TYPE)

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

    def _validate_capabiliy_supported(self, capabilities):
        # TODO: add capability name validation
        return True

    def _validate_pool_capabilities(self, pool, capabilities):
        # TODO: add pool capability validation
        return True

    def _convert_size_bytes_to_blocks(self, size_in_bytes):
        """:rtype: float"""
        return float(size_in_bytes) / self.BLOCK_SIZE_IN_BYTES

    def create_volume(self, vol_name, size_in_bytes, capabilities, pool):
        logger.debug("creating volume with name : {}. size : {} . in pool : {} with capabilities : {}".format(
            vol_name, size_in_bytes, pool, capabilities))

        res = self._validate_capabiliy_supported(capabilities)
        if not res:
            raise CapabilityNotSupported(capabilities)

        res = self._validate_pool_capabilities(pool, capabilities)
        if not res:
            raise PoolDoesNotMatchCapabilities(pool, capabilities)

        size_in_blocks = int(self._convert_size_bytes_to_blocks(size_in_bytes))

        try:
            cli_volume = self.client.cmd.vol_create(vol=vol_name, size_blocks=size_in_blocks,
                                                    pool=pool).as_single_element
            logger.debug("cli volume : {}".format(cli_volume))
            return self._generate_volume_response(cli_volume)
        except xcli_errors.IllegalNameForObjectError as ex:
            logger.exception(ex)
            raise IllegalObjectName(ex.status)
        except xcli_errors.VolumeExistsError as ex:
            logger.exception(ex)
            raise VolumeAlreadyExists(vol_name)
        except xcli_errors.PoolDoesNotExistError as ex:
            logger.exception(ex)
            raise PoolDoesNotExist(pool)

    def delete_volume(self, volume_id):
        logger.debug("Deleting volume with id : {}".format(volume_id))
        vol_by_wwn = self.client.cmd.vol_list(vol=volume_id).as_single_element
        if not vol_by_wwn:
            raise VolumeNotFoundError(volume_id)

        vol_name = vol_by_wwn.name
        logger.debug("found volume name : {}".format(vol_name))

        try:
            self.client.cmd.vol_delete(vol=vol_name)
        except xcli_errors.VolumeBadNameError as ex:
            logger.exception(ex)
            raise VolumeNotFoundError(vol_name)
