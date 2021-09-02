from dataclasses import dataclass


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
    internal_id: str
    object_id: str


@dataclass
class ObjectParameters:
    pool: str
    space_efficiency: str
    prefix: str
