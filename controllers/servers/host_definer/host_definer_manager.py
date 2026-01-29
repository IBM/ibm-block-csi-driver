from threading import Thread

from controllers.common.csi_logger import get_stdout_logger
from controllers.servers.host_definer.storage_manager.host_definer_server import HostDefinerServicer
from controllers.servers.host_definer.watcher.csi_node_watcher import CsiNodeWatcher
from controllers.servers.host_definer.watcher.secret_watcher import SecretWatcher
from controllers.servers.host_definer.watcher.storage_class_watcher import StorageClassWatcher
from controllers.servers.host_definer.watcher.host_definition_watcher import HostDefinitionWatcher
from controllers.servers.host_definer.watcher.node_watcher import NodeWatcher

logger = get_stdout_logger()


class HostDefinerManager:
    def __init__(self):
        self.storage_host_servicer = HostDefinerServicer()
        self.secret_watcher = SecretWatcher()
        self.storage_class_watcher = StorageClassWatcher()
        self.csi_node_watcher = CsiNodeWatcher()
        self.host_definition_watcher = HostDefinitionWatcher()
        self.node_watcher = NodeWatcher()

    def start_host_definition(self):
        logger.info('starting host definer')
        self.csi_node_watcher.add_initial_csi_nodes()
        self.storage_class_watcher.add_initial_storage_classes()
        self.node_watcher.add_initial_nodes()
        self._start_watchers()

    def _start_watchers(self):
        watchers = (
            self.csi_node_watcher.watch_csi_nodes_resources,
            self.host_definition_watcher.watch_host_definitions_resources,
            self.secret_watcher.watch_secret_resources,
            self.node_watcher.watch_nodes_resources,
            self.storage_class_watcher.watch_storage_class_resources)
        for watch_function in watchers:
            thread = Thread(target=watch_function,)
            thread.start()
