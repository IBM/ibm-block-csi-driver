from dataclasses import dataclass, field
from controllers.servers.csi.controller_types import ArrayConnectionInfo
from controllers.servers.host_definer.utils import utils


@dataclass
class DefineHostRequest:
    prefix: str = ''
    connectivity_type_from_user: str = ''
    node_id_from_host_definition: str = ''
    node_id_from_csi_node: str = ''
    array_connection_info: ArrayConnectionInfo = ArrayConnectionInfo(array_addresses='', user='', password='')
    io_group: str = ''


@dataclass
class DefineHostResponse:
    error_message: str = ''
    connectivity_type: str = ''
    ports: list = field(default_factory=list)
    node_name_on_storage: str = ''
    io_group: list = field(default_factory=list)
    management_address: str = ''


@dataclass
class SecretInfo:
    name: str
    namespace: str
    nodes_with_system_id: dict
    system_ids_topologies: dict
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


class ManagedNode:
    def __init__(self, csi_node_info, labels):
        self.name = csi_node_info.name
        self.node_id = csi_node_info.node_id
        self.io_group = utils.generate_io_group_from_labels(labels)
