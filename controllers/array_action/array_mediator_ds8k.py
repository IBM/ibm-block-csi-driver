from decorator import decorator
from packaging.version import parse
from pyds8k import exceptions
from pyds8k.resources.ds8k.v1.common import attr_names
from pyds8k.resources.ds8k.v1.common import types as ds8k_types
from retry import retry

import controllers.array_action.errors as array_errors
import controllers.servers.config as controller_config
from controllers.array_action import config
from controllers.array_action.array_action_types import Volume, Snapshot, Host
from controllers.array_action.array_mediator_abstract import ArrayMediatorAbstract
from controllers.array_action.ds8k_rest_client import RESTClient, scsilun_to_int
from controllers.array_action.ds8k_volume_cache import VolumeCache
from controllers.array_action.utils import ClassProperty
from controllers.common import settings
from controllers.common.csi_logger import get_stdout_logger

LOGIN_PORT_WWPN = attr_names.IOPORT_WWPN
LOGIN_PORT_STATE = attr_names.IOPORT_STATUS
LOGIN_PORT_STATE_ONLINE = 'online'

logger = get_stdout_logger()

# response error codes
ERROR_CODE_INVALID_CREDENTIALS = 'BE7A002D'
KNOWN_ERROR_CODE_INVALID_CREDENTIALS = 'BE7A0029'
ERROR_CODE_RESOURCE_NOT_EXISTS = 'BE7A0001'
INCORRECT_ID = 'BE7A0005'
NO_TOKEN_IS_SPECIFIED = 'BE7A001A'
HOST_DOES_NOT_EXIST = 'BE7A0016'
MAPPING_DOES_NOT_EXIST = 'BE7A001F'
ERROR_CODE_MAP_VOLUME_NOT_ENOUGH_EXTENTS = 'BE74121B'
ERROR_CODE_VOLUME_NOT_FOUND_FOR_MAPPING = 'BE586015'
ERROR_CODE_ALREADY_FLASHCOPY = '000000AE'
ERROR_CODE_VOLUME_NOT_FOUND_OR_ALREADY_PART_OF_CS_RELATIONSHIP = '00000013'
ERROR_CODE_EXPAND_VOLUME_NOT_ENOUGH_EXTENTS = 'BE531465'
ERROR_CODE_CREATE_VOLUME_NOT_ENOUGH_EXTENTS = 'BE534459'

FLASHCOPY_PERSISTENT_OPTION = ds8k_types.DS8K_OPTION_PER
FLASHCOPY_NO_BACKGROUND_COPY_OPTION = ds8k_types.DS8K_OPTION_NBC
FLASHCOPY_PERMIT_SPACE_EFFICIENT_TARGET_OPTION = ds8k_types.DS8K_OPTION_PSET
FLASHCOPY_STATE_VALID = 'valid'

ARRAY_SPACE_EFFICIENCY_THIN = ds8k_types.DS8K_TP_ESE
ARRAY_SPACE_EFFICIENCY_NONE = ds8k_types.DS8K_TP_NONE


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


def scsi_id_to_volume_id(scsi_id):
    return scsi_id[-4:]


def try_convert_first_args(converter, args, args_amount):
    if args:
        args_to_convert = args[:args_amount]
        converted = map(converter, args_to_convert)
        return tuple(converted) + args[args_amount:]
    return ()


def is_snapshot(api_volume):
    flashcopies = api_volume.flashcopy
    for flashcopy in flashcopies:
        if flashcopy.targetvolume == api_volume.id and flashcopy.backgroundcopy == "disabled":
            return True
    return False


def convert_scsi_ids_to_array_ids(args_amount=1):
    @decorator
    def convert_first_args_of_method(mediator_method, self, *args):
        args = try_convert_first_args(scsi_id_to_volume_id, args, args_amount)
        return mediator_method(self, *args)

    return convert_first_args_of_method


def get_flashcopy_as_target_if_exists(api_volume):
    flashcopies = [flashcopy for flashcopy in api_volume.flashcopy
                   if flashcopy.targetvolume == api_volume.id]
    if len(flashcopies) != 1:
        return None
    return flashcopies[0]


def get_array_space_efficiency(space_efficiency):
    if space_efficiency:
        space_efficiency_lower = space_efficiency.lower()
        if space_efficiency_lower == config.SPACE_EFFICIENCY_THIN:
            return ARRAY_SPACE_EFFICIENCY_THIN
    return ARRAY_SPACE_EFFICIENCY_NONE


def _get_parameter_space_efficiency(array_space_efficiency):
    if array_space_efficiency == ARRAY_SPACE_EFFICIENCY_THIN:
        return config.SPACE_EFFICIENCY_THIN
    if array_space_efficiency == ARRAY_SPACE_EFFICIENCY_NONE:
        return config.SPACE_EFFICIENCY_NONE
    raise array_errors.SpaceEfficiencyNotSupported(array_space_efficiency)


class DS8KArrayMediator(ArrayMediatorAbstract):
    SUPPORTED_FROM_VERSION = '7.5.1'

    @ClassProperty
    def array_type(self):
        return settings.ARRAY_TYPE_DS8K

    @ClassProperty
    def port(self):
        return 8452

    @ClassProperty
    def max_object_name_length(self):
        return 16

    @ClassProperty
    def max_object_prefix_length(self):
        return 5

    @ClassProperty
    def max_connections(self):
        # max for rest api is 128.
        return 50

    @ClassProperty
    def minimal_volume_size_in_bytes(self):
        return 512  # 1 block, 512 bytes

    @ClassProperty
    def maximal_volume_size_in_bytes(self):
        return 16 * 1024 * 1024 * 1024 * 1024

    @ClassProperty
    def max_lun_retries(self):
        return 10

    @ClassProperty
    def default_object_prefix(self):
        return None

    def __init__(self, user, password, endpoint):
        super().__init__(user, password, endpoint)
        self.service_address = \
            self.endpoint[0] if isinstance(self.endpoint, list) else self.endpoint

        self._connect()
        self.volume_cache = VolumeCache(self.service_address)

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
        except exceptions.ClientException as ex:
            error_message = str(ex.message).upper()
            if ERROR_CODE_INVALID_CREDENTIALS in error_message or KNOWN_ERROR_CODE_INVALID_CREDENTIALS in error_message:
                raise array_errors.CredentialsError(self.service_address)
            logger.error(
                'Failed to connect to DS8K array {}, reason is {}'.format(self.service_address, ex.details))
            raise ex

    def disconnect(self):
        pass

    def is_active(self):
        return self.client.is_valid()

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

    def _get_source_id(self, api_volume):
        source_id = None
        flashcopy_as_target = get_flashcopy_as_target_if_exists(api_volume=api_volume)
        if flashcopy_as_target:
            source_volume_id = flashcopy_as_target.sourcevolume
            source_id = self._generate_volume_scsi_identifier(volume_id=source_volume_id)
        return source_id

    def _generate_volume_response(self, api_volume):
        space_efficiency = _get_parameter_space_efficiency(api_volume.tp)
        return Volume(
            capacity_bytes=int(api_volume.cap),
            id=self._generate_volume_scsi_identifier(volume_id=api_volume.id),
            internal_id=api_volume.id,
            name=api_volume.name,
            array_address=self.service_address,
            source_id=self._get_source_id(api_volume=api_volume),
            pool=api_volume.pool,
            array_type=self.array_type,
            space_efficiency=space_efficiency,
            default_space_efficiency=config.SPACE_EFFICIENCY_NONE
        )

    def _create_api_volume(self, name, size_in_bytes, array_space_efficiency, pool_id):
        logger.info("creating volume with name: {}, size: {}, in pool: {}, with parameters: {}".format(
            name, size_in_bytes, pool_id, array_space_efficiency))
        try:
            cli_kwargs = {}
            cli_kwargs.update({
                'name': name,
                'capacity_in_bytes': size_in_bytes,
                'pool_id': pool_id,
                'thin_provisioning': array_space_efficiency,

            })
            logger.debug("start to create volume with parameters: {}".format(cli_kwargs))
            api_volume = self.client.create_volume(**cli_kwargs)
            logger.info("finished creating volume {}".format(name))
            return self.client.get_volume(api_volume.id)
        except exceptions.ClientException as ex:
            error_message = str(ex.message).upper()
            if ERROR_CODE_RESOURCE_NOT_EXISTS in error_message or INCORRECT_ID in error_message:
                raise array_errors.PoolDoesNotExist(pool_id, self.identifier)
            if ERROR_CODE_CREATE_VOLUME_NOT_ENOUGH_EXTENTS in error_message:
                raise array_errors.NotEnoughSpaceInPool(pool_id)
            logger.error(
                "failed to create volume {} on array {}, reason is: {}".format(
                    name,
                    self.identifier,
                    ex.details
                )
            )
            raise array_errors.VolumeCreationError(name)

    def create_volume(self, name, size_in_bytes, space_efficiency, pool, io_group, volume_group, source_ids,
                      source_type, is_virt_snap_func):
        array_space_efficiency = get_array_space_efficiency(space_efficiency)
        api_volume = self._create_api_volume(name, size_in_bytes, array_space_efficiency, pool)
        self.volume_cache.add(api_volume.name, api_volume.id)
        return self._generate_volume_response(api_volume)

    def _extend_volume(self, api_volume, new_size_in_bytes):
        try:
            self.client.extend_volume(volume_id=api_volume.id,
                                      new_size_in_bytes=new_size_in_bytes)
        except exceptions.NotFound:
            raise array_errors.ObjectNotFoundError(api_volume.id)
        except exceptions.ClientException as ex:
            if ERROR_CODE_EXPAND_VOLUME_NOT_ENOUGH_EXTENTS in str(ex.message).upper():
                raise array_errors.NotEnoughSpaceInPool(api_volume.pool)
            raise ex

    @convert_scsi_ids_to_array_ids(args_amount=2)
    def copy_to_existing_volume(self, volume_id, source_id, source_capacity_in_bytes,
                                minimum_volume_size_in_bytes):
        logger.debug(
            "copy source {0} data to volume {1}. source capacity {2}. Minimal requested volume capacity {3}".format(
                source_id, volume_id, source_capacity_in_bytes,
                minimum_volume_size_in_bytes))
        if minimum_volume_size_in_bytes < source_capacity_in_bytes:
            new_api_volume = self._get_api_volume_by_id(volume_id)
            self._extend_volume(api_volume=new_api_volume,
                                new_size_in_bytes=source_capacity_in_bytes)
        options = [FLASHCOPY_PERSISTENT_OPTION]
        self._create_flashcopy(source_volume_id=source_id, target_volume_id=volume_id,
                               options=options)

    def _delete_volume(self, volume_id, not_exist_err=True):
        logger.info("deleting volume {}".format(volume_id))
        try:
            self.client.delete_volume(
                volume_id=volume_id
            )
            logger.info("finished deleting volume {}".format(volume_id))
        except exceptions.NotFound:
            if not_exist_err:
                raise array_errors.ObjectNotFoundError(volume_id)
        except exceptions.ClientException as ex:
            logger.error(
                "failed to delete volume {} on array {}, reason is: {}".format(
                    volume_id,
                    self.identifier,
                    ex.details
                )
            )
            raise array_errors.VolumeDeletionError(volume_id)

    def _safe_delete_flashcopies(self, flashcopies, volume_name):
        for flashcopy in flashcopies:
            self._ensure_flashcopy_safe_to_delete(flashcopy, volume_name)
        for flashcopy in flashcopies:
            self._delete_flashcopy(flashcopy.id)

    def _ensure_flashcopy_safe_to_delete(self, flashcopy, volume_name):
        flashcopy_process = self._get_flashcopy_process(flashcopy.id)
        if flashcopy.backgroundcopy == "disabled":
            raise array_errors.ObjectIsStillInUseError(id_or_name=volume_name,
                                                       used_by=[flashcopy.representation])
        if flashcopy_process.out_of_sync_tracks != '0':
            raise array_errors.ObjectIsStillInUseError(id_or_name=volume_name,
                                                       used_by=[flashcopy_process.representation])

    def _delete_object(self, object_id, object_is_snapshot=False):
        api_volume = self._get_api_volume_by_id(object_id)
        if object_is_snapshot and not is_snapshot(api_volume):
            raise array_errors.ObjectNotFoundError(name=object_id)
        flashcopies = api_volume.flashcopy
        flashcopies_as_source = [flashcopy for flashcopy in flashcopies
                                 if flashcopy.sourcevolume == api_volume.id]
        self._safe_delete_flashcopies(flashcopies=flashcopies_as_source, volume_name=api_volume.name)
        flashcopy_as_target = get_flashcopy_as_target_if_exists(api_volume=api_volume)
        if flashcopy_as_target:
            self._delete_flashcopy(flashcopy_id=flashcopy_as_target.id)
        self._delete_volume(object_id)
        self.volume_cache.remove(api_volume.name)

    @convert_scsi_ids_to_array_ids()
    def delete_volume(self, volume_id):
        logger.info("deleting volume with id : {0}".format(volume_id))
        self._delete_object(volume_id)
        logger.info("finished deleting volume {}".format(volume_id))

    def _get_api_volume_with_cache(self, name, pool_id):
        cached_volume_id = self.volume_cache.get(name)
        api_volume = None
        if cached_volume_id:
            logger.debug("found object id: {} in cache".format(cached_volume_id))
            api_volume = self._get_api_volume_by_id(volume_id=cached_volume_id)
        if not api_volume or api_volume.name != name:
            api_volume = self._get_api_volume_by_name(volume_name=name, pool_id=pool_id)
        return api_volume

    def get_volume(self, name, pool, is_virt_snap_func):
        logger.debug("getting volume {} in pool {}".format(name, pool))
        api_volume = self._get_api_volume_with_cache(name, pool)
        if api_volume:
            self.volume_cache.add_or_delete(api_volume.name, api_volume.id)
            return self._generate_volume_response(api_volume)
        raise array_errors.ObjectNotFoundError(name)

    @convert_scsi_ids_to_array_ids()
    def expand_volume(self, volume_id, required_bytes):
        logger.info("expanding volume with id : {0} to {1} bytes".format(volume_id, required_bytes))
        api_volume = self._get_api_volume_by_id(volume_id)
        flashcopies = api_volume.flashcopy
        self._safe_delete_flashcopies(flashcopies=flashcopies, volume_name=api_volume.name)

        self._extend_volume(api_volume=api_volume, new_size_in_bytes=required_bytes)
        logger.info("finished Expanding volume {0}.".format(volume_id))

    @convert_scsi_ids_to_array_ids()
    def get_volume_mappings(self, volume_id):
        logger.debug("getting volume mappings for volume {}".format(volume_id))
        try:
            host_name_to_lun_id = {}
            for host in self.client.get_hosts():
                host_mappings = host.mappings_briefs
                for mapping in host_mappings:
                    if volume_id == mapping["volume_id"]:
                        host_name_to_lun_id[host.name] = scsilun_to_int(mapping["lunid"])
                        break
            logger.debug("found volume mappings: {}".format(host_name_to_lun_id))
            return host_name_to_lun_id
        except exceptions.ClientException as ex:
            logger.error(
                "failed to get volume mappings. Reason is: {}".format(ex.details)
            )
            raise ex

    @convert_scsi_ids_to_array_ids()
    def map_volume(self, volume_id, host_name, connectivity_type):
        logger.debug("mapping volume {} to host {}".format(volume_id, host_name))
        try:
            mapping = self.client.map_volume_to_host(host_name, volume_id)
            lun = scsilun_to_int(mapping.lunid)
            logger.debug("successfully mapped volume to host with lun {}".format(lun))
            return lun
        except exceptions.NotFound:
            raise array_errors.HostNotFoundError(host_name)
        except exceptions.ClientException as ex:
            if ERROR_CODE_MAP_VOLUME_NOT_ENOUGH_EXTENTS in str(ex.message).upper():
                raise array_errors.NoAvailableLunError(volume_id)
            if ERROR_CODE_VOLUME_NOT_FOUND_FOR_MAPPING in str(ex.message).upper():
                raise array_errors.ObjectNotFoundError(volume_id)
            raise array_errors.MappingError(volume_id, host_name, ex.details)

    @convert_scsi_ids_to_array_ids()
    def unmap_volume(self, volume_id, host_name):
        logger.debug("unmapping volume {} from host {}".format(volume_id, host_name))
        try:
            mappings = self.client.get_host_mappings(host_name)
            lunid = None
            for mapping in mappings:
                if mapping.volume == volume_id:
                    lunid = mapping.id
                    break
            if lunid is not None:
                self.client.unmap_volume_from_host(
                    host_name=host_name,
                    lunid=lunid
                )
                logger.debug("successfully unmapped volume from host with lun {}.".format(lunid))
            else:
                raise array_errors.ObjectNotFoundError(volume_id)
        except exceptions.ClientException as ex:
            if HOST_DOES_NOT_EXIST in str(ex.message).upper():
                raise array_errors.HostNotFoundError(host_name)
            if MAPPING_DOES_NOT_EXIST in str(ex.message).upper():
                raise array_errors.VolumeAlreadyUnmappedError(volume_id)
            raise array_errors.UnmappingError(volume_id, host_name, ex.details)

    def _get_api_volume_from_volumes(self, volume_candidates, volume_name):
        for volume in volume_candidates:
            if volume.name == volume_name:
                logger.debug("found volume: {} with id: {}".format(volume.name, volume.id))
                volume.flashcopy = self.client.get_flashcopies_by_volume(volume.id)
                return volume
        return None

    def _get_api_volume_by_name(self, volume_name, pool_id):
        logger.info("getting volume {} in pool {}".format(volume_name, pool_id))
        if pool_id is None:
            logger.error(
                "pool_id is not specified, can not get volumes from storage."
            )
            raise array_errors.PoolParameterIsMissing(self.array_type)

        try:
            volume_candidates = []
            volume_candidates.extend(self.client.get_volumes_by_pool(pool_id))
        except exceptions.ClientException as ex:
            error_message = str(ex.message).upper()
            if ERROR_CODE_RESOURCE_NOT_EXISTS in error_message or INCORRECT_ID in error_message:
                raise array_errors.PoolDoesNotExist(pool_id, self.identifier)
            raise ex

        return self._get_api_volume_from_volumes(volume_candidates, volume_name)

    def _get_api_volume_by_id(self, volume_id, not_exist_err=True):
        try:
            volume = self.client.get_volume(volume_id)
            volume.flashcopy = self.client.get_flashcopies_by_volume(volume.id)
            return volume
        except exceptions.NotFound:
            if not_exist_err:
                raise array_errors.ObjectNotFoundError(volume_id)
        except (exceptions.ClientError, exceptions.InternalServerError) as ex:
            if INCORRECT_ID in str(ex.message).upper():
                raise array_errors.InvalidArgumentError(volume_id)
        return None

    def _get_flashcopy_process(self, flashcopy_id, not_exist_err=True):
        logger.info("getting flashcopy {}".format(flashcopy_id))
        try:
            return self.client.get_flashcopies(flashcopy_id)
        except exceptions.ClientException as ex:
            if ERROR_CODE_RESOURCE_NOT_EXISTS in str(ex.message).upper():
                logger.info("{} not found".format(flashcopy_id))
                if not_exist_err:
                    raise ex
            else:
                raise ex
        return None

    def _get_api_snapshot(self, snapshot_name, pool_id=None):
        logger.debug("get snapshot : {} in pool: {}".format(snapshot_name, pool_id))
        api_snapshot = self._get_api_volume_with_cache(snapshot_name, pool_id)
        if not api_snapshot:
            return None
        if not is_snapshot(api_snapshot):
            logger.error(
                "flashCopy relationship not found for target volume: {}".format(snapshot_name))
            raise array_errors.ExpectedSnapshotButFoundVolumeError(api_snapshot.name,
                                                                   self.service_address)
        return api_snapshot

    @convert_scsi_ids_to_array_ids()
    def get_snapshot(self, volume_id, snapshot_name, pool, is_virt_snap_func):
        if not pool:
            source_api_volume = self._get_api_volume_by_id(volume_id)
            pool = source_api_volume.pool
        api_snapshot = self._get_api_snapshot(snapshot_name, pool)
        if api_snapshot is None:
            return None
        self.volume_cache.add_or_delete(api_snapshot.name, api_snapshot.id)
        return self._generate_snapshot_response_with_verification(api_snapshot)

    def _create_similar_volume(self, target_volume_name, source_api_volume, space_efficiency, pool):
        logger.info(
            "creating target api volume '{0}' from source volume '{1}'".format(target_volume_name,
                                                                               source_api_volume.name))
        if space_efficiency:
            array_space_efficiency = get_array_space_efficiency(space_efficiency)
        else:
            array_space_efficiency = source_api_volume.tp
        size_in_bytes = int(source_api_volume.cap)
        if not pool:
            pool = source_api_volume.pool
        return self._create_api_volume(target_volume_name, size_in_bytes, array_space_efficiency, pool)

    def _create_flashcopy(self, source_volume_id, target_volume_id, options):
        logger.info(
            "creating FlashCopy relationship from '{0}' to '{1}'".format(source_volume_id,
                                                                         target_volume_id))
        options.append(FLASHCOPY_PERMIT_SPACE_EFFICIENT_TARGET_OPTION)
        try:
            api_flashcopy = self.client.create_flashcopy(source_volume_id=source_volume_id,
                                                         target_volume_id=target_volume_id,
                                                         options=options)
        except exceptions.ClientException as ex:
            if ERROR_CODE_ALREADY_FLASHCOPY in str(ex.message).upper():
                raise array_errors.SnapshotAlreadyExists(target_volume_id, self.service_address)
            if ERROR_CODE_VOLUME_NOT_FOUND_OR_ALREADY_PART_OF_CS_RELATIONSHIP in str(ex.message).upper():
                raise array_errors.ObjectNotFoundError('{} or {}'.format(source_volume_id, target_volume_id))
            raise ex
        flashcopy_state = self.get_flashcopy_state(api_flashcopy.id)
        if not flashcopy_state == FLASHCOPY_STATE_VALID:
            self._delete_flashcopy(api_flashcopy.id)
            raise ValueError("flashcopy state is not correct. expected: '{}' , got: '{}'.".format(FLASHCOPY_STATE_VALID,
                                                                                                  flashcopy_state))
        return self._get_api_volume_by_id(target_volume_id)

    @retry(Exception, tries=11, delay=1)
    def _delete_target_volume_if_exist(self, target_volume_id):
        self._delete_volume(target_volume_id, not_exist_err=False)

    def _create_snapshot(self, target_volume_name, source_api_volume, space_efficiency, pool):
        target_api_volume = self._create_similar_volume(target_volume_name, source_api_volume, space_efficiency, pool)
        options = [FLASHCOPY_NO_BACKGROUND_COPY_OPTION, FLASHCOPY_PERSISTENT_OPTION]
        try:
            return self._create_flashcopy(source_api_volume.id, target_api_volume.id, options)
        except (array_errors.ObjectNotFoundError, array_errors.SnapshotAlreadyExists) as ex:
            logger.error("failed to create snapshot '{0}': {1}".format(target_volume_name, ex))
            self._delete_target_volume_if_exist(target_api_volume.id)
            raise ex

    def _generate_snapshot_response_with_verification(self, api_object):
        flashcopy_as_target = get_flashcopy_as_target_if_exists(api_object)
        if flashcopy_as_target is None or flashcopy_as_target.backgroundcopy != "disabled":
            raise array_errors.ExpectedSnapshotButFoundVolumeError(api_object.name, self.service_address)
        return self._generate_snapshot_response(api_object, flashcopy_as_target.sourcevolume)

    @convert_scsi_ids_to_array_ids()
    def get_object_by_id(self, object_id, object_type):
        api_object = self._get_api_volume_by_id(object_id, not_exist_err=False)
        if not api_object:
            return None
        if object_type is controller_config.SNAPSHOT_TYPE_NAME:
            return self._generate_snapshot_response_with_verification(api_object)
        return self._generate_volume_response(api_object)

    @convert_scsi_ids_to_array_ids()
    def create_snapshot(self, volume_id, snapshot_name, space_efficiency, pool, is_virt_snap_func):
        logger.info("creating snapshot '{0}' from volume '{1}'".format(snapshot_name, volume_id))
        source_api_volume = self._get_api_volume_by_id(volume_id)
        if source_api_volume is None:
            raise array_errors.ObjectNotFoundError(volume_id)
        target_api_volume = self._create_snapshot(snapshot_name, source_api_volume, space_efficiency, pool)
        logger.info("finished creating snapshot '{0}' from volume '{1}'".format(snapshot_name, volume_id))
        self.volume_cache.add(target_api_volume.name, target_api_volume.id)
        return self._generate_snapshot_response(target_api_volume, volume_id)

    def _delete_flashcopy(self, flashcopy_id):
        try:
            self.client.delete_flashcopy(flashcopy_id)
        except exceptions.ClientException as ex:
            logger.error(
                "failed to delete flashcopy {} on array {}, reason is: {}".format(
                    flashcopy_id,
                    self.identifier,
                    ex.details
                )
            )
            raise ex

    @convert_scsi_ids_to_array_ids()
    def delete_snapshot(self, snapshot_id, internal_snapshot_id):
        logger.info("deleting snapshot with id : {0}".format(snapshot_id))
        self._delete_object(snapshot_id, object_is_snapshot=True)
        logger.info("finished snapshot deletion. id : {0}".format(snapshot_id))

    def get_iscsi_targets_by_iqn(self, host_name):
        return {}

    def get_array_fc_wwns(self, host_name):
        logger.debug("getting the connected fc port wwpns for host {} from array".format(host_name))
        api_host = self._get_api_host(host_name)
        wwpns = [port[LOGIN_PORT_WWPN] for port in api_host.login_ports if
                 port[LOGIN_PORT_STATE] == LOGIN_PORT_STATE_ONLINE]
        logger.debug("found wwpns: {}".format(wwpns))
        return wwpns

    def _get_api_host(self, host_name):
        try:
            return self.client.get_host(host_name)
        except exceptions.NotFound:
            raise array_errors.HostNotFoundError(host_name)
        except exceptions.ClientException as ex:
            raise ex

    def _get_fc_wwns_from_api_host(self, api_host):
        host_ports = api_host.host_ports_briefs
        return [p["wwpn"] for p in host_ports]

    def get_host_by_name(self, host_name):
        api_host = self._get_api_host(host_name)
        fc_wwns = self._get_fc_wwns_from_api_host(api_host)
        connectivity_types = []
        if fc_wwns:
            connectivity_types.append(config.FC_CONNECTIVITY_TYPE)
        return Host(name=api_host.name, connectivity_types=connectivity_types, fc_wwns=fc_wwns)

    def get_host_by_host_identifiers(self, initiators):
        logger.debug("getting host by initiators: {}".format(initiators))
        found = ""
        for host in self.client.get_hosts():
            wwpns = self._get_fc_wwns_from_api_host(host)
            if initiators.is_array_wwns_match(wwpns):
                found = host.name
                break
        if found:
            logger.debug("found host {0} with fc wwpns: {1}".format(found, initiators.fc_wwns))
            return found, [config.FC_CONNECTIVITY_TYPE]
        logger.debug("can not found host by initiators: {0} ".format(initiators))
        raise array_errors.HostNotFoundError(initiators)

    def validate_supported_space_efficiency(self, space_efficiency):
        logger.debug("validate_supported_space_efficiency for space efficiency : {0}".format(space_efficiency))

        if (space_efficiency and space_efficiency.lower() not in
                [config.SPACE_EFFICIENCY_THIN, config.SPACE_EFFICIENCY_NONE]):
            logger.error("space efficiency is not supported.")
            raise array_errors.SpaceEfficiencyNotSupported(
                space_efficiency)

        logger.debug("finished validate_supported_space_efficiency.")

    def _generate_snapshot_response(self, api_snapshot, source_id):
        return Snapshot(capacity_bytes=int(api_snapshot.cap),
                        id=self._generate_volume_scsi_identifier(api_snapshot.id),
                        internal_id=api_snapshot.id,
                        name=api_snapshot.name,
                        array_address=self.service_address,
                        source_id=self._generate_volume_scsi_identifier(source_id),
                        is_ready=True,
                        array_type=self.array_type)

    def get_flashcopy_state(self, flashcopy_id):
        flashcopy_process = self._get_flashcopy_process(flashcopy_id)
        return flashcopy_process.state

    def get_replication(self, volume_internal_id, other_volume_internal_id, other_system_id):
        raise NotImplementedError

    def create_replication(self, volume_internal_id, other_volume_internal_id, other_system_id, copy_type):
        raise NotImplementedError

    def delete_replication(self, replication_name):
        raise NotImplementedError

    def promote_replication_volume(self, replication_name):
        raise NotImplementedError

    def demote_replication_volume(self, replication_name):
        raise NotImplementedError

    def validate_space_efficiency_matches_source(self, space_efficiency, source_id, source_type):
        raise NotImplementedError
