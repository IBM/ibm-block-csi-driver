from dataclasses import dataclass, field


@dataclass
class VerifyHostRequest:
    name: str = field(default_factory=str)
    secret_name: str = field(default_factory=str)
    secret_namespace: str = field(default_factory=str)
    connectivity_type: str = field(default_factory=str)
    system_info: dict = field(default_factory=dict)


@dataclass
class VerifyHostResponse:
    error_message: str = field(default_factory=str)
