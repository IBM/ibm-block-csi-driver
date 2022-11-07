from dataclasses import dataclass, field
from controllers.servers.csi.controller_types import ArrayConnectionInfo


@dataclass
class DefineHostRequest:
    prefix: str = ''
    connectivity_type_from_user: str = ''
    node_id_from_host_definition: str = ''
    node_id_from_csi_node: str = ''
    array_connection_info: ArrayConnectionInfo = ArrayConnectionInfo(array_addresses='', user='', password='')


@dataclass
class DefineHostResponse:
    error_message: str = ''
    connectivity_type: str = ''
    ports: list = field(default_factory=list)
    node_name_on_storage: str = ''


@dataclass
class SecretInfo:
    name: str
    namespace: str
    nodes_with_system_id: dict = field(default_factory=dict)
    managed_storage_classes: int = 0


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
    connectivity_type: str = ''
    ports: list = field(default_factory=list)
    node_name_on_storage: str = ''


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
    name: str
    labels: dict


@dataclass
class StorageClassInfo:
    name: str = ''
    provisioner: str = ''
    parameters: dict = field(default_factory=dict)
