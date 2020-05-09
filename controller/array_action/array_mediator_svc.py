from collections import defaultdict

from pysvc import errors as svc_errors
from pysvc.unified.client import connect
from pysvc.unified.response import CLIFailureError

import controller.array_action.config as config
import controller.array_action.errors as controller_errors
from controller.array_action.array_action_types import Volume
from controller.array_action.array_mediator_abstract import ArrayMediatorAbstract
from controller.array_action.utils import classproperty
from controller.common.csi_logger import get_stdout_logger

array_connections_dict = {}
logger = get_stdout_logger()

OBJ_NOT_FOUND = 'CMMVC5753E'
NAME_NOT_MEET = 'CMMVC5754E'
SPECIFIED_OBJ_NOT_EXIST = 'CMMVC5804E'
VOL_ALREADY_MAPPED = 'CMMVC5878E'
VOL_ALREADY_UNMAPPED = 'CMMVC5842E'
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
    if capability:
        capability = capability.lower()
        if capability == config.CAPABILITY_THIN:
            cli_kwargs.update({'thin': True})
        elif capability == config.CAPABILITY_COMPRESSED:
            cli_kwargs.update({'compressed': True})
        elif capability == config.CAPABILITY_DEDUPLICATED:
            cli_kwargs.update({'compressed': True, 'deduplicated': True})

    return cli_kwargs


class SVCArrayMediator(ArrayMediatorAbstract):
    ARRAY_ACTIONS = {}
    BLOCK_SIZE_IN_BYTES = 512
    MAX_LUN_NUMBER = 511
    MIN_LUN_NUMBER = 0

    @classproperty
    def array_type(self):
        return 'SVC'

    @classproperty
    def port(self):
        return 22

    @classproperty
    def max_volume_name_length(self):
        return 63

    @classproperty
    def max_volume_prefix_length(self):
        return 20

    @classproperty
    def max_snapshot_name_length(self):
        # TODO: CSI-1024
        pass

    @classproperty
    def max_snapshot_prefix_length(self):
        # TODO: CSI-1024
        pass

    @classproperty
    def max_connections(self):
        return 2

    @classproperty
    def minimal_volume_size_in_bytes(self):
        return 512  # 512 Bytes

    @classproperty
    def max_lun_retries(self):
        return 10

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
            cli_volume.vdisk_UID,
            cli_volume.name,
            self.endpoint,
            cli_volume.mdisk_grp_name,
            self.array_type)

    def get_volume(self, volume_name, volume_context=None, volume_prefix=""):
        logger.debug("Get volume : {}".format(volume_name))
        cli_volume = None
        try:
            cli_volume = self.client.svcinfo.lsvdisk(
                bytes=True, object_id=volume_name).as_single_element
        except (svc_errors.CommandExecutionError, CLIFailureError) as ex:
            if not is_warning_message(ex.my_message):
                if (OBJ_NOT_FOUND in ex.my_message or
                        NAME_NOT_MEET in ex.my_message):
                    logger.error("Volume not found")
                    raise controller_errors.VolumeNotFoundError(volume_name)
        except Exception as ex:
            logger.exception(ex)
            raise ex

        if not cli_volume:
            raise controller_errors.VolumeNotFoundError(volume_name)
        logger.debug("cli volume returned : {}".format(cli_volume))
        return self._generate_volume_response(cli_volume)

    def get_volume_name(self, volume_id):
        # TODO: CSI-1024
        pass

    def validate_supported_capabilities(self, capabilities):
        logger.debug("validate_supported_capabilities for "
                     "capabilities : {0}".format(capabilities))
        # Currently, we only support one capability "SpaceEfficiency"
        # The value should be: "thin/thick/compressed/deduplicated"
        if (capabilities and capabilities.get(
                config.CAPABILITIES_SPACEEFFICIENCY).lower() not in
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

    def _get_vol_by_wwn(self, volume_id):
        filter_value = 'vdisk_UID=' + volume_id
        vol_by_wwn = self.client.svcinfo.lsvdisk(
            filtervalue=filter_value).as_single_element
        if not vol_by_wwn:
            raise controller_errors.VolumeNotFoundError(volume_id)

        vol_name = vol_by_wwn.name
        logger.debug("found volume name : {0}".format(vol_name))
        return vol_name

    def create_volume(self, name, size_in_bytes, capabilities, pool, volume_prefix=""):
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
        vol_name = self._get_vol_by_wwn(volume_id)
        try:
            self.client.svctask.rmvolume(vdisk_id=vol_name)
        except (svc_errors.CommandExecutionError, CLIFailureError) as ex:
            if not is_warning_message(ex.my_message):
                logger.warning("Failed to delete volume {}".format(vol_name))
                if (OBJ_NOT_FOUND in ex.my_message
                        or VOL_NOT_FOUND in ex.my_message):
                    raise controller_errors.VolumeNotFoundError(vol_name)
                else:
                    raise ex
        except Exception as ex:
            logger.exception(ex)
            raise ex

        logger.info("Finished volume deletion. id : {0}".format(volume_id))

    def get_snapshot(self, snapshot_name):
        # TODO: CSI-1024
        raise NotImplementedError

    def create_snapshot(self, name, volume_name):
        # TODO: CSI-1024
        raise NotImplementedError

    def get_host_by_host_identifiers(self, initiators):
        logger.debug("Getting host id for initiators : {0}".format(initiators))
        host_list = self.client.svcinfo.lshost()
        iscsi_host, fc_host = None, None
        for host in host_list:
            host_detail = self.client.svcinfo.lshost(
                object_id=host.get('id', '')).as_single_element
            iscsi_names = host_detail.get('iscsi_name', '')
            wwns_value = host_detail.get('WWPN', [])
            if not isinstance(wwns_value, list):
                wwns_value = [wwns_value, ]
            if not isinstance(iscsi_names, list):
                iscsi_names = [] if len(iscsi_names) == 0 else [iscsi_names]
            if initiators.is_array_iscsi_iqns_match(iscsi_names):
                iscsi_host = host_detail.get('name', '')
                logger.debug("found iscsi iqn in list : {0} for host : "
                             "{1}".format(initiators.iscsi_iqn, iscsi_host))
            if initiators.is_array_wwns_match(wwns_value):
                fc_host = host_detail.get('name', '')
                logger.debug("found fc wwns in list : {0} for host : "
                             "{1}".format(initiators.fc_wwns, fc_host))
        if iscsi_host and fc_host:
            if iscsi_host == fc_host:
                return fc_host, [config.ISCSI_CONNECTIVITY_TYPE,
                                 config.FC_CONNECTIVITY_TYPE]
            else:
                raise controller_errors.MultipleHostsFoundError(initiators, fc_host)
        elif iscsi_host:
            logger.debug("found host : {0} with iqn : {1}".format(iscsi_host, initiators.iscsi_iqn))
            return iscsi_host, [config.ISCSI_CONNECTIVITY_TYPE]
        elif fc_host:
            logger.debug("found host : {0} with fc wwn : {1}".format(fc_host, initiators.fc_wwns))
            return fc_host, [config.FC_CONNECTIVITY_TYPE]
        else:
            logger.debug("can not found host by using initiators: {0} ".format(initiators))
            raise controller_errors.HostNotFoundError(initiators)

    def get_volume_mappings(self, volume_id):
        logger.debug("Getting volume mappings for volume id : "
                     "{0}".format(volume_id))
        vol_name = self._get_vol_by_wwn(volume_id)
        logger.debug("vol name : {0}".format(vol_name))
        try:
            mapping_list = self.client.svcinfo.lsvdiskhostmap(vdisk_name=vol_name)
            res = {}
            for mapping in mapping_list:
                logger.debug("mapping for vol is :{0}".format(mapping))
                res[mapping.get('host_name', '')] = mapping.get('SCSI_id', '')
        except(svc_errors.CommandExecutionError, CLIFailureError) as ex:
            logger.error(ex)
            raise controller_errors.VolumeNotFoundError(volume_id)

        return res

    def _get_used_lun_ids_from_host(self, host_name):
        logger.debug("getting used lun ids for host :{0}".format(host_name))
        luns_in_use = set()

        try:
            for mapping in self.client.svcinfo.lshostvdiskmap(host=host_name):
                luns_in_use.add(mapping.get('SCSI_id', ''))
        except(svc_errors.CommandExecutionError, CLIFailureError) as ex:
            logger.error(ex)
            raise controller_errors.HostNotFoundError(host_name)
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
            raise controller_errors.NoAvailableLunError(host_name)
        logger.debug("The first available lun is : {0}".format(lun))
        return lun

    def map_volume(self, volume_id, host_name):
        logger.debug("mapping volume : {0} to host : "
                     "{1}".format(volume_id, host_name))
        vol_name = self._get_vol_by_wwn(volume_id)
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
                if NAME_NOT_MEET in ex.my_message:
                    raise controller_errors.HostNotFoundError(host_name)
                if SPECIFIED_OBJ_NOT_EXIST in ex.my_message:
                    raise controller_errors.VolumeNotFoundError(vol_name)
                if VOL_ALREADY_MAPPED in ex.my_message:
                    raise controller_errors.LunAlreadyInUseError(lun,
                                                                 host_name)
                raise controller_errors.MappingError(vol_name, host_name, ex)
        except Exception as ex:
            logger.exception(ex)
            raise ex

        return str(lun)

    def unmap_volume(self, volume_id, host_name):
        logger.debug("un-mapping volume : {0} from host : "
                     "{1}".format(volume_id, host_name))
        vol_name = self._get_vol_by_wwn(volume_id)

        cli_kwargs = {
            'host': host_name,
            'vdisk_id': vol_name
        }

        try:
            self.client.svctask.rmvdiskhostmap(**cli_kwargs)
        except (svc_errors.CommandExecutionError, CLIFailureError) as ex:
            if not is_warning_message(ex.my_message):
                logger.error(msg="Map volume {0} to host {1} failed. Reason "
                                 "is: {2}".format(vol_name, host_name, ex))
                if NAME_NOT_MEET in ex.my_message:
                    raise controller_errors.HostNotFoundError(host_name)
                if OBJ_NOT_FOUND in ex.my_message:
                    raise controller_errors.VolumeNotFoundError(vol_name)
                if VOL_ALREADY_UNMAPPED in ex.my_message:
                    raise controller_errors.VolumeAlreadyUnmappedError(
                        vol_name)
                raise controller_errors.UnMappingError(vol_name,
                                                       host_name, ex)
        except Exception as ex:
            logger.exception(ex)
            raise ex

    def _get_array_iqns_by_node_id(self):
        logger.debug("Getting array nodes id and iscsi name")
        try:
            nodes_list = self.client.svcinfo.lsnode()
            array_iqns_by_id = {node.id: node.iscsi_name for node in nodes_list
                                if node.status.lower() == "online"}
        except Exception as ex:
            logger.exception(ex)
            raise ex
        logger.debug("Found iqns by node id: {}".format(array_iqns_by_id))
        return array_iqns_by_id

    def _list_ip_ports(self):
        try:
            return self.client.svcinfo.lsportip(filtervalue='state=configured:failover=no')
        except (svc_errors.CommandExecutionError, CLIFailureError) as ex:
            logger.error("Get iscsi targets failed. Reason is: {}".format(ex))
            raise controller_errors.NoIscsiTargetsFoundError(self.endpoint)

    @staticmethod
    def _create_ips_by_node_id_map(ports):
        ips_by_node_id = defaultdict(list)
        for port in ports:
            if port.IP_address:
                ips_by_node_id[port.node_id].append(port.IP_address)
            if port.IP_address_6:
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

    def _get_iscsi_targets_by_node_id(self):
        ports = self._list_ip_ports()
        return self._create_ips_by_node_id_map(ports)

    def get_iscsi_targets_by_iqn(self):
        logger.debug("Getting iscsi targets by iqn")
        iqns_by_node_id = self._get_array_iqns_by_node_id()
        ips_by_node_id = self._get_iscsi_targets_by_node_id()
        ips_by_iqn = self._unify_ips_by_iqn(iqns_by_node_id, ips_by_node_id)

        if ips_by_iqn and any(ips_by_iqn.values()):
            logger.debug("Found iscsi target IPs by iqn: {}".format(ips_by_iqn))
            return ips_by_iqn
        else:
            raise controller_errors.NoIscsiTargetsFoundError(self.endpoint)

    def get_array_fc_wwns(self, host_name):
        logger.debug("Getting the connected fc port wwn value from array "
                     "related to host : {}.".format(host_name))
        fc_port_wwns = []
        try:
            fc_wwns = self.client.svcinfo.lsfabric(host=host_name)
            for wwn in fc_wwns:
                state = wwn.get('state', '')
                if state == 'active' or state == 'inactive':
                    fc_port_wwns.append(wwn.get('local_wwpn', ''))
            logger.debug("Getting fc wwns : {}".format(fc_port_wwns))
            return fc_port_wwns
        except(svc_errors.CommandExecutionError, CLIFailureError) as ex:
            logger.error(msg="Failed to get array fc wwn. Reason "
                             "is: {0}".format(ex))
            raise ex
