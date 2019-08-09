class Volume:
    def __init__(self, vol_size_bytes, vol_id, vol_name, array_name, pool_name, array_type):
        self.capacity_bytes = vol_size_bytes
        self.id = vol_id
        self.volume_name = vol_name
        self.array_name = array_name
        self.pool_name = pool_name
        self.array_type = array_type


class Host:
    def __init__(self, host_id, name, array, array_type, host_type="", status="", iqns=None, wwpns=None):
        self.id = host_id
        self.name = name
        self.array = array
        self.array_type = array_type
        self.host_type = host_type
        self.status = status
        self.iqns = iqns
        self.wwpns = wwpns


class IscsiTarget:
    def __init__(self, ip_address, iqn, array, array_type):
        self.ip_address = ip_address
        self.iqn = iqn
        self.array = array
        self.array_type = array_type
