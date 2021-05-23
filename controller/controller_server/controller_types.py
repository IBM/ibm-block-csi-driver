from dataclasses import dataclass


@dataclass
class ArrayConnectionInfo:
    user: str
    password: str
    array_addresses: list
    uid: str = None


@dataclass
class VolumeIdInfo:
    array_type: str
    volume_id: str
    secret_uid: str


@dataclass
class VolumeParameters:
    pool: str
    space_efficiency: str
    prefix: str
