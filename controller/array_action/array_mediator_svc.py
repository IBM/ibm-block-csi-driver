from pysvc.unified.client import connect
from pysvc import errors as svc_errors
from pysvc.unified.response import CLIFailureError
from controller.common.csi_logger import get_stdout_logger
from controller.array_action.array_mediator_interface import ArrayMediator
from controller.array_action.array_action_types import Volume
from controller.array_action.errors import CredentialsError, \
    VolumeNotFoundError, IllegalObjectName, VolumeCreateError, \
    VolumeDeleteError, NoConnectionAvailableException, \
    VolumeAlreadyExists, PoolDoesNotExist
from controller.array_action.utils import classproperty

array_connections_dict = {}
logger = get_stdout_logger()


def is_warning_message(ex):
    """ Return True if the exception message is warning """
    info_seperated_by_quotation = str(ex).split('"')
    message = info_seperated_by_quotation[1]
    word_in_message = message.split()
    message_tag = word_in_message[0]
    if message_tag[-1] == 'W':
        return True
    return False


def build_kwargs_from_capabilities(capabilities, pool_name, volume_name,
                                   volume_size):
    cli_kwargs = {}
    cli_kwargs.update({
        'object_id': volume_name,
        'unit': 'b',
        'size': volume_size,
        'pool': pool_name
    })
    # if capabilities == None, create default capability volume thick
    if capabilities.get('SpaceEfficiency') == 'Thin':
        cli_kwargs.update({'thin': True})
    elif capabilities.get('SpaceEfficiency') == 'Compression':
        cli_kwargs.update({'compressed': True})
    elif capabilities.get('SpaceEfficiency') == 'Dedup':
        cli_kwargs.update({'compressed': True, 'deduplicated': True})

    return cli_kwargs


class SVCArrayMediator(ArrayMediator):
    ARRAY_ACTIONS = {}
    BLOCK_SIZE_IN_BYTES = 512

    @classproperty
    def array_type(self):
        return 'SVC'

    @classproperty
    def port(self):
        return 22

    @classproperty
    def max_vol_name_length(self):
        return 64

    @classproperty
    def max_connections(self):
        return 2

    @classproperty
    def minimal_volume_size_in_bytes(self):
        return 512   # 512 Bytes

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
            self.client = connect(self.endpoint, username=self.user,
                                  password=self.password)

        except svc_errors.IncorrectCredentials:
            raise CredentialsError(self.endpoint)
        except (svc_errors.ConnectionTimedoutException,
                svc_errors.UnableToConnectException):
            raise NoConnectionAvailableException(self.endpoint)
        except svc_errors.StorageArrayClientException:
            raise CredentialsError(self.endpoint)

    def disconnect(self):
        if self.client and self.client.is_connected():
            self.client.close()

    def _generate_volume_response(self, cli_volume):
        return Volume(
            int(cli_volume.capacity),
            cli_volume.id,
            cli_volume.name,
            self.endpoint,
            cli_volume.mdisk_grp_name,
            self.array_type)

    def get_volume(self, vol_name):
        logger.debug("Get volume : {}".format(vol_name))
        try:
            cli_volume = self.client.svcinfo.lsvdisk(
                bytes=True, object_id=vol_name).as_single_element
        except (svc_errors.CommandExecutionError, CLIFailureError) as ex:
            logger.exception(ex)
            raise IllegalObjectName("Failed to get the "
                                    "volume : {0}".format(vol_name))

        logger.debug("cli volume returned : {}".format(cli_volume))
        if not cli_volume:
            raise VolumeNotFoundError(vol_name)
        array_vol = self._generate_volume_response(cli_volume)
        return array_vol

    def _convert_size_bytes(self, size_in_bytes):
        # SVC volume size must be the multiple of 512 bytes
        return (size_in_bytes // self.BLOCK_SIZE_IN_BYTES
                ) * self.BLOCK_SIZE_IN_BYTES + self.BLOCK_SIZE_IN_BYTES

    def create_volume(self, name, size_in_bytes, capabilities, pool):
        logger.info("creating volume with name : {}. size : {} . in pool : {} "
                    "with capabilities : {}".format(name, size_in_bytes, pool,
                                                    capabilities))
        try:
            size = self._convert_size_bytes(size_in_bytes)
            cli_kwargs = build_kwargs_from_capabilities(capabilities, pool,
                                                        name, size)
            self.client.svctask.mkvolume(**cli_kwargs)
            vol = self.get_volume(name)
            logger.info("finished creating cli volume : {}".format(vol))
            return vol
        except (svc_errors.CommandExecutionError, CLIFailureError) as ex:
            if not is_warning_message(ex.my_message):
                logger.error(msg="Cannot create volume {0}, "
                                 "Reason is: {1}".format(name, ex))
                if 'CMMVC5753E' in ex.my_message:
                    raise VolumeAlreadyExists(name, self.endpoint)
                if 'CMMVC5754E' in ex.my_message:
                    raise PoolDoesNotExist(pool, self.endpoint)
                else:
                    raise VolumeCreateError(name, self.endpoint)
        except Exception as ex:
            logger.error(msg="Cannot create volume {0}, {1}".format(name, ex))
            raise VolumeCreateError(name, self.endpoint)

    def delete_volume(self, volume_id):
        logger.info("Deleting volume with id : {0}".format(volume_id))
        try:
            self.client.svctask.rmvolume(object_id=volume_id)
        except (svc_errors.CommandExecutionError, CLIFailureError) as ex:
            if not is_warning_message(ex.my_message):
                if ('CMMVC5753E' in ex.my_message
                        or 'CMMVC8957E' in ex.my_message):
                    logger.warning("Failed to delete volume {}, "
                                   "it's already deleted.".format(volume_id))
                    raise VolumeNotFoundError(volume_id)
                else:
                    logger.error(msg="Cannot delete volume {0}, Reason "
                                     "is: {1}".format(volume_id, ex))
                    raise VolumeDeleteError(volume_id, self.endpoint)
        except Exception as ex:
            logger.info("Cannot delete volume {0}, Reason "
                        "is: {1}".format(volume_id, ex))
            raise VolumeDeleteError(volume_id, self.endpoint)

        logger.info("Finished volume deletion. id : {0}".format(volume_id))
