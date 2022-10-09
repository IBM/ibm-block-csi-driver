import unittest
from dataclasses import dataclass, field
import func_timeout
from munch import Munch
from mock import patch, Mock

from controllers.servers.host_definer.types import CsiNodeInfo
import controllers.servers.host_definer.messages as messages
import controllers.tests.controller_server.host_definer.utils.k8s_manifests_utils as manifest_utils
import controllers.tests.controller_server.host_definer.settings as settings
from controllers.servers.host_definer.kubernetes_manager.manager import KubernetesManager


@dataclass
class K8sResourceItems():
    items: list = field(default_factory=list)


class HttpResp():
    def __init__(self, status, data, reason):
        self.status = status
        self.data = data
        self.reason = reason

    def getheaders(self):
        return None


def get_fake_csi_node_info():
    csi_node_info = CsiNodeInfo()
    csi_node_info.name = settings.FAKE_NODE_NAME
    csi_node_info.node_id = settings.FAKE_NODE_ID
    return csi_node_info


def get_fake_k8s_csi_nodes(csi_provisioner):
    k8s_csi_node = K8sResourceItems()
    k8s_csi_node.items = [Munch.fromDict(manifest_utils.get_k8s_csi_node_manifest(csi_provisioner))]
    return k8s_csi_node


def get_fake_k8s_csi_node(csi_provisioner):
    csi_node_manifest = manifest_utils.get_k8s_csi_node_manifest(csi_provisioner)
    return Munch.fromDict(csi_node_manifest)


def get_fake_csi_node_watch_event(event_type):
    return {
        settings.TYPE: event_type,
        settings.OBJECT: manifest_utils.get_k8s_csi_node_manifest(settings.CSI_PROVISIONER_NAME)
    }


def get_fake_k8s_node(label):
    node_manifest = {
        settings.METADATA: {
            settings.NAME: settings.FAKE_NODE_NAME,
            settings.LABELS: {label: settings.TRUE_STRING}
        }
    }
    return Munch.fromDict(node_manifest)


def get_fake_k8s_daemon_set_items(updated_pods, desired_updated_pods):
    k8s_daemon_set = K8sResourceItems()
    k8s_daemon_set_manifest = manifest_utils.get_fake_k8s_daemon_set_manifest(updated_pods, desired_updated_pods)
    k8s_daemon_set.items = [Munch.fromDict(k8s_daemon_set_manifest)]
    return k8s_daemon_set


def get_no_k8s_pods_items():
    k8s_pod = K8sResourceItems()
    k8s_pod.items = []
    return k8s_pod


def get_fake_k8s_pods_items():
    k8s_pod = K8sResourceItems()
    k8s_pod.items = [Munch.fromDict(manifest_utils.get_fake_k8s_pod_manifest())]
    return k8s_pod


def get_empty_k8s_host_definitions_items():
    k8s_host_definition = K8sResourceItems()
    k8s_host_definition.items = []
    return k8s_host_definition


def get_fake_k8s_host_definitions_items(host_definition_phase):
    k8s_host_definition = K8sResourceItems()
    k8s_host_definition.items = [_get_fake_k8s_host_definitions(host_definition_phase)]
    return k8s_host_definition


def _get_fake_k8s_host_definitions(host_definition_phase):
    return Munch.fromDict(manifest_utils.get_fake_k8s_host_manifest(host_definition_phase))


def get_fake_host_definition_watch_event(event_type, host_definition_phase):
    return {
        settings.TYPE: event_type,
        settings.OBJECT: manifest_utils.get_fake_k8s_host_manifest(host_definition_phase)
    }


def get_fake_node_watch_event(event_type):
    return {
        settings.TYPE: event_type,
        settings.OBJECT: manifest_utils.get_fake_k8s_node_manifest()
    }


def get_fake_k8s_nodes_items():
    k8s_nodes = K8sResourceItems()
    k8s_nodes.items = [Munch.fromDict(manifest_utils.get_fake_k8s_node_manifest())]
    return k8s_nodes


def get_fake_secret_watch_event(event_type):
    return {
        settings.TYPE: event_type,
        settings.OBJECT: manifest_utils.get_fake_k8s_secret_manifest()
    }


def get_fake_k8s_secret():
    return Munch.fromDict(manifest_utils.get_fake_k8s_secret_manifest())


def get_fake_k8s_storage_class_items(provisioner):
    k8s_storage_classes = K8sResourceItems()
    k8s_storage_classes.items = [Munch.fromDict(manifest_utils.get_fake_k8s_storage_class_manifest(provisioner))]
    return k8s_storage_classes


def get_fake_secret_storage_event(event_type, provisioner):
    return {
        settings.TYPE: event_type,
        settings.OBJECT: manifest_utils.get_fake_k8s_storage_class_manifest(provisioner)
    }


def patch_pending_variables():
    for pending_var, value in settings.HOST_DEFINITION_PENDING_VARS.items():
        patch('{}.{}'.format(
            settings.SETTINGS_PATH, pending_var), value).start()


def patch_kubernetes_manager_init():
    for function_to_patch in settings.KUBERNETES_MANAGER_INIT_FUNCTIONS_TO_PATCH:
        _patch_function(KubernetesManager, function_to_patch)


def _patch_function(class_type, function):
    patcher = patch.object(class_type, function)
    patcher.start()


def get_class_mock(class_type):
    class_type_dict = _get_class_dict(class_type)
    class_mock = _get_class(class_type, class_type_dict)
    return _mock_class_vars(class_mock)


def _get_class_dict(class_type):
    class_type_copy = class_type.__dict__.copy()
    return class_type_copy


def _get_class(class_type, class_type_dict):
    return type(_get_dummy_class_name(class_type), (class_type,), class_type_dict)


def _get_dummy_class_name(class_type):
    return 'dummy_{}'.format(class_type.__name__)


def _mock_class_vars(class_type):
    class_instance = class_type()
    for method in vars(class_instance):
        class_instance.__dict__[method] = Mock()
    return class_instance


def run_function_with_timeout(function, max_wait):
    try:
        func_timeout.func_timeout(max_wait, function)
    except func_timeout.FunctionTimedOut:
        pass


def get_error_http_resp():
    return HttpResp(405, 'some problem', 'some reason')


def patch_nodes_global_variable(module_path):
    return patch('{}.NODES'.format(module_path), {settings.FAKE_NODE_NAME: settings.FAKE_NODE_ID}).start()


def patch_secret_ids_global_variable(module_path):
    return patch('{}.SECRET_IDS'.format(module_path), {settings.FAKE_SECRET_ID: 1}).start()


def assert_fail_to_update_label_log_message(http_resp_data, records):
    unittest.TestCase().assertIn(messages.FAILED_TO_UPDATE_NODE_LABEL.format(
        settings.FAKE_NODE_NAME, settings.MANAGE_NODE_LABEL, http_resp_data), records)
