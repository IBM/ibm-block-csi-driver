from dataclasses import dataclass

from controller.common.node_info import Initiators


@dataclass()
class Volume:
    capacity_bytes: int
    id: str
    internal_id: str
    name: str
    array_address: str
    source_id: str
    array_type: str
    pool: str
    space_efficiency: str = None
    default_space_efficiency: str = None


@dataclass()
class Snapshot(Volume):
    pool: str = None
    is_ready: bool = False


class Replication:
    def __init__(self, name, volume_internal_id, other_volume_internal_id, copy_type, is_ready, is_primary=None):
        self.name = name
        self.volume_internal_id = volume_internal_id
        self.other_volume_internal_id = other_volume_internal_id
        self.copy_type = copy_type
        self.is_ready = is_ready
        self.is_primary = is_primary


@dataclass()
class Host:
    name: str
    connectivity_types: list
    initiators: Initiators = None
