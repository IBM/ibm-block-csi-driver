from hashlib import sha1
import base58
from packaging.version import parse
from pyds8k import exceptions
from controller.common.csi_logger import get_stdout_logger
from controller.common import settings
from controller.array_action.array_mediator_interface import ArrayMediator
from controller.array_action.utils import classproperty
from controller.array_action.ds8k_rest_client import RESTClient
import controller.array_action.errors as array_errors
from controller.array_action import config
from controller.array_action.array_action_types import Volume


logger = get_stdout_logger()


# response error codes
INVALID_CREDENTIALS = 'BE7A002D'
RESOURCE_NOT_EXISTS = 'BE7A0001'
MAX_VOLUME_LENGTH = 16


def parse_version(bundle):
    """
    Parse ds8k version number from bundle.

    rule is:
    87.51.34.0 => 7.5.1
    87.50.21.1 => 7.5.0
    88.0.151.0 => 8.0.0
    """

    v1, v2, _ = bundle.split('.', 2)
    v2 = '{0:0{1}}'.format(int(v2), 2)

    return '.'.join([v1[-1], v2[0], v2[1]])


def get_volume_id_from_scsi_identifier(scsi_id):
    return scsi_id[-4:]


def hash_string(string):
    return base58.b58encode(sha1(string.encode()).digest()).decode()


#
def shorten_volume_name(name, prefix):
    """
    shorten volume name to 16 characters if it is too long.

    :param name: the origin name with prefix
    :param prefix: the prefix of the name
    :return: a short name with the origin prefix
    """

    if len(name) <= MAX_VOLUME_LENGTH:
        return name

    if not prefix:
        return hash_string(name)[:MAX_VOLUME_LENGTH]
    else:
        name_without_prefix = str(name).split(prefix+settings.NAME_PREFIX_SEPARATOR, 2)[1]
        hashed = hash_string(name_without_prefix)
        return (prefix + settings.NAME_PREFIX_SEPARATOR + hashed)[:MAX_VOLUME_LENGTH]


class DS8KArrayMediator(ArrayMediator):
    SUPPORTED_FROM_VERSION = '7.5.1'

    @classproperty
    def array_type(self):
        return 'DS8K'

    @classproperty
    def port(self):
        return 8452

    @classproperty
    def max_vol_name_length(self):
        # the max length is 16 on storage side, it is too short, use shorten_volume_name to workaround it.
        # so 63 here is just a soft limit, to make sure the volume name won't be very long.
        return 63

    @classproperty
    def max_volume_prefix_length(self):
        return 5

    @classproperty
    def max_connections(self):
        # max for rest api is 128.
        return 50

    @classproperty
    def minimal_volume_size_in_bytes(self):
        return 512  # 1 block, 512 bytes

    @classproperty
    def max_lun_retries(self):
        return 10

    def __init__(self, user, password, endpoint):
        self.user = user
        self.service_address = \
            endpoint[0] if isinstance(endpoint, list) else endpoint
        self.password = password

        self._connect()

    def _connect(self):
        try:
            self.client = RESTClient(service_address=self.service_address,
                                     user=self.user,
                                     password=self.password,
                                     )

            self.system_info = self.get_system_info()

            if parse(self.version) < parse(self.SUPPORTED_FROM_VERSION):
                raise array_errors.UnsupportedStorageVersionError(
                    self.version, self.SUPPORTED_FROM_VERSION
                )
        except exceptions.ClientError as e:
            # BE7A002D=Authentication has failed because the user name and
            # password combination that you have entered is not valid.
            if INVALID_CREDENTIALS in str(e.message).upper():
                raise array_errors.CredentialsError(self.service_address)
            else:
                raise ConnectionError()
        except exceptions.ClientException as e:
            logger.error(
                'Failed to connect to DS8K array {}, reason is {}'.format(
                    self.identifier,
                    e.details
                    )
                )
            raise ConnectionError()

    def disconnect(self):
        pass

    def get_system_info(self):
        return self.client.get_system()

    @property
    def identifier(self):
        return self.system_info.id

    @property
    def name(self):
        return self.system_info.name or self.identifier

    @property
    def version(self):
        return parse_version(self.system_info.bundle)

    @property
    def wwnn(self):
        return self.system_info.wwnn

    def _generate_volume_scsi_identifier(self, volume_id):
        return '6{}000000000000{}'.format(self.wwnn[1:], volume_id)

    def _generate_volume_response(self, res):
        return Volume(
            vol_size_bytes=int(res.cap),
            vol_id=self._generate_volume_scsi_identifier(res.id),
            vol_name=res.name,
            array_address=self.service_address,
            pool_name=res.pool,
            array_type=self.array_type
        )

    @staticmethod
    def get_se_capability_value(capabilities):
        capability = capabilities.get(config.CAPABILITIES_SPACEEFFICIENCY)
        if capability:
            capability = capability.lower()
            if capability == config.CAPABILITY_THIN:
                return "ese"
        return "none"

    def create_volume(self, name, size_in_bytes, capabilities, pool_id, volume_prefix=""):
        logger.info(
            "Creating volume with name: {}, size: {}, in pool: {}, "
            "with capabilities: {}".format(
                name, size_in_bytes, pool_id,
                capabilities
            )
        )
        try:
            cli_kwargs = {}
            cli_kwargs.update({
                'name': shorten_volume_name(name, volume_prefix),
                'capacity_in_bytes': size_in_bytes,
                'pool_id': pool_id,
                'tp': self.get_se_capability_value(capabilities),

            })
            logger.debug(
                "Start to create volume with parameters: {}".format(cli_kwargs)
            )

            try:
                # get the volume before creating again, to make sure it is not existing,
                # because volume name is not unique in ds8k.
                vol = self.get_volume(
                    name,
                    volume_context={config.CONTEXT_POOL: pool_id},
                    volume_prefix=volume_prefix
                )
                logger.info("Found volume {}".format(name))
                return vol
            except array_errors.VolumeNotFoundError:
                vol = self.client.create_volume(**cli_kwargs)

                logger.info("finished creating volume {}".format(name))
                return self._generate_volume_response(self.client.get_volume(vol.id))
        except exceptions.NotFound as ex:
            if RESOURCE_NOT_EXISTS in str(ex.message).upper():
                raise array_errors.PoolDoesNotExist(pool_id, self.identifier)
            else:
                logger.error(
                    "Failed to create volume {} on array {}, reason is: {}".format(
                        name,
                        self.identifier,
                        ex.details
                    )
                )
                raise array_errors.VolumeCreationError(name)
        except exceptions.ClientException as ex:
            logger.error(
                "Failed to create volume {} on array {}, reason is: {}".format(
                    name,
                    self.identifier,
                    ex.details
                )
            )
            raise array_errors.VolumeCreationError(name)

    def delete_volume(self, volume_id):
        logger.info("Deleting volume {}".format(volume_id))
        try:
            self.client.delete_volume(
                volume_id=get_volume_id_from_scsi_identifier(volume_id)
            )
            logger.info("Finished deleting volume {}".format(volume_id))
        except exceptions.NotFound:
            raise array_errors.VolumeNotFoundError(volume_id)
        except exceptions.ClientException as ex:
            logger.error(
                "Failed to delete volume {} on array {}, reason is: {}".format(
                    volume_id,
                    self.identifier,
                    ex.details
                )
            )
            raise array_errors.VolumeDeletionError(volume_id)

    def get_volume(self, name, volume_context=None, volume_prefix=""):
        logger.debug("Getting volume {} under context {}".format(name, volume_context))
        if not volume_context:
            logger.error(
                "volume_context is not specified, can not get volumes from storage."
            )
            raise array_errors.VolumeNotFoundError(name)

        volume_candidates = []
        if config.CONTEXT_POOL in volume_context:
            volume_candidates = self.client.get_volumes_by_pool(
                volume_context[config.CONTEXT_POOL]
            )
        for vol in volume_candidates:
            if vol.name == shorten_volume_name(name, volume_prefix):
                logger.debug("Found volume: {}".format(vol))
                return self._generate_volume_response(vol)

        raise array_errors.VolumeNotFoundError(name)

    def get_volume_mappings(self, volume_id):
        # TODO: CSI-1197
        pass

    def map_volume(self, volume_id, host_name):
        # TODO: CSI-1198
        pass

    def unmap_volume(self, volume_id, host_name):
        # TODO: CSI-1199
        pass

    def get_array_iqns(self):
        return []

    def get_array_fc_wwns(self, host_name=None):
        # TODO: CSI-1200
        pass

    def get_host_by_host_identifiers(self, initiators):
        # TODO: CSI-1201
        pass

    def validate_supported_capabilities(self, capabilities):
        logger.debug("Validating capabilities: {0}".format(capabilities))

        # Currently, we only support one capability "SpaceEfficiency"
        # The value should be: "thin"
        if (capabilities and capabilities.get(
                config.CAPABILITIES_SPACEEFFICIENCY).lower() not in
                [config.CAPABILITY_THIN, ]):
            logger.error("capabilities is not supported.")
            raise array_errors.StorageClassCapabilityNotSupported(
                capabilities)

        logger.debug("Finished validating capabilities.")
