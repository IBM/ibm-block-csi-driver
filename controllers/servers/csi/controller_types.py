from dataclasses import dataclass, field, InitVar

from controllers.array_action.array_action_types import ObjectIds


@dataclass
class ArrayConnectionInfo:
    array_addresses: list
    user: str
    password: str
    system_id: str = None


@dataclass
class ObjectIdInfo:
    array_type: str
    system_id: str
    internal_id: InitVar[str]
    uid: InitVar[str]
    ids: ObjectIds = field(init=False)

    def __post_init__(self, internal_id, uid):
        self.ids = ObjectIds(internal_id=internal_id, uid=uid)


@dataclass
class ObjectParameters:
    pool: str
    space_efficiency: str
    prefix: str
    io_group: str
    volume_group: str
    virt_snap_func: bool
