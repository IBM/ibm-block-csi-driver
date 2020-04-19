class Volume:
    def __init__(self, vol_size_bytes, vol_id, vol_name, array_address, pool_name, array_type):
        self.capacity_bytes = vol_size_bytes
        self.id = vol_id
        self.volume_name = vol_name
        self.array_address = array_address
        self.pool_name = pool_name
        self.array_type = array_type


class Snapshot:
    def __init__(self, capacity_bytes, snapshot_id, snapshot_name, array_address, volume_name, is_ready, array_type):
        self.capacity_bytes = capacity_bytes
        self.id = snapshot_id
        self.snapshot_name = snapshot_name
        self.array_address = array_address
        self.volume_name = volume_name
        self.is_ready = is_ready
        self.array_type = array_type
