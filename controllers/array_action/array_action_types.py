from dataclasses import dataclass, field

from controllers.common.node_info import Initiators


@dataclass
class Volume:
    capacity_bytes: int
    id: str
    internal_id: str
    name: str
    array_address: str
    source_id: str
    array_type: str
    pool: str
    space_efficiency_aliases: set = field(default_factory=set)


@dataclass
class VolumeGroup:
    name: str
    id: str
    volumes: list = field(default_factory=list)


@dataclass
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


@dataclass
class Host:
    name: str
    connectivity_types: list = field(repr=False)
    nvme_nqns: list = field(default_factory=list, repr=False)
    fc_wwns: list = field(default_factory=list, repr=False)
    iscsi_iqns: list = field(default_factory=list, repr=False)
    initiators: Initiators = field(init=False)

    def __post_init__(self):
        self.initiators = Initiators(nvme_nqns=self.nvme_nqns, fc_wwns=self.fc_wwns, iscsi_iqns=self.iscsi_iqns)


@dataclass
class ObjectIds:
    internal_id: str = ''
    uid: str = ''

    def __bool__(self):
        return bool(self.internal_id or self.uid)
