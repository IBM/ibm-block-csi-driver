from dataclasses import dataclass


@dataclass()
class Volume:
    capacity_bytes: int
    id: str
    internal_id: str
    name: str
    array_address: str
    pool: str
    copy_source_id: str
    array_type: str
    space_efficiency: str = None
    default_space_efficiency: str = None


@dataclass()
class Snapshot:
    capacity_bytes: int
    id: str
    internal_id: str
    name: str
    array_address: str
    source_volume_id: str
    is_ready: bool
    array_type: str


class Host:
    def __init__(self, host_id, host_name, iscsi_names, wwns):
        self.id = host_id
        self.name = host_name
        self.iscsi_names = iscsi_names
        self.wwns = wwns


class Replication:
    def __init__(self, name, volume_internal_id, other_volume_internal_id, copy_type, is_primary, is_ready):
        self.name = name
        self.volume_internal_id = volume_internal_id
        self.other_volume_internal_id = other_volume_internal_id
        self.copy_type = copy_type
        self.is_primary = is_primary
        self.is_ready = is_ready
