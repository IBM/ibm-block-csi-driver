from threading import Thread

from common import utils
from storage_manager.host import StorageHostManager
from watcher.csi_node_watcher import CsiNodeWatcher
from watcher.secret_watcher import SecretWatcher
from watcher.storage_class_watcher import StorageClassWatcher
from watcher.csi_host_definition_watcher import CsiHostDefinitionWatcher

logger = utils.get_stdout_logger()

class HostDefinerManager:
    def __init__(self):
        self.storage_host_manager = StorageHostManager()
        self.secret_watcher = SecretWatcher()
        self.storage_class_watcher = StorageClassWatcher()
        self.csi_node_watcher = CsiNodeWatcher()
        self.csi_host_definition_watcher = CsiHostDefinitionWatcher()

    def start_host_definition(self):
        print('starting host definer')
        self._start_watchers()

    def _start_watchers(self):
        watchers = [
            self.secret_watcher.watch_secret_resources,
            self.storage_class_watcher.watch_storage_class_resources,
            self.csi_node_watcher.watch_csi_nodes_resources,
            self.csi_host_definition_watcher.watch_csi_host_definitions_resources]
        threads = []
        for index in range(len(watchers)):
            threads.append(Thread(target=self._thread_wrapper,
                                  args=(watchers[index],)))
            threads[index].start()

    def _thread_wrapper(self, thread_func):
        while True:
            try:
                thread_func()
            except Exception as ex:
                logger.error('Restarting thread, got Error: {}'.format(ex))
