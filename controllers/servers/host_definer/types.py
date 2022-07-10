from dataclasses import dataclass, field


@dataclass
class DefineHostRequest:
    prefix: str = ''
    connectivity_type: str = ''
    node_id: str = ''
    system_info: dict = field(default_factory=dict)


@dataclass
class DefineHostResponse:
    error_message: str = ''


@dataclass
class Secret:
    name: str = ''
    namespace: str = ''


@dataclass
class HostDefinition:
    name: str = ''
    secret: Secret = Secret()
    node_name: str = ''
    node_id: str = ''
    phase: str = ''
    resource_version: str = ''
    uid: str = ''


@dataclass
class CsiNode:
    name: str = ''
    node_id: str = ''
