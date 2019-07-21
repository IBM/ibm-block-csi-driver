from pysvc.unified.client import connect
from pysvc import errors as svc_errors
from pysvc.unified.response import CLIFailureError
from controller.common.csi_logger import get_stdout_logger
from controller.array_action.array_mediator_interface import ArrayMediator
from controller.array_action.array_action_types import Volume
import controller.array_action.errors as controller_errors
from controller.array_action.utils import classproperty
import controller.array_action.config as config

array_connections_dict = {}
logger = get_stdout_logger()

OBJ_NOT_FOUND = 'CMMVC5753E'
NAME_NOT_MEET = 'CMMVC5754E'
OBJ_ALREADY_EXIST = 'CMMVC6035E'
VOL_NOT_FOUND = 'CMMVC8957E'
POOL_NOT_MATCH_VOL_CAPABILITIES = 'CMMVC9292E'
NOT_REDUCTION_POOL = 'CMMVC9301E'


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
        'name': volume_name,
        'unit': 'b',
        'size': volume_size,
        'pool': pool_name
    })
    # if capabilities == None, create default capability volume thick
    capability = capabilities.get(config.CAPABILITIES_SPACEEFFICIENCY)
    if capability == config.CAPABILITY_THIN:
        cli_kwargs.update({'thin': True})
    elif capability == config.CAPABILITY_COMPRESSED:
        cli_kwargs.update({'compressed': True})
    elif capability == config.CAPABILITY_DEDUPLICATED:
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
        self.client = None
        # SVC only accept one IP address
        if len(endpoint) == 0 or len(endpoint) > 1:
            logger.error("SVC only support one cluster IP")
            raise controller_errors.StorageManagementIPsNotSupportError(
                endpoint)
        self.endpoint = endpoint[0]

        logger.debug("in init")
        self._connect()

    def _connect(self):
        logger.debug("Connecting to SVC {0}".format(self.endpoint))
        try:
            self.client = connect(self.endpoint, username=self.user,
                                  password=self.password)
        except (svc_errors.IncorrectCredentials,
                svc_errors.StorageArrayClientException):
            raise controller_errors.CredentialsError(self.endpoint)

    def disconnect(self):
        if self.client:
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
        cli_volume = None
        try:
            cli_volume = self.client.svcinfo.lsvdisk(
                bytes=True, object_id=vol_name).as_single_element
        except (svc_errors.CommandExecutionError, CLIFailureError) as ex:
            if not is_warning_message(ex.my_message):
                if (OBJ_NOT_FOUND in ex.my_message or
                        NAME_NOT_MEET in ex.my_message):
                    logger.error("Volume not found")
                    raise controller_errors.VolumeNotFoundError(vol_name)
        except Exception as ex:
            logger.exception(ex)
            raise ex

        if not cli_volume:
            raise controller_errors.VolumeNotFoundError(vol_name)
        logger.debug("cli volume returned : {}".format(cli_volume))
        return self._generate_volume_response(cli_volume)

    def validate_supported_capabilities(self, capabilities):
        logger.debug("validate_supported_capabilities for "
                     "capabilities : {0}".format(capabilities))
        # Currently, we only support one capability "SpaceEfficiency"
        # The value should be: "thin/thick/compressed/deduplicated"
        if capabilities:
            if config.CAPABILITIES_SPACEEFFICIENCY not in capabilities:
                logger.error("Currently, the capability {0} is not "
                             "support for SVC".format(capabilities))
                raise controller_errors.StorageClassCapabilityNotSupported(
                    capabilities)
            if (capabilities.get(config.CAPABILITIES_SPACEEFFICIENCY) not in
                    [config.CAPABILITY_THIN, config.CAPABILITY_THICK,
                     config.CAPABILITY_COMPRESSED,
                     config.CAPABILITY_DEDUPLICATED]):
                logger.error("capability value is not "
                             "supported {0}".format(capabilities))
                raise controller_errors.StorageClassCapabilityNotSupported(
                    capabilities)

        logger.info("Finished validate_supported_capabilities")

    def _convert_size_bytes(self, size_in_bytes):
        # SVC volume size must be the multiple of 512 bytes
        ret = size_in_bytes % self.BLOCK_SIZE_IN_BYTES
        if ret > 0:
            return size_in_bytes - ret + 512
        return size_in_bytes

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
                if OBJ_ALREADY_EXIST in ex.my_message:
                    raise controller_errors.VolumeAlreadyExists(name,
                                                                self.endpoint)
                if NAME_NOT_MEET in ex.my_message:
                    raise controller_errors.PoolDoesNotExist(pool,
                                                             self.endpoint)
                if (POOL_NOT_MATCH_VOL_CAPABILITIES in ex.my_message
                        or NOT_REDUCTION_POOL in ex.my_message):
                    raise controller_errors.PoolDoesNotMatchCapabilities(
                        pool, capabilities, ex)
                raise ex
        except Exception as ex:
            logger.exception(ex)
            raise ex

    def delete_volume(self, volume_id):
        logger.info("Deleting volume with id : {0}".format(volume_id))
        try:
            self.client.svctask.rmvolume(vdisk_id=volume_id)
        except (svc_errors.CommandExecutionError, CLIFailureError) as ex:
            if not is_warning_message(ex.my_message):
                logger.warning("Failed to delete volume {}, "
                               "it's already deleted.".format(volume_id))
                if (OBJ_NOT_FOUND in ex.my_message
                        or VOL_NOT_FOUND in ex.my_message):
                    raise controller_errors.VolumeNotFoundError(volume_id)
                else:
                    raise ex
        except Exception as ex:
            logger.exception(ex)
            raise ex

        logger.info("Finished volume deletion. id : {0}".format(volume_id))
