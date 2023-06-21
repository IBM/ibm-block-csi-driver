from controllers.servers.host_definer.k8s.api import K8SApi
from controllers.servers.host_definer.resource_manager.host_definition import HostDefinitionManager
from controllers.servers.host_definer.resource_manager.secret import SecretManager
from controllers.servers.host_definer.resource_manager.node import NodeManager
from controllers.servers.host_definer.resource_manager.csi_node import CSINodeManager
from controllers.servers.host_definer.definition_manager.definition import DefinitionManager
from controllers.servers.host_definer.resource_manager.resource_info import ResourceInfoManager
from controllers.servers.host_definer.resource_manager.storage_class import StorageClassManager


class Watcher():
    def __init__(self):
        super().__init__()
        self.k8s_api = K8SApi()
        self.host_definition_manager = HostDefinitionManager()
        self.secret_manager = SecretManager()
        self.definition_manager = DefinitionManager()
        self.node_manager = NodeManager()
        self.csi_node = CSINodeManager()
        self.resource_info_manager = ResourceInfoManager()
        self.storage_class_manager = StorageClassManager()
