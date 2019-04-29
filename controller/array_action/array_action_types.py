class Volume:
    def __init__(self, vol_size_bytes, vol_id, vol_name, array_name, pool_name, storage_type):
        self.capacity_bytes = vol_size_bytes
        self.volume_id = vol_id
        self.volume_name = vol_name
        self.array_name = array_name
        self.pool_name = pool_name
        self.storage_type = storage_type
