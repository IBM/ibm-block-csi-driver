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
