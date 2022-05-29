from dataclasses import dataclass, field, InitVar

from controller.common.common_types import ObjectIds


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
    object_uid: InitVar[str]
    object_ids: ObjectIds = field(init=False)

    def __post_init__(self, internal_id, object_uid):
        self.object_ids = ObjectIds(internal_id=internal_id, object_uid=object_uid)


@dataclass
class ObjectParameters:
    pool: str
    space_efficiency: str
    prefix: str
    io_group: str
