import controllers.common.settings as common_settings
import controllers.tests.controller_server.host_definer.settings as test_settings
from controllers.servers.settings import (SECRET_ARRAY_PARAMETER,
                                          SECRET_PASSWORD_PARAMETER,
                                          SECRET_USERNAME_PARAMETER)


def get_k8s_csi_node_manifest(csi_provisioner_name, csi_node_suffix=''):
    k8s_csi_node_spec = {
        test_settings.SPEC_FIELD: {
            test_settings.STORAGE_CLASS_DRIVERS_FIELD: [{
                common_settings.NAME_FIELD: csi_provisioner_name,
                test_settings.CSI_NODE_NODE_ID_FIELD: test_settings.FAKE_NODE_ID
            }]
        },
    }
    return _generate_manifest(test_settings.FAKE_NODE_NAME + csi_node_suffix, k8s_csi_node_spec)


def get_fake_k8s_daemon_set_manifest(updated_pods, desired_updated_pods):
    k8s_daemon_set_status = {
        test_settings.STATUS_FIELD: {
            test_settings.UPDATED_PODS: updated_pods,
            test_settings.DESIRED_UPDATED_PODS: desired_updated_pods,
        }}
    return _generate_manifest(test_settings.FAKE_NODE_PODS_NAME, k8s_daemon_set_status)


def get_fake_k8s_pod_manifest():
    k8s_pod_spec = {
        test_settings.SPEC_FIELD: {
            test_settings.POD_NODE_NAME_FIELD: test_settings.FAKE_NODE_NAME
        }}
    return _generate_manifest(test_settings.FAKE_NODE_PODS_NAME, k8s_pod_spec)


def get_fake_k8s_host_definition_manifest(host_definition_phase):
    status_phase_manifest = get_status_phase_manifest(host_definition_phase)
    k8s_host_definition_body = {
        test_settings.SPEC_FIELD: {
            test_settings.HOST_DEFINITION_FIELD: {
                test_settings.SECRET_NAME_FIELD: test_settings.FAKE_SECRET,
                test_settings.SECRET_NAMESPACE_FIELD: test_settings.FAKE_SECRET_NAMESPACE,
                test_settings.HOST_DEFINITION_NODE_NAME_FIELD: test_settings.FAKE_NODE_NAME,
                common_settings.HOST_DEFINITION_NODE_ID_FIELD: test_settings.FAKE_NODE_ID
            }
        }}
    return _generate_manifest(test_settings.FAKE_NODE_NAME, status_phase_manifest, k8s_host_definition_body)


def get_status_phase_manifest(phase):
    return {
        test_settings.STATUS_FIELD: {
            test_settings.STATUS_PHASE_FIELD: phase
        }
    }


def get_fake_k8s_node_manifest(label):
    node_manifest = _generate_manifest(test_settings.FAKE_NODE_NAME)
    node_manifest[test_settings.METADATA_FIELD][test_settings.NODE_LABELS_FIELD] = {
        label: test_settings.TRUE_STRING,
        common_settings.IO_GROUP_LABEL_PREFIX + str(0): test_settings.TRUE_STRING,
        common_settings.IO_GROUP_LABEL_PREFIX + str(2): test_settings.TRUE_STRING}
    return node_manifest


def get_fake_k8s_secret_manifest():
    secret_data_manifest = {
        test_settings.SECRET_DATA_FIELD: {
            SECRET_ARRAY_PARAMETER: test_settings.FAKE_SECRET_ARRAY,
            SECRET_PASSWORD_PARAMETER: test_settings.FAKE_SECRET_PASSWORD,
            SECRET_USERNAME_PARAMETER: test_settings.FAKE_SECRET_USER_NAME
        }}
    secret_manifest = _generate_manifest(test_settings.FAKE_SECRET, secret_data_manifest)
    secret_manifest[test_settings.METADATA_FIELD][common_settings.NAMESPACE_FIELD] = test_settings.FAKE_SECRET_NAMESPACE
    return secret_manifest


def get_fake_k8s_storage_class_manifest(provisioner):
    k8s_storage_class_body = {
        test_settings.STORAGE_CLASS_PROVISIONER_FIELD: provisioner,
        test_settings.STORAGE_CLASS_PARAMETERS_FIELD: {
            test_settings.STORAGE_CLASS_SECRET_FIELD: test_settings.FAKE_SECRET,
            test_settings.STORAGE_CLASS_SECRET_NAMESPACE_FIELD: test_settings.FAKE_SECRET_NAMESPACE
        }}
    return _generate_manifest(test_settings.FAKE_STORAGE_CLASS, k8s_storage_class_body)


def _generate_manifest(object_name, *extra_dicts):
    metadata_manifest = _get_metadata_manifest()
    metadata_manifest[test_settings.METADATA_FIELD][common_settings.NAME_FIELD] = object_name
    if len(extra_dicts) > 0:
        merged_dicts = _merge_dicts(metadata_manifest, extra_dicts[0])
    else:
        return metadata_manifest
    for extra_dict in extra_dicts[1:]:
        merged_dicts = _merge_dicts(merged_dicts, extra_dict)
    return merged_dicts


def _get_metadata_manifest():
    return {
        test_settings.METADATA_FIELD: {
            test_settings.METADATA_RESOURCE_VERSION_FIELD: test_settings.FAKE_RESOURCE_VERSION,
            test_settings.METADATA_UID_FIELD: test_settings.FAKE_UID
        }}


def _merge_dicts(dict1, dict2):
    return {**dict1, **dict2}


def generate_watch_event(event_type, object_function):
    return {
        test_settings.EVENT_TYPE_FIELD: event_type,
        test_settings.EVENT_OBJECT_FIELD: object_function
    }


def get_metadata_with_manage_node_labels_manifest(label_value):
    return {
        test_settings.METADATA_FIELD: {
            test_settings.NODE_LABELS_FIELD: {test_settings.MANAGE_NODE_LABEL: label_value}
        }
    }


def get_host_io_group_manifest():
    return {
        test_settings.IO_GROUP_ID_FIELD: test_settings.IO_GROUP_IDS,
        common_settings.NAME_FIELD: test_settings.IO_GROUP_NAMES
    }
