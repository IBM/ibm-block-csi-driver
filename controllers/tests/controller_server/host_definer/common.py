import unittest
import logging
from mock import patch
from kubernetes.client.rest import ApiException

from controllers.common.csi_logger import get_stdout_logger
import controllers.tests.controller_server.host_definer.utils as utils
from controllers.servers.host_definer.watcher.csi_node_watcher import CsiNodeWatcher
from controllers.servers.host_definer.watcher.host_definition_watcher import HostDefinitionWatcher
from controllers.servers.host_definer.watcher.node_watcher import NodeWatcher
from controllers.servers.host_definer.watcher.secret_watcher import SecretWatcher
from controllers.servers.host_definer.watcher.storage_class_watcher import StorageClassWatcher
import controllers.tests.controller_server.host_definer.settings as settings


class MockLogHandler(logging.Handler):
    records = []

    def emit(self, record):
        self.records.append(record.getMessage())


class BaseSetUp(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        logger = get_stdout_logger()
        cls._mock_logger = MockLogHandler(level='DEBUG')
        logger.addHandler(cls._mock_logger)

    def setUp(self):
        utils.patch_kubernetes_manager_init()
        self.csi_node_watcher = utils.get_class_mock(CsiNodeWatcher)
        self.host_definition_watcher = utils.get_class_mock(HostDefinitionWatcher)
        self.node_watcher = utils.get_class_mock(NodeWatcher)
        self.secret_watcher = utils.get_class_mock(SecretWatcher)
        self.storage_class_watcher = utils.get_class_mock(StorageClassWatcher)

        self._mock_logger.records = []
        self.mock_os = patch('{}.os'.format(settings.WATCHER_HELPER_PATH)).start()
        self.mock_nodes_on_csi_node_watcher = utils.patch_nodes_global_variable(settings.CSI_NODE_WATCHER_PATH)
        self.mock_nodes_on_watcher_helper = utils.patch_nodes_global_variable(settings.WATCHER_HELPER_PATH)
        self.mock_secret_ids_on_csi_node_watcher = utils.patch_secret_ids_global_variable(
            settings.CSI_NODE_WATCHER_PATH)
        self.mock_secret_ids_on_watcher_helper = utils.patch_secret_ids_global_variable(settings.WATCHER_HELPER_PATH)
        self.mock_secret_ids_on_secret_watcher = utils.patch_secret_ids_global_variable(settings.SECRET_WATCHER_PATH)
        self.mock_secret_ids_on_storage_class_watcher = utils.patch_secret_ids_global_variable(
            settings.STORAGE_CLASS_WATCHER_PATH)
        self.fake_csi_node_info = utils.get_fake_csi_node_info()
        self.fake_csi_nodes_info = [self.fake_csi_node_info]
        self.fake_k8s_csi_node_with_ibm_block = utils.get_fake_k8s_csi_node(settings.CSI_PROVISIONER_NAME)
        self.fake_k8s_csi_node_without_ibm_block = utils.get_fake_k8s_csi_node(settings.FAKE_CSI_PROVISIONER)
        self.fake_k8s_csi_nodes_with_ibm_block = utils.get_fake_k8s_csi_nodes(settings.CSI_PROVISIONER_NAME)
        self.fake_k8s_csi_nodes_without_ibm_block = utils.get_fake_k8s_csi_nodes(settings.FAKE_CSI_PROVISIONER)
        self.fake_k8s_node_with_manage_node_label = utils.get_fake_k8s_node(settings.MANAGE_NODE_LABEL)
        self.fake_k8s_node_with_forbid_deletion_label = utils.get_fake_k8s_node(settings.FORBID_DELETION_LABEL)
        self.fake_k8s_node_with_fake_label = utils.get_fake_k8s_node(settings.FAKE_LABEL)
        self.fake_updated_daemon_set = utils.get_fake_k8s_daemon_set_items(1, 1)
        self.fake_not_updated_daemon_set = utils.get_fake_k8s_daemon_set_items(0, 1)
        self.fake_deleted_daemon_set = utils.get_fake_k8s_daemon_set_items(0, 0)
        self.fake_k8s_pods = utils.get_fake_k8s_pods_items()
        self.no_k8s_pods = utils.get_no_k8s_pods_items()
        self.fake_ready_k8s_host_definitions = utils.get_fake_k8s_host_definitions_items(settings.READY_PHASE)
        self.fake_pending_deletion_k8s_host_definitions = utils.get_fake_k8s_host_definitions_items(
            settings.PENDING_DELETION_PHASE)
        self.fake_empty_k8s_host_definitions = utils.get_empty_k8s_host_definitions_items()
        self.fake_k8s_secret = utils.get_fake_k8s_secret()
        self.http_resp = utils.get_error_http_resp()
        self.fake_api_exception = ApiException(http_resp=self.http_resp)
        self.fake_k8s_nodes = utils.get_fake_k8s_nodes_items()
        self.fake_k8s_storage_classes_with_ibm_block = utils.get_fake_k8s_storage_class_items(
            settings.CSI_PROVISIONER_NAME)
        self.fake_k8s_storage_classes_without_ibm_block = utils.get_fake_k8s_storage_class_items(
            settings.FAKE_CSI_PROVISIONER)
