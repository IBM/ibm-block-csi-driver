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


def get_fake_k8s_pod_manifest(pod_suffix=''):
    k8s_pod_spec = {
        test_settings.SPEC_FIELD: {
            test_settings.POD_NODE_NAME_FIELD: test_settings.FAKE_NODE_NAME
        }}
    return _generate_manifest(test_settings.FAKE_NODE_PODS_NAME + pod_suffix, k8s_pod_spec)


def get_fake_k8s_host_definition_manifest(host_definition_phase='ready'):
    status_phase_manifest = get_status_phase_manifest(host_definition_phase)
    fields_manifest = get_fake_k8s_host_definition_response_fields_manifest()
    k8s_host_definition_body = {
        test_settings.API_VERSION_FIELD: test_settings.CSI_IBM_API_VERSION,
        test_settings.KIND_FIELD: test_settings.HOST_DEFINITION_KIND,
        test_settings.SPEC_FIELD: {
            test_settings.HOST_DEFINITION_FIELD: {
                test_settings.HOST_DEFINITION_NODE_NAME_FIELD: test_settings.FAKE_NODE_NAME,
                test_settings.SECRET_NAME_FIELD: test_settings.FAKE_SECRET,
                test_settings.SECRET_NAMESPACE_FIELD: test_settings.FAKE_SECRET_NAMESPACE,
                common_settings.HOST_DEFINITION_NODE_ID_FIELD: test_settings.FAKE_NODE_ID,
            }
        }}
    k8s_host_definition_body[test_settings.SPEC_FIELD][test_settings.HOST_DEFINITION_FIELD].update(
        fields_manifest[test_settings.SPEC_FIELD][test_settings.HOST_DEFINITION_FIELD])
    return _generate_manifest(test_settings.FAKE_NODE_NAME, status_phase_manifest, k8s_host_definition_body)


def get_fake_k8s_host_definition_response_fields_manifest():
    manifest = {
        test_settings.SPEC_FIELD: {
            test_settings.HOST_DEFINITION_FIELD: {
                test_settings.NODE_NAME_ON_STORAGE_FIELD: test_settings.FAKE_NODE_NAME,
                test_settings.CONNECTIVITY_TYPE_FIELD: test_settings.FAKE_CONNECTIVITY_TYPE,
                test_settings.PORTS_FIELD: test_settings.FAKE_FC_PORTS,
                test_settings.IO_GROUP_FIELD: test_settings.IO_GROUP_IDS,
                test_settings.MANAGEMENT_ADDRESS_FIELD: test_settings.FAKE_SECRET_ARRAY
            }
        }
    }
    return _generate_manifest(test_settings.FAKE_NODE_NAME, manifest)


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
        test_settings.STORAGE_CLASS_PARAMETERS_FIELD: test_settings.FAKE_STORAGE_CLASS_PARAMETERS}
    return _generate_manifest(test_settings.FAKE_STORAGE_CLASS, k8s_storage_class_body)


def _generate_manifest(object_name, *extra_dicts):
    metadata_manifest = get_metadata_manifest()
    metadata_manifest[test_settings.METADATA_FIELD][common_settings.NAME_FIELD] = object_name
    if len(extra_dicts) > 0:
        merged_dicts = _merge_dicts(metadata_manifest, extra_dicts[0])
    else:
        return metadata_manifest
    for extra_dict in extra_dicts[1:]:
        merged_dicts = _merge_dicts(merged_dicts, extra_dict)
    return merged_dicts


def get_metadata_manifest():
    return {
        test_settings.METADATA_FIELD: {
            common_settings.RESOURCE_VERSION_FIELD: test_settings.FAKE_RESOURCE_VERSION,
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


def get_empty_k8s_list_manifest():
    return {
        common_settings.ITEMS_FIELD: [],
        test_settings.METADATA_FIELD: {
            common_settings.RESOURCE_VERSION_FIELD
        }
    }


def get_finalizers_manifest(finalizers):
    return {
        test_settings.METADATA_FIELD: {
            common_settings.NAME_FIELD: test_settings.FAKE_NODE_NAME,
            test_settings.FINALIZERS_FIELD: finalizers,
        }
    }


def get_general_labels_manifest(labels):
    return {
        test_settings.METADATA_FIELD: {
            test_settings.NODE_LABELS_FIELD: labels
        }
    }
