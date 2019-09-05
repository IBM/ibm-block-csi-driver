class Volume:
    def __init__(self, vol_size_bytes, vol_id, vol_name, array_address, pool_name, array_type):
        self.capacity_bytes = vol_size_bytes
        self.id = vol_id
        self.volume_name = vol_name
        self.array_address = array_address
        self.pool_name = pool_name
        self.array_type = array_type
