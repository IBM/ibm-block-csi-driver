from pyds8k.client.ds8k.v1.client import Client
from pyds8k.exceptions import NotFound
from controller.common.csi_logger import get_stdout_logger

logger = get_stdout_logger()


def dictionarize(func):
    def inner(self, *args, **kwargs):
        res_obj = func(self, *args, **kwargs)
        if not isinstance(res_obj, list):
            res_obj = [res_obj, ]
        coverted = []
        for res in res_obj:
            try:
                coverted.append(res.representation)
            except AttributeError:
                coverted.append(res)
        return coverted
    return inner


def _int_lunid_to_hex(lunid):
    return '{0:0{1}x}'.format(int(lunid), 2)


def int_to_scsilun(lun):
    # There are two style lun number, one's decimal value is <256 and the other
    # is full as 16 hex digit. According to T10 SAN, when decimal value value
    # is more then 256 and it is converted to the full 16 hex digit, it should
    # be swapped and hex.
    # For example, zlinux lun number stored in SC DB is 1075331113. It should
    # be hex to '40184029' and swapped to '40294018'. When the lun number is 12
    # and it is <256, it should be hex to '0c' directly.
    pretreated_lun = int(lun)
    if pretreated_lun < 256:
        return _int_lunid_to_hex(pretreated_lun)
    else:
        return '{0:x}'.format(
            (pretreated_lun >> 16 & 0xFFFF) | (pretreated_lun & 0xFFFF) << 16
        )


def scsilun_to_int(lun):
    # There are two style lun number, one's decimal value is <256 and the other
    # is full as 16 hex digit. According to T10 SAN, the full 16 hex digit
    # should be swapped and converted into decimal.
    # For example, SC got zlinux lun number from DS8K API, '40294018'. And it
    # should be swapped to '40184029' and converted into decimal, 1075331113.
    # When the lun number is '0c' and its decimal value is <256, it should be
    # converted directly into decimal, 12.
    pretreated_scsilun = int(lun, 16)
    if pretreated_scsilun < 256:
        return pretreated_scsilun
    else:
        return (pretreated_scsilun >> 16 & 0xFFFF) | \
               (pretreated_scsilun & 0xFFFF) << 16


class RESTClient(object):
    """
    driver side client. Used to interaction with pyds8k client as an adaptor.

    !--important: the id field of all resources are case insensitive--!
    """

    def __init__(self, service_address, user, password,
                 port=None,
                 hostname='',
                 ):
        client_kwargs = {'service_address': service_address,
                         'user': user,
                         'password': password,
                         }
        if port:
            client_kwargs.update({'port': port})
        if hostname:
            client_kwargs.update({'hostname': hostname})

        self.client = Client(**client_kwargs)

    @dictionarize
    def get_system(self):
        return self.client.get_system()

    @dictionarize
    def get_volume(self, volume_id):
        return self.client.get_volumes(volume_id)

    @dictionarize
    def get_host(self, host_name):
        return self.client.get_hosts(host_name)

    @dictionarize
    def get_host_mapping(self, host_name, lunid):
        return self.client.get_mapping_by_host(host_name=host_name,
                                               lunid=int_to_scsilun(lunid)
                                               )

    @dictionarize
    def get_host_port(self, port_id):
        return self.client.get_host_ports(port_id)

    @dictionarize
    def get_extentpool(self, pool_id):
        pool = self.client.get_pools(pool_id)
        try:
            # lazy-loading
            eserep = pool.eserep[0]
            pool.representation['eserep'] = eserep.representation
        except AttributeError:
            pass
        return pool

    @dictionarize
    def list_extentpools(self):
        """
        return extent pool list without ese capacity info.
        """
        return self.client.get_pools()

    @dictionarize
    def list_hosts(self):
        return self.client.get_hosts()

    @dictionarize
    def list_host_ports(self):
        return self.client.get_host_ports()

    @dictionarize
    def list_extentpool_volumes(self, pool_id):
        # two requests
        # pool = self.client.get_pools(pool_id)
        # return pool.volumes

        # one request
        return self.client.get_volumes_by_pool(pool_id)

    @dictionarize
    def list_extentpool_virtualpool(self, pool_id):
        return self.client.get_eserep_by_pool(pool_id)

    @dictionarize
    def list_flashcopies(self):
        return self.client.get_flashcopies()

    @dictionarize
    def list_volume_flashcopies(self, volume_id):
        # two requests
        # volume = self.client.get_volumes(volume_id)
        # return volume.flashcopy

        # one request
        return self.client.get_flashcopies_by_volume(volume_id)

    @dictionarize
    def list_remotecopies(self):
        return self.client.get_cs_pprcs()

    @dictionarize
    def list_volume_remotecopies(self, volume_id):
        return self.client.get_pprc_by_volume(volume_id)

    @dictionarize
    def get_remotecopy(self, rc_id):
        return self.client.get_cs_pprcs(rc_id)

    @dictionarize
    def list_logical_subsystems(self):
        return self.client.get_lss()

    @dictionarize
    def list_lss_volumes(self, lss_number):
        return self.client.get_volumes_by_lss(lss_number)

    @dictionarize
    def list_fcports(self):
        return self.client.get_ioports()

    @dictionarize
    def list_host_mappings(self, host_name):
        return self.client.get_mappings_by_host(host_name)

    @dictionarize
    def list_ioenclosures(self):
        return self.client.get_io_enclosures()

    @dictionarize
    def list_nodes(self):
        return self.client.get_nodes()

    @dictionarize
    def list_marrays(self):
        return self.client.get_marrays()

    @dictionarize
    def list_encryption_groups(self):
        return self.client.get_encryption_groups()

    @dictionarize
    def list_events(self, before=None, after=None):
        # before&after are datetime instances.
        return self.client.get_events_by_filter(before=before,
                                                after=after
                                                )

    @dictionarize
    def get_user(self):
        return self.client.get_users(user_name=self.client.client.user)

    def get_used_lun_numbers_by_host(self, host_name):
        mappings = self.client.get_mappings_by_host(host_name)
        return [mapping.id for mapping in mappings]

    @dictionarize
    def create_volumes(self, pool_id, capacity_in_gib, tp,
                       volume_names_list):
        """
        :returns: The representation of created volumes.
        :rtype: list of volume dict, if all volumes are created successfully.
                If some failed, an error dict will be returned.

                So it may look like:
                [volume1_dict, volume2_dict, volume3_error_msg_dict]
        """
        return self.client.create_volumes(name_col=volume_names_list,
                                          cap=capacity_in_gib,
                                          pool=pool_id,
                                          tp=tp)

    def rename_volume(self, volume_id, new_name):
        return self.client.update_volume_rename(volume_id=volume_id,
                                                new_name=new_name)

    def extend_volume(self, volume_id, new_size_in_GiB):
        return self.client.update_volume_extend(volume_id=volume_id,
                                                new_size=new_size_in_GiB,
                                                captype='gib')

    def delete_volume(self, volume_id):
        # remember to unmap all hosts before delete.
        return self.client.delete_volume(volume_id=volume_id)

    def relocate_volume(self, volume_id, new_pool_id):
        return self.client.update_volume_move(volume_id=volume_id,
                                              new_pool=new_pool_id)

    def crate_host(self, host_name, wwpn, host_type='VMWare'):
        hosts = self.client.create_host(host_name=host_name,
                                        hosttype=host_type
                                        )
        self.attach_hostport_to_host(host_name=hosts[0].id, wwpn=wwpn)
        return hosts[0].id

    def delete_host(self, host_name):
        # delete a host will delete all the attached host ports.
        return self.client.delete_host(host_name=host_name)

    def attach_hostport_to_host(self, host_name, wwpn):
        return self._get_attach_or_create_host_port(host_name=host_name,
                                                    wwpn=wwpn
                                                    )

    def detach_hostport_from_host(self, wwpn):
        return self.client.delete_host_port(port_id=wwpn)

    def map_volume_to_host(self, host_name, volume_id, lunid=''):
        return self.client.map_volume_to_host(
            host_name=host_name,
            volume_id=volume_id,
            lunid=int_to_scsilun(lunid) if lunid else ''
            )

    def unmap_volume_from_host(self, host_name, lunid):
        return self.client.unmap_volume_from_host(
            host_name=host_name,
            lunid=int_to_scsilun(lunid)
            )

    def _get_attach_or_create_host_port(self, host_name, wwpn):
        try:
            host_port = self.client.get_host_port(port_id=wwpn)
            return self.client.update_host_port_change_host(
                port_id=host_port.id,
                host_name=host_name
            )
        except NotFound:
            logger.debug(
                'host port {} is not found, creating new...'.format(wwpn)
            )
            return self.client.create_host_port(port_id=wwpn,
                                                host_name=host_name
                                                )
