from random import randint

from pyxcli import errors as xcli_errors
from pyxcli.client import XCLIClient

import controller.array_action.errors as controller_errors
import controller.controller_server.config as controller_config
from controller.array_action.array_action_types import Volume, Snapshot
from controller.array_action.array_mediator_abstract import ArrayMediatorAbstract
from controller.array_action.config import FC_CONNECTIVITY_TYPE, ISCSI_CONNECTIVITY_TYPE
from controller.array_action.utils import classproperty
from controller.common import settings
from controller.common.csi_logger import get_stdout_logger
from controller.common.utils import string_to_array

array_connections_dict = {}
logger = get_stdout_logger()


class XIVArrayMediator(ArrayMediatorAbstract):
    ARRAY_ACTIONS = {}
    BLOCK_SIZE_IN_BYTES = 512
    MAX_LUN_NUMBER = 250
    MIN_LUN_NUMBER = 1

    @classproperty
    def array_type(self):
        return settings.ARRAY_TYPE_XIV

    @classproperty
    def port(self):
        return 7778

    @classproperty
    def max_volume_name_length(self):
        return 63

    @classproperty
    def max_volume_prefix_length(self):
        return 20

    @classproperty
    def max_snapshot_name_length(self):
        return self.max_volume_name_length

    @classproperty
    def max_snapshot_prefix_length(self):
        return self.max_volume_prefix_length

    @classproperty
    def max_connections(self):
        return 2

    @classproperty
    def minimal_volume_size_in_bytes(self):
        return 1 * 1024 * 1024 * 1024  # 1 GiB

    @classproperty
    def max_lun_retries(self):
        return 10

    def __init__(self, user, password, endpoint):
        self.user = user
        self.password = password
        self.endpoint = endpoint
        self.client = None
        self._identifier = None

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
            raise controller_errors.CredentialsError(self.endpoint)
        except xcli_errors.XCLIError:
            raise controller_errors.CredentialsError(self.endpoint)

    def disconnect(self):
        if self.client and self.client.is_connected():
            self.client.close()

    @property
    def identifier(self):
        if self._identifier is None:
            self._identifier = self.get_fully_qualified_serial()
        return self._identifier

    def get_fully_qualified_serial(self):
        config = self.client.cmd.config_get().as_dict('name')
        machine_type = config['machine_type'].value
        machine_model = config['machine_model'].value
        machine_serial = config['machine_serial_number'].value
        return '{type}-{model}-{serial}'.format(
            type=machine_type,
            model=machine_model,
            serial=machine_serial
        )

    def is_active(self):
        return True

    def _convert_size_blocks_to_bytes(self, size_in_blocks):
        return int(size_in_blocks) * self.BLOCK_SIZE_IN_BYTES

    def _generate_volume_response(self, cli_volume):
        # vol_copy_type and copy_master_wwn were added in a9k. In xiv they didn't exist
        is_copy = hasattr(cli_volume, "vol_copy_type") and cli_volume.vol_copy_type == "Copy"
        copy_src_object_wwn = cli_volume.copy_master_wwn if is_copy else None
        return Volume(self._convert_size_blocks_to_bytes(cli_volume.capacity),
                      cli_volume.wwn,
                      cli_volume.name,
                      self.endpoint,
                      cli_volume.pool_name,
                      copy_src_object_wwn,
                      self.array_type)

    def _generate_snapshot_response(self, cli_snapshot):
        return Snapshot(self._convert_size_blocks_to_bytes(cli_snapshot.capacity),
                        cli_snapshot.wwn,
                        cli_snapshot.name,
                        self.endpoint,
                        cli_snapshot.master_name,
                        is_ready=True,
                        array_type=self.array_type)

    def get_volume(self, volume_name, pool_id=None):
        logger.debug("Get volume : {}".format(volume_name))
        try:
            cli_volume = self.client.cmd.vol_list(vol=volume_name).as_single_element
        except xcli_errors.IllegalNameForObjectError as ex:
            logger.exception(ex)
            raise controller_errors.IllegalObjectName(ex.status)

        logger.debug("cli volume returned : {}".format(cli_volume))
        if not cli_volume:
            raise controller_errors.ObjectNotFoundError(volume_name)

        if cli_volume.master_name:
            raise controller_errors.VolumeNameBelongsToSnapshotError(volume_name, self.endpoint)

        array_vol = self._generate_volume_response(cli_volume)
        return array_vol

    def get_volume_name(self, volume_id):
        try:
            return self._get_vol_by_wwn(volume_id)
        except xcli_errors.IllegalNameForObjectError as ex:
            logger.exception(ex)
            raise controller_errors.IllegalObjectName(ex.status)

    def is_volume_has_snapshots(self, volume_id):
        try:
            volume_name = self._get_vol_by_wwn(volume_id)
            return bool(self.client.cmd.snapshot_list(vol=volume_name).as_list)
        except xcli_errors.IllegalValueForArgumentError as ex:
            logger.exception(ex)
            raise controller_errors.IllegalObjectID(ex.status)

    def validate_supported_capabilities(self, capabilities):
        logger.info("validate_supported_capabilities for capabilities : {0}".format(capabilities))
        # for a9k there should be no capabilities
        if capabilities or len(capabilities) > 0:
            raise controller_errors.StorageClassCapabilityNotSupported(capabilities)

        logger.info("Finished validate_supported_capabilities")

    def _convert_size_bytes_to_blocks(self, size_in_bytes):
        return int(size_in_bytes / self.BLOCK_SIZE_IN_BYTES)

    def create_volume(self, name, size_in_bytes, capabilities, pool):
        logger.info("creating volume with name : {}. size : {} . in pool : {} with capabilities : {}".format(
            name, size_in_bytes, pool, capabilities))

        size_in_blocks = self._convert_size_bytes_to_blocks(size_in_bytes)

        try:
            cli_volume = self.client.cmd.vol_create(vol=name, size_blocks=size_in_blocks,
                                                    pool=pool).as_single_element
            logger.info("finished creating cli volume : {}".format(cli_volume))
            return self._generate_volume_response(cli_volume)
        except xcli_errors.IllegalNameForObjectError as ex:
            logger.exception(ex)
            raise controller_errors.IllegalObjectName(ex.status)
        except xcli_errors.VolumeExistsError as ex:
            logger.exception(ex)
            raise controller_errors.VolumeAlreadyExists(name, self.endpoint)
        except xcli_errors.PoolDoesNotExistError as ex:
            logger.exception(ex)
            raise controller_errors.PoolDoesNotExist(pool, self.endpoint)
        except xcli_errors.OperationForbiddenForUserCategoryError as ex:
            logger.exception(ex)
            raise controller_errors.PermissionDeniedError("create vol : {0}".format(name))

    def copy_to_existing_volume_from_source(self, name, source_name, source_capacity_in_bytes,
                                            minimum_volume_size_in_bytes, pool_id=None):
        logger.debug(
            "Copy snapshot {0} data to volume {1}. Snapshot capacity {2}. Minimal requested volume capacity {3}".format(
                name, source_name, source_capacity_in_bytes, minimum_volume_size_in_bytes))
        try:
            logger.debug("Formatting volume {0}".format(name))
            self.client.cmd.vol_format(vol=name)
            logger.debug("Copying Snapshot {0} data to volume {1}.".format(name, source_name))
            self.client.cmd.vol_copy(vol_src=source_name, vol_trg=name)
            if minimum_volume_size_in_bytes > source_capacity_in_bytes:
                min_vol_size_in_blocks = self._convert_size_bytes_to_blocks(minimum_volume_size_in_bytes)
                logger.debug(
                    "Increasing volume {0} size to {1} blocks.".format(name, min_vol_size_in_blocks))
                self.client.cmd.vol_resize(vol=name, size_blocks=min_vol_size_in_blocks)
        except xcli_errors.IllegalNameForObjectError as ex:
            logger.exception(ex)
            raise controller_errors.IllegalObjectName(ex.status)
        except xcli_errors.SourceVolumeBadNameError as ex:
            logger.exception(ex)
            raise controller_errors.ObjectNotFoundError(source_name)
        except (xcli_errors.VolumeBadNameError, xcli_errors.TargetVolumeBadNameError) as ex:
            logger.exception(ex)
            raise controller_errors.ObjectNotFoundError(name)
        except xcli_errors.OperationForbiddenForUserCategoryError as ex:
            logger.exception(ex)
            raise controller_errors.PermissionDeniedError("create vol : {0}".format(name))

    def _get_vol_by_wwn(self, volume_id):
        vol_by_wwn = self.client.cmd.vol_list(wwn=volume_id).as_single_element
        if not vol_by_wwn:
            raise controller_errors.ObjectNotFoundError(volume_id)

        vol_name = vol_by_wwn.name
        logger.debug("found volume name : {0}".format(vol_name))
        return vol_name

    def delete_volume(self, volume_id):
        logger.info("Deleting volume with id : {0}".format(volume_id))
        volume_name = self._get_vol_by_wwn(volume_id)

        try:
            self.client.cmd.vol_delete(vol=volume_name)
        except xcli_errors.VolumeBadNameError as ex:
            logger.exception(ex)
            raise controller_errors.ObjectNotFoundError(volume_name)

        except xcli_errors.OperationForbiddenForUserCategoryError as ex:
            logger.exception(ex)
            raise controller_errors.PermissionDeniedError("delete vol : {0}".format(volume_name))

        logger.info("Finished volume deletion. id : {0}".format(volume_id))

    def get_snapshot(self, snapshot_name, pool_id=None):
        logger.debug("Get snapshot : {}".format(snapshot_name))
        try:
            cli_snapshot = self.client.cmd.vol_list(vol=snapshot_name).as_single_element
        except xcli_errors.IllegalNameForObjectError as ex:
            logger.exception(ex)
            raise controller_errors.IllegalObjectName(ex.status)
        if not cli_snapshot:
            return None
        if not cli_snapshot.master_name:
            raise controller_errors.ExpectedSnapshotButFoundVolumeError(cli_snapshot.name, self.endpoint)
        array_snapshot = self._generate_snapshot_response(cli_snapshot)
        return array_snapshot

    def get_object_by_id(self, object_id, object_type):
        try:
            cli_object = self.client.cmd.vol_list(wwn=object_id).as_single_element
        except xcli_errors.IllegalValueForArgumentError as ex:
            logger.exception(ex)
            raise controller_errors.IllegalObjectID(ex.status)
        if not cli_object:
            return None
        if object_type is controller_config.SNAPSHOT_TYPE_NAME:
            if not cli_object.master_name:
                raise controller_errors.ExpectedSnapshotButFoundVolumeError(object_id, self.endpoint)
            return self._generate_snapshot_response(cli_object)
        return self._generate_volume_response(cli_object)

    def create_snapshot(self, name, volume_name, pool_id=None):
        logger.info("creating snapshot {0} from volume {1}".format(name, volume_name))

        try:
            cli_snapshot = self.client.cmd.snapshot_create(name=name, vol=volume_name).as_single_element
            logger.info("finished creating cli snapshot {0} from volume {1}".format(name, volume_name))
            return self._generate_snapshot_response(cli_snapshot)
        except xcli_errors.IllegalNameForObjectError as ex:
            logger.exception(ex)
            raise controller_errors.IllegalObjectName(ex.status)
        except xcli_errors.VolumeExistsError as ex:
            logger.exception(ex)
            raise controller_errors.SnapshotAlreadyExists(name, self.endpoint)
        except xcli_errors.VolumeBadNameError as ex:
            logger.exception(ex)
            raise controller_errors.ObjectNotFoundError(volume_name)
        except xcli_errors.OperationForbiddenForUserCategoryError as ex:
            logger.exception(ex)
            raise controller_errors.PermissionDeniedError(
                "create snapshot {0} from volume {1}".format(name, volume_name))

    def delete_snapshot(self, snapshot_id):
        logger.info("Deleting snapshot with id : {0}".format(snapshot_id))
        try:
            snapshot_name = self._get_vol_by_wwn(snapshot_id)
        except controller_errors.ObjectNotFoundError:
            raise controller_errors.ObjectNotFoundError(snapshot_id)

        try:
            self.client.cmd.snapshot_delete(snapshot=snapshot_name)
        except xcli_errors.VolumeBadNameError as ex:
            logger.exception(ex)
            raise controller_errors.ObjectNotFoundError(snapshot_name)

        except xcli_errors.OperationForbiddenForUserCategoryError as ex:
            logger.exception(ex)
            raise controller_errors.PermissionDeniedError("delete snapshot : {0}".format(snapshot_name))

        logger.info("Finished snapshot deletion. id : {0}".format(snapshot_id))

    def get_host_by_host_identifiers(self, initiators):
        logger.debug("Getting host id for initiators : {0}".format(initiators))
        matching_hosts_set = set()
        port_types = []

        host_list = self.client.cmd.host_list().as_list
        for host in host_list:
            host_iscsi_ports = string_to_array(host.iscsi_ports, ',')
            host_fc_ports = string_to_array(host.fc_ports, ',')
            if initiators.is_array_wwns_match(host_fc_ports):
                matching_hosts_set.add(host.name)
                logger.debug("found host : {0}, by fc port : {1}".format(host.name, host_fc_ports))
                port_types.append(FC_CONNECTIVITY_TYPE)
            if initiators.is_array_iscsi_iqns_match(host_iscsi_ports):
                matching_hosts_set.add(host.name)
                logger.debug("found host : {0}, by iscsi port : {1}".format(host.name, host_iscsi_ports))
                port_types.append(ISCSI_CONNECTIVITY_TYPE)
        matching_hosts = sorted(matching_hosts_set)
        if not matching_hosts:
            raise controller_errors.HostNotFoundError(initiators)
        elif len(matching_hosts) > 1:
            raise controller_errors.MultipleHostsFoundError(initiators, matching_hosts)
        return matching_hosts[0], port_types

    def get_volume_mappings(self, volume_id):
        logger.debug("Getting volume mappings for volume id : {0}".format(volume_id))
        vol_name = self._get_vol_by_wwn(volume_id)
        logger.debug("vol name : {0}".format(vol_name))
        mapping_list = self.client.cmd.vol_mapping_list(vol=vol_name).as_list
        res = {}
        for mapping in mapping_list:
            logger.debug("mapping for vol is :{0}".format(mapping))
            res[mapping.host] = mapping.lun

        return res

    def _get_next_available_lun(self, host_name):
        logger.debug("getting host mapping list for host :{0}".format(host_name))
        try:
            host_mapping_list = self.client.cmd.mapping_list(host=host_name).as_list
        except xcli_errors.HostBadNameError as ex:
            logger.exception(ex)
            raise controller_errors.HostNotFoundError(host_name)

        luns_in_use = set([int(host_mapping.lun) for host_mapping in host_mapping_list])
        logger.debug("luns in use : {0}".format(luns_in_use))

        # try to use random lun number just in case there are many calls at the same time to reduce re-tries
        all_available_luns = [i for i in range(self.MIN_LUN_NUMBER, self.MAX_LUN_NUMBER + 1) if i not in luns_in_use]

        if len(all_available_luns) == 0:
            raise controller_errors.NoAvailableLunError(host_name)

        index = randint(0, len(all_available_luns) - 1)
        lun = all_available_luns[index]
        logger.debug("next random available lun is : {0}".format(lun))
        return lun

    def map_volume(self, volume_id, host_name):
        logger.debug("mapping volume : {0} to host : {1}".format(volume_id, host_name))
        vol_name = self._get_vol_by_wwn(volume_id)
        lun = self._get_next_available_lun(host_name)

        try:
            self.client.cmd.map_vol(host=host_name, vol=vol_name, lun=lun)
        except xcli_errors.OperationForbiddenForUserCategoryError as ex:
            logger.exception(ex)
            raise controller_errors.PermissionDeniedError("map volume : {0} to host : {1}".format(volume_id, host_name))
        except xcli_errors.VolumeBadNameError as ex:
            logger.exception(ex)
            raise controller_errors.ObjectNotFoundError(vol_name)
        except xcli_errors.HostBadNameError as ex:
            logger.exception(ex)
            raise controller_errors.HostNotFoundError(host_name)
        except xcli_errors.CommandFailedRuntimeError as ex:
            logger.exception(ex)
            if "LUN is already in use" in ex.status:
                raise controller_errors.LunAlreadyInUseError(lun, host_name)
            else:
                raise controller_errors.MappingError(vol_name, host_name, ex)

        return str(lun)

    def unmap_volume(self, volume_id, host_name):
        logger.debug("un-mapping volume : {0} from host : {1}".format(volume_id, host_name))

        vol_name = self._get_vol_by_wwn(volume_id)

        try:
            self.client.cmd.unmap_vol(host=host_name, vol=vol_name)
        except xcli_errors.VolumeBadNameError as ex:
            logger.exception(ex)
            raise controller_errors.ObjectNotFoundError(vol_name)
        except xcli_errors.HostBadNameError as ex:
            logger.exception(ex)
            raise controller_errors.HostNotFoundError(host_name)
        except xcli_errors.OperationForbiddenForUserCategoryError as ex:
            logger.exception(ex)
            raise controller_errors.PermissionDeniedError(
                "unmap volume : {0} from host : {1}".format(volume_id, host_name))
        except xcli_errors.CommandFailedRuntimeError as ex:
            logger.exception(ex)
            if "The requested mapping is not defined" in ex.status:
                raise controller_errors.VolumeAlreadyUnmappedError(vol_name)
            else:
                raise controller_errors.UnMappingError(vol_name, host_name, ex)

    def _get_iscsi_targets(self):
        ip_interfaces = self.client.cmd.ipinterface_list()
        iscsi_interfaces = (i for i in ip_interfaces if i.type == "iSCSI")
        ips = []
        for interface in iscsi_interfaces:
            if interface.address:
                ips.append(interface.address)
            if interface.address6:
                ipv6 = interface.address6.join('[]')
                ips.append(ipv6)
        return ips

    def _get_array_iqn(self):
        config_get_list = self.client.cmd.config_get().as_list
        return next(c.value for c in config_get_list if c.name == "iscsi_name")

    def get_iscsi_targets_by_iqn(self):
        array_iqn = self._get_array_iqn()
        iscsi_targets = self._get_iscsi_targets()
        return {array_iqn: iscsi_targets}

    def get_array_fc_wwns(self, host_name):
        fc_wwns_objects = self.client.cmd.fc_port_list()
        return [port.wwpn for port in fc_wwns_objects if port.port_state == 'Online' and port.role == 'Target']
