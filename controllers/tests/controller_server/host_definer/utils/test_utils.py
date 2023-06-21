from dataclasses import dataclass, field
import func_timeout
from munch import Munch
from kubernetes import client
from mock import patch, Mock

from controllers.servers.host_definer.types import DefineHostRequest, DefineHostResponse
from controllers.servers.csi.controller_types import ArrayConnectionInfo
from controllers.servers.host_definer.k8s.api import K8SApi
from controllers.tests.common.test_settings import HOST_NAME, SECRET_MANAGEMENT_ADDRESS_VALUE
import controllers.tests.controller_server.host_definer.settings as test_settings
import controllers.common.settings as common_settings
import controllers.tests.controller_server.host_definer.utils.k8s_manifests_utils as test_manifest_utils


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


def get_fake_k8s_csi_nodes(csi_provisioner_name, number_of_csi_nodes):
    k8s_csi_nodes = []
    for csi_node_index in range(number_of_csi_nodes):
        k8s_csi_node_manifest = test_manifest_utils.get_k8s_csi_node_manifest(
            csi_provisioner_name, '-{}'.format(csi_node_index))
        k8s_csi_nodes.append(Munch.fromDict(k8s_csi_node_manifest))
    return K8sResourceItems(k8s_csi_nodes)


def get_fake_k8s_csi_node(csi_provisioner_name=""):
    csi_node_manifest = test_manifest_utils.get_k8s_csi_node_manifest(csi_provisioner_name)
    return Munch.fromDict(csi_node_manifest)


def get_fake_csi_node_watch_event(event_type):
    return test_manifest_utils.generate_watch_event(event_type, test_manifest_utils.get_k8s_csi_node_manifest(
        common_settings.CSI_PROVISIONER_NAME))


def get_fake_k8s_node(label):
    return Munch.fromDict(test_manifest_utils.get_fake_k8s_node_manifest(label))


def get_fake_k8s_daemon_set_items(updated_pods, desired_updated_pods):
    return K8sResourceItems([get_fake_k8s_daemon_set(updated_pods, desired_updated_pods)])


def get_fake_k8s_daemon_set(updated_pods, desired_updated_pods):
    k8s_daemon_set_manifest = test_manifest_utils.get_fake_k8s_daemon_set_manifest(updated_pods, desired_updated_pods)
    return Munch.fromDict(k8s_daemon_set_manifest)


def get_empty_k8s_pods():
    return K8sResourceItems()


def get_fake_k8s_pods_items(number_of_pods=1):
    k8s_pods = []
    for pod_index in range(number_of_pods):
        k8s_pod_manifest = test_manifest_utils.get_fake_k8s_pod_manifest('-{}'.format(pod_index))
        k8s_pods.append(Munch.fromDict(k8s_pod_manifest))
    return K8sResourceItems(k8s_pods)


def get_empty_k8s_host_definitions():
    return K8sResourceItems()


def get_fake_k8s_host_definitions_items(host_definition_phase='ready'):
    return K8sResourceItems([get_fake_k8s_host_definition(host_definition_phase)])


def get_fake_k8s_host_definition(host_definition_phase):
    return Munch.fromDict(test_manifest_utils.get_fake_k8s_host_definition_manifest(host_definition_phase))


def get_fake_host_definition_watch_event(event_type):
    return test_manifest_utils.generate_watch_event(
        event_type, test_manifest_utils.get_fake_k8s_host_definition_manifest())


def get_fake_node_watch_event(event_type):
    return test_manifest_utils.generate_watch_event(event_type, test_manifest_utils.get_fake_k8s_node_manifest(
        common_settings.MANAGE_NODE_LABEL))


def get_fake_k8s_nodes_items():
    k8s_node_manifest = test_manifest_utils.get_fake_k8s_node_manifest(common_settings.MANAGE_NODE_LABEL)
    return K8sResourceItems([Munch.fromDict(k8s_node_manifest)])


def get_fake_secret_watch_event(event_type):
    return test_manifest_utils.generate_watch_event(event_type,
                                                    test_manifest_utils.get_fake_k8s_secret_manifest())


def get_fake_k8s_secret():
    return Munch.fromDict(test_manifest_utils.get_fake_k8s_secret_manifest())


def get_fake_k8s_storage_class_items(provisioner):
    k8s_storage_classes_manifest = test_manifest_utils.get_fake_k8s_storage_class_manifest(provisioner)
    return K8sResourceItems([Munch.fromDict(k8s_storage_classes_manifest)])


def get_fake_k8s_storage_class(provisioner):
    k8s_storage_classes_manifest = test_manifest_utils.get_fake_k8s_storage_class_manifest(provisioner)
    return Munch.fromDict(k8s_storage_classes_manifest)


def get_fake_storage_class_watch_event(event_type, provisioner='provisioner'):
    return test_manifest_utils.generate_watch_event(
        event_type, test_manifest_utils.get_fake_k8s_storage_class_manifest(provisioner))


def patch_pending_variables():
    for pending_var, value in test_settings.HOST_DEFINITION_PENDING_VARS.items():
        patch('{}.{}'.format(
            test_settings.SETTINGS_PATH, pending_var), value).start()


def patch_k8s_api_init():
    for function_to_patch in test_settings.KUBERNETES_MANAGER_INIT_FUNCTIONS_TO_PATCH:
        patch_function(K8SApi, function_to_patch)


def patch_function(class_type, function):
    patcher = patch.object(class_type, function)
    patcher.start()


def run_function_with_timeout(function, max_wait):
    try:
        func_timeout.func_timeout(max_wait, function)
    except func_timeout.FunctionTimedOut:
        pass


def get_error_http_resp(status_code):
    return HttpResp(status_code, 'some problem', 'some reason')


def patch_nodes_global_variable(module_path):
    return patch('{}.NODES'.format(module_path), {}).start()


def patch_managed_secrets_global_variable(module_path):
    return patch('{}.MANAGED_SECRETS'.format(module_path), []).start()


def get_pending_creation_status_manifest():
    return test_manifest_utils.get_status_phase_manifest(common_settings.PENDING_CREATION_PHASE)


def get_ready_status_manifest():
    return test_manifest_utils.get_status_phase_manifest(common_settings.READY_PHASE)


def get_array_connection_info():
    return ArrayConnectionInfo(
        [test_settings.FAKE_SECRET_ARRAY],
        test_settings.FAKE_SECRET_USER_NAME, test_settings.FAKE_SECRET_PASSWORD)


def get_define_request(prefix='', connectivity_type='', node_id_from_host_definition=''):
    return DefineHostRequest(
        prefix, connectivity_type, node_id_from_host_definition, test_settings.FAKE_NODE_ID,
        get_array_connection_info(),
        test_settings.FAKE_STRING_IO_GROUP)


def get_define_response(connectivity_type, ports):
    return DefineHostResponse(
        '', connectivity_type, ports, HOST_NAME, get_fake_host_io_group_id(),
        SECRET_MANAGEMENT_ADDRESS_VALUE)


def get_fake_secret_info(managed_storage_classes=0):
    secret_info = Mock(spec_set=['name', 'namespace', 'nodes_with_system_id',
                       'system_ids_topologies', 'managed_storage_classes'])
    secret_info.name = test_settings.FAKE_SECRET
    secret_info.namespace = test_settings.FAKE_SECRET_NAMESPACE
    secret_info.nodes_with_system_id = {test_settings.FAKE_NODE_NAME: test_settings.FAKE_SYSTEM_ID}
    secret_info.system_ids_topologies = {test_settings.FAKE_NODE_NAME: test_settings.FAKE_TOPOLOGY_LABELS}
    secret_info.managed_storage_classes = managed_storage_classes
    return secret_info


def get_fake_host_io_group_id():
    io_group_ids = get_fake_host_io_group().id
    return [int(io_group_id) for io_group_id in io_group_ids]


def get_fake_host_io_group():
    return Munch.fromDict(test_manifest_utils.get_host_io_group_manifest())


def get_fake_empty_k8s_list():
    much_object = Munch.fromDict(test_manifest_utils.get_empty_k8s_list_manifest())
    much_object.items = []
    return much_object


def get_fake_managed_node():
    managed_node = Mock(spec_set=['name', 'node_id', 'io_group'])
    managed_node.name = test_settings.FAKE_NODE_NAME
    managed_node.node_id = test_settings.FAKE_NODE_ID
    managed_node.io_group = test_settings.FAKE_STRING_IO_GROUP
    return managed_node


def get_fake_csi_node_info():
    csi_node_info = Mock(spec_set=['name', 'node_id'])
    csi_node_info.name = test_settings.FAKE_NODE_NAME
    csi_node_info.node_id = test_settings.FAKE_NODE_ID
    return csi_node_info


def get_fake_node_info():
    node_info = Mock(spec_set=['name', 'labels'])
    node_info.name = test_settings.FAKE_NODE_NAME
    node_info.labels = {common_settings.MANAGE_NODE_LABEL: common_settings.TRUE_STRING}
    return node_info


def get_fake_storage_class_info():
    storage_class_info = Mock(spec_set=['name', 'provisioner', 'parameters'])
    storage_class_info.name = test_settings.FAKE_STORAGE_CLASS
    storage_class_info.provisioner = common_settings.CSI_PROVISIONER_NAME
    storage_class_info.parameters = test_settings.FAKE_STORAGE_CLASS_PARAMETERS
    return storage_class_info


def get_fake_host_definition_info():
    host_definition_info = Mock(spec_set=['name', 'resource_version', 'uid', 'phase', 'secret_name',
                                          'secret_namespace', 'node_name', 'node_id', 'connectivity_type'])
    host_definition_info.name = test_settings.FAKE_NODE_NAME
    host_definition_info.resource_version = test_settings.FAKE_RESOURCE_VERSION
    host_definition_info.uid = test_settings.FAKE_UID
    host_definition_info.phase = common_settings.READY_PHASE
    host_definition_info.secret_name = test_settings.FAKE_SECRET
    host_definition_info.secret_namespace = test_settings.FAKE_SECRET_NAMESPACE
    host_definition_info.node_name = test_settings.FAKE_NODE_NAME
    host_definition_info.node_id = test_settings.FAKE_NODE_ID
    host_definition_info.connectivity_type = test_settings.FAKE_CONNECTIVITY_TYPE
    return host_definition_info


def get_fake_empty_host_definition_info():
    host_definition_info = Mock(spec_set=['name', 'node_name', 'node_id'])
    host_definition_info.name = ''
    host_definition_info.node_name = ''
    host_definition_info.node_id = ''
    return host_definition_info


def get_object_reference():
    return client.V1ObjectReference(
        api_version=common_settings.CSI_IBM_API_VERSION, kind=common_settings.HOST_DEFINITION_KIND,
        name=test_settings.FAKE_NODE_NAME, resource_version=test_settings.FAKE_RESOURCE_VERSION,
        uid=test_settings.FAKE_UID, )


def get_event_object_metadata():
    return client.V1ObjectMeta(generate_name='{}.'.format(test_settings.FAKE_NODE_NAME), )


def get_fake_define_host_response():
    response = Mock(spec_set=['error_message', 'connectivity_type', 'ports',
                    'node_name_on_storage', 'io_group', 'management_address'])
    response.error_message = test_settings.MESSAGE
    response.connectivity_type = test_settings.FAKE_CONNECTIVITY_TYPE
    response.ports = test_settings.FAKE_FC_PORTS
    response.node_name_on_storage = test_settings.FAKE_NODE_NAME
    response.io_group = test_settings.IO_GROUP_IDS
    response.management_address = test_settings.FAKE_SECRET_ARRAY
    return response


def get_fake_io_group_labels(number_of_io_groups):
    labels = {}
    for index in range(number_of_io_groups):
        labels[test_settings.IO_GROUP_LABEL_PREFIX + str(index)] = common_settings.TRUE_STRING
    return labels


def get_fake_k8s_metadata():
    return Munch.fromDict(test_manifest_utils.get_metadata_manifest())


def get_fake_array_connectivity_info():
    array_connectivity_info = Mock(spec_set=['array_addresses', 'user', 'password', 'system_id'])
    array_connectivity_info.array_addresses = [test_settings.FAKE_SECRET_ARRAY]
    array_connectivity_info.user = test_settings.FAKE_SECRET_USER_NAME
    array_connectivity_info.password = test_settings.FAKE_SECRET_PASSWORD
    array_connectivity_info.system_id = '2'
    return array_connectivity_info


def get_fake_pod_info():
    pod_info = Mock(spec_set=['name', 'node_name'])
    pod_info.name = test_settings.FAKE_NODE_PODS_NAME
    pod_info.node_name = test_settings.FAKE_NODE_NAME
    return pod_info


def convert_manifest_to_munch(manifest):
    return Munch.fromDict(manifest)
