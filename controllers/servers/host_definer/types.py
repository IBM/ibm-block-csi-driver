from dataclasses import dataclass, field
from controllers.servers.csi.controller_types import ArrayConnectionInfo


@dataclass
class DefineHostRequest:
    prefix: str = ''
    connectivity_type: str = ''
    node_id: str = ''
    array_connection_info: ArrayConnectionInfo = ArrayConnectionInfo(array_addresses='', user='', password='')


@dataclass
class DefineHostResponse:
    error_message: str = ''


@dataclass
class SecretInfo:
    name: str = ''
    namespace: str = ''


@dataclass
class HostDefinitionInfo:
    name: str = ''
    secret_name: str = ''
    secret_namespace: str = ''
    node_name: str = ''
    node_id: str = ''
    phase: str = ''
    resource_version: str = ''
    uid: str = ''


@dataclass
class CsiNodeInfo:
    name: str = ''
    node_id: str = ''


@dataclass
class PodInfo:
    name: str = ''
    node_name: str = ''


@dataclass
class NodeInfo:
    name: str = ''


@dataclass
class StorageClassInfo:
    name: str = ''
    provisioner: str = ''
    parameters: dict = field(default_factory=dict)
