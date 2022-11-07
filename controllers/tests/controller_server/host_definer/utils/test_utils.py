from dataclasses import dataclass, field
import func_timeout
from munch import Munch
from mock import patch, Mock

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


def get_fake_k8s_csi_nodes(csi_provisioner):
    k8s_csi_node_manifest = manifest_utils.get_k8s_csi_node_manifest(csi_provisioner)
    return K8sResourceItems([Munch.fromDict(k8s_csi_node_manifest)])


def get_fake_k8s_csi_node(csi_provisioner):
    csi_node_manifest = manifest_utils.get_k8s_csi_node_manifest(csi_provisioner)
    return Munch.fromDict(csi_node_manifest)


def get_fake_csi_node_watch_event(event_type):
    return manifest_utils.generate_watch_event(event_type,
                                               manifest_utils.get_k8s_csi_node_manifest(settings.CSI_PROVISIONER_NAME))


def get_fake_k8s_node(label):
    return Munch.fromDict(manifest_utils.get_fake_k8s_node_manifest(label))


def get_fake_k8s_daemon_set_items(updated_pods, desired_updated_pods):
    k8s_daemon_set_manifest = manifest_utils.get_fake_k8s_daemon_set_manifest(updated_pods, desired_updated_pods)
    return K8sResourceItems([Munch.fromDict(k8s_daemon_set_manifest)])


def get_empty_k8s_pods():
    return K8sResourceItems()


def get_fake_k8s_pods_items():
    k8s_pod_manifest = manifest_utils.get_fake_k8s_pod_manifest()
    return K8sResourceItems([Munch.fromDict(k8s_pod_manifest)])


def get_empty_k8s_host_definitions():
    return K8sResourceItems()


def get_fake_k8s_host_definitions_items(host_definition_phase):
    return K8sResourceItems([_get_fake_k8s_host_definitions(host_definition_phase)])


def _get_fake_k8s_host_definitions(host_definition_phase):
    return Munch.fromDict(manifest_utils.get_fake_k8s_host_definition_manifest(host_definition_phase))


def get_fake_host_definition_watch_event(event_type, host_definition_phase):
    return manifest_utils.generate_watch_event(
        event_type, manifest_utils.get_fake_k8s_host_definition_manifest(host_definition_phase))


def get_fake_node_watch_event(event_type):
    return manifest_utils.generate_watch_event(event_type,
                                               manifest_utils.get_fake_k8s_node_manifest(settings.MANAGE_NODE_LABEL))


def get_fake_k8s_nodes_items():
    k8s_node_manifest = manifest_utils.get_fake_k8s_node_manifest(settings.MANAGE_NODE_LABEL)
    return K8sResourceItems([Munch.fromDict(k8s_node_manifest)])


def get_fake_secret_watch_event(event_type):
    return manifest_utils.generate_watch_event(event_type,
                                               manifest_utils.get_fake_k8s_secret_manifest())


def get_fake_k8s_secret():
    return Munch.fromDict(manifest_utils.get_fake_k8s_secret_manifest())


def get_fake_k8s_storage_class_items(provisioner):
    k8s_storage_classes_manifest = manifest_utils.get_fake_k8s_storage_class_manifest(provisioner)
    return K8sResourceItems([Munch.fromDict(k8s_storage_classes_manifest)])


def get_fake_secret_storage_event(event_type, provisioner):
    return manifest_utils.generate_watch_event(event_type,
                                               manifest_utils.get_fake_k8s_storage_class_manifest(provisioner))


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
    return patch('{}.NODES'.format(module_path), {}).start()


def patch_managed_secrets_global_variable(module_path):
    return patch('{}.MANAGED_SECRETS'.format(module_path), []).start()


def get_pending_creation_status_manifest():
    return manifest_utils.get_status_phase_manifest(settings.PENDING_CREATION_PHASE)


def get_ready_status_manifest():
    return manifest_utils.get_status_phase_manifest(settings.READY_PHASE)


def get_fake_secret_info():
    secret_info = Mock(spec_set=['name', 'namespace', 'nodes_with_system_id', 'managed_storage_classes'])
    secret_info.name = settings.FAKE_SECRET
    secret_info.namespace = settings.FAKE_SECRET_NAMESPACE
    secret_info.nodes_with_system_id = {}
    secret_info.managed_storage_classes = 1
    return secret_info
