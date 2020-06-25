import base58
from hashlib import sha1
from munch import Munch
from packaging.version import parse
from pyds8k import exceptions
from pyds8k.resources.ds8k.v1.common import attr_names
from retry import retry

import controller.array_action.errors as array_errors
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
ERROR_CODE_VOLUME_NOT_FOUND_FOR_MAPPING = 'BE586015'
ERROR_CODE_ALREADY_FLASHCOPY = '000000AE'
ERROR_CODE_VOLUME_NOT_FOUND_OR_ALREADY_PART_OF_CS_RELATIONSHIP = '00000013'
MAX_VOLUME_LENGTH = 16

FLASHCOPY_PERSISTENT_OPTION = "persistent"
FLASHCOPY_NO_BACKGROUND_COPY_OPTION = "no_background_copy"
FLASHCOPY_PERMIT_SPACE_EFFICIENT_TARGET = "permit_space_efficient_target"


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


def get_source_volume_id_if_exists(api_volume):
    flashcopy_rel_sources = [flashcopy.sourcevolume for flashcopy in api_volume.flashcopy
                             if flashcopy.targetvolume == api_volume.id]
    if len(flashcopy_rel_sources) != 1:
        return None
    return flashcopy_rel_sources[0]


def is_flashcopy_source(volume_id, volume_flashcopy):
    array_volume_id = get_volume_id_from_scsi_identifier(volume_id)
    return volume_flashcopy.sourcevolume == array_volume_id


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
        name_without_prefix = str(name).split(prefix + settings.NAME_PREFIX_SEPARATOR, 2)[1]
        hashed = hash_string(name_without_prefix)
        return (prefix + settings.NAME_PREFIX_SEPARATOR + hashed)[:MAX_VOLUME_LENGTH]


class DS8KArrayMediator(ArrayMediatorAbstract):
    SUPPORTED_FROM_VERSION = '7.5.1'

    @classproperty
    def array_type(self):
        return settings.ARRAY_TYPE_DS8K

    @classproperty
    def port(self):
        return 8452

    @classproperty
    def max_volume_name_length(self):
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
    def max_snapshot_name_length(self):
        return self.max_volume_name_length

    @classproperty
    def max_snapshot_prefix_length(self):
        return self.max_volume_prefix_length

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
        except (exceptions.ClientError, exceptions.Unauthorized) as e:
            # BE7A002D=Authentication has failed because the user name and
            # password combination that you have entered is not valid.
            if ERROR_CODE_INVALID_CREDENTIALS or KNOWN_ERROR_CODE_INVALID_CREDENTIALS in str(
                    e.message).upper():
                raise array_errors.CredentialsError(self.service_address)
            else:
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

    def _generate_volume_response(self, api_volume):
        source_volume_id = get_source_volume_id_if_exists(api_volume)
        return Volume(
            vol_size_bytes=int(api_volume.cap),
            vol_id=self._generate_volume_scsi_identifier(api_volume.id),
            vol_name=api_volume.name,
            array_address=self.service_address,
            copy_src_object_id=source_volume_id,
            pool_name=api_volume.pool,
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
                volume = self.get_volume(
                    name,
                    volume_context={config.CONTEXT_POOL: pool_id},
                    volume_prefix=volume_prefix
                )
                logger.info("Found volume {}".format(name))
                return volume
            except array_errors.VolumeNotFoundError:
                volume = self.client.create_volume(**cli_kwargs)

                logger.info("finished creating volume {}".format(name))
                return self._generate_volume_response(self.client.get_volume(volume.id))
        except (exceptions.NotFound, exceptions.InternalServerError) as ex:
            if ERROR_CODE_RESOURCE_NOT_EXISTS or INCORRECT_ID in str(ex.message).upper():
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

    def _extend_volume(self, volume_id, new_size_in_bytes):
        try:
            self.client.extend_volume(volume_id=volume_id,
                                      new_size_in_bytes=new_size_in_bytes)
        except exceptions.NotFound:
            raise array_errors.VolumeNotFoundError(volume_id)

    def copy_to_existing_volume_from_snapshot(self, name, src_snap_name, src_snap_capacity_in_bytes,
                                              min_vol_size_in_bytes, pool=None):
        logger.debug(
            "Copy snapshot {0} data to volume {1}. Snapshot capacity {2}. Minimal requested volume capacity {3}".format(
                name, src_snap_name, src_snap_capacity_in_bytes, min_vol_size_in_bytes))
        api_new_volume = self._get_api_volume_by_name(name, pool_id=pool)
        api_snapshot = self.get_snapshot(src_snap_name, volume_context={config.CONTEXT_POOL: pool})
        if min_vol_size_in_bytes < src_snap_capacity_in_bytes:
            self._extend_volume(volume_id=api_new_volume.id,
                                new_size_in_bytes=src_snap_capacity_in_bytes)
        options = [FLASHCOPY_PERSISTENT_OPTION]
        self._create_flashcopy(source_volume_id=api_snapshot.id, target_volume_id=api_new_volume.id,
                               options=options)

    def _delete_volume(self, volume_id, not_exist_err=True):
        logger.info("Deleting volume {}".format(volume_id))
        try:
            self.client.delete_volume(
                volume_id=get_volume_id_from_scsi_identifier(volume_id)
            )
            logger.info("Finished deleting volume {}".format(volume_id))
        except exceptions.NotFound:
            if not_exist_err:
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

    def delete_volume(self, volume_id):
        logger.info("Deleting volume with id : {0}".format(volume_id))
        volume_id = get_volume_id_from_scsi_identifier(volume_id)
        api_volume = self._get_api_volume_by_id(volume_id)
        for flashcopy in api_volume.flashcopy:
            self._delete_flashcopy(flashcopy.id)
        self._delete_volume(volume_id)
        logger.info("Finished deleting volume {}".format(volume_id))

    def get_volume(self, name, volume_context=None, volume_prefix=""):
        logger.debug("Getting volume {} under context {}".format(name, volume_context))
        if not volume_context:
            logger.error(
                "volume_context is not specified, can not get volumes from storage."
            )
            raise array_errors.VolumeNotFoundError(name)

        api_volume = self._get_api_volume_by_name(volume_name=name,
                                                  pool_id=volume_context[config.CONTEXT_POOL])

        if api_volume:
            return self._generate_volume_response(api_volume)

        raise array_errors.VolumeNotFoundError(name)

    def get_volume_name(self, volume_id):
        logger.debug("Searching for volume with id: {0}".format(volume_id))
        volume_id = get_volume_id_from_scsi_identifier(volume_id)
        try:
            api_volume = self.client.get_volume(volume_id)
        except exceptions.NotFound:
            raise array_errors.VolumeNotFoundError(volume_id)

        vol_name = api_volume.name
        logger.debug("found volume name : {0}".format(vol_name))
        return vol_name

    def is_volume_has_snapshots(self, volume_id):
        array_volume_id = get_volume_id_from_scsi_identifier(volume_id)
        array_volume = self._get_api_volume_by_id(array_volume_id)
        flash_copies = array_volume.flashcopy
        for flashcopy in flash_copies:
            if flashcopy.sourcevolume == array_volume_id:
                return True
        return False

    def get_volume_mappings(self, volume_id):
        logger.debug("Getting volume mappings for volume {}".format(volume_id))
        volume_id = get_volume_id_from_scsi_identifier(volume_id)
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

    def map_volume(self, volume_id, host_name):
        logger.debug("Mapping volume {} to host {}".format(volume_id, host_name))
        array_volume_id = get_volume_id_from_scsi_identifier(volume_id)
        try:
            mapping = self.client.map_volume_to_host(host_name, array_volume_id)
            lun = scsilun_to_int(mapping.lunid)
            logger.debug("Successfully mapped volume to host with lun {}".format(lun))
            return lun
        except exceptions.NotFound:
            raise array_errors.HostNotFoundError(host_name)
        except exceptions.ClientException as ex:
            # [BE586015] addLunMappings Volume group operation failure: volume does not exist.
            if ERROR_CODE_VOLUME_NOT_FOUND_FOR_MAPPING in str(ex.message).upper():
                raise array_errors.VolumeNotFoundError(volume_id)
            else:
                raise array_errors.MappingError(volume_id, host_name, ex.details)

    def unmap_volume(self, volume_id, host_name):
        logger.debug("Unmapping volume {} from host {}".format(volume_id, host_name))
        array_volume_id = get_volume_id_from_scsi_identifier(volume_id)
        try:
            mappings = self.client.get_host_mappings(host_name)
            lunid = None
            for mapping in mappings:
                if mapping.volume == array_volume_id:
                    lunid = mapping.id
                    break
            if lunid is not None:
                self.client.unmap_volume_from_host(
                    host_name=host_name,
                    lunid=lunid
                )
                logger.debug("Successfully unmapped volume from host with lun {}.".format(lunid))
            else:
                raise array_errors.VolumeNotFoundError(volume_id)
        except exceptions.NotFound:
            raise array_errors.HostNotFoundError(host_name)
        except exceptions.ClientException as ex:
            raise array_errors.UnMappingError(volume_id, host_name, ex.details)

    def _get_pools(self):
        logger.info("Getting pools")
        try:
            pools = self.client.get_pools()
        except exceptions.ClientException as ex:
            logger.error(
                "Failed to get pools, reason is: {}".format(
                    ex.details
                )
            )
            raise ex
        logger.info("Pools found: {}".format(pools))
        return pools

    def get_flashcopies_by_volume(self, volume_id):
        try:
            return self.client.get_flashcopies_by_volume(volume_id)
        except exceptions.NotFound:
            raise array_errors.VolumeNotFoundError(volume_id)

    def _get_api_volume_by_name(self, volume_name, pool_id):
        logger.info("Getting volume {} in pool {}".format(volume_name, pool_id))
        if not pool_id:
            pools = self._get_pools()
        else:
            pools = [Munch({"id": pool_id})]

        for pool in pools:
            volume_candidates = []
            try:
                volume_candidates.extend(self.client.get_volumes_by_pool(pool.id))
            except (exceptions.NotFound, exceptions.InternalServerError) as ex:
                if ERROR_CODE_RESOURCE_NOT_EXISTS or INCORRECT_ID in str(ex.message).upper():
                    raise array_errors.PoolDoesNotExist(pool.id, self.identifier)
                else:
                    raise ex
            for volume in volume_candidates:
                logger.info("Checking volume: {}".format(volume.name))
                if volume.name == shorten_volume_name(volume_name, prefix=''):
                    logger.debug("Found volume: {}".format(volume))
                    volume.flashcopy = self.get_flashcopies_by_volume(volume.id)
                    return volume
        return None

    def _get_api_volume_by_id(self, volume_id, not_exist_err=True):
        try:
            volume = self.client.get_volume(volume_id)
            volume.flashcopy = self.get_flashcopies_by_volume(volume.id)
            return volume
        except exceptions.NotFound:
            if not_exist_err:
                raise array_errors.VolumeNotFoundError(volume_id)

    def _get_flashcopy(self, flashcopy_id, not_exist_err=True):
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

    def get_snapshot(self, snapshot_name, volume_context=None):
        logger.debug("Get snapshot : {} with context: {}".format(snapshot_name, volume_context))
        if not volume_context:
            logger.error(
                "volume_context is not specified, can not get volumes from storage."
            )
            raise array_errors.VolumeNotFoundError(snapshot_name)
        target_api_volume = self._get_api_volume_by_name(volume_name=snapshot_name,
                                                         pool_id=volume_context[config.CONTEXT_POOL])
        if not target_api_volume:
            return None
        if not target_api_volume.flashcopy:
            logger.error(
                "FlashCopy relationship not found for target volume: {}".format(snapshot_name))
            raise array_errors.SnapshotNameBelongsToVolumeError(target_api_volume.name,
                                                                self.service_address)
        flashcopy_rel = self._get_flashcopy(target_api_volume.flashcopy[0].id)
        source_volume_name = self.get_volume_name(flashcopy_rel.source_volume['id'])
        return self._generate_snapshot_response(target_api_volume, source_volume_name)

    def _create_similar_volume(self, target_volume_name, source_volume_name, pool_id):
        logger.info(
            "creating target api volume '{0}' from source volume '{1}'".format(target_volume_name,
                                                                               source_volume_name))
        source_api_volume = self._get_api_volume_by_name(source_volume_name, pool_id=pool_id)
        if not source_api_volume:
            raise array_errors.VolumeNotFoundError(source_volume_name)
        capabilities = {config.CAPABILITIES_SPACEEFFICIENCY: source_api_volume.tp}
        size_in_bytes = int(source_api_volume.cap)
        pool = source_api_volume.pool
        return self.create_volume(target_volume_name, size_in_bytes, capabilities, pool)

    def _create_flashcopy(self, source_volume_id, target_volume_id, options=None):
        logger.info(
            "creating FlashCopy relationship from '{0}' to '{1}'".format(source_volume_id,
                                                                         target_volume_id))
        source_volume_id = get_volume_id_from_scsi_identifier(source_volume_id)
        target_volume_id = get_volume_id_from_scsi_identifier(target_volume_id)
        if not options:
            options = []
        options.append(FLASHCOPY_PERMIT_SPACE_EFFICIENT_TARGET)
        try:
            api_flashcopy = self.client.create_flashcopy(source_volume_id=source_volume_id,
                                                         target_volume_id=target_volume_id,
                                                         options=options)
        except (exceptions.ClientError, exceptions.ClientException) as ex:
            if ERROR_CODE_ALREADY_FLASHCOPY in str(ex.message).upper():
                raise array_errors.SnapshotAlreadyExists(target_volume_id,
                                                         self.service_address)
            elif ERROR_CODE_VOLUME_NOT_FOUND_OR_ALREADY_PART_OF_CS_RELATIONSHIP in str(
                    ex.message).upper():
                raise array_errors.VolumeNotFoundError('{} or {}'.format(source_volume_id,
                                                                         target_volume_id))
            else:
                raise ex
        if not self.validate_flashcopy(api_flashcopy.id):
            self._delete_flashcopy(api_flashcopy.id)
            logger.info("Flashcopy is not in a valid state")
            raise ValueError
        return self._get_api_volume_by_id(target_volume_id)

    @retry(Exception, tries=11, delay=1)
    def _delete_target_volume_if_exist(self, target_volume_id):
        self._delete_volume(target_volume_id, not_exist_err=False)

    def _create_snapshot(self, target_volume_name, pool_id, source_volume_name):
        target_volume = self._create_similar_volume(target_volume_name, source_volume_name, pool_id)
        source_volume = self.get_volume(source_volume_name, volume_context={config.CONTEXT_POOL: pool_id})
        options = [FLASHCOPY_NO_BACKGROUND_COPY_OPTION, FLASHCOPY_PERSISTENT_OPTION]
        try:
            return self._create_flashcopy(source_volume.id, target_volume.id, options)
        except (array_errors.VolumeNotFoundError, array_errors.SnapshotAlreadyExists) as ex:
            logger.error("Failed to create snapshot '{0}': {1}".format(target_volume_name, ex))
            self._delete_target_volume_if_exist(target_volume.id)
            raise ex

    def get_snapshot_by_id(self, src_snapshot_id):
        src_snapshot_id = get_volume_id_from_scsi_identifier(src_snapshot_id)
        api_snapshot = self._get_api_volume_by_id(src_snapshot_id)
        src_volume_id = get_source_volume_id_if_exists(api_snapshot)
        api_source_volume = self._get_api_volume_by_id(src_volume_id)
        return self._generate_snapshot_response(api_snapshot, api_source_volume.name)

    def create_snapshot(self, name, volume_name, volume_context=None):
        logger.info("creating snapshot '{0}' from volume '{1}'".format(name, volume_name))
        if not volume_context:
            logger.error(
                "volume_context is not specified, can not get volumes from storage."
            )
        pool = volume_context[config.CONTEXT_POOL]
        target_api_volume = self._create_snapshot(name, pool, source_volume_name=volume_name)
        logger.info("finished creating snapshot '{0}' from volume '{1}'".format(name, volume_name))
        return self._generate_snapshot_response(target_api_volume, volume_name)

    def _delete_flashcopy(self, flascopy_id):
        try:
            self.client.delete_flashcopy(flascopy_id)
        except exceptions.NotFound:
            raise array_errors.VolumeNotFoundError(flascopy_id)
        except exceptions.ClientException as ex:
            logger.error(
                "Failed to delete flascopy {} on array {}, reason is: {}".format(
                    flascopy_id,
                    self.identifier,
                    ex.details
                )
            )
            raise ex

    def delete_snapshot(self, snapshot_id):
        logger.info("Deleting snapshot with id : {0}".format(snapshot_id))
        volume_id = get_volume_id_from_scsi_identifier(snapshot_id)
        api_volume = self._get_api_volume_by_id(volume_id, not_exist_err=False)
        if not api_volume:
            raise array_errors.SnapshotNotFoundError(snapshot_id)
        if not api_volume.flashcopy:
            logger.error(
                "FlashCopy relationship not found for target volume: {}".format(api_volume.name))
            raise array_errors.SnapshotNameBelongsToVolumeError(api_volume.name,
                                                                self.service_address)
        self._check_snapshot_use_status(volume_id, api_volume.flashcopy)
        self.delete_volume(volume_id)
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
        else:
            logger.debug("can not found host by initiators: {0} ".format(initiators))
            raise array_errors.HostNotFoundError(initiators)

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

    def _generate_snapshot_response(self, api_snapshot, source_volume_name):
        return Snapshot(capacity_bytes=int(api_snapshot.cap),
                        snapshot_id=self._generate_volume_scsi_identifier(api_snapshot.id),
                        snapshot_name=api_snapshot.name,
                        array_address=self.service_address,
                        volume_name=source_volume_name,
                        is_ready=True,
                        array_type=self.array_type)

    def validate_flashcopy(self, flashcopy_id):
        api_flashcopy = self._get_flashcopy(flashcopy_id)
        return api_flashcopy.state == 'valid'

    def _check_snapshot_use_status(self, snapshot_id, flashcopy_list):
        for flashcopy in flashcopy_list:
            logger.info("Deleting flashcopy: {}".format(flashcopy))
            if flashcopy.sourcevolume == snapshot_id:
                flashcopy_rel = self._get_flashcopy(flashcopy.id)
                if flashcopy_rel.out_of_sync_tracks != '0':
                    raise array_errors.SnapshotIsStillInUseError(snapshot_id, flashcopy_rel.targetvolume)
