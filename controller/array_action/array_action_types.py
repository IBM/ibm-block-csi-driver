from enum import Enum


class VolumeSrcObjectType(Enum):
    UNKNOWN = 0
    VOLUME = 1
    SNAPSHOT = 2


class Volume:
    def __init__(self, vol_size_bytes, vol_id, vol_name, array_address, pool_name, copy_src_object_type,
                 copy_src_object_id, array_type):
        self.capacity_bytes = vol_size_bytes
        self.id = vol_id
        self.volume_name = vol_name
        self.array_address = array_address
        self.pool_name = pool_name
        self.copy_src_object_type = copy_src_object_type
        self.copy_src_object_id = copy_src_object_id
        self.array_type = array_type

    def is_copy_of_snapshpot(self, snapshot_id):
        return _self.copy_src_object_type in (VolumeSrcObjectType.SNAPSHOT, VolumeSrcObjectType.UNKNOWN) and \
               self.copy_src_object_id = snapshot_id

class Snapshot:
    def __init__(self, capacity_bytes, snapshot_id, snapshot_name, array_address, volume_name, is_ready, array_type):
        self.capacity_bytes = capacity_bytes
        self.id = snapshot_id
        self.snapshot_name = snapshot_name
        self.array_address = array_address
        self.volume_name = volume_name
        self.is_ready = is_ready
        self.array_type = array_type
