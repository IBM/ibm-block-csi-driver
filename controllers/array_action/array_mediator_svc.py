from collections import defaultdict
from io import StringIO
from random import choice

from packaging.version import Version
from pysvc import errors as svc_errors
from pysvc.unified.client import connect
from pysvc.unified.response import CLIFailureError, SVCResponse
from retry import retry

import controllers.array_action.errors as array_errors
import controllers.array_action.settings as array_settings
import controllers.servers.settings as controller_settings
from controllers.array_action.array_action_types import Volume, Snapshot, Replication, Host
from controllers.array_action.array_mediator_abstract import ArrayMediatorAbstract
from controllers.array_action.utils import ClassProperty, convert_scsi_id_to_nguid
from controllers.common import settings as common_settings
from controllers.common.csi_logger import get_stdout_logger

array_connections_dict = {}
logger = get_stdout_logger()

OBJ_NOT_FOUND = 'CMMVC5753E'
SNAPSHOT_NOT_EXIST = 'CMMVC9755E'
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
NOT_CHILD_POOL = 'CMMVC9760E'
NOT_REDUCTION_POOL = 'CMMVC9301E'
NOT_ENOUGH_EXTENTS_IN_POOL_EXPAND = 'CMMVC5860E'
NOT_ENOUGH_EXTENTS_IN_POOL_CREATE = 'CMMVC8710E'

HOST_NQN = 'nqn'
HOST_WWPN = 'WWPN'
HOST_ISCSI_NAME = 'iscsi_name'
HOST_PORTSET_ID = 'portset_id'
LIST_HOSTS_CMD_FORMAT = 'lshost {HOST_ID};echo;'
HOSTS_LIST_ERR_MSG_MAX_LENGTH = 300
DEFAULT_PORTS_DELIMITER = ":"
QUALIFIED_NAME_PORTS_DELIMITER = ","

LUN_INTERVAL = 128

FCMAP_STATUS_DONE = 'idle_or_copied'
RCRELATIONSHIP_STATE_IDLE = 'idling'
RCRELATIONSHIP_STATE_READY = 'consistent_synchronized'

YES = 'yes'

ENDPOINT_TYPE_SOURCE = 'source'
ENDPOINT_TYPE_TARGET = 'target'

ENDPOINT_TYPE_MASTER = 'master'
ENDPOINT_TYPE_AUX = 'aux'

ENDPOINT_TYPE_PRODUCTION = 'production'
ENDPOINT_TYPE_INDEPENDENT = 'independent'
ENDPOINT_TYPE_RECOVERY = 'recovery'


def is_warning_message(exception):
    """ Return True if the exception message is warning """
    info_seperated_by_quotation = str(exception).split('"')
    message = info_seperated_by_quotation[1]
    word_in_message = message.split()
    message_tag = word_in_message[0]
    if message_tag[-1] == 'W':
        return True
    return False


def _get_space_efficiency_kwargs(space_efficiency):
    if space_efficiency:
        space_efficiency = space_efficiency.lower()
        if space_efficiency == common_settings.SPACE_EFFICIENCY_THIN:
            return {'thin': True}
        if space_efficiency == common_settings.SPACE_EFFICIENCY_COMPRESSED:
            return {'compressed': True}
        if space_efficiency == common_settings.SPACE_EFFICIENCY_DEDUPLICATED_THIN:
            return {'deduplicated': True, 'thin': True}
        if space_efficiency in (common_settings.SPACE_EFFICIENCY_DEDUPLICATED,
                                common_settings.SPACE_EFFICIENCY_DEDUPLICATED_COMPRESSED):
            return {'deduplicated': True, 'compressed': True}
    return {}


def _is_space_efficiency_matches_source(parameter_space_efficiency, array_space_efficiency):
    return (not parameter_space_efficiency and array_space_efficiency == common_settings.SPACE_EFFICIENCY_THICK) or \
           (parameter_space_efficiency and parameter_space_efficiency == array_space_efficiency)


def build_create_volume_group_kwargs(name):
    cli_kwargs = {
        'name': name
    }
    return cli_kwargs


def build_create_volume_in_volume_group_kwargs(name, pool, io_group, source_id):
    cli_kwargs = {
        'type': 'clone',
        'fromsnapshotid': source_id,
        'pool': pool,
        'name': name
    }
    if io_group:
        cli_kwargs['iogroup'] = io_group
    return cli_kwargs


def build_create_host_kwargs(host_name, connectivity_type, ports):
    cli_kwargs = {'name': host_name}
    if connectivity_type == array_settings.NVME_OVER_FC_CONNECTIVITY_TYPE:
        cli_kwargs.update({'nqn': QUALIFIED_NAME_PORTS_DELIMITER.join(ports), 'protocol': 'nvme'})
    elif connectivity_type == array_settings.FC_CONNECTIVITY_TYPE:
        cli_kwargs['fcwwpn'] = DEFAULT_PORTS_DELIMITER.join(ports)
    elif connectivity_type == array_settings.ISCSI_CONNECTIVITY_TYPE:
        cli_kwargs['iscsiname'] = QUALIFIED_NAME_PORTS_DELIMITER.join(ports)
    else:
        raise array_errors.UnsupportedConnectivityTypeError(connectivity_type)
    return cli_kwargs


def build_kwargs_from_parameters(space_efficiency, pool_name, io_group,
                                 volume_group, volume_name, volume_size):
    cli_kwargs = {}
    cli_kwargs.update({
        'name': volume_name,
        'unit': 'b',
        'size': volume_size,
        'pool': pool_name
    })
    space_efficiency_kwargs = _get_space_efficiency_kwargs(space_efficiency)
    cli_kwargs.update(space_efficiency_kwargs)
    if io_group:
        cli_kwargs['iogrp'] = io_group
    if volume_group:
        cli_kwargs['volumegroup'] = volume_group
    return cli_kwargs


def build_create_replication_kwargs(master_cli_volume_id, aux_cli_volume_id, other_system_id, copy_type):
    cli_kwargs = {
        'master': master_cli_volume_id,
        'aux': aux_cli_volume_id,
        'cluster': other_system_id,
    }
    if copy_type == array_settings.REPLICATION_COPY_TYPE_ASYNC:
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


def _get_cli_volume_space_efficiency_aliases(cli_volume):
    space_efficiency_aliases = {common_settings.SPACE_EFFICIENCY_THICK, ''}
    if cli_volume.se_copy == YES:
        space_efficiency_aliases = {common_settings.SPACE_EFFICIENCY_THIN}
    if cli_volume.compressed_copy == YES:
        space_efficiency_aliases = {common_settings.SPACE_EFFICIENCY_COMPRESSED}
    if hasattr(cli_volume, "deduplicated_copy"):
        if cli_volume.deduplicated_copy == YES:
            if cli_volume.se_copy == YES:
                space_efficiency_aliases = {common_settings.SPACE_EFFICIENCY_DEDUPLICATED_THIN}
            else:
                space_efficiency_aliases = {common_settings.SPACE_EFFICIENCY_DEDUPLICATED_COMPRESSED,
                                            common_settings.SPACE_EFFICIENCY_DEDUPLICATED}
    return space_efficiency_aliases


class SVCArrayMediator(ArrayMediatorAbstract):
    ARRAY_ACTIONS = {}
    BLOCK_SIZE_IN_BYTES = 512
    MAX_LUN_NUMBER = 511
    MIN_LUN_NUMBER = 0
    MIN_SUPPORTED_VERSION = '7.8'

    @ClassProperty
    def array_type(self):
        return common_settings.ARRAY_TYPE_SVC

    @ClassProperty
    def port(self):
        return 22

    @ClassProperty
    def max_object_name_length(self):
        return 63

    @ClassProperty
    def max_object_prefix_length(self):
        return 20

    @ClassProperty
    def max_connections(self):
        return 2

    @ClassProperty
    def minimal_volume_size_in_bytes(self):
        return 512  # 512 Bytes

    @ClassProperty
    def maximal_volume_size_in_bytes(self):
        return 256 * 1024 * 1024 * 1024 * 1024

    @ClassProperty
    def max_lun_retries(self):
        return 10

    @ClassProperty
    def default_object_prefix(self):
        return "CSI"

    def __init__(self, user, password, endpoint):
        super().__init__(user, password, endpoint)
        self.client = None
        # SVC only accept one IP address
        if len(endpoint) == 0 or len(endpoint) > 1:
            logger.error("SVC only support one cluster IP")
            raise array_errors.StorageManagementIPsNotSupportError(
                endpoint)
        self.endpoint = self.endpoint[0]
        self._cluster = None

        logger.debug("in init")
        self._connect()

    def _connect(self):
        logger.debug("Connecting to SVC {0}".format(self.endpoint))
        try:
            self.client = connect(self.endpoint, username=self.user,
                                  password=self.password)
            if Version(self._code_level) < Version(self.MIN_SUPPORTED_VERSION):
                raise array_errors.UnsupportedStorageVersionError(
                    self._code_level, self.MIN_SUPPORTED_VERSION
                )
        except (svc_errors.IncorrectCredentials,
                svc_errors.StorageArrayClientException):
            raise array_errors.CredentialsError(self.endpoint)

    def disconnect(self):
        if self.client:
            self.client.close()

    @property
    def _system_info(self):
        if self._cluster is None:
            for cluster in self.client.svcinfo.lssystem():
                if cluster.location == 'local':
                    self._cluster = cluster
        return self._cluster

    @property
    def _code_level(self):
        return self._system_info.code_level.split(None, 1)[0]

    @property
    def identifier(self):
        return self._system_info.id_alias

    def is_active(self):
        return self.client.transport.transport.get_transport().is_active()

    def _generate_volume_response(self, cli_volume, is_virt_snap_func=False):
        pool = self._get_volume_pool(cli_volume)
        source_id = None
        if not is_virt_snap_func:
            source_id = self._get_source_volume_wwn_if_exists(cli_volume)
        space_efficiency = _get_cli_volume_space_efficiency_aliases(cli_volume)
        return Volume(
            capacity_bytes=int(cli_volume.capacity),
            id=cli_volume.vdisk_UID,
            internal_id=cli_volume.id,
            name=cli_volume.name,
            array_address=self.endpoint,
            pool=pool,
            source_id=source_id,
            array_type=self.array_type,
            space_efficiency_aliases=space_efficiency,
            volume_group_id=cli_volume.volume_group_id
        )

    def _generate_snapshot_response_from_cli_volume(self, cli_volume, source_id):
        return self._generate_snapshot_response(cli_volume.capacity, cli_volume.name, source_id, cli_volume.id,
                                                cli_volume.vdisk_UID)

    def _generate_snapshot_response_from_cli_snapshot(self, cli_snapshot, source_cli_volume):
        return self._generate_snapshot_response(source_cli_volume.capacity, cli_snapshot.snapshot_name,
                                                source_cli_volume.vdisk_UID, cli_snapshot.snapshot_id)

    def _generate_snapshot_response(self, capacity, name, source_id, internal_id, vdisk_uid=''):
        return Snapshot(
            capacity_bytes=int(capacity),
            name=name,
            source_id=source_id,
            internal_id=internal_id,
            id=vdisk_uid,
            array_address=self.endpoint,
            is_ready=True,
            array_type=self.array_type
        )

    def _generate_snapshot_response_with_verification(self, cli_object):
        if not cli_object.FC_id:
            logger.error("FlashCopy Mapping not found for target volume: {}".format(cli_object.name))
            raise array_errors.ExpectedSnapshotButFoundVolumeError(cli_object.name, self.endpoint)
        fcmap = self._get_fcmap_as_target_if_exists(cli_object.name)
        if fcmap is None or fcmap.copy_rate != '0':
            raise array_errors.ExpectedSnapshotButFoundVolumeError(cli_object.name, self.endpoint)
        source_id = self._get_wwn_by_volume_name_if_exists(fcmap.source_vdisk_name)
        return self._generate_snapshot_response_from_cli_volume(cli_object, source_id)

    def _lsvdisk(self, **kwargs):
        kwargs['bytes'] = True
        try:
            return self.client.svcinfo.lsvdisk(**kwargs).as_single_element
        except (svc_errors.CommandExecutionError, CLIFailureError) as ex:
            if (OBJ_NOT_FOUND in ex.my_message or
                    NAME_NOT_EXIST_OR_MEET_RULES in ex.my_message):
                logger.info("volume not found")
                return None
            if any(msg_id in ex.my_message for msg_id in (NON_ASCII_CHARS, VALUE_TOO_LONG, INVALID_FILTER_VALUE)):
                raise array_errors.InvalidArgumentError(ex.my_message)
            raise ex

    def _lsvolumegroup(self, id_or_name):
        try:
            return self.client.svcinfo.lsvolumegroup(object_id=id_or_name).as_single_element
        except (svc_errors.CommandExecutionError, CLIFailureError) as ex:
            if (SPECIFIED_OBJ_NOT_EXIST in ex.my_message or
                    NAME_NOT_EXIST_OR_MEET_RULES in ex.my_message):
                logger.info("volume group {} was not found".format(id_or_name))
                return None
            if any(msg_id in ex.my_message for msg_id in (NON_ASCII_CHARS, VALUE_TOO_LONG)):
                raise array_errors.InvalidArgumentError(ex.my_message)
            raise ex

    def _chvolumegroup(self, id_or_name, **cli_kwargs):
        try:
            self.client.svctask.chvolumegroup(object_id=id_or_name, **cli_kwargs)
        except (svc_errors.CommandExecutionError, CLIFailureError) as ex:
            if is_warning_message(ex.my_message):
                logger.warning(
                    "exception encountered while changing volume group '{}': {}".format(cli_kwargs, ex.my_message))
            else:
                if OBJ_ALREADY_EXIST in ex.my_message:
                    raise array_errors.VolumeAlreadyExists(cli_kwargs, self.endpoint)
                raise ex

    def _lsvolumegroupreplication(self, id_or_name):
        try:
            return self.client.svcinfo.lsvolumegroupreplication(object_id=id_or_name).as_single_element
        except (svc_errors.CommandExecutionError, CLIFailureError) as ex:
            if (SPECIFIED_OBJ_NOT_EXIST in ex.my_message or
                    NAME_NOT_EXIST_OR_MEET_RULES in ex.my_message):
                logger.info("volume group {} was not found".format(id_or_name))
                return None
            if any(msg_id in ex.my_message for msg_id in (NON_ASCII_CHARS, VALUE_TOO_LONG)):
                raise array_errors.InvalidArgumentError(ex.my_message)
            raise ex

    def _chvolumegroupreplication(self, id_or_name, **cli_kwargs):
        try:
            self.client.svctask.chvolumegroupreplication(object_id=id_or_name, **cli_kwargs)
        except (svc_errors.CommandExecutionError, CLIFailureError) as ex:
            if is_warning_message(ex.my_message):
                logger.warning(
                    "exception encountered while changing volume parameters '{}': {}".format(cli_kwargs, ex.my_message))
            else:
                if OBJ_ALREADY_EXIST in ex.my_message:
                    raise array_errors.VolumeAlreadyExists(cli_kwargs, self.endpoint)
                raise ex

    def _get_cli_volume_group(self, volume_group_name, not_exist_err=True):
        cli_volume_group = self._lsvolumegroup(volume_group_name)
        if not cli_volume_group and not_exist_err:
            raise array_errors.ObjectNotFoundError(volume_group_name)
        return cli_volume_group

    def _get_cli_volume(self, volume_name, not_exist_err=True):
        cli_volume = self._lsvdisk(object_id=volume_name)
        if not cli_volume and not_exist_err:
            raise array_errors.ObjectNotFoundError(volume_name)
        return cli_volume

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
        if self._is_in_remote_copy_relationship(fcmap):
            return None
        source_volume_name = fcmap.source_vdisk_name
        return self._get_wwn_by_volume_name_if_exists(source_volume_name)

    def _get_volume_pools(self, cli_volume):
        pool = cli_volume.mdisk_grp_name
        if isinstance(pool, list):
            pool_names = pool[:]
            pool_names.remove('many')
            return pool_names
        return [pool]

    def _get_volume_pool(self, cli_volume):
        pools = self._get_volume_pools(cli_volume)
        return ':'.join(pools)

    def get_volume(self, name, pool, is_virt_snap_func):
        cli_volume = self._get_cli_volume(name)
        return self._generate_volume_response(cli_volume, is_virt_snap_func)

    def _get_object_fcmaps(self, object_name):
        all_fcmaps = []
        fcmap_as_target = self._get_fcmap_as_target_if_exists(object_name)
        if fcmap_as_target:
            all_fcmaps.append(fcmap_as_target)
        all_fcmaps.extend(self._get_fcmaps_as_source_if_exist(object_name))
        return all_fcmaps

    def _expand_cli_volume(self, cli_volume, increase_in_bytes, is_hyperswap):
        volume_name = cli_volume.name
        try:
            if is_hyperswap:
                self.client.svctask.expandvolume(object_id=volume_name, unit='b', size=increase_in_bytes)
            else:
                self.client.svctask.expandvdisksize(vdisk_id=volume_name, unit='b', size=increase_in_bytes)
        except (svc_errors.CommandExecutionError, CLIFailureError) as ex:
            if is_warning_message(ex.my_message):
                logger.warning("exception encountered during volume expansion of {}: {}".format(volume_name,
                                                                                                ex.my_message))
            else:
                logger.error("Failed to expand volume {}".format(volume_name))
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
        is_hyperswap = any(self._is_in_remote_copy_relationship(fcmap) for fcmap in fcmaps)

        current_size = int(cli_volume.capacity)
        final_size = self._convert_size_bytes(required_bytes)
        increase_in_bytes = final_size - current_size
        self._expand_cli_volume(cli_volume, increase_in_bytes, is_hyperswap)
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
                [common_settings.SPACE_EFFICIENCY_THIN, common_settings.SPACE_EFFICIENCY_THICK,
                 common_settings.SPACE_EFFICIENCY_COMPRESSED,
                 common_settings.SPACE_EFFICIENCY_DEDUPLICATED,
                 common_settings.SPACE_EFFICIENCY_DEDUPLICATED_THIN,
                 common_settings.SPACE_EFFICIENCY_DEDUPLICATED_COMPRESSED]):
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

    def _lsvdisk_by_uid(self, vdisk_uid):
        filter_value = 'vdisk_UID=' + vdisk_uid
        return self._lsvdisk(filtervalue=filter_value)

    def _get_cli_volume_by_wwn(self, volume_id, not_exist_err=False):
        cli_volume = self._lsvdisk_by_uid(volume_id)
        if not cli_volume:
            volume_nguid = convert_scsi_id_to_nguid(volume_id)
            cli_volume = self._lsvdisk_by_uid(volume_nguid)
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

    def _create_cli_volume(self, name, size_in_bytes, space_efficiency, pool, io_group, volume_group=None):
        logger.info("creating volume with name : {}. size : {} . in pool : {} with parameters : {}".format(
            name, size_in_bytes, pool, space_efficiency))
        try:
            size = self._convert_size_bytes(size_in_bytes)
            cli_kwargs = build_kwargs_from_parameters(space_efficiency, pool, io_group,
                                                      volume_group, name, size)
            self.client.svctask.mkvolume(**cli_kwargs)
        except (svc_errors.CommandExecutionError, CLIFailureError) as ex:
            if is_warning_message(ex.my_message):
                logger.warning("exception encountered during creation of volume {0}: {1}".format(name,
                                                                                                 ex.my_message))
            else:
                logger.error("Cannot create volume {0}, Reason is: {1}".format(name, ex))
                if OBJ_ALREADY_EXIST in ex.my_message:
                    raise array_errors.VolumeAlreadyExists(name, self.endpoint)
                if NAME_NOT_EXIST_OR_MEET_RULES in ex.my_message:
                    raise array_errors.InvalidArgumentError(ex.my_message)
                if POOL_NOT_MATCH_VOL_SPACE_EFFICIENCY in ex.my_message or NOT_REDUCTION_POOL in ex.my_message:
                    raise array_errors.PoolDoesNotMatchSpaceEfficiency(pool, space_efficiency, ex)
                if NOT_ENOUGH_EXTENTS_IN_POOL_CREATE in ex.my_message:
                    raise array_errors.NotEnoughSpaceInPool(id_or_name=pool)
                if any(msg_id in ex.my_message for msg_id in (NON_ASCII_CHARS, INVALID_NAME, TOO_MANY_CHARS)):
                    raise array_errors.InvalidArgumentError(ex.my_message)
                raise ex
        logger.info("finished creating cli volume : {}".format(name))

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

    def copy_to_existing_volume(self, volume_id, source_id, source_capacity_in_bytes,
                                minimum_volume_size_in_bytes):
        source_name = self._get_volume_name_by_wwn(source_id)
        target_volume_name = self._get_volume_name_by_wwn(volume_id)
        self._copy_to_target_volume(target_volume_name, source_name)

    def _create_volume_group(self, name):
        cli_kwargs = build_create_volume_group_kwargs(name)
        self._mkvolumegroup(**cli_kwargs)

    def _create_volume_in_volume_group(self, name, pool, io_group, source_id):
        cli_kwargs = build_create_volume_in_volume_group_kwargs(name, pool, io_group, source_id)
        self._mkvolumegroup(**cli_kwargs)

    def _fix_creation_side_effects(self, name, cli_volume_id, volume_group):
        self._change_volume_group(cli_volume_id, volume_group)
        self._rmvolumegroup(name)
        self._rename_volume(cli_volume_id, name)

    def _create_cli_volume_from_snapshot(self, name, pool, io_group, volume_group, source_id):
        logger.info("creating volume from snapshot")
        self._create_volume_in_volume_group(name, pool, io_group, source_id)
        cli_volume_id = self._get_cli_volume_id_from_volume_group("volume_group_name", name)
        try:
            self._fix_creation_side_effects(name, cli_volume_id, volume_group)
        except (svc_errors.CommandExecutionError, CLIFailureError, array_errors.VolumeAlreadyExists) as ex:
            self._rollback_create_volume_from_snapshot(cli_volume_id, name)
            raise ex

    def _create_cli_volume_from_volume(self, name, pool, io_group, volume_group, source_id):
        logger.info("creating volume from volume")
        cli_snapshot = self._add_snapshot(name, source_id, pool)
        self._create_cli_volume_from_snapshot(name, pool, io_group, volume_group, cli_snapshot.snapshot_id)
        self._rmsnapshot(cli_snapshot.snapshot_id)

    def _create_cli_volume_from_source(self, name, pool, io_group, volume_group, source_ids, source_type):
        if source_type == controller_settings.SNAPSHOT_TYPE_NAME:
            self._create_cli_volume_from_snapshot(name, pool, io_group, volume_group, source_ids.internal_id)
        else:
            self._create_cli_volume_from_volume(name, pool, io_group, volume_group, source_ids.internal_id)

    def _is_vdisk_support_addsnapshot(self, vdisk_uid):
        return self._is_addsnapshot_supported() and not self._is_vdisk_has_fcmaps(vdisk_uid)

    def create_volume(self, name, size_in_bytes, space_efficiency, pool, io_group, volume_group, source_ids,
                      source_type, is_virt_snap_func):
        if is_virt_snap_func and source_ids:
            if self._is_vdisk_support_addsnapshot(source_ids.uid):
                self._create_cli_volume_from_source(name, pool, io_group, volume_group, source_ids, source_type)
            else:
                raise array_errors.VirtSnapshotFunctionNotSupportedMessage(name)
        else:
            self._create_cli_volume(name, size_in_bytes, space_efficiency, pool, io_group, volume_group)
        cli_volume = self._get_cli_volume(name)
        return self._generate_volume_response(cli_volume, is_virt_snap_func)

    def _rmvolume(self, volume_id_or_name, not_exist_err=True):
        logger.info("deleting volume with name : {0}".format(volume_id_or_name))
        try:
            self.client.svctask.rmvolume(vdisk_id=volume_id_or_name)
        except (svc_errors.CommandExecutionError, CLIFailureError) as ex:
            if is_warning_message(ex.my_message):
                logger.warning("exception encountered during deletion of volume {}: {}".format(volume_id_or_name,
                                                                                               ex.my_message))
            else:
                logger.error("Failed to delete volume {}".format(volume_id_or_name))
                if (OBJ_NOT_FOUND in ex.my_message or VOL_NOT_FOUND in ex.my_message) and not_exist_err:
                    raise array_errors.ObjectNotFoundError(volume_id_or_name)
                raise ex

    def delete_volume(self, volume_id):
        logger.info("Deleting volume with id : {0}".format(volume_id))
        self._delete_volume(volume_id)
        logger.info("Finished volume deletion. id : {0}".format(volume_id))

    def get_snapshot(self, volume_id, snapshot_name, pool, is_virt_snap_func):
        logger.debug("Get snapshot : {}".format(snapshot_name))
        if is_virt_snap_func:
            if self._is_addsnapshot_supported():
                cli_snapshot = self._get_cli_snapshot_by_name(snapshot_name)
                if not cli_snapshot:
                    return None
                source_cli_volume = self._get_cli_volume_by_wwn(volume_id)
                return self._generate_snapshot_response_from_cli_snapshot(cli_snapshot, source_cli_volume)
            raise array_errors.VirtSnapshotFunctionNotSupportedMessage(volume_id)
        target_cli_volume = self._get_cli_volume_if_exists(snapshot_name)
        if not target_cli_volume:
            return None
        return self._generate_snapshot_response_with_verification(target_cli_volume)

    def get_object_by_id(self, object_id, object_type, is_virt_snap_func=False):
        if is_virt_snap_func and object_type == controller_settings.SNAPSHOT_TYPE_NAME:
            cli_snapshot = self._get_cli_snapshot_by_id(object_id)
            if not cli_snapshot:
                return None
            source_cli_volume = self._get_cli_volume(cli_snapshot.volume_name)
            if not source_cli_volume:
                return None
            return self._generate_snapshot_response_from_cli_snapshot(cli_snapshot, source_cli_volume)
        cli_volume = self._get_cli_volume_by_wwn(object_id)
        if not cli_volume:
            return None
        if object_type is controller_settings.SNAPSHOT_TYPE_NAME:
            return self._generate_snapshot_response_with_verification(cli_volume)
        cli_volume = self._get_cli_volume(cli_volume.name)
        return self._generate_volume_response(cli_volume)

    def _create_similar_volume(self, source_cli_volume, target_volume_name, space_efficiency, pool):
        logger.info("creating target cli volume '{0}' from source volume '{1}'".format(target_volume_name,
                                                                                       source_cli_volume.name))
        if not space_efficiency:
            space_efficiency_aliases = _get_cli_volume_space_efficiency_aliases(source_cli_volume)
            space_efficiency = space_efficiency_aliases.pop()
        size_in_bytes = int(source_cli_volume.capacity)
        io_group = source_cli_volume.IO_group_name
        self._create_cli_volume(target_volume_name, size_in_bytes, space_efficiency, pool, io_group)

    def _create_fcmap(self, source_volume_name, target_volume_name, is_copy):
        logger.info("creating FlashCopy Mapping from '{0}' to '{1}'".format(source_volume_name, target_volume_name))
        mkfcmap_kwargs = {} if is_copy else {'copyrate': 0}
        try:
            self.client.svctask.mkfcmap(source=source_volume_name, target=target_volume_name, **mkfcmap_kwargs)
        except (svc_errors.CommandExecutionError, CLIFailureError) as ex:
            if is_warning_message(ex.my_message):
                logger.warning("exception encountered during FlashCopy Mapping creation"
                               " for source '{0}' and target '{1}': {2}".format(source_volume_name,
                                                                                target_volume_name,
                                                                                ex.my_message))
            else:
                if FCMAP_ALREADY_EXIST in ex.my_message:
                    logger.info("FlashCopy Mapping already exists"
                                " for source '{0}' and target '{1}'".format(source_volume_name,
                                                                            target_volume_name))
                else:
                    raise ex

    def _start_fcmap(self, fcmap_id):
        logger.info("starting FlashCopy Mapping '{0}'".format(fcmap_id))
        try:
            self.client.svctask.startfcmap(prep=True, object_id=fcmap_id)
        except (svc_errors.CommandExecutionError, CLIFailureError) as ex:
            if is_warning_message(ex.my_message):
                logger.warning("exception encountered while starting"
                               " FlashCopy Mapping '{}': {}".format(fcmap_id,
                                                                    ex.my_message))
            else:
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
            if is_warning_message(ex.my_message):
                logger.warning("exception encountered during fcmap '{}' deletion: {}".format(fcmap_id,
                                                                                             ex.my_message))
            else:
                logger.error("Failed to delete fcmap '{0}': {1}".format(fcmap_id, ex))
                raise ex

    def _stop_fcmap(self, fcmap_id):
        logger.info("stopping fcmap with id : {0}".format(fcmap_id))
        try:
            self.client.svctask.stopfcmap(object_id=fcmap_id)
        except (svc_errors.CommandExecutionError, CLIFailureError) as ex:
            if is_warning_message(ex.my_message):
                logger.warning("exception encountered while stopping fcmap '{}': {}".format(fcmap_id,
                                                                                            ex.my_message))
            else:
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

    def _delete_volume(self, volume_id, is_snapshot=False):
        cli_volume = self._get_cli_volume_by_wwn(volume_id, not_exist_err=True)
        object_name = cli_volume.name
        if is_snapshot and not cli_volume.FC_id:
            raise array_errors.ObjectNotFoundError(object_name)
        fcmap_as_target = self._get_fcmap_as_target_if_exists(object_name)
        if is_snapshot and not fcmap_as_target:
            raise array_errors.ObjectNotFoundError(object_name)
        fcmaps_as_source = self._get_fcmaps_as_source_if_exist(object_name)
        if fcmaps_as_source:
            self._safe_delete_fcmaps(object_name, fcmaps_as_source)
        if fcmap_as_target:
            self._safe_stop_and_delete_fcmap(fcmap_as_target)
        self._rmvolume(object_name)

    def _delete_unstarted_fcmap_if_exists(self, target_volume_name):
        target_cli_volume = self._get_cli_volume_if_exists(target_volume_name)
        if target_cli_volume and target_cli_volume.FC_id:
            self._delete_fcmap(target_cli_volume.FC_id, force=False)
        return target_cli_volume

    def _delete_target_volume_if_exists(self, target_cli_volume):
        if target_cli_volume:
            self._rmvolume(target_cli_volume.name, not_exist_err=False)

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

    def _get_pool_site(self, pool):
        filter_value = 'name={}'.format(pool)
        cli_pool = self.client.svcinfo.lsmdiskgrp(filtervalue=filter_value).as_single_element
        if cli_pool:
            return cli_pool.site_name
        raise array_errors.PoolDoesNotExist(pool, self.endpoint)

    def _is_cli_volume_in_site(self, cli_volume, site_name):
        volume_pools = self._get_volume_pools(cli_volume)
        for pool in volume_pools:
            volume_site_name = self._get_pool_site(pool)
            if volume_site_name == site_name:
                return True
        return False

    def _get_rcrelationships_as_master_in_cluster(self, volume_name):
        filter_value = 'master_vdisk_name={}:aux_cluster_id={}'.format(volume_name, self.identifier)
        return self._lsrcrelationship(filter_value).as_list

    def _get_cli_volume_in_pool_site(self, volume_name, pool_name):
        cli_volume = self._get_cli_volume(volume_name)
        if not pool_name or ':' in pool_name:
            return cli_volume
        pool_site_name = self._get_pool_site(pool_name)
        if self._is_cli_volume_in_site(cli_volume, pool_site_name):
            return cli_volume
        rcrelationships = self._get_rcrelationships_as_master_in_cluster(volume_name)
        for rcrelationship in rcrelationships:
            other_cli_volume = self._get_cli_volume(rcrelationship.aux_vdisk_name)
            if self._is_cli_volume_in_site(other_cli_volume, pool_site_name):
                return other_cli_volume
        raise RuntimeError('could not find a volume for {} in site {}'.format(volume_name, pool_site_name))

    def create_snapshot(self, volume_id, snapshot_name, space_efficiency, pool, is_virt_snap_func):
        logger.info("creating snapshot '{0}' from volume '{1}'".format(snapshot_name, volume_id))
        source_volume_name = self._get_volume_name_by_wwn(volume_id)
        source_cli_volume = self._get_cli_volume_in_pool_site(source_volume_name, pool)
        if not pool:
            pool = self._get_volume_pools(source_cli_volume)[0]
        if is_virt_snap_func:
            if self._is_vdisk_support_addsnapshot(volume_id):
                target_cli_snapshot = self._add_snapshot(snapshot_name, source_cli_volume.id, pool)
                snapshot = self._generate_snapshot_response_from_cli_snapshot(target_cli_snapshot, source_cli_volume)
            else:
                raise array_errors.VirtSnapshotFunctionNotSupportedMessage(volume_id)
        else:
            target_cli_volume = self._create_snapshot(snapshot_name, source_cli_volume, space_efficiency, pool)
            snapshot = self._generate_snapshot_response_from_cli_volume(target_cli_volume, source_cli_volume.vdisk_UID)
        logger.info("finished creating snapshot '{0}' from volume '{1}'".format(snapshot_name, volume_id))
        return snapshot

    def _is_addsnapshot_supported(self):
        return hasattr(self.client.svctask, "addsnapshot")

    def _rmsnapshot(self, internal_snapshot_id):
        try:
            self.client.svctask.rmsnapshot(snapshotid=internal_snapshot_id)
        except (svc_errors.CommandExecutionError, CLIFailureError) as ex:
            if SNAPSHOT_NOT_EXIST in ex.my_message:
                raise array_errors.ObjectNotFoundError(internal_snapshot_id)
            raise ex

    def delete_snapshot(self, snapshot_id, internal_snapshot_id):
        logger.info("Deleting snapshot with id : {0}".format(snapshot_id))
        if self._is_addsnapshot_supported() and not snapshot_id:
            self._rmsnapshot(internal_snapshot_id)
        else:
            self._delete_volume(snapshot_id, is_snapshot=True)
        logger.info("Finished snapshot deletion. id : {0}".format(snapshot_id))

    def _get_host_ports(self, host, attribute_name):
        ports = host.get(attribute_name, [])
        return ports if isinstance(ports, list) else [ports]

    def _get_host_by_host_identifiers_slow(self, initiators):
        logger.debug("Scanning all hosts for initiators : {0}".format(initiators))
        detailed_hosts_list = self._get_detailed_hosts_list()
        nvme_host, fc_host, iscsi_host = None, None, None
        connectivity_types = set()
        for host in detailed_hosts_list:
            host_nqns = self._get_host_ports(host, HOST_NQN)
            if initiators.is_array_nvme_nqn_match(host_nqns):
                nvme_host = host.name
                connectivity_types.add(array_settings.NVME_OVER_FC_CONNECTIVITY_TYPE)
                logger.debug("found nvme nqn in list : {0} for host : "
                             "{1}".format(initiators.nvme_nqns, nvme_host))
            host_wwns = self._get_host_ports(host, HOST_WWPN)
            if initiators.is_array_wwns_match(host_wwns):
                fc_host = host.name
                connectivity_types.add(array_settings.FC_CONNECTIVITY_TYPE)
                logger.debug("found fc wwns in list : {0} for host : "
                             "{1}".format(initiators.fc_wwns, fc_host))
            host_iqns = self._get_host_ports(host, HOST_ISCSI_NAME)
            if initiators.is_array_iscsi_iqns_match(host_iqns):
                iscsi_host = host.name
                connectivity_types.add(array_settings.ISCSI_CONNECTIVITY_TYPE)
                logger.debug("found iscsi iqn in list : {0} for host : "
                             "{1}".format(initiators.iscsi_iqns, iscsi_host))
        if not connectivity_types:
            logger.debug("could not find host by using initiators: {0} ".format(initiators))
            raise array_errors.HostNotFoundError(initiators)
        host_name = self._get_host_name_if_equal(nvme_host, fc_host, iscsi_host)
        if not host_name:
            raise array_errors.MultipleHostsFoundError(initiators, fc_host)
        return host_name, list(connectivity_types)

    def _get_host_names_by_wwpn(self, host_wwpn):
        fabrics = self._lsfabric(wwpn=host_wwpn).as_list
        return set(fabric.name for fabric in fabrics)

    def _lsnvmefabric(self, host_nqn):
        try:
            return self.client.svcinfo.lsnvmefabric(remotenqn=host_nqn).as_list
        except(svc_errors.CommandExecutionError, CLIFailureError) as ex:
            logger.error("Failed to get nvme fabrics. Reason "
                         "is: {0}".format(ex))
            raise ex

    def _is_lsnvmefabric_supported(self):
        return hasattr(self.client.svcinfo, "lsnvmefabric")

    def _get_host_names_by_nqn(self, nqn):
        if self._is_lsnvmefabric_supported():
            nvme_fabrics = self._lsnvmefabric(nqn)
            return set(nvme_fabric.object_name for nvme_fabric in nvme_fabrics)
        return None

    def _lshostiplogin(self, iqn):
        try:
            return self.client.svcinfo.lshostiplogin(object_id=iqn).as_single_element
        except(svc_errors.CommandExecutionError, CLIFailureError) as ex:
            if SPECIFIED_OBJ_NOT_EXIST in ex.my_message:
                return None
            logger.error("Failed to get iscsi host. Reason "
                         "is: {0}".format(ex))
            raise ex

    def _is_lshostiplogin_supported(self):
        return hasattr(self.client.svcinfo, "lshostiplogin")

    def _get_host_name_by_iqn(self, iqn):
        if self._is_lshostiplogin_supported():
            iscsi_login = self._lshostiplogin(iqn)
            if iscsi_login:
                return iscsi_login.host_name
        return None

    def _get_host_names_and_connectivity_types(self, initiators):
        host_names = set()
        connectivity_types = set()
        for connectivity_type, initiator in initiators:
            if connectivity_type == array_settings.NVME_OVER_FC_CONNECTIVITY_TYPE:
                nvme_host_names = self._get_host_names_by_nqn(initiator)
                if nvme_host_names:
                    host_names.update(nvme_host_names)
                    connectivity_types.add(array_settings.NVME_OVER_FC_CONNECTIVITY_TYPE)
            elif connectivity_type == array_settings.FC_CONNECTIVITY_TYPE:
                fc_host_names = self._get_host_names_by_wwpn(initiator)
                if fc_host_names:
                    host_names.update(fc_host_names)
                    connectivity_types.add(array_settings.FC_CONNECTIVITY_TYPE)
            elif connectivity_type == array_settings.ISCSI_CONNECTIVITY_TYPE:
                iscsi_host_name = self._get_host_name_by_iqn(initiator)
                if iscsi_host_name:
                    host_names.add(iscsi_host_name)
                    connectivity_types.add(array_settings.ISCSI_CONNECTIVITY_TYPE)
        return host_names, connectivity_types

    def get_host_by_host_identifiers(self, initiators):
        logger.debug("Getting host name for initiators : {0}".format(initiators))
        host_names, connectivity_types = self._get_host_names_and_connectivity_types(initiators)
        host_names = set(filter(None, host_names))
        if len(host_names) > 1:
            raise array_errors.MultipleHostsFoundError(initiators, host_names)
        if len(host_names) == 1:
            return host_names.pop(), connectivity_types
        return self._get_host_by_host_identifiers_slow(initiators)

    def _get_detailed_hosts_list(self):
        logger.debug("Getting detailed hosts list on array {0}".format(self.endpoint))
        hosts_list = self.client.svcinfo.lshost()
        if not hosts_list:
            return []

        # get all hosts details by sending a single batch of commands, in which each command is per host
        detailed_hosts_list_cmd = self._get_detailed_hosts_list_cmd(hosts_list)
        logger.debug("Sending getting detailed hosts list commands batch")
        raw_response = self.client.send_raw_command(detailed_hosts_list_cmd)
        response = SVCResponse(raw_response, {'delim': ' '})
        return response.as_list

    def _get_detailed_hosts_list_cmd(self, host_list):
        writer = StringIO()
        for host in host_list:
            writer.write(LIST_HOSTS_CMD_FORMAT.format(HOST_ID=host.id))
        return writer.getvalue()

    def _get_cli_host(self, id_or_name):
        cli_host = self.client.svcinfo.lshost(object_id=id_or_name).as_single_element
        if not cli_host:
            raise array_errors.HostNotFoundError(id_or_name)
        return cli_host

    def get_host_by_name(self, host_name):
        cli_host = self._get_cli_host(host_name)
        nvme_nqns = self._get_host_ports(cli_host, HOST_NQN)
        fc_wwns = self._get_host_ports(cli_host, HOST_WWPN)
        iscsi_iqns = self._get_host_ports(cli_host, HOST_ISCSI_NAME)
        connectivity_types = []
        if nvme_nqns:
            connectivity_types.append(array_settings.NVME_OVER_FC_CONNECTIVITY_TYPE)
        if fc_wwns:
            connectivity_types.append(array_settings.FC_CONNECTIVITY_TYPE)
        if iscsi_iqns:
            connectivity_types.append(array_settings.ISCSI_CONNECTIVITY_TYPE)
        return Host(name=cli_host.name, connectivity_types=connectivity_types, nvme_nqns=nvme_nqns,
                    fc_wwns=fc_wwns, iscsi_iqns=iscsi_iqns)

    def _lsvdiskhostmap(self, volume_name):
        try:
            return self.client.svcinfo.lsvdiskhostmap(vdisk_name=volume_name)
        except(svc_errors.CommandExecutionError, CLIFailureError) as ex:
            logger.error(ex)
            raise array_errors.ObjectNotFoundError(volume_name)

    def get_volume_mappings(self, volume_id):
        logger.debug("Getting volume mappings for volume id : "
                     "{0}".format(volume_id))
        volume_name = self._get_volume_name_by_wwn(volume_id)
        logger.debug("volume name : {0}".format(volume_name))
        mapping_list = self._lsvdiskhostmap(volume_name)
        luns_by_host = {}
        for mapping in mapping_list:
            logger.debug("mapping for volume is :{0}".format(mapping))
            luns_by_host[mapping.get('host_name', '')] = mapping.get('SCSI_id', '')
        return luns_by_host

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

    def _get_free_lun(self, host_name):
        logger.debug("getting random free lun id for "
                     "host :{0}".format(host_name))
        lun = None
        luns_in_use = self._get_used_lun_ids_from_host(host_name)
        # Today we have SS_MAX_HLUN_MAPPINGS_PER_HOST as 2048 on high end
        # platforms (SVC / V7000 etc.) and 512 for the lower
        # end platforms (V3500 etc.). This limits the number of volumes that
        # can be mapped to a single host. (Note that some hosts such as linux
        # do not support more than 255 or 511 mappings today irrespective of
        # our constraint).
        lun_range_gen = range(self.MIN_LUN_NUMBER, self.MAX_LUN_NUMBER + 1)
        lun_range = [str(lun) for lun in lun_range_gen]
        free_luns = [lun for lun in lun_range if lun not in luns_in_use]
        free_luns_in_interval = free_luns[:LUN_INTERVAL]
        if free_luns:
            lun = choice(free_luns_in_interval)
        else:
            raise array_errors.NoAvailableLunError(host_name)
        logger.debug("The chosen available lun is : {0}".format(lun))
        return lun

    def map_volume(self, volume_id, host_name, connectivity_type):
        logger.debug("mapping volume : {0} to host : "
                     "{1}".format(volume_id, host_name))
        volume_name = self._get_volume_name_by_wwn(volume_id)
        cli_kwargs = {
            'host': host_name,
            'object_id': volume_name,
            'force': True
        }
        lun = ""
        try:
            if connectivity_type != array_settings.NVME_OVER_FC_CONNECTIVITY_TYPE:
                lun = self._get_free_lun(host_name)
                cli_kwargs.update({'scsi': lun})
            self.client.svctask.mkvdiskhostmap(**cli_kwargs)
        except (svc_errors.CommandExecutionError, CLIFailureError) as ex:
            if is_warning_message(ex.my_message):
                logger.warning("exception encountered during volume {0} mapping to host {1}: {2}".format(volume_name,
                                                                                                         host_name,
                                                                                                         ex.my_message))
            else:
                logger.error("Map volume {0} to host {1} failed. Reason "
                             "is: {2}".format(volume_name, host_name, ex))
                if NAME_NOT_EXIST_OR_MEET_RULES in ex.my_message:
                    raise array_errors.HostNotFoundError(host_name)
                if SPECIFIED_OBJ_NOT_EXIST in ex.my_message:
                    raise array_errors.ObjectNotFoundError(volume_name)
                if LUN_ALREADY_IN_USE in ex.my_message:
                    raise array_errors.LunAlreadyInUseError(lun,
                                                            host_name)
                raise array_errors.MappingError(volume_name, host_name, ex)

        return str(lun)

    def unmap_volume(self, volume_id, host_name):
        logger.debug("unmapping volume : {0} from host : "
                     "{1}".format(volume_id, host_name))
        volume_name = self._get_volume_name_by_wwn(volume_id)

        cli_kwargs = {
            'host': host_name,
            'vdisk_id': volume_name
        }

        try:
            self.client.svctask.rmvdiskhostmap(**cli_kwargs)
        except (svc_errors.CommandExecutionError, CLIFailureError) as ex:
            if is_warning_message(ex.my_message):
                logger.warning("exception encountered during volume {0}"
                               " unmapping from host {1}: {2}".format(volume_name,
                                                                      host_name,
                                                                      ex.my_message))
            else:
                logger.error("unmapping volume {0} from host {1} failed. Reason "
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
        nodes_list = self.client.svcinfo.lsnode()
        array_iqns_by_id = {node.id: node.iscsi_name for node in nodes_list
                            if node.status.lower() == "online"}
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

    def _lsfabric(self, **kwargs):
        try:
            return self.client.svcinfo.lsfabric(**kwargs)
        except(svc_errors.CommandExecutionError, CLIFailureError) as ex:
            logger.error("Failed to get fabrics for {0}. Reason "
                         "is: {1}".format(kwargs, ex))
            raise ex

    def get_array_fc_wwns(self, host_name):
        logger.debug("Getting the connected fc port wwn value from array "
                     "related to host : {}.".format(host_name))
        fc_port_wwns = []
        fc_wwns = self._lsfabric(host=host_name)
        for wwn in fc_wwns:
            state = wwn.get('state', '')
            if state in ('active', 'inactive'):
                fc_port_wwns.append(wwn.get('local_wwpn', ''))
        logger.debug("Getting fc wwns : {}".format(fc_port_wwns))
        return fc_port_wwns

    def _get_host_portset_id(self, host_name):
        cli_host = self._get_cli_host(host_name)
        return cli_host.get(HOST_PORTSET_ID)

    def _get_replication_endpoint_type(self, rcrelationship):
        if self.identifier == rcrelationship.master_cluster_id:
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
            return array_settings.REPLICATION_COPY_TYPE_ASYNC
        return array_settings.REPLICATION_COPY_TYPE_SYNC

    def _generate_replication_response(self, rcrelationship):
        copy_type = self._get_replication_copy_type(rcrelationship)
        is_ready = self._is_replication_ready(rcrelationship)
        is_primary = self._is_replication_endpoint_primary(rcrelationship)
        return Replication(name=rcrelationship.name,
                           copy_type=copy_type,
                           is_ready=is_ready,
                           is_primary=is_primary)

    def _generate_ear_replication_response(self, volume_group_replication, replication_mode):
        name = volume_group_replication.replication_policy_name
        copy_type = array_settings.REPLICATION_COPY_TYPE_ASYNC
        is_ready = True
        is_primary = (replication_mode == ENDPOINT_TYPE_PRODUCTION)

        return Replication(name=name,
                           copy_type=copy_type,
                           is_ready=is_ready,
                           is_primary=is_primary,
                           volume_group_id=volume_group_replication.id)

    def _lsrcrelationship(self, filter_value):
        return self.client.svcinfo.lsrcrelationship(filtervalue=filter_value)

    def _get_rcrelationship_by_name(self, replication_name, not_exist_error=True):
        filter_value = 'RC_rel_name={0}'.format(replication_name)
        rcrelationship = self._lsrcrelationship(filter_value).as_single_element
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
        return self._lsrcrelationship(filter_value).as_list

    def _get_rcrelationship(self, cli_volume_id, other_cli_volume_id, other_system_id):
        rcrelationships = self._get_rcrelationships(cli_volume_id, other_cli_volume_id,
                                                    other_system_id, as_master=True)
        rcrelationships.extend(self._get_rcrelationships(cli_volume_id, other_cli_volume_id,
                                                         other_system_id, as_master=False))
        if len(rcrelationships) > 1:
            error_message = ('found {0} rcrelationships for volume id {1} '
                             'with volume id {2} of system {3}: {4}'.format(len(rcrelationships),
                                                                            cli_volume_id,
                                                                            other_cli_volume_id,
                                                                            other_system_id,
                                                                            rcrelationships))
            logger.error(error_message)
            raise RuntimeError(error_message)
        return rcrelationships[0] if rcrelationships else None

    def get_replication(self, replication_request):
        if replication_request.replication_type == array_settings.REPLICATION_TYPE_MIRROR:
            return self._get_replication(replication_request)
        elif replication_request.replication_type == array_settings.REPLICATION_TYPE_EAR:
            return self._get_ear_replication(replication_request)

    def _get_replication(self, replication_request):
        rcrelationship = self._get_rcrelationship(replication_request.volume_internal_id,
                                                  replication_request.other_volume_internal_id,
                                                  replication_request.other_system_id)
        if not rcrelationship:
            return None
        logger.info("found rcrelationship: {}".format(rcrelationship))
        return self._generate_replication_response(rcrelationship)

    def _get_ear_replication(self, replication_request):
        # for phase 1 - find volume by id and get volume group id from result volume
        volume_internal_id = replication_request.volume_internal_id
        cli_volume = self._get_cli_volume(volume_internal_id)
        volume_group_id = cli_volume.volume_group_id
        if volume_group_id == "":
            return None

        cli_volume_group = self._get_cli_volume_group(volume_group_id, not_exist_err=False)
        if not cli_volume_group:
            return None
        cli_volume_group_replication = self._lsvolumegroupreplication(volume_group_id)
        replication_mode = self._get_replication_mode(cli_volume_group_replication)
        logger.info("found replication: {} in mode: {}".format(cli_volume_group.name,
                                                               replication_mode))
        return self._generate_ear_replication_response(cli_volume_group_replication, replication_mode)

    def _get_replication_mode(self, volume_group_id):
        volume_group_replication = self._lsvolumegroupreplication(volume_group_id)
        replication_local_location = volume_group_replication.local_location
        if replication_local_location == "1":
            location_parameter = "location"+replication_local_location+"_replication_mode"
            logger.info("local replication location is: {}".format(location_parameter))

            mode = volume_group_replication.location1_replication_mode
        elif replication_local_location == "2":
            location_parameter = "location"+replication_local_location+"_replication_mode"
            logger.info("local replication location is: {}".format(location_parameter))

            mode = volume_group_replication.location2_replication_mode
        else:
            location_parameter = "location"+replication_local_location+"_replication_mode"
            logger.info("local replication location is: {}".format(location_parameter))

            mode = None
        return mode

    def _is_earreplication_supported(self):
        return hasattr(self.client.svctask, "chvolumereplicationinternals")

    def _create_rcrelationship(self, master_cli_volume_id, aux_cli_volume_id, other_system_id, copy_type):
        logger.info("creating remote copy relationship for master volume id: {0} "
                    "and auxiliary volume id: {1} with system {2} using {3} copy type".format(master_cli_volume_id,
                                                                                              aux_cli_volume_id,
                                                                                              other_system_id,
                                                                                              copy_type))
        kwargs = build_create_replication_kwargs(master_cli_volume_id, aux_cli_volume_id, other_system_id, copy_type)
        try:
            svc_response = self.client.svctask.mkrcrelationship(**kwargs)
            return self._get_id_from_response(svc_response)
        except (svc_errors.CommandExecutionError, CLIFailureError) as ex:
            if is_warning_message(ex.my_message):
                logger.warning("exception encountered during creation of rcrelationship for volume id {0} "
                               "with volume id {1} of system {2}: {3}".format(master_cli_volume_id,
                                                                              aux_cli_volume_id,
                                                                              other_system_id,
                                                                              ex))
            else:
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
            if is_warning_message(ex.my_message):
                logger.warning("exception encountered while starting rcrelationship '{}': {}".format(rcrelationship_id,
                                                                                                     ex.my_message))
            else:
                logger.warning("failed to start rcrelationship '{}': {}".format(rcrelationship_id, ex))

    def create_replication(self, replication_request):
        if replication_request.replication_type == array_settings.REPLICATION_TYPE_MIRROR:
            self._create_replication(replication_request)
        elif replication_request.replication_type == array_settings.REPLICATION_TYPE_EAR:
            self._create_ear_replication(replication_request)

    def _create_replication(self, replication_request):
        rc_id = self._create_rcrelationship(replication_request.volume_internal_id,
                                            replication_request.other_volume_internal_id,
                                            replication_request.other_system_id,
                                            replication_request.copy_type)
        self._start_rcrelationship(rc_id)

    def _create_ear_replication(self, replication_request):
        if not self._is_earreplication_supported():
            logger.info("EAR replication is not supported on the existing storage")
            return
        volume_internal_id = replication_request.volume_internal_id
        replication_policy = replication_request.replication_policy

        cli_volume = self._get_cli_volume(volume_internal_id)
        volume_group_name = cli_volume.name + "_vg"

        # for phase 1 - create empty volume group and move volume into it
        self._create_volume_group(volume_group_name)
        self._change_volume_group(volume_internal_id, volume_group_name)

        self._change_volume_group_policy(volume_group_name, replication_policy)

    def _stop_rcrelationship(self, rcrelationship_id, add_access_to_secondary=False):
        logger.info("stopping remote copy relationship with id: {}. access: {}".format(rcrelationship_id,
                                                                                       add_access_to_secondary))
        kwargs = build_stop_replication_kwargs(rcrelationship_id, add_access_to_secondary)
        try:
            self.client.svctask.stoprcrelationship(**kwargs)
        except (svc_errors.CommandExecutionError, CLIFailureError) as ex:
            if is_warning_message(ex.my_message):
                logger.warning("exception encountered while stopping"
                               " rcrelationship '{0}': {1}".format(rcrelationship_id,
                                                                   ex.my_message))
            else:
                logger.warning("failed to stop rcrelationship '{0}': {1}".format(rcrelationship_id, ex))

    def _delete_rcrelationship(self, rcrelationship_id):
        logger.info("deleting remote copy relationship with id: {0}".format(rcrelationship_id))
        try:
            self.client.svctask.rmrcrelationship(object_id=rcrelationship_id)
        except (svc_errors.CommandExecutionError, CLIFailureError) as ex:
            if is_warning_message(ex.my_message):
                logger.warning("exception encountered during rcrelationship"
                               " '{0}' deletion: {1}".format(rcrelationship_id,
                                                             ex.my_message))
            else:
                logger.warning("failed to delete rcrelationship '{0}': {1}".format(rcrelationship_id, ex))

    def delete_replication(self, replication):
        if replication.replication_type == array_settings.REPLICATION_TYPE_MIRROR:
            self._delete_replication(replication.name)
        elif replication.replication_type == array_settings.REPLICATION_TYPE_EAR:
            self._delete_ear_replication(replication.volume_group_id)

    def _delete_replication(self, replication_name):
        rcrelationship = self._get_rcrelationship_by_name(replication_name, not_exist_error=False)
        if not rcrelationship:
            logger.info("could not find replication with name {}".format(replication_name))
            return
        self._stop_rcrelationship(rcrelationship.id)
        self._delete_rcrelationship(rcrelationship.id)

    def _delete_ear_replication(self, volume_group_id):
        if not self._is_earreplication_supported():
            logger.info("EAR replication is not supported on the existing storage")
            return

        self._change_volume_group_policy(volume_group_id)

        # for phase 1 - move volume outside the group and delete the volume group
        cli_volume_id = self._get_cli_volume_id_from_volume_group("volume_group_id", volume_group_id)
        self._change_volume_group(cli_volume_id)
        self._rmvolumegroup(volume_group_id)

    def _promote_replication_endpoint(self, endpoint_type, replication_name):
        logger.info("making '{}' primary for remote copy relationship {}".format(endpoint_type, replication_name))
        try:
            self.client.svctask.switchrcrelationship(primary=endpoint_type, object_id=replication_name)
        except (svc_errors.CommandExecutionError, CLIFailureError) as ex:
            if is_warning_message(ex.my_message):
                logger.warning("exception encountered while making '{}' primary"
                               " for rcrelationship {}: {}".format(endpoint_type,
                                                                   replication_name,
                                                                   ex.my_message))
            else:
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

    def promote_replication_volume(self, replication):
        if replication.replication_type == array_settings.REPLICATION_TYPE_MIRROR:
            self._promote_replication_volume(replication.name)
        elif replication.replication_type == array_settings.REPLICATION_TYPE_EAR:
            self._promote_ear_replication_volume(replication.volume_group_id)

    def _promote_replication_volume(self, replication_name):
        rcrelationship = self._get_rcrelationship_by_name(replication_name)
        if self._is_replication_disconnected(rcrelationship):
            self._stop_rcrelationship(rcrelationship.id, add_access_to_secondary=True)
            return
        endpoint_type = self._get_replication_endpoint_type(rcrelationship)
        self._ensure_endpoint_is_primary(rcrelationship, endpoint_type)

    def _promote_ear_replication_volume(self, volume_group_id):
        if not self._is_earreplication_supported():
            logger.info("EAR replication is not supported on the existing storage")
            return
        cli_kwargs = {}
        if self._get_replication_mode(volume_group_id) == ENDPOINT_TYPE_RECOVERY:
            cli_kwargs['mode'] = ENDPOINT_TYPE_INDEPENDENT
            logger.info("Changing the local volume group to be an independent copy")
            self._chvolumegroupreplication(volume_group_id, **cli_kwargs)

        if self._get_replication_mode(volume_group_id) == ENDPOINT_TYPE_INDEPENDENT:
            cli_kwargs['mode'] = ENDPOINT_TYPE_PRODUCTION
            logger.info("Changing the local volume group to be a production copy")
            self._chvolumegroupreplication(volume_group_id, **cli_kwargs)
        else:
            logger.info("Can't be promoted because the local volume group is not an independent copy")

    def demote_replication_volume(self, replication):
        if replication.replication_type == array_settings.REPLICATION_TYPE_MIRROR:
            self._demote_replication_volume(replication.name)
        elif replication.replication_type == array_settings.REPLICATION_TYPE_EAR:
            self._demote_ear_replication_volume()

    def _demote_replication_volume(self, replication_name):
        rcrelationship = self._get_rcrelationship_by_name(replication_name)
        endpoint_type_to_promote = self._get_replication_other_endpoint_type(rcrelationship)
        self._ensure_endpoint_is_primary(rcrelationship, endpoint_type_to_promote)

    def _demote_ear_replication_volume(self):
        if not self._is_earreplication_supported():
            logger.info("EAR replication is not supported on the existing storage")
            return
        logger.info("Demote volume is not supported in the current version")

    def _get_host_name_if_equal(self, nvme_host, fc_host, iscsi_host):
        unique_names = {nvme_host, iscsi_host, fc_host}
        unique_names.discard(None)
        if len(unique_names) == 1:
            return unique_names.pop()
        return None

    def _addsnapshot(self, name, source_volume_id, pool):
        try:
            return self.client.svctask.addsnapshot(name=name, volumes=source_volume_id, pool=pool)
        except (svc_errors.CommandExecutionError, CLIFailureError) as ex:
            if is_warning_message(ex.my_message):
                logger.warning("exception encountered while creating snapshot '{}': {}".format(name,
                                                                                               ex.my_message))
            else:
                logger.error("cannot create snapshot {0}, Reason is: {1}".format(name, ex))
                if OBJ_ALREADY_EXIST in ex.my_message:
                    raise array_errors.SnapshotAlreadyExists(name, self.endpoint)
                if NAME_NOT_EXIST_OR_MEET_RULES in ex.my_message or NOT_CHILD_POOL in ex.my_message:
                    raise array_errors.PoolDoesNotExist(pool, self.endpoint)
                if NOT_ENOUGH_EXTENTS_IN_POOL_CREATE in ex.my_message:
                    raise array_errors.NotEnoughSpaceInPool(id_or_name=pool)
                if any(msg_id in ex.my_message for msg_id in (NON_ASCII_CHARS, INVALID_NAME, TOO_MANY_CHARS)):
                    raise array_errors.InvalidArgumentError(ex.my_message)
                raise ex
            return None

    def _get_id_from_response(self, response):
        message = str(response.response[0])
        id_start, id_end = message.find('[') + 1, message.find(']')
        raw_id = message[id_start:id_end]
        return int(raw_id)

    def _lsvolumesnapshot(self, **kwargs):
        try:
            return self.client.svcinfo.lsvolumesnapshot(**kwargs).as_single_element
        except (svc_errors.CommandExecutionError, CLIFailureError) as ex:
            if OBJ_NOT_FOUND in ex.my_message or NAME_NOT_EXIST_OR_MEET_RULES in ex.my_message:
                logger.info("snapshot not found for args: {}".format(kwargs))
            elif any(msg_id in ex.my_message for msg_id in (NON_ASCII_CHARS, VALUE_TOO_LONG)):
                raise array_errors.InvalidArgumentError(ex.my_message)
            else:
                raise ex
        return None

    def _get_cli_snapshot_by_id(self, snapshot_id):
        return self._lsvolumesnapshot(object_id=snapshot_id)

    def _get_cli_snapshot_by_name(self, snapshot_name):
        filter_value = 'snapshot_name={}'.format(snapshot_name)
        return self._lsvolumesnapshot(filtervalue=filter_value)

    def _add_snapshot(self, snapshot_name, source_id, pool):
        svc_response = self._addsnapshot(name=snapshot_name, source_volume_id=source_id, pool=pool)
        snapshot_id = self._get_id_from_response(svc_response)
        cli_snapshot = self._get_cli_snapshot_by_id(snapshot_id)
        if cli_snapshot is None:
            raise array_errors.ObjectNotFoundError(snapshot_id)
        return cli_snapshot

    def _mkvolumegroup(self, **cli_kwargs):
        name = cli_kwargs['name']
        pool = cli_kwargs['pool'] if 'pool' in cli_kwargs else None
        try:
            return self.client.svctask.mkvolumegroup(**cli_kwargs)
        except (svc_errors.CommandExecutionError, CLIFailureError) as ex:
            if is_warning_message(ex.my_message):
                logger.warning(
                    "exception encountered during creation of volume group and volume {0}: {1}".format(name,
                                                                                                       ex.my_message))
            else:
                logger.error("Cannot create volume {0}, Reason is: {1}".format(name, ex.my_message))
                if OBJ_ALREADY_EXIST in ex.my_message:
                    raise array_errors.VolumeAlreadyExists(name, self.endpoint)
                if NOT_ENOUGH_EXTENTS_IN_POOL_CREATE in ex.my_message:
                    raise array_errors.NotEnoughSpaceInPool(id_or_name=pool)
                if any(msg_id in ex.my_message for msg_id in (NAME_NOT_EXIST_OR_MEET_RULES, NON_ASCII_CHARS,
                                                              INVALID_NAME, TOO_MANY_CHARS)):
                    raise array_errors.InvalidArgumentError(ex.my_message)
                raise ex
        return None

    def _get_cli_volume_id_from_volume_group(self, filter, filter_parameter):
        filter_value = '{}={}'.format(filter, filter_parameter)
        cli_volume = self._lsvdisk(filtervalue=filter_value)
        return cli_volume.id

    def _rollback_create_volume_from_snapshot(self, cli_volume_id, volume_group_name):
        self._rmvolume(cli_volume_id)
        self._rmvolumegroup(volume_group_name)

    def _change_volume_group(self, cli_volume_id, volume_group=None):
        cli_kwargs = {}
        if volume_group:
            cli_kwargs['volumegroup'] = volume_group
        else:
            cli_kwargs['novolumegroup'] = True
        self._chvdisk(cli_volume_id, **cli_kwargs)

    def _change_volume_group_policy(self, id_or_name, replication_policy=None):
        cli_kwargs = {}
        if replication_policy:
            cli_kwargs['replicationpolicy'] = replication_policy
        else:
            cli_kwargs['noreplicationpolicy'] = True
        self._chvolumegroup(id_or_name, **cli_kwargs)

    def _rename_volume(self, cli_volume_id, name):
        self._chvdisk(cli_volume_id, name=name)

    def _chvdisk(self, cli_volume_id, **kwargs):
        try:
            self.client.svctask.chvdisk(vdisk_id=cli_volume_id, **kwargs)
        except (svc_errors.CommandExecutionError, CLIFailureError) as ex:
            if is_warning_message(ex.my_message):
                logger.warning(
                    "exception encountered while changing volume parameters '{}': {}".format(kwargs, ex.my_message))
            else:
                if OBJ_ALREADY_EXIST in ex.my_message:
                    raise array_errors.VolumeAlreadyExists(kwargs, self.endpoint)
                raise ex

    def _rmvolumegroup(self, id_or_name, not_exist_error=False):
        logger.info("deleting volume group : {0}".format(id_or_name))
        try:
            self.client.svctask.rmvolumegroup(object_id=id_or_name)
        except (svc_errors.CommandExecutionError, CLIFailureError) as ex:
            if is_warning_message(ex.my_message):
                logger.warning("exception encountered during deletion of volume group {}: {}".format(id_or_name,
                                                                                                     ex.my_message))
            else:
                logger.error("Failed to delete volume group {}".format(id_or_name))
                if OBJ_NOT_FOUND in ex.my_message or VOL_NOT_FOUND in ex.my_message:
                    logger.warning(array_errors.ObjectNotFoundError(id_or_name))
                    if not not_exist_error:
                        return
                raise ex

    def _is_vdisk_has_fcmaps(self, vdisk_uid):
        if not vdisk_uid:
            return False
        cli_volume = self._get_cli_volume_by_wwn(vdisk_uid, not_exist_err=False)
        return cli_volume and cli_volume.FC_id

    def _mkhost(self, host_name, connectivity_type, ports):
        cli_kwargs = build_create_host_kwargs(host_name, connectivity_type, ports)
        try:
            self.client.svctask.mkhost(**cli_kwargs)
        except (svc_errors.CommandExecutionError, CLIFailureError) as ex:
            if is_warning_message(ex.my_message):
                logger.warning("exception encountered during host {} creation : {}".format(host_name, ex.my_message))
            if OBJ_ALREADY_EXIST in ex.my_message:
                raise array_errors.HostAlreadyExists(host_name, self.endpoint)
            raise ex

    def _get_connectivity_type_by_initiators(self, initiators):
        if initiators.nvme_nqns:
            return array_settings.NVME_OVER_FC_CONNECTIVITY_TYPE
        if initiators.fc_wwns:
            return array_settings.FC_CONNECTIVITY_TYPE
        if initiators.iscsi_iqns:
            return array_settings.ISCSI_CONNECTIVITY_TYPE
        return None

    def create_host(self, host_name, initiators, connectivity_type):
        if not connectivity_type:
            connectivity_type = self._get_connectivity_type_by_initiators(initiators)
        ports = initiators.get_by_connectivity_type(connectivity_type)
        if ports:
            self._mkhost(host_name, connectivity_type, ports)
            return
        raise array_errors.NoPortFoundByConnectivityType(initiators, connectivity_type)

    def _rmhost(self, host_name):
        try:
            self.client.svctask.rmhost(object_id=host_name)
        except (svc_errors.CommandExecutionError, CLIFailureError) as ex:
            if is_warning_message(ex.my_message):
                logger.warning("exception encountered during host {} deletion : {}".format(host_name, ex.my_message))
                return
            raise ex

    def delete_host(self, host_name):
        self._rmhost(host_name)
