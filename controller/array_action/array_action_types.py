from dataclasses import dataclass


class MembersPrintable:
    def __str__(self):
        return "<{}: {}>".format(self.__class__.__name__,
                                 " ".join("{}={!r}".format(k, v) for k, v in self.__dict__.items()), )


@dataclass()
class Volume:
    capacity_bytes: int
    id: str
    storage_id: str
    name: str
    array_address: str
    pool: str
    copy_source_id: str
    array_type: str
    space_efficiency: str = None
    default_space_efficiency: str = None


class Snapshot(MembersPrintable):
    def __init__(self, capacity_bytes, snapshot_id, snapshot_name, array_address, volume_id, is_ready, array_type):
        self.capacity_bytes = capacity_bytes
        self.id = snapshot_id
        self.name = snapshot_name
        self.array_address = array_address
        self.source_volume_id = volume_id
        self.is_ready = is_ready
        self.array_type = array_type


class Host:
    def __init__(self, host_id, host_name, iscsi_names, wwns):
        self.id = host_id
        self.name = host_name
        self.iscsi_names = iscsi_names
        self.wwns = wwns
