from threading import Thread

from controllers.common.csi_logger import get_stdout_logger
from controllers.servers.host_definer.globals import MANAGED_SECRETS, NODES
from controllers.servers.host_definer.watcher.watcher_helper import Watcher
import controllers.servers.host_definer.messages as messages
from controllers.servers.host_definer import settings
from controllers.servers.host_definer.utils import utils

logger = get_stdout_logger()


class CsiNodeWatcher(Watcher):

    def add_initial_csi_nodes(self):
        csi_nodes_info = self.csi_node.get_csi_nodes_info_with_driver()
        for csi_node_info in csi_nodes_info:
            if self.node_manager.is_node_can_be_defined(csi_node_info.name):
                self.node_manager.add_node_to_nodes(csi_node_info)

    def watch_csi_nodes_resources(self):
        while utils.loop_forever():
            stream = self.k8s_api.get_csi_node_stream()
            for watch_event in stream:
                watch_event = utils.munch(watch_event)
                csi_node_info = self.resource_info_manager.generate_csi_node_info(watch_event.object)
                if (watch_event.type == settings.DELETED_EVENT) and (csi_node_info.name in NODES):
                    self._handle_deleted_csi_node_pod(csi_node_info)
                elif watch_event.type == settings.MODIFIED_EVENT:
                    self._handle_modified_csi_node(csi_node_info)

    def _handle_modified_csi_node(self, csi_node_info):
        if self._is_new_csi_node(csi_node_info):
            self.node_manager.add_node_to_nodes(csi_node_info)
            self.definition_manager.define_node_on_all_storages(csi_node_info.name)
        elif csi_node_info.name in NODES:
            self._handle_deleted_csi_node_pod(csi_node_info)

    def _is_new_csi_node(self, csi_node_info):
        return csi_node_info.node_id and self.node_manager.is_node_can_be_defined(csi_node_info.name) and \
            csi_node_info.name not in NODES

    def _handle_deleted_csi_node_pod(self, csi_node_info):
        if self.node_manager.is_node_has_manage_node_label(csi_node_info.name):
            remove_host_thread = Thread(target=self._undefine_host_when_node_pod_is_deleted, args=(csi_node_info,))
            remove_host_thread.start()

    def _undefine_host_when_node_pod_is_deleted(self, csi_node_info):
        node_name = csi_node_info.name
        if self.csi_node.is_host_part_of_update(node_name):
            self._create_definitions_when_csi_node_changed(csi_node_info)
        elif utils.is_host_definer_can_delete_hosts() and \
                not self.node_manager.is_node_has_forbid_deletion_label(node_name):
            self._undefine_all_the_definitions_of_a_node(csi_node_info.name)
        else:
            NODES.pop(node_name, None)

    def _create_definitions_when_csi_node_changed(self, csi_node_info):
        for secret_info in MANAGED_SECRETS:
            secret_name, secret_namespace = secret_info.name, secret_info.namespace
            host_definition_info = self.host_definition_manager.get_matching_host_definition_info(
                csi_node_info.name, secret_name, secret_namespace)
            if host_definition_info and self.csi_node.is_node_id_changed(
                    host_definition_info.node_id, csi_node_info.node_id):
                logger.info(messages.NODE_ID_WAS_CHANGED.format(csi_node_info.name,
                            host_definition_info.node_id, csi_node_info.node_id))
                NODES[csi_node_info.name] = self.node_manager.generate_managed_node(csi_node_info)
                self.definition_manager.create_definition(host_definition_info)

    def _undefine_all_the_definitions_of_a_node(self, node_name):
        self.definition_manager.undefine_node_definitions(node_name)
        self.node_manager.remove_manage_node_label(node_name)
        NODES.pop(node_name, None)
