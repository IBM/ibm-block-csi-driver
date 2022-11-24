from dataclasses import dataclass, field
from controllers.servers.csi.controller_types import ArrayConnectionInfo
from controllers.servers.host_definer import settings


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
        self.io_group = self._generate_io_group_from_labels(labels)

    def _generate_io_group_from_labels(self, labels):
        io_group = ''
        for io_group_index in range(4):
            label_content = labels.get(settings.IO_GROUP_LABEL_PREFIX + str(io_group_index))
            if label_content == settings.TRUE_STRING:
                if io_group:
                    io_group += settings.IO_GROUP_DELIMITER
                io_group += str(io_group_index)
        return io_group
