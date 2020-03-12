from pyds8k.client.ds8k.v1.client import Client
from pyds8k.exceptions import NotFound
from controller.common.csi_logger import get_stdout_logger

logger = get_stdout_logger()


def _int_lunid_to_hex(lunid):
    return '{0:0{1}x}'.format(int(lunid), 2)


def int_to_scsilun(lun):
    """
    There are two style lun number, one's decimal value is <256 and the other
    is full as 16 hex digit. According to T10 SAM, when decimal value is more
    than 256 and it is converted to the full 16 hex digit, it should be
    swapped and converted into hex.
    For example, zlinux lun number stored in SC DB is 1075331113. It should
    be hex to '40184029' and swapped to '40294018'. When the lun number is 12
    and it is <256, it should be hex to '0c' directly.

    https://github.com/kubernetes/kubernetes/issues/45024
    """
    pretreated_lun = int(lun)
    if pretreated_lun < 256:
        return _int_lunid_to_hex(pretreated_lun)
    else:
        return '{0:x}'.format(
            (pretreated_lun >> 16 & 0xFFFF) | (pretreated_lun & 0xFFFF) << 16
        )


def scsilun_to_int(lun):
    """
    There are two style lun number, one's decimal value is <256 and the other
    is full as 16 hex digit. According to T10 SAM, the full 16 hex digit
    should be swapped and converted into decimal.
    For example, SC got zlinux lun number from DS8K API, '40294018'. And it
    should be swapped to '40184029' and converted into decimal, 1075331113.
    When the lun number is '0c' and its decimal value is <256, it should be
    converted directly into decimal, 12.

    https://github.com/kubernetes/kubernetes/issues/45024
    """
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
        self.user = user

        client_kwargs = {'service_address': service_address,
                         'user': user,
                         'password': password,
                         }
        if port:
            client_kwargs.update({'port': port})
        if hostname:
            client_kwargs.update({'hostname': hostname})

        self._client = Client(**client_kwargs)

    def get_system(self):
        return self._client.get_systems()[0]

    def get_volume(self, volume_id):
        return self._client.get_volumes(volume_id)

    def get_host(self, host_name):
        return self._client.get_hosts(host_name)

    def get_host_mapping(self, host_name, lunid):
        return self._client.get_mapping_by_host(
            host_name=host_name,
            lunid=int_to_scsilun(lunid)
        )

    def get_host_port(self, port_id):
        return self._client.get_host_ports(port_id)

    def get_pool(self, pool_id):
        pool = self._client.get_pools(pool_id)
        try:
            # lazy-loading
            eserep = pool.eserep[0]
            pool.representation['eserep'] = eserep.representation
        except AttributeError:
            pass
        return pool

    def get_pools(self):
        """
        return extent pool list without ese capacity info.
        """
        return self._client.get_pools()

    def get_hosts(self):
        return self._client.get_hosts()

    def get_host_ports(self):
        return self._client.get_host_ports()

    def get_volumes_by_pool(self, pool_id):
        return self._client.get_volumes_by_pool(pool_id)

    def get_flashcopies(self):
        return self._client.get_flashcopies()

    def get_flashcopies_by_volume(self, volume_id):
        return self._client.get_flashcopies_by_volume(volume_id)

    def get_lss(self):
        return self._client.get_lss()

    def get_volumes_by_lss(self, lss_number):
        return self._client.get_volumes_by_lss(lss_number)

    def get_fcports(self):
        return self._client.get_ioports()

    def get_host_mappings(self, host_name):
        return self._client.get_mappings_by_host(host_name)

    def get_ioports_by_host(self, host_name):
        return self._client.get_ioports_by_host(host_name)

    def get_user(self):
        return self._client.get_users(user_name=self.user)

    def get_used_lun_numbers_by_host(self, host_name):
        mappings = self._client.get_mappings_by_host(host_name)
        return [mapping.id for mapping in mappings]

    def create_volume(self, name, capacity_in_bytes, pool_id, tp):
        return self._client.create_volume_fb(
            name=name,
            cap=capacity_in_bytes,
            captype='bytes',
            pool=pool_id,
            tp=tp
        )[0]

    def rename_volume(self, volume_id, new_name):
        return self._client.update_volume_rename(
            volume_id=volume_id,
            new_name=new_name
        )

    def extend_volume(self, volume_id, new_size_in_bytes):
        return self._client.update_volume_extend(
            volume_id=volume_id,
            new_size=new_size_in_bytes,
            captype='bytes'
        )

    def delete_volume(self, volume_id):
        # remember to unmap all hosts before delete.
        return self._client.delete_volume(volume_id=volume_id)

    def move_volume(self, volume_id, new_pool_id):
        return self._client.update_volume_move(
            volume_id=volume_id,
            new_pool=new_pool_id
        )

    def create_host(self, host_name, wwpn, host_type='VMWare'):
        hosts = self._client.create_host(
            host_name=host_name,
            hosttype=host_type
        )
        self.attach_port_to_host(host_name=hosts[0].id, wwpn=wwpn)
        return hosts[0].id

    def delete_host(self, host_name):
        # delete a host will delete all the attached host ports.
        return self._client.delete_host(host_name=host_name)

    def attach_port_to_host(self, host_name, wwpn):
        return self._get_attach_or_create_host_port(
            host_name=host_name,
            wwpn=wwpn,
        )

    def detach_port_from_host(self, wwpn):
        return self._client.delete_host_port(port_id=wwpn)

    def map_volume_to_host(self, host_name, volume_id, lunid=''):
        return self._client.map_volume_to_host(
            host_name=host_name,
            volume_id=volume_id,
            lunid=int_to_scsilun(lunid) if lunid else ''
        )[0]

    def unmap_volume_from_host(self, host_name, lunid):
        return self._client.unmap_volume_from_host(
            host_name=host_name,
            lunid=int_to_scsilun(lunid)
        )

    def _get_attach_or_create_host_port(self, host_name, wwpn):
        try:
            host_port = self._client.get_host_port(port_id=wwpn)
            return self._client.update_host_port_change_host(
                port_id=host_port.id,
                host_name=host_name
            )
        except NotFound:
            logger.debug(
                'host port {} is not found, creating new...'.format(wwpn)
            )
            return self._client.create_host_port(
                port_id=wwpn,
                host_name=host_name
            )
