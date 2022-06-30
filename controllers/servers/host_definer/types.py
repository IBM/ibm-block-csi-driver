from dataclasses import dataclass, field


@dataclass
class DefineHostRequest:
    prefix: str = field(default_factory=str)
    secret_name: str = field(default_factory=str)
    secret_namespace: str = field(default_factory=str)
    connectivity_type: str = field(default_factory=str)
    node_id: str = field(default_factory=str)
    system_info: dict = field(default_factory=dict)


@dataclass
class DefineHostResponse:
    error_message: str = field(default_factory=str)


@dataclass
class HostDefinition:
    name: str = field(default_factory=str)
    secret_name: str = field(default_factory=str)
    secret_namespace: str = field(default_factory=str)
    node_name: str = field(default_factory=str)
    node_id: str = field(default_factory=str)
    management_address: str = field(default_factory=str)
    phase: str = field(default_factory=str)


@dataclass
class CsiNode:
    name: str = field(default_factory=str)
    node_id: str = field(default_factory=str)
