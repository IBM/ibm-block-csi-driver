from decorator import decorator
from packaging.version import parse
from pyds8k import exceptions
from pyds8k.resources.ds8k.v1.common import attr_names
from pyds8k.resources.ds8k.v1.common import types as ds8k_types
from retry import retry

import controller.array_action.errors as array_errors
import controller.controller_server.config as controller_config
from controller.array_action import config
from controller.array_action.array_action_types import Volume, Snapshot
from controller.array_action.array_mediator_abstract import ArrayMediatorAbstract
from controller.array_action.ds8k_rest_client import RESTClient, scsilun_to_int
from controller.array_action.utils import classproperty
from controller.common import settings
from controller.common.csi_logger import get_stdout_logger

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


def try_convert_first_arg(converter, args):
    if args:
        converted = converter(args[0])
        return (converted,) + args[1:]
    return ()


def is_snapshot(api_volume):
    flashcopies = api_volume.flashcopy
    for flashcopy in flashcopies:
        if flashcopy.targetvolume == api_volume.id and flashcopy.backgroundcopy == "disabled":
            return True
    return False


@decorator
def convert_scsi_id_to_array_id(mediator_method, self, *args):
    args = try_convert_first_arg(scsi_id_to_volume_id, args)
    return mediator_method(self, *args)


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


class DS8KArrayMediator(ArrayMediatorAbstract):
    SUPPORTED_FROM_VERSION = '7.5.1'

    @classproperty
    def array_type(self):
        return settings.ARRAY_TYPE_DS8K

    @classproperty
    def port(self):
        return 8452

    @classproperty
    def max_object_name_length(self):
        return 16

    @classproperty
    def max_object_prefix_length(self):
        return 5

    @classproperty
    def max_connections(self):
        # max for rest api is 128.
        return 50

    @classproperty
    def minimal_volume_size_in_bytes(self):
        return 512  # 1 block, 512 bytes

    @classproperty
    def maximal_volume_size_in_bytes(self):
        return 16 * 1024 * 1024 * 1024 * 1024

    @classproperty
    def max_lun_retries(self):
        return 10

    @classproperty
    def default_object_prefix(self):
        return None

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
        except (exceptions.ClientError, exceptions.Unauthorized) as e:
            # BE7A002D=Authentication has failed because the user name and
            # password combination that you have entered is not valid.
            if ERROR_CODE_INVALID_CREDENTIALS or KNOWN_ERROR_CODE_INVALID_CREDENTIALS in str(
                    e.message).upper():
                raise array_errors.CredentialsError(self.service_address)
            raise ConnectionError()
        except exceptions.ClientException as e:
            logger.error(
                'Failed to connect to DS8K array {}, reason is {}'.format(
                    self.service_address,
                    e.details
                )
            )
            raise ConnectionError()

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

    def _get_copy_source_id(self, api_volume):
        copy_source_id = None
        flashcopy_as_target = get_flashcopy_as_target_if_exists(api_volume=api_volume)
        if flashcopy_as_target:
            source_volume_id = flashcopy_as_target.sourcevolume
            copy_source_id = self._generate_volume_scsi_identifier(volume_id=source_volume_id)
        return copy_source_id

    def _generate_volume_response(self, api_volume):

        return Volume(
            vol_size_bytes=int(api_volume.cap),
            vol_id=self._generate_volume_scsi_identifier(volume_id=api_volume.id),
            vol_name=api_volume.name,
            array_address=self.service_address,
            copy_source_id=self._get_copy_source_id(api_volume=api_volume),
            pool_name=api_volume.pool,
            array_type=self.array_type
        )

    def _create_api_volume(self, name, size_in_bytes, space_efficiency, pool_id):
        logger.info("Creating volume with name: {}, size: {}, in pool: {}, with parameters: {}".format(
            name, size_in_bytes, pool_id, space_efficiency))
        try:
            cli_kwargs = {}
            cli_kwargs.update({
                'name': name,
                'capacity_in_bytes': size_in_bytes,
                'pool_id': pool_id,
                'tp': get_array_space_efficiency(space_efficiency),

            })
            logger.debug(
                "Start to create volume with parameters: {}".format(cli_kwargs)
            )

            # get the volume before creating again, to make sure it is not existing,
            # because volume name is not unique in ds8k.
            api_volume = self._get_api_volume_by_name(
                name,
                pool_id=pool_id
            )
            logger.info("Found volume {}".format(name))
            if api_volume is not None:
                raise array_errors.VolumeAlreadyExists(name, self.identifier)
            api_volume = self.client.create_volume(**cli_kwargs)

            logger.info("finished creating volume {}".format(name))
            return self.client.get_volume(api_volume.id)
        except (exceptions.NotFound, exceptions.InternalServerError) as ex:
            if ERROR_CODE_RESOURCE_NOT_EXISTS or INCORRECT_ID in str(ex.message).upper():
                raise array_errors.PoolDoesNotExist(pool_id, self.identifier)
            logger.error(
                "Failed to create volume {} on array {}, reason is: {}".format(
                    name,
                    self.identifier,
                    ex.details
                )
            )
            raise array_errors.VolumeCreationError(name)
        except (exceptions.ClientError, exceptions.ClientException) as ex:
            if ERROR_CODE_CREATE_VOLUME_NOT_ENOUGH_EXTENTS in str(ex.message).upper():
                raise array_errors.NotEnoughSpaceInPool(id_or_name=pool_id)
            logger.error(
                "Failed to create volume {} on array {}, reason is: {}".format(
                    name,
                    self.identifier,
                    ex.details
                )
            )
            raise array_errors.VolumeCreationError(name)

    def create_volume(self, volume_name, size_in_bytes, space_efficiency, pool):
        api_volume = self._create_api_volume(volume_name, size_in_bytes, space_efficiency, pool)
        return self._generate_volume_response(api_volume)

    def _extend_volume(self, api_volume, new_size_in_bytes):
        try:
            self.client.extend_volume(volume_id=api_volume.id,
                                      new_size_in_bytes=new_size_in_bytes)
        except exceptions.NotFound:
            raise array_errors.ObjectNotFoundError(api_volume.id)
        except (exceptions.ClientError, exceptions.ClientException) as ex:
            if ERROR_CODE_EXPAND_VOLUME_NOT_ENOUGH_EXTENTS in str(ex.message).upper():
                raise array_errors.NotEnoughSpaceInPool(api_volume.pool)

    def copy_to_existing_volume_from_source(self, name, source_name, source_capacity_in_bytes,
                                            minimum_volume_size_in_bytes, pool_id=None):
        logger.debug(
            "Copy source {0} data to volume {1}. source capacity {2}. Minimal requested volume capacity {3}".format(
                name, source_name, source_capacity_in_bytes,
                minimum_volume_size_in_bytes))
        api_new_volume = self._get_api_volume_by_name(name, pool_id=pool_id)
        api_source_object = self._get_api_volume_by_name(source_name, pool_id=pool_id)
        if minimum_volume_size_in_bytes < source_capacity_in_bytes:
            self._extend_volume(api_volume=api_new_volume,
                                new_size_in_bytes=source_capacity_in_bytes)
        options = [FLASHCOPY_PERSISTENT_OPTION]
        self._create_flashcopy(source_volume_id=api_source_object.id, target_volume_id=api_new_volume.id,
                               options=options)

    def _delete_volume(self, volume_id, not_exist_err=True):
        logger.info("Deleting volume {}".format(volume_id))
        try:
            self.client.delete_volume(
                volume_id=volume_id
            )
            logger.info("Finished deleting volume {}".format(volume_id))
        except exceptions.NotFound:
            if not_exist_err:
                raise array_errors.ObjectNotFoundError(volume_id)
        except exceptions.ClientException as ex:
            logger.error(
                "Failed to delete volume {} on array {}, reason is: {}".format(
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

    @convert_scsi_id_to_array_id
    def delete_volume(self, volume_id):
        logger.info("Deleting volume with id : {0}".format(volume_id))
        self._delete_object(volume_id)
        logger.info("Finished deleting volume {}".format(volume_id))

    def get_volume(self, name, pool_id=None):
        logger.debug("Getting volume {} in pool {}".format(name, pool_id))
        api_volume = self._get_api_volume_by_name(volume_name=name,
                                                  pool_id=pool_id)
        if api_volume:
            return self._generate_volume_response(api_volume)
        raise array_errors.ObjectNotFoundError(name)

    @convert_scsi_id_to_array_id
    def get_volume_name(self, volume_id):
        logger.debug("Searching for volume with id: {0}".format(volume_id))
        try:
            api_volume = self.client.get_volume(volume_id)
        except (exceptions.NotFound, exceptions.InternalServerError) as ex:
            uppercase_message = str(ex.message).upper()
            if ERROR_CODE_RESOURCE_NOT_EXISTS in uppercase_message:
                raise array_errors.ObjectNotFoundError(volume_id)
            if INCORRECT_ID in uppercase_message:
                raise array_errors.IllegalObjectID(volume_id)
            raise ex

        vol_name = api_volume.name
        logger.debug("found volume name : {0}".format(vol_name))
        return vol_name

    @convert_scsi_id_to_array_id
    def expand_volume(self, volume_id, required_bytes):
        logger.info("Expanding volume with id : {0} to {1} bytes".format(volume_id, required_bytes))
        api_volume = self._get_api_volume_by_id(volume_id)
        flashcopies = api_volume.flashcopy
        self._safe_delete_flashcopies(flashcopies=flashcopies, volume_name=api_volume.name)

        self._extend_volume(api_volume=api_volume, new_size_in_bytes=required_bytes)
        logger.info("Finished Expanding volume {0}.".format(volume_id))

    @convert_scsi_id_to_array_id
    def get_volume_mappings(self, volume_id):
        logger.debug("Getting volume mappings for volume {}".format(volume_id))
        try:
            host_name_to_lun_id = {}
            for host in self.client.get_hosts():
                host_mappings = host.mappings_briefs
                for mapping in host_mappings:
                    if volume_id == mapping["volume_id"]:
                        host_name_to_lun_id[host.name] = scsilun_to_int(mapping["lunid"])
                        break
            logger.debug("Found volume mappings: {}".format(host_name_to_lun_id))
            return host_name_to_lun_id
        except exceptions.ClientException as ex:
            logger.error(
                "Failed to get volume mappings. Reason is: {}".format(ex.details)
            )
            raise ex

    @convert_scsi_id_to_array_id
    def map_volume(self, volume_id, host_name):
        logger.debug("Mapping volume {} to host {}".format(volume_id, host_name))
        try:
            mapping = self.client.map_volume_to_host(host_name, volume_id)
            lun = scsilun_to_int(mapping.lunid)
            logger.debug("Successfully mapped volume to host with lun {}".format(lun))
            return lun
        except exceptions.NotFound:
            raise array_errors.HostNotFoundError(host_name)
        except exceptions.ClientException as ex:
            # [BE586015] addLunMappings Volume group operation failure: volume does not exist.
            if ERROR_CODE_VOLUME_NOT_FOUND_FOR_MAPPING in str(ex.message).upper():
                raise array_errors.ObjectNotFoundError(volume_id)
            raise array_errors.MappingError(volume_id, host_name, ex.details)

    @convert_scsi_id_to_array_id
    def unmap_volume(self, volume_id, host_name):
        logger.debug("Unmapping volume {} from host {}".format(volume_id, host_name))
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
                logger.debug("Successfully unmapped volume from host with lun {}.".format(lunid))
            else:
                raise array_errors.ObjectNotFoundError(volume_id)
        except exceptions.NotFound as ex:
            if HOST_DOES_NOT_EXIST in str(ex.message).upper():
                raise array_errors.HostNotFoundError(host_name)
            if MAPPING_DOES_NOT_EXIST in str(ex.message).upper():
                raise array_errors.VolumeAlreadyUnmappedError(volume_id)
        except exceptions.ClientException as ex:
            raise array_errors.UnmappingError(volume_id, host_name, ex.details)

    def _get_api_volume_from_volumes(self, volume_candidates, volume_name):
        for volume in volume_candidates:
            logger.info("Checking volume: {}".format(volume.name))
            if volume.name == volume_name:
                logger.debug("Found volume: {}".format(volume))
                volume.flashcopy = self.client.get_flashcopies_by_volume(volume.id)
                return volume
        return None

    def _get_api_volume_by_name(self, volume_name, pool_id):
        logger.info("Getting volume {} in pool {}".format(volume_name, pool_id))
        if pool_id is None:
            logger.error(
                "pool_id is not specified, can not get volumes from storage."
            )
            raise array_errors.PoolParameterIsMissing(self.array_type)

        try:
            volume_candidates = []
            volume_candidates.extend(self.client.get_volumes_by_pool(pool_id))
        except (exceptions.NotFound, exceptions.InternalServerError) as ex:
            if ERROR_CODE_RESOURCE_NOT_EXISTS or INCORRECT_ID in str(ex.message).upper():
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
                raise array_errors.IllegalObjectID(volume_id)

    def _get_flashcopy_process(self, flashcopy_id, not_exist_err=True):
        logger.info("Getting flashcopy {}".format(flashcopy_id))
        try:
            return self.client.get_flashcopies(flashcopy_id)
        except exceptions.NotFound as ex:
            if ERROR_CODE_RESOURCE_NOT_EXISTS in str(ex.message).upper():
                logger.info("{} not found".format(flashcopy_id))
                if not_exist_err:
                    raise ex
        except Exception as ex:
            logger.exception(ex)
            raise ex

    def _get_api_snapshot(self, snapshot_name, pool_id=None):
        logger.debug("Get snapshot : {} in pool: {}".format(snapshot_name, pool_id))
        api_snapshot = self._get_api_volume_by_name(volume_name=snapshot_name,
                                                    pool_id=pool_id)
        if not api_snapshot:
            return None
        if not is_snapshot(api_snapshot):
            logger.error(
                "FlashCopy relationship not found for target volume: {}".format(snapshot_name))
            raise array_errors.ExpectedSnapshotButFoundVolumeError(api_snapshot.name,
                                                                   self.service_address)
        return api_snapshot

    def get_snapshot(self, snapshot_name, pool_id=None):
        api_snapshot = self._get_api_snapshot(snapshot_name, pool_id)
        if api_snapshot is None:
            return None
        return self._generate_snapshot_response_with_verification(api_snapshot)

    def _create_similar_volume(self, target_volume_name, source_api_volume):
        logger.info(
            "creating target api volume '{0}' from source volume '{1}'".format(target_volume_name,
                                                                               source_api_volume.name))
        space_efficiency = source_api_volume.tp
        size_in_bytes = int(source_api_volume.cap)
        pool = source_api_volume.pool
        return self._create_api_volume(target_volume_name, size_in_bytes, space_efficiency, pool)

    def _create_flashcopy(self, source_volume_id, target_volume_id, options):
        logger.info(
            "creating FlashCopy relationship from '{0}' to '{1}'".format(source_volume_id,
                                                                         target_volume_id))
        options.append(FLASHCOPY_PERMIT_SPACE_EFFICIENT_TARGET_OPTION)
        try:
            api_flashcopy = self.client.create_flashcopy(source_volume_id=source_volume_id,
                                                         target_volume_id=target_volume_id,
                                                         options=options)
        except (exceptions.ClientError, exceptions.ClientException) as ex:
            if ERROR_CODE_ALREADY_FLASHCOPY in str(ex.message).upper():
                raise array_errors.SnapshotAlreadyExists(target_volume_id,
                                                         self.service_address)
            if ERROR_CODE_VOLUME_NOT_FOUND_OR_ALREADY_PART_OF_CS_RELATIONSHIP in str(
                    ex.message).upper():
                raise array_errors.ObjectNotFoundError('{} or {}'.format(source_volume_id,
                                                                         target_volume_id))
            raise ex
        flashcopy_state = self.get_flashcopy_state(api_flashcopy.id)
        if not flashcopy_state == FLASHCOPY_STATE_VALID:
            self._delete_flashcopy(api_flashcopy.id)
            raise ValueError("Flashcopy state is not correct. expected: '{}' , got: '{}'.".format(FLASHCOPY_STATE_VALID,
                                                                                                  flashcopy_state))
        return self._get_api_volume_by_id(target_volume_id)

    @retry(Exception, tries=11, delay=1)
    def _delete_target_volume_if_exist(self, target_volume_id):
        self._delete_volume(target_volume_id, not_exist_err=False)

    def _create_snapshot(self, target_volume_name, pool_id, source_volume_name):
        source_volume = self._get_api_volume_by_name(source_volume_name, pool_id=pool_id)
        if source_volume is None:
            raise array_errors.ObjectNotFoundError(source_volume_name)
        target_api_volume = self._create_similar_volume(target_volume_name, source_volume)
        options = [FLASHCOPY_NO_BACKGROUND_COPY_OPTION, FLASHCOPY_PERSISTENT_OPTION]
        try:
            return self._create_flashcopy(source_volume.id, target_api_volume.id, options)
        except (array_errors.ObjectNotFoundError, array_errors.SnapshotAlreadyExists) as ex:
            logger.error("Failed to create snapshot '{0}': {1}".format(target_volume_name, ex))
            self._delete_target_volume_if_exist(target_api_volume.id)
            raise ex

    def _generate_snapshot_response_with_verification(self, api_object):
        flashcopy_as_target = get_flashcopy_as_target_if_exists(api_object)
        if flashcopy_as_target is None or flashcopy_as_target.backgroundcopy != "disabled":
            raise array_errors.ExpectedSnapshotButFoundVolumeError(api_object.name, self.service_address)
        source_volume_name = self.get_volume_name(flashcopy_as_target.sourcevolume)
        return self._generate_snapshot_response(api_object, source_volume_name)

    @convert_scsi_id_to_array_id
    def get_object_by_id(self, object_id, object_type):
        api_object = self._get_api_volume_by_id(object_id, not_exist_err=False)
        if not api_object:
            return None
        if object_type is controller_config.SNAPSHOT_TYPE_NAME:
            return self._generate_snapshot_response_with_verification(api_object)
        return self._generate_volume_response(api_object)

    def create_snapshot(self, name, volume_name, pool_id=None):
        logger.info("creating snapshot '{0}' from volume '{1}'".format(name, volume_name))
        target_api_volume = self._create_snapshot(name, pool_id, source_volume_name=volume_name)
        logger.info("finished creating snapshot '{0}' from volume '{1}'".format(name, volume_name))
        return self._generate_snapshot_response(target_api_volume, volume_name)

    def _delete_flashcopy(self, flashcopy_id):
        try:
            self.client.delete_flashcopy(flashcopy_id)
        except exceptions.ClientException as ex:
            logger.error(
                "Failed to delete flashcopy {} on array {}, reason is: {}".format(
                    flashcopy_id,
                    self.identifier,
                    ex.details
                )
            )
            raise ex

    @convert_scsi_id_to_array_id
    def delete_snapshot(self, snapshot_id):
        logger.info("Deleting snapshot with id : {0}".format(snapshot_id))
        self._delete_object(snapshot_id, object_is_snapshot=True)
        logger.info("Finished snapshot deletion. id : {0}".format(snapshot_id))

    def get_iscsi_targets_by_iqn(self):
        return {}

    def get_array_fc_wwns(self, host_name=None):
        logger.debug("Getting the connected fc port wwpns for host {} from array".format(host_name))

        try:
            host = self.client.get_host(host_name)
            wwpns = [port[LOGIN_PORT_WWPN] for port in host.login_ports if
                     port[LOGIN_PORT_STATE] == LOGIN_PORT_STATE_ONLINE]
            logger.debug("Found wwpns: {}".format(wwpns))
            return wwpns
        except exceptions.NotFound:
            raise array_errors.HostNotFoundError(host_name)
        except exceptions.ClientException as ex:
            logger.error(
                "Failed to get array fc wwpn. Reason is: {}".format(ex.details)
            )
            raise ex

    def get_host_by_host_identifiers(self, initiators):
        logger.debug("Getting host by initiators: {}".format(initiators))
        found = ""
        for host in self.client.get_hosts():
            host_ports = host.host_ports_briefs
            wwpns = [p["wwpn"] for p in host_ports]
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

        logger.debug("Finished validate_supported_space_efficiency.")

    def _generate_snapshot_response(self, api_snapshot, source_volume_name):
        return Snapshot(capacity_bytes=int(api_snapshot.cap),
                        snapshot_id=self._generate_volume_scsi_identifier(api_snapshot.id),
                        snapshot_name=api_snapshot.name,
                        array_address=self.service_address,
                        volume_name=source_volume_name,
                        is_ready=True,
                        array_type=self.array_type)

    def get_flashcopy_state(self, flashcopy_id):
        flashcopy_process = self._get_flashcopy_process(flashcopy_id)
        return flashcopy_process.state
