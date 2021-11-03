from collections import defaultdict
from io import StringIO

from pysvc import errors as svc_errors
from pysvc.unified.client import connect
from pysvc.unified.response import CLIFailureError
from retry import retry

import controller.array_action.config as config
import controller.array_action.errors as array_errors
import controller.controller_server.config as controller_config
from controller.array_action.array_action_types import Volume, Snapshot, Host, Replication
from controller.array_action.array_mediator_abstract import ArrayMediatorAbstract
from controller.array_action.svc_cli_result_reader import SVCListResultsReader
from controller.array_action.utils import classproperty, bytes_to_string
from controller.common import settings
from controller.common.csi_logger import get_stdout_logger

array_connections_dict = {}
logger = get_stdout_logger()

OBJ_NOT_FOUND = 'CMMVC5753E'
NAME_NOT_EXIST_OR_MEET_RULES = 'CMMVC5754E'
NON_ASCII_CHARS = 'CMMVC6017E'
INVALID_NAME = 'CMMVC6527E'
TOO_MANY_CHARS = 'CMMVC5738E'
VALUE_TOO_LONG = 'CMMVC5703E'
INVALID_FILTER_VALUE = 'CMMVC5741E'
SPECIFIED_OBJ_NOT_EXIST = 'CMMVC5804E'
LUN_ALREADY_IN_USE = 'CMMVC5879E'
VOL_ALREADY_UNMAPPED = 'CMMVC5842E'
OBJ_ALREADY_EXIST = 'CMMVC6035E'
FCMAP_ALREADY_EXIST = 'CMMVC6466E'
FCMAP_ALREADY_COPYING = 'CMMVC5907E'
FCMAP_ALREADY_IN_THE_STOPPED_STATE = 'CMMVC5912E'
VOL_NOT_FOUND = 'CMMVC8957E'
POOL_NOT_MATCH_VOL_SPACE_EFFICIENCY = 'CMMVC9292E'
NOT_REDUCTION_POOL = 'CMMVC9301E'
NOT_ENOUGH_EXTENTS_IN_POOL_EXPAND = 'CMMVC5860E'
NOT_ENOUGH_EXTENTS_IN_POOL_CREATE = 'CMMVC8710E'

LIST_HOSTS_CMD_FORMAT = 'lshost {HOST_ID};'
HOST_ID_PARAM = 'id'
HOST_NAME_PARAM = 'name'
HOST_ISCSI_NAMES_PARAM = 'iscsi_name'
HOST_WWPNS_PARAM = 'WWPN'
HOST_PORTSET_ID = 'portset_id'
HOSTS_LIST_ERR_MSG_MAX_LENGTH = 300

FCMAP_STATUS_DONE = 'idle_or_copied'
RCRELATIONSHIP_STATE_IDLE = 'idling'
RCRELATIONSHIP_STATE_READY = 'consistent_synchronized'

YES = 'yes'

ENDPOINT_TYPE_SOURCE = 'source'
ENDPOINT_TYPE_TARGET = 'target'

ENDPOINT_TYPE_MASTER = 'master'
ENDPOINT_TYPE_AUX = 'aux'


def is_warning_message(ex):
    """ Return True if the exception message is warning """
    info_seperated_by_quotation = str(ex).split('"')
    message = info_seperated_by_quotation[1]
    word_in_message = message.split()
    message_tag = word_in_message[0]
    if message_tag[-1] == 'W':
        return True
    return False


def _get_array_space_efficiency(space_efficiency):
    if space_efficiency:
        space_efficiency = space_efficiency.lower()
        if space_efficiency == config.SPACE_EFFICIENCY_THIN:
            return {'thin': True}
        if space_efficiency == config.SPACE_EFFICIENCY_COMPRESSED:
            return {'compressed': True}
        if space_efficiency == config.SPACE_EFFICIENCY_DEDUPLICATED_THIN:
            return {'deduplicated': True, 'thin': True}
        if space_efficiency in (config.SPACE_EFFICIENCY_DEDUPLICATED,
                                config.SPACE_EFFICIENCY_DEDUPLICATED_COMPRESSED):
            return {'deduplicated': True, 'compressed': True}
    return {}


def build_kwargs_from_parameters(space_efficiency, pool_name, volume_name,
                                 volume_size):
    cli_kwargs = {}
    cli_kwargs.update({
        'name': volume_name,
        'unit': 'b',
        'size': volume_size,
        'pool': pool_name
    })
    space_efficiency_kwargs = _get_array_space_efficiency(space_efficiency)
    cli_kwargs.update(space_efficiency_kwargs)
    return cli_kwargs


def build_create_replication_kwargs(master_cli_volume_id, aux_cli_volume_id, other_system_id, copy_type):
    cli_kwargs = {
        'master': master_cli_volume_id,
        'aux': aux_cli_volume_id,
        'cluster': other_system_id,
    }
    if copy_type == config.REPLICATION_COPY_TYPE_ASYNC:
        cli_kwargs.update({'global': True})
    return cli_kwargs


def build_start_replication_kwargs(rcrelationship_id, primary_endpoint_type, force):
    cli_kwargs = {'object_id': rcrelationship_id}
    if primary_endpoint_type:
        cli_kwargs.update({'primary': primary_endpoint_type})
    if force:
        cli_kwargs.update({'force': True})
    return cli_kwargs


def build_stop_replication_kwargs(rcrelationship_id, add_access):
    cli_kwargs = {'object_id': rcrelationship_id}
    if add_access:
        cli_kwargs.update({'access': True})
    return cli_kwargs


def _get_cli_volume_space_efficiency(cli_volume):
    space_efficiency = config.SPACE_EFFICIENCY_THICK
    if cli_volume.se_copy == YES:
        space_efficiency = config.SPACE_EFFICIENCY_THIN
    if cli_volume.compressed_copy == YES:
        space_efficiency = config.SPACE_EFFICIENCY_COMPRESSED
    if hasattr(cli_volume, "deduplicated_copy"):
        if cli_volume.deduplicated_copy == YES:
            if cli_volume.se_copy == YES:
                space_efficiency = config.SPACE_EFFICIENCY_DEDUPLICATED_THIN
            else:
                space_efficiency = config.SPACE_EFFICIENCY_DEDUPLICATED_COMPRESSED
    return space_efficiency


class SVCArrayMediator(ArrayMediatorAbstract):
    ARRAY_ACTIONS = {}
    BLOCK_SIZE_IN_BYTES = 512
    MAX_LUN_NUMBER = 511
    MIN_LUN_NUMBER = 0

    @classproperty
    def array_type(self):
        return settings.ARRAY_TYPE_SVC

    @classproperty
    def port(self):
        return 22

    @classproperty
    def max_object_name_length(self):
        return 63

    @classproperty
    def max_object_prefix_length(self):
        return 20

    @classproperty
    def max_connections(self):
        return 2

    @classproperty
    def minimal_volume_size_in_bytes(self):
        return 512  # 512 Bytes

    @classproperty
    def maximal_volume_size_in_bytes(self):
        return 256 * 1024 * 1024 * 1024 * 1024

    @classproperty
    def max_lun_retries(self):
        return 10

    @classproperty
    def default_object_prefix(self):
        return "CSI"

    def __init__(self, user, password, endpoint):
        self.user = user
        self.password = password
        self.client = None
        # SVC only accept one IP address
        if len(endpoint) == 0 or len(endpoint) > 1:
            logger.error("SVC only support one cluster IP")
            raise array_errors.StorageManagementIPsNotSupportError(
                endpoint)
        self.endpoint = endpoint[0]
        self._identifier = None

        logger.debug("in init")
        self._connect()

    def _connect(self):
        logger.debug("Connecting to SVC {0}".format(self.endpoint))
        try:
            self.client = connect(self.endpoint, username=self.user,
                                  password=self.password)
        except (svc_errors.IncorrectCredentials,
                svc_errors.StorageArrayClientException):
            raise array_errors.CredentialsError(self.endpoint)

    def disconnect(self):
        if self.client:
            self.client.close()

    def get_system_info(self):
        for cluster in self.client.svcinfo.lssystem():
            if cluster['location'] == 'local':
                return cluster
        return None

    @property
    def identifier(self):
        if self._identifier is None:
            cluster = self.get_system_info()
            self._identifier = cluster['id_alias']
        return self._identifier

    def is_active(self):
        return self.client.transport.transport.get_transport().is_active()

    def _generate_volume_response(self, cli_volume):
        source_volume_wwn = self._get_source_volume_wwn_if_exists(cli_volume)
        space_efficiency = _get_cli_volume_space_efficiency(cli_volume)
        return Volume(
            capacity_bytes=int(cli_volume.capacity),
            id=cli_volume.vdisk_UID,
            internal_id=cli_volume.id,
            name=cli_volume.name,
            array_address=self.endpoint,
            pool=cli_volume.mdisk_grp_name,
            copy_source_id=source_volume_wwn,
            array_type=self.array_type,
            space_efficiency=space_efficiency,
            default_space_efficiency=config.SPACE_EFFICIENCY_THICK
        )

    def _generate_snapshot_response(self, cli_snapshot, source_volume_id):
        return Snapshot(int(cli_snapshot.capacity),
                        cli_snapshot.vdisk_UID,
                        cli_snapshot.id,
                        cli_snapshot.name,
                        self.endpoint,
                        source_volume_id=source_volume_id,
                        is_ready=True,
                        array_type=self.array_type)

    def _generate_snapshot_response_with_verification(self, cli_object):
        if not cli_object.FC_id:
            logger.error("FlashCopy Mapping not found for target volume: {}".format(cli_object.name))
            raise array_errors.ExpectedSnapshotButFoundVolumeError(cli_object.name, self.endpoint)
        fcmap = self._get_fcmap_as_target_if_exists(cli_object.name)
        if fcmap is None or fcmap.copy_rate != '0':
            raise array_errors.ExpectedSnapshotButFoundVolumeError(cli_object.name, self.endpoint)
        source_volume_id = self._get_wwn_by_volume_name_if_exists(fcmap.source_vdisk_name)
        return self._generate_snapshot_response(cli_object, source_volume_id)

    def _get_cli_volume(self, volume_name, not_exist_err=True):
        try:
            cli_volume = self.client.svcinfo.lsvdisk(bytes=True, object_id=volume_name).as_single_element
            if not cli_volume and not_exist_err:
                raise array_errors.ObjectNotFoundError(volume_name)
            return cli_volume
        except (svc_errors.CommandExecutionError, CLIFailureError) as ex:
            if not is_warning_message(ex.my_message):
                if (OBJ_NOT_FOUND in ex.my_message or
                        NAME_NOT_EXIST_OR_MEET_RULES in ex.my_message):
                    logger.info("volume not found")
                    if not_exist_err:
                        raise array_errors.ObjectNotFoundError(volume_name)
                elif any(msg_id in ex.my_message for msg_id in (NON_ASCII_CHARS, VALUE_TOO_LONG)):
                    raise array_errors.IllegalObjectName(ex.my_message)
                else:
                    raise ex
        return None

    def _get_cli_volume_if_exists(self, volume_name):
        cli_volume = self._get_cli_volume(volume_name, not_exist_err=False)
        logger.debug("cli volume returned : {}".format(cli_volume))
        return cli_volume

    def _get_fcmap_as_target_if_exists(self, volume_name):
        fcmaps_as_target = self._get_fcmaps(volume_name, ENDPOINT_TYPE_TARGET)
        if len(fcmaps_as_target) != 1:
            return None
        return fcmaps_as_target[0]

    def _get_fcmaps_as_source_if_exist(self, volume_name):
        return self._get_fcmaps(volume_name, ENDPOINT_TYPE_SOURCE)

    def _get_source_volume_wwn_if_exists(self, target_cli_object):
        fcmap = self._get_fcmap_as_target_if_exists(target_cli_object.name)
        if not fcmap:
            return None
        source_volume_name = fcmap.source_vdisk_name
        return self._get_wwn_by_volume_name_if_exists(source_volume_name)

    def get_volume(self, volume_name, pool=None):
        cli_volume = self._get_cli_volume(volume_name)
        return self._generate_volume_response(cli_volume)

    def _get_object_fcmaps(self, object_name):
        all_fcmaps = []
        fcmap_as_target = self._get_fcmap_as_target_if_exists(object_name)
        if fcmap_as_target:
            all_fcmaps.append(fcmap_as_target)
        all_fcmaps.extend(self._get_fcmaps_as_source_if_exist(object_name))
        return all_fcmaps

    def _expand_cli_volume(self, cli_volume, increase_in_bytes):
        volume_name = cli_volume.name
        try:
            self.client.svctask.expandvdisksize(vdisk_id=volume_name, unit='b', size=increase_in_bytes)
        except (svc_errors.CommandExecutionError, CLIFailureError) as ex:
            if not is_warning_message(ex.my_message):
                logger.warning("Failed to expand volume {}".format(volume_name))
                if OBJ_NOT_FOUND in ex.my_message or VOL_NOT_FOUND in ex.my_message:
                    raise array_errors.ObjectNotFoundError(volume_name)
                if NOT_ENOUGH_EXTENTS_IN_POOL_EXPAND in ex.my_message:
                    raise array_errors.NotEnoughSpaceInPool(id_or_name=cli_volume.mdisk_grp_name)
                raise ex

    def expand_volume(self, volume_id, required_bytes):
        logger.info("Expanding volume with id : {0} to {1} bytes".format(volume_id, required_bytes))
        cli_volume = self._get_cli_volume_by_wwn(volume_id, not_exist_err=True)
        volume_name = cli_volume.name
        fcmaps = self._get_object_fcmaps(volume_name)
        self._safe_delete_fcmaps(volume_name, fcmaps)

        current_size = int(cli_volume.capacity)
        final_size = self._convert_size_bytes(required_bytes)
        increase_in_bytes = final_size - current_size
        self._expand_cli_volume(cli_volume, increase_in_bytes)
        logger.info(
            "Finished volume expansion. id : {0}. volume increased by {1} bytes".format(volume_id, increase_in_bytes))

    def _get_fcmaps(self, volume_name, endpoint_type):
        """
        Args:
            endpoint_type : 'source' or 'target'
        """
        filter_value = '{0}_vdisk_name={1}'.format(endpoint_type, volume_name)
        return self.client.svcinfo.lsfcmap(filtervalue=filter_value).as_list

    def validate_supported_space_efficiency(self, space_efficiency):
        logger.debug("validate_supported_space_efficiency for "
                     "space efficiency : {0}".format(space_efficiency))
        if (space_efficiency and space_efficiency.lower() not in
                [config.SPACE_EFFICIENCY_THIN, config.SPACE_EFFICIENCY_THICK,
                 config.SPACE_EFFICIENCY_COMPRESSED,
                 config.SPACE_EFFICIENCY_DEDUPLICATED,
                 config.SPACE_EFFICIENCY_DEDUPLICATED_COMPRESSED,
                 config.SPACE_EFFICIENCY_DEDUPLICATED_THIN]):
            logger.error("space efficiency value is not "
                         "supported {0}".format(space_efficiency))
            raise array_errors.SpaceEfficiencyNotSupported(
                space_efficiency)

        logger.info("Finished validate_supported_space_efficiency")

    def _convert_size_bytes(self, size_in_bytes):
        # SVC volume size must be the multiple of 512 bytes
        ret = size_in_bytes % self.BLOCK_SIZE_IN_BYTES
        if ret > 0:
            return size_in_bytes - ret + 512
        return size_in_bytes

    def _get_wwn_by_volume_name_if_exists(self, volume_name):
        cli_volume = self._get_cli_volume_if_exists(volume_name)
        if not cli_volume:
            return None

        wwn = cli_volume.vdisk_UID
        logger.debug("found wwn : {0}".format(wwn))
        return wwn

    def _get_cli_volume_by_wwn(self, volume_id, not_exist_err=False):
        filter_value = 'vdisk_UID=' + volume_id
        try:
            cli_volume = self.client.svcinfo.lsvdisk(bytes=True, filtervalue=filter_value).as_single_element
        except (svc_errors.CommandExecutionError, CLIFailureError) as ex:
            if not is_warning_message(ex.my_message):
                if any(msg_id in ex.my_message for msg_id in (NON_ASCII_CHARS, INVALID_FILTER_VALUE)):
                    raise array_errors.IllegalObjectID(ex.my_message)
                raise ex
        if not cli_volume and not_exist_err:
            raise array_errors.ObjectNotFoundError(volume_id)
        return cli_volume

    def _get_volume_name_by_wwn_if_exists(self, volume_id):
        cli_volume = self._get_cli_volume_by_wwn(volume_id)
        if not cli_volume:
            return None
        vol_name = cli_volume.name
        logger.debug("found volume name : {0}".format(vol_name))
        return vol_name

    def _get_volume_name_by_wwn(self, volume_id):
        vol_name = self._get_volume_name_by_wwn_if_exists(volume_id)
        if not vol_name:
            raise array_errors.ObjectNotFoundError(volume_id)
        return vol_name

    def _create_cli_volume(self, name, size_in_bytes, space_efficiency, pool):
        logger.info("creating volume with name : {}. size : {} . in pool : {} with parameters : {}".format(
            name, size_in_bytes, pool, space_efficiency))
        try:
            size = self._convert_size_bytes(size_in_bytes)
            cli_kwargs = build_kwargs_from_parameters(space_efficiency, pool,
                                                      name, size)
            self.client.svctask.mkvolume(**cli_kwargs)
            cli_volume = self._get_cli_volume(name)
            logger.info("finished creating cli volume : {}".format(cli_volume.name))
            return cli_volume
        except (svc_errors.CommandExecutionError, CLIFailureError) as ex:
            if not is_warning_message(ex.my_message):
                logger.error(msg="Cannot create volume {0}, Reason is: {1}".format(name, ex))
                if OBJ_ALREADY_EXIST in ex.my_message:
                    raise array_errors.VolumeAlreadyExists(name, self.endpoint)
                if NAME_NOT_EXIST_OR_MEET_RULES in ex.my_message:
                    raise array_errors.PoolDoesNotExist(pool, self.endpoint)
                if POOL_NOT_MATCH_VOL_SPACE_EFFICIENCY in ex.my_message or NOT_REDUCTION_POOL in ex.my_message:
                    raise array_errors.PoolDoesNotMatchSpaceEfficiency(pool, space_efficiency, ex)
                if NOT_ENOUGH_EXTENTS_IN_POOL_CREATE in ex.my_message:
                    raise array_errors.NotEnoughSpaceInPool(id_or_name=pool)
                if any(msg_id in ex.my_message for msg_id in (NON_ASCII_CHARS, INVALID_NAME, TOO_MANY_CHARS)):
                    raise array_errors.IllegalObjectName(ex.my_message)
                raise ex
        return None

    @retry(svc_errors.StorageArrayClientException, tries=5, delay=1)
    def _rollback_copy_to_target_volume(self, target_volume_name):
        self._delete_unstarted_fcmap_if_exists(target_volume_name)

    def _copy_to_target_volume(self, target_volume_name, source_volume_name):
        logger.debug("copying volume {0} data to volume {1}.".format(source_volume_name,
                                                                     target_volume_name))
        try:
            return self._create_and_start_fcmap(source_volume_name, target_volume_name, is_copy=True)
        except (svc_errors.CommandExecutionError, CLIFailureError) as ex:
            logger.error("Failed to copy to '{0}': {1}".format(target_volume_name, ex))
            logger.info("rolling back copy to '{0}'".format(target_volume_name))
            self._rollback_copy_to_target_volume(target_volume_name)
            raise ex

    def copy_to_existing_volume_from_source(self, volume_id, source_id, source_capacity_in_bytes,
                                            minimum_volume_size_in_bytes):
        source_name = self._get_volume_name_by_wwn(source_id)
        target_volume_name = self._get_volume_name_by_wwn(volume_id)
        self._copy_to_target_volume(target_volume_name, source_name)

    def create_volume(self, name, size_in_bytes, space_efficiency, pool):
        cli_volume = self._create_cli_volume(name, size_in_bytes, space_efficiency, pool)

        return self._generate_volume_response(cli_volume)

    def _delete_volume_by_name(self, volume_name, not_exist_err=True):
        logger.info("deleting volume with name : {0}".format(volume_name))
        try:
            self.client.svctask.rmvolume(vdisk_id=volume_name)
        except (svc_errors.CommandExecutionError, CLIFailureError) as ex:
            if not is_warning_message(ex.my_message):
                logger.warning("Failed to delete volume {}".format(volume_name))
                if (OBJ_NOT_FOUND in ex.my_message or VOL_NOT_FOUND in ex.my_message) and not_exist_err:
                    raise array_errors.ObjectNotFoundError(volume_name)
                raise ex

    def delete_volume(self, volume_id):
        logger.info("Deleting volume with id : {0}".format(volume_id))
        cli_volume = self._get_cli_volume_by_wwn(volume_id, not_exist_err=True)
        self._delete_object(cli_volume)
        logger.info("Finished volume deletion. id : {0}".format(volume_id))

    def get_snapshot(self, volume_id, snapshot_name, pool=None):
        logger.debug("Get snapshot : {}".format(snapshot_name))
        target_cli_volume = self._get_cli_volume_if_exists(snapshot_name)
        if not target_cli_volume:
            return None
        return self._generate_snapshot_response_with_verification(target_cli_volume)

    def get_object_by_id(self, object_id, object_type):
        cli_object = self._get_cli_volume_by_wwn(object_id)
        if not cli_object:
            return None
        if object_type is controller_config.SNAPSHOT_TYPE_NAME:
            return self._generate_snapshot_response_with_verification(cli_object)
        cli_volume = self._get_cli_volume(cli_object.name)
        return self._generate_volume_response(cli_volume)

    def _create_similar_volume(self, source_cli_volume, target_volume_name, space_efficiency, pool):
        logger.info("creating target cli volume '{0}' from source volume '{1}'".format(target_volume_name,
                                                                                       source_cli_volume.name))
        if not space_efficiency:
            space_efficiency = _get_cli_volume_space_efficiency(source_cli_volume)
        size_in_bytes = int(source_cli_volume.capacity)
        if not pool:
            pool = source_cli_volume.mdisk_grp_name
        self._create_cli_volume(target_volume_name, size_in_bytes, space_efficiency, pool)

    def _create_fcmap(self, source_volume_name, target_volume_name, is_copy):
        logger.info("creating FlashCopy Mapping from '{0}' to '{1}'".format(source_volume_name, target_volume_name))
        mkfcmap_kwargs = {} if is_copy else {'copyrate': 0}
        try:
            self.client.svctask.mkfcmap(source=source_volume_name, target=target_volume_name, **mkfcmap_kwargs)
        except (svc_errors.CommandExecutionError, CLIFailureError) as ex:
            if not is_warning_message(ex.my_message):
                if FCMAP_ALREADY_EXIST in ex.my_message:
                    logger.info(("FlashCopy Mapping already exists"
                                 " for source '{0}' and target '{1}'").format(source_volume_name,
                                                                              target_volume_name))
                else:
                    raise ex

    def _start_fcmap(self, fcmap_id):
        logger.info("starting FlashCopy Mapping '{0}'".format(fcmap_id))
        try:
            self.client.svctask.startfcmap(prep=True, object_id=fcmap_id)
        except (svc_errors.CommandExecutionError, CLIFailureError) as ex:
            if not is_warning_message(ex.my_message):
                if FCMAP_ALREADY_COPYING in ex.my_message:
                    logger.info("FlashCopy Mapping '{0}' already copying".format(fcmap_id))
                else:
                    raise ex

    def _create_and_start_fcmap(self, source_volume_name, target_volume_name, is_copy):
        self._create_fcmap(source_volume_name, target_volume_name, is_copy)
        target_cli_volume = self._get_cli_volume(target_volume_name)
        self._start_fcmap(target_cli_volume.FC_id)
        return target_cli_volume

    def _delete_fcmap(self, fcmap_id, force):
        logger.info("deleting fcmap with id : {0}".format(fcmap_id))
        try:
            self.client.svctask.rmfcmap(object_id=fcmap_id, force=force)
        except (svc_errors.CommandExecutionError, CLIFailureError) as ex:
            if not is_warning_message(ex.my_message):
                logger.error("Failed to delete fcmap '{0}': {1}".format(fcmap_id, ex))
                raise ex

    def _stop_fcmap(self, fcmap_id):
        logger.info("stopping fcmap with id : {0}".format(fcmap_id))
        try:
            self.client.svctask.stopfcmap(object_id=fcmap_id)
        except (svc_errors.CommandExecutionError, CLIFailureError) as ex:
            if not is_warning_message(ex.my_message):
                if FCMAP_ALREADY_IN_THE_STOPPED_STATE in ex.my_message:
                    logger.info("fcmap '{0}' is already in the stopped state".format(fcmap_id))
                else:
                    logger.error("Failed to stop fcmap '{0}': {1}".format(fcmap_id, ex))
                    raise ex

    def _safe_stop_and_delete_fcmap(self, fcmap):
        if not self._is_in_remote_copy_relationship(fcmap):
            self._stop_fcmap(fcmap.id)
            self._delete_fcmap(fcmap.id, force=True)

    def _safe_delete_fcmaps(self, object_name, fcmaps):
        fcmaps_to_delete = []
        fcmaps_in_use = []

        for fcmap in fcmaps:
            if not self._is_in_remote_copy_relationship(fcmap):
                if fcmap.status != FCMAP_STATUS_DONE or fcmap.copy_rate == "0":
                    fcmaps_in_use.append(fcmap)
                else:
                    fcmaps_to_delete.append(fcmap)
        if fcmaps_in_use:
            raise array_errors.ObjectIsStillInUseError(id_or_name=object_name, used_by=fcmaps_in_use)
        for fcmap in fcmaps_to_delete:
            self._delete_fcmap(fcmap.id, force=False)

    def _is_in_remote_copy_relationship(self, fcmap):
        return fcmap.rc_controlled == YES

    def _delete_object(self, cli_object, is_snapshot=False):
        object_name = cli_object.name
        fcmap_as_target = self._get_fcmap_as_target_if_exists(object_name)
        if is_snapshot and not fcmap_as_target:
            raise array_errors.ObjectNotFoundError(object_name)
        fcmaps_as_source = self._get_fcmaps_as_source_if_exist(object_name)
        if fcmaps_as_source:
            self._safe_delete_fcmaps(object_name, fcmaps_as_source)
        if fcmap_as_target:
            self._safe_stop_and_delete_fcmap(fcmap_as_target)
        self._delete_volume_by_name(object_name)

    def _delete_unstarted_fcmap_if_exists(self, target_volume_name):
        target_cli_volume = self._get_cli_volume_if_exists(target_volume_name)
        if target_cli_volume and target_cli_volume.FC_id:
            self._delete_fcmap(target_cli_volume.FC_id, force=False)
        return target_cli_volume

    def _delete_target_volume_if_exists(self, target_cli_volume):
        if target_cli_volume:
            self._delete_volume_by_name(target_cli_volume.name, not_exist_err=False)

    @retry(svc_errors.StorageArrayClientException, tries=5, delay=1)
    def _rollback_create_snapshot(self, target_volume_name):
        target_cli_volume = self._delete_unstarted_fcmap_if_exists(target_volume_name)
        self._delete_target_volume_if_exists(target_cli_volume)

    def _create_snapshot(self, target_volume_name, source_cli_volume, space_efficiency, pool):
        try:
            self._create_similar_volume(source_cli_volume, target_volume_name, space_efficiency, pool)
            return self._create_and_start_fcmap(source_cli_volume.name, target_volume_name, is_copy=False)
        except (svc_errors.CommandExecutionError, CLIFailureError) as ex:
            logger.error("Failed to create snapshot '{0}': {1}".format(target_volume_name, ex))
            logger.info("rolling back create snapshot '{0}'".format(target_volume_name))
            self._rollback_create_snapshot(target_volume_name)
            raise ex

    def create_snapshot(self, volume_id, snapshot_name, space_efficiency, pool):
        logger.info("creating snapshot '{0}' from volume '{1}'".format(snapshot_name, volume_id))
        source_volume_name = self._get_volume_name_by_wwn(volume_id)
        source_cli_volume = self._get_cli_volume(source_volume_name)
        target_cli_volume = self._create_snapshot(snapshot_name, source_cli_volume, space_efficiency, pool)
        logger.info("finished creating snapshot '{0}' from volume '{1}'".format(snapshot_name, volume_id))
        return self._generate_snapshot_response(target_cli_volume, source_cli_volume.vdisk_UID)

    def delete_snapshot(self, snapshot_id):
        logger.info("Deleting snapshot with id : {0}".format(snapshot_id))
        cli_volume = self._get_cli_volume_by_wwn(snapshot_id)
        if not cli_volume or not cli_volume.FC_id:
            raise array_errors.ObjectNotFoundError(snapshot_id)
        self._delete_object(cli_volume, is_snapshot=True)
        logger.info("Finished snapshot deletion. id : {0}".format(snapshot_id))

    def get_host_by_host_identifiers(self, initiators):
        logger.debug("Getting host name for initiators : {0}".format(initiators))
        detailed_hosts_list = self._get_detailed_hosts_list()
        iscsi_host, fc_host = None, None
        for host in detailed_hosts_list:
            if initiators.is_array_iscsi_iqns_match(host.iscsi_names):
                iscsi_host = host.name
                logger.debug("found iscsi iqn in list : {0} for host : "
                             "{1}".format(initiators.iscsi_iqn, iscsi_host))
            if initiators.is_array_wwns_match(host.wwns):
                fc_host = host.name
                logger.debug("found fc wwns in list : {0} for host : "
                             "{1}".format(initiators.fc_wwns, fc_host))
        if iscsi_host and fc_host:
            if iscsi_host == fc_host:
                return fc_host, [config.ISCSI_CONNECTIVITY_TYPE,
                                 config.FC_CONNECTIVITY_TYPE]
            raise array_errors.MultipleHostsFoundError(initiators, fc_host)
        if iscsi_host:
            logger.debug("found host : {0} with iqn : {1}".format(iscsi_host, initiators.iscsi_iqn))
            return iscsi_host, [config.ISCSI_CONNECTIVITY_TYPE]
        if fc_host:
            logger.debug("found host : {0} with fc wwn : {1}".format(fc_host, initiators.fc_wwns))
            return fc_host, [config.FC_CONNECTIVITY_TYPE]
        logger.debug("can not found host by using initiators: {0} ".format(initiators))
        raise array_errors.HostNotFoundError(initiators)

    def _get_detailed_hosts_list(self):
        logger.debug("Getting detailed hosts list on array {0}".format(self.endpoint))
        hosts_list = self.client.svcinfo.lshost()
        if not hosts_list:
            return []

        # get all hosts details by sending a single batch of commands, in which each command is per host
        detailed_hosts_list_cmd = self._get_detailed_hosts_list_cmd(hosts_list)
        logger.debug("Sending getting detailed hosts list commands batch")
        detailed_hosts_list_output, detailed_hosts_list_errors = self._send_raw_cli_command(detailed_hosts_list_cmd)
        if detailed_hosts_list_errors:
            logger.error("Errors returned from getting detailed hosts list: {0}".format(detailed_hosts_list_errors))

        return self._get_detailed_hosts_by_raw_output(detailed_hosts_list_output)

    def _get_detailed_hosts_by_raw_output(self, detailed_hosts_list_raw_output):
        logger.debug("Reading detailed hosts list commands batch response")
        hosts_reader = SVCListResultsReader(detailed_hosts_list_raw_output)
        res = []
        for host_details in hosts_reader:
            host_id = host_details.get(HOST_ID_PARAM)
            host_name = host_details.get(HOST_NAME_PARAM)
            iscsi_names = host_details.get_as_list(HOST_ISCSI_NAMES_PARAM)
            wwns = host_details.get_as_list(HOST_WWPNS_PARAM)
            host = Host(host_id, host_name, iscsi_names, wwns)
            res.append(host)
        return res

    def _get_detailed_hosts_list_cmd(self, host_list):
        writer = StringIO()
        for host in host_list:
            host_id = host.get(HOST_ID_PARAM)
            writer.write(LIST_HOSTS_CMD_FORMAT.format(HOST_ID=host_id))
        return writer.getvalue()

    def _send_raw_cli_command(self, cmd):
        output_as_bytes, errors_as_bytes = self.client.send_raw_command(cmd)
        output_as_str = bytes_to_string(output_as_bytes)
        errors_as_str = bytes_to_string(errors_as_bytes)
        formatted_errors_as_str = self._truncate_error_msg(errors_as_str)
        return output_as_str, formatted_errors_as_str

    def _truncate_error_msg(self, detailed_host_list_errors):
        if len(detailed_host_list_errors) <= HOSTS_LIST_ERR_MSG_MAX_LENGTH:
            return detailed_host_list_errors
        return "{0} ...".format(detailed_host_list_errors[HOSTS_LIST_ERR_MSG_MAX_LENGTH])

    def get_volume_mappings(self, volume_id):
        logger.debug("Getting volume mappings for volume id : "
                     "{0}".format(volume_id))
        vol_name = self._get_volume_name_by_wwn(volume_id)
        logger.debug("volume name : {0}".format(vol_name))
        try:
            mapping_list = self.client.svcinfo.lsvdiskhostmap(vdisk_name=vol_name)
            res = {}
            for mapping in mapping_list:
                logger.debug("mapping for volume is :{0}".format(mapping))
                res[mapping.get('host_name', '')] = mapping.get('SCSI_id', '')
        except(svc_errors.CommandExecutionError, CLIFailureError) as ex:
            logger.error(ex)
            raise array_errors.ObjectNotFoundError(volume_id)

        return res

    def _get_used_lun_ids_from_host(self, host_name):
        logger.debug("getting used lun ids for host :{0}".format(host_name))
        luns_in_use = set()

        try:
            for mapping in self.client.svcinfo.lshostvdiskmap(host=host_name):
                luns_in_use.add(mapping.get('SCSI_id', ''))
        except(svc_errors.CommandExecutionError, CLIFailureError) as ex:
            logger.error(ex)
            raise array_errors.HostNotFoundError(host_name)
        logger.debug("The used lun ids for host :{0}".format(luns_in_use))

        return luns_in_use

    def get_first_free_lun(self, host_name):
        logger.debug("getting first free lun id for "
                     "host :{0}".format(host_name))
        lun = None
        luns_in_use = self._get_used_lun_ids_from_host(host_name)
        # Today we have SS_MAX_HLUN_MAPPINGS_PER_HOST as 2048 on high end
        # platforms (SVC / V7000 etc.) and 512 for the lower
        # end platforms (V3500 etc.). This limits the number of volumes that
        # can be mapped to a single host. (Note that some hosts such as linux
        # do not support more than 255 or 511 mappings today irrespective of
        # our constraint).
        for candidate in range(self.MIN_LUN_NUMBER, self.MAX_LUN_NUMBER + 1):
            if str(candidate) not in luns_in_use:
                logger.debug("First available LUN number for {0} is "
                             "{1}".format(host_name, str(candidate)))
                lun = str(candidate)
                break
        if not lun:
            raise array_errors.NoAvailableLunError(host_name)
        logger.debug("The first available lun is : {0}".format(lun))
        return lun

    def map_volume(self, volume_id, host_name):
        logger.debug("mapping volume : {0} to host : "
                     "{1}".format(volume_id, host_name))
        vol_name = self._get_volume_name_by_wwn(volume_id)
        cli_kwargs = {
            'host': host_name,
            'object_id': vol_name,
            'force': True
        }

        try:
            lun = self.get_first_free_lun(host_name)
            cli_kwargs.update({'scsi': lun})
            self.client.svctask.mkvdiskhostmap(**cli_kwargs)
        except (svc_errors.CommandExecutionError, CLIFailureError) as ex:
            if not is_warning_message(ex.my_message):
                logger.error(msg="Map volume {0} to host {1} failed. Reason "
                                 "is: {2}".format(vol_name, host_name, ex))
                if NAME_NOT_EXIST_OR_MEET_RULES in ex.my_message:
                    raise array_errors.HostNotFoundError(host_name)
                if SPECIFIED_OBJ_NOT_EXIST in ex.my_message:
                    raise array_errors.ObjectNotFoundError(vol_name)
                if LUN_ALREADY_IN_USE in ex.my_message:
                    raise array_errors.LunAlreadyInUseError(lun,
                                                            host_name)
                raise array_errors.MappingError(vol_name, host_name, ex)

        return str(lun)

    def unmap_volume(self, volume_id, host_name):
        logger.debug("un-mapping volume : {0} from host : "
                     "{1}".format(volume_id, host_name))
        volume_name = self._get_volume_name_by_wwn(volume_id)

        cli_kwargs = {
            'host': host_name,
            'vdisk_id': volume_name
        }

        try:
            self.client.svctask.rmvdiskhostmap(**cli_kwargs)
        except (svc_errors.CommandExecutionError, CLIFailureError) as ex:
            if not is_warning_message(ex.my_message):
                logger.error(msg="Map volume {0} to host {1} failed. Reason "
                                 "is: {2}".format(volume_name, host_name, ex))
                if NAME_NOT_EXIST_OR_MEET_RULES in ex.my_message:
                    raise array_errors.HostNotFoundError(host_name)
                if OBJ_NOT_FOUND in ex.my_message:
                    raise array_errors.ObjectNotFoundError(volume_name)
                if VOL_ALREADY_UNMAPPED in ex.my_message:
                    raise array_errors.VolumeAlreadyUnmappedError(
                        volume_name)
                raise array_errors.UnmappingError(volume_name,
                                                  host_name, ex)

    def _get_array_iqns_by_node_id(self):
        logger.debug("Getting array nodes id and iscsi name")
        try:
            nodes_list = self.client.svcinfo.lsnode()
            array_iqns_by_id = {node.id: node.iscsi_name for node in nodes_list
                                if node.status.lower() == "online"}
        except (svc_errors.CommandExecutionError, CLIFailureError) as ex:
            if not is_warning_message(ex.my_message):
                logger.error(ex)
                raise ex
        logger.debug("Found iqns by node id: {}".format(array_iqns_by_id))
        return array_iqns_by_id

    def _list_ip_ports(self, portset_id):
        try:
            if portset_id:
                filter_value = 'portset_id={}'.format(portset_id)
                return self.client.svcinfo.lsip(filtervalue=filter_value)
            return self.client.svcinfo.lsportip(filtervalue='state=configured:failover=no')
        except (svc_errors.CommandExecutionError, CLIFailureError) as ex:
            logger.error("Get iscsi targets failed. Reason is: {}".format(ex))
            raise array_errors.NoIscsiTargetsFoundError(self.endpoint)

    @staticmethod
    def _create_ips_by_node_id_map(ports):
        ips_by_node_id = defaultdict(list)
        for port in ports:
            if port.get('IP_address'):
                ips_by_node_id[port.node_id].append(port.IP_address)
            if port.get('IP_address_6'):
                ipv6 = port.IP_address_6.join('[]')
                ips_by_node_id[port.node_id].append(ipv6)
        return dict(ips_by_node_id)

    @staticmethod
    def _unify_ips_by_iqn(iqns_by_node_id, ips_by_node_id):
        ips_by_iqn = defaultdict(list)
        for node_id, iqn in iqns_by_node_id.items():
            ips = ips_by_node_id.get(node_id, [])
            ips_by_iqn[iqn].extend(ips)
        return dict(ips_by_iqn)

    def _get_iscsi_targets_by_node_id(self, host_name):
        portset_id = self._get_host_portset_id(host_name)
        ports = self._list_ip_ports(portset_id)
        return self._create_ips_by_node_id_map(ports)

    def get_iscsi_targets_by_iqn(self, host_name):
        logger.debug("Getting iscsi targets by iqn")
        iqns_by_node_id = self._get_array_iqns_by_node_id()
        ips_by_node_id = self._get_iscsi_targets_by_node_id(host_name)
        ips_by_iqn = self._unify_ips_by_iqn(iqns_by_node_id, ips_by_node_id)

        if ips_by_iqn and any(ips_by_iqn.values()):
            logger.debug("Found iscsi target IPs by iqn: {}".format(ips_by_iqn))
            return ips_by_iqn
        raise array_errors.NoIscsiTargetsFoundError(self.endpoint)

    def get_array_fc_wwns(self, host_name):
        logger.debug("Getting the connected fc port wwn value from array "
                     "related to host : {}.".format(host_name))
        fc_port_wwns = []
        try:
            fc_wwns = self.client.svcinfo.lsfabric(host=host_name)
            for wwn in fc_wwns:
                state = wwn.get('state', '')
                if state in ('active', 'inactive'):
                    fc_port_wwns.append(wwn.get('local_wwpn', ''))
            logger.debug("Getting fc wwns : {}".format(fc_port_wwns))
            return fc_port_wwns
        except(svc_errors.CommandExecutionError, CLIFailureError) as ex:
            logger.error(msg="Failed to get array fc wwn. Reason "
                             "is: {0}".format(ex))
            raise ex

    def _get_cli_host_by_name(self, host_name):
        filter_value = 'name={}'.format(host_name)
        cli_host = self.client.svcinfo.lshost(filtervalue=filter_value).as_single_element
        if not cli_host:
            raise array_errors.HostNotFoundError(host_name)
        return cli_host

    def _get_host_portset_id(self, host_name):
        cli_host = self._get_cli_host_by_name(host_name)
        return cli_host.get(HOST_PORTSET_ID)

    def _get_replication_endpoint_type(self, rcrelationship):
        if self._identifier == rcrelationship.master_cluster_id:
            return ENDPOINT_TYPE_MASTER
        return ENDPOINT_TYPE_AUX

    @staticmethod
    def _get_other_endpoint_type(endpoint_type):
        if endpoint_type == ENDPOINT_TYPE_MASTER:
            return ENDPOINT_TYPE_AUX
        return ENDPOINT_TYPE_MASTER

    def _get_replication_other_endpoint_type(self, rcrelationship):
        endpoint_type = self._get_replication_endpoint_type(rcrelationship)
        return self._get_other_endpoint_type(endpoint_type)

    @staticmethod
    def _is_replication_idle(rcrelationship):
        return rcrelationship.state == RCRELATIONSHIP_STATE_IDLE

    @staticmethod
    def _is_replication_disconnected(rcrelationship):
        return 'disconnected' in rcrelationship.state

    @staticmethod
    def _is_replication_ready(rcrelationship):
        return rcrelationship.state == RCRELATIONSHIP_STATE_READY

    def _is_replication_endpoint_primary(self, rcrelationship, endpoint_type=None):
        if not endpoint_type:
            endpoint_type = self._get_replication_endpoint_type(rcrelationship)
        if rcrelationship.primary:
            return rcrelationship.primary == endpoint_type
        return None

    @staticmethod
    def _get_replication_copy_type(rcrelationship):
        if rcrelationship.copy_type == 'global':
            return config.REPLICATION_COPY_TYPE_ASYNC
        return config.REPLICATION_COPY_TYPE_SYNC

    def _generate_replication_response(self, rcrelationship, volume_internal_id, other_volume_internal_id):
        copy_type = self._get_replication_copy_type(rcrelationship)
        is_ready = self._is_replication_ready(rcrelationship)
        is_primary = self._is_replication_endpoint_primary(rcrelationship)
        return Replication(name=rcrelationship.name,
                           volume_internal_id=volume_internal_id,
                           other_volume_internal_id=other_volume_internal_id,
                           copy_type=copy_type,
                           is_ready=is_ready,
                           is_primary=is_primary)

    def _get_lsrcrelationship(self, filter_value):
        return self.client.svcinfo.lsrcrelationship(filtervalue=filter_value)

    def _get_rcrelationship_by_name(self, replication_name, not_exist_error=True):
        filter_value = 'RC_rel_name={0}'.format(replication_name)
        rcrelationship = self._get_lsrcrelationship(filter_value).as_single_element
        if not rcrelationship and not_exist_error:
            raise array_errors.ObjectNotFoundError(replication_name)
        return rcrelationship

    def _get_rcrelationships(self, cli_volume_id, other_cli_volume_id, other_system_id, as_master):
        endpoint_type = ENDPOINT_TYPE_AUX
        other_endpoint_type = ENDPOINT_TYPE_MASTER
        if as_master:
            endpoint_type = ENDPOINT_TYPE_MASTER
            other_endpoint_type = ENDPOINT_TYPE_AUX
        filter_value = '{END}_vdisk_id={VDISK_ID}:' \
                       '{OTHER_END}_vdisk_id={OTHER_VDISK_ID}:' \
                       '{OTHER_END}_cluster_id={OTHER_CLUSTER_ID}'.format(END=endpoint_type, VDISK_ID=cli_volume_id,
                                                                          OTHER_END=other_endpoint_type,
                                                                          OTHER_VDISK_ID=other_cli_volume_id,
                                                                          OTHER_CLUSTER_ID=other_system_id)
        return self._get_lsrcrelationship(filter_value).as_list

    def _get_rcrelationship(self, cli_volume_id, other_cli_volume_id, other_system_id):
        rcrelationships = self._get_rcrelationships(cli_volume_id, other_cli_volume_id,
                                                    other_system_id, as_master=True)
        rcrelationships.extend(self._get_rcrelationships(cli_volume_id, other_cli_volume_id,
                                                         other_system_id, as_master=False))
        if len(rcrelationships) != 1:
            logger.warning('found {0} rcrelationships for volume id {1} '
                           'with volume id {2} of system {3}'.format(len(rcrelationships),
                                                                     cli_volume_id,
                                                                     other_cli_volume_id,
                                                                     other_system_id))
            return None
        return rcrelationships[0]

    def get_replication(self, volume_internal_id, other_volume_internal_id, other_system_id):
        rcrelationship = self._get_rcrelationship(volume_internal_id, other_volume_internal_id, other_system_id)
        if not rcrelationship:
            return None
        logger.info("found rcrelationship: {}".format(rcrelationship))
        return self._generate_replication_response(rcrelationship, volume_internal_id, other_volume_internal_id)

    def _create_rcrelationship(self, master_cli_volume_id, aux_cli_volume_id, other_system_id, copy_type):
        logger.info("creating remote copy relationship for master volume id: {0} "
                    "and auxiliary volume id: {1} with system {2} using {3} copy type".format(master_cli_volume_id,
                                                                                              aux_cli_volume_id,
                                                                                              other_system_id,
                                                                                              copy_type))
        kwargs = build_create_replication_kwargs(master_cli_volume_id, aux_cli_volume_id, other_system_id, copy_type)
        try:
            svc_response = self.client.svctask.mkrcrelationship(**kwargs)
            message = str(svc_response.response[0])
            id_start, id_end = message.find('[') + 1, message.find(']')
            raw_id = message[id_start:id_end]
            return int(raw_id)
        except (svc_errors.CommandExecutionError, CLIFailureError) as ex:
            if not is_warning_message(ex.my_message):
                logger.error("failed to create rcrelationship for volume id {0} "
                             "with volume id {1} of system {2}: {3}".format(master_cli_volume_id,
                                                                            aux_cli_volume_id,
                                                                            other_system_id,
                                                                            ex))
                raise ex
        return None

    def _start_rcrelationship(self, rcrelationship_id, primary_endpoint_type=None, force=False):
        logger.info("starting remote copy relationship with id: {} primary: {} force: {}".format(rcrelationship_id,
                                                                                                 primary_endpoint_type,
                                                                                                 force))
        try:
            kwargs = build_start_replication_kwargs(rcrelationship_id, primary_endpoint_type, force)
            self.client.svctask.startrcrelationship(**kwargs)
        except (svc_errors.CommandExecutionError, CLIFailureError) as ex:
            if not is_warning_message(ex.my_message):
                logger.warning("failed to start rcrelationship '{}': {}".format(rcrelationship_id, ex))

    def create_replication(self, volume_internal_id, other_volume_internal_id, other_system_id, copy_type):
        rc_id = self._create_rcrelationship(volume_internal_id, other_volume_internal_id, other_system_id, copy_type)
        self._start_rcrelationship(rc_id)

    def _stop_rcrelationship(self, rcrelationship_id, add_access_to_secondary=False):
        logger.info("stopping remote copy relationship with id: {}. access: {}".format(rcrelationship_id,
                                                                                       add_access_to_secondary))
        kwargs = build_stop_replication_kwargs(rcrelationship_id, add_access_to_secondary)
        try:
            self.client.svctask.stoprcrelationship(**kwargs)
        except (svc_errors.CommandExecutionError, CLIFailureError) as ex:
            if not is_warning_message(ex.my_message):
                logger.warning("failed to stop rcrelationship '{0}': {1}".format(rcrelationship_id, ex))

    def _delete_rcrelationship(self, rcrelationship_id):
        logger.info("deleting remote copy relationship with id: {0}".format(rcrelationship_id))
        try:
            self.client.svctask.rmrcrelationship(object_id=rcrelationship_id)
        except (svc_errors.CommandExecutionError, CLIFailureError) as ex:
            if not is_warning_message(ex.my_message):
                logger.warning("failed to delete rcrelationship '{0}': {1}".format(rcrelationship_id, ex))

    def delete_replication(self, replication_name):
        rcrelationship = self._get_rcrelationship_by_name(replication_name, not_exist_error=False)
        if not rcrelationship:
            logger.info("could not find replication with name {}".format(replication_name))
            return
        self._stop_rcrelationship(rcrelationship.id)
        self._delete_rcrelationship(rcrelationship.id)

    def _promote_replication_endpoint(self, endpoint_type, replication_name):
        logger.info("making '{}' primary for remote copy relationship {}".format(endpoint_type, replication_name))
        try:
            self.client.svctask.switchrcrelationship(primary=endpoint_type, object_id=replication_name)
        except (svc_errors.CommandExecutionError, CLIFailureError) as ex:
            if not is_warning_message(ex.my_message):
                logger.error("failed to make '{}' primary for rcrelationship {}: {}".format(endpoint_type,
                                                                                            replication_name,
                                                                                            ex.my_message))
                raise
        logger.info("succeeded making '{}' primary for remote copy relationship {}".format(endpoint_type,
                                                                                           replication_name))

    def _ensure_endpoint_is_primary(self, rcrelationship, endpoint_type):
        if self._is_replication_endpoint_primary(rcrelationship, endpoint_type):
            logger.info("'{}' is already primary for rcrelationship {}. "
                        "skipping the switch".format(endpoint_type,
                                                     rcrelationship.name))
            return
        if self._is_replication_idle(rcrelationship):
            other_endpoint_type = self._get_other_endpoint_type(endpoint_type)
            self._start_rcrelationship(rcrelationship.id, primary_endpoint_type=other_endpoint_type, force=True)
        self._promote_replication_endpoint(endpoint_type, rcrelationship.name)

    def promote_replication_volume(self, replication_name):
        rcrelationship = self._get_rcrelationship_by_name(replication_name)
        if self._is_replication_disconnected(rcrelationship):
            self._stop_rcrelationship(rcrelationship.id, add_access_to_secondary=True)
            return
        endpoint_type = self._get_replication_endpoint_type(rcrelationship)
        self._ensure_endpoint_is_primary(rcrelationship, endpoint_type)

    def demote_replication_volume(self, replication_name):
        rcrelationship = self._get_rcrelationship_by_name(replication_name)
        endpoint_type_to_promote = self._get_replication_other_endpoint_type(rcrelationship)
        self._ensure_endpoint_is_primary(rcrelationship, endpoint_type_to_promote)
