import controllers.tests.controller_server.host_definer.settings as settings
from controllers.servers.settings import (SECRET_ARRAY_PARAMETER,
                                          SECRET_PASSWORD_PARAMETER,
                                          SECRET_USERNAME_PARAMETER)


def get_k8s_csi_node_manifest(csi_provisioner):
    k8s_csi_node_spec = {
        settings.SPEC_FIELD: {
            settings.STORAGE_CLASS_DRIVERS_FIELD: [{
                settings.NAME_FIELD: csi_provisioner,
                settings.CSI_NODE_NODE_ID_FIELD: settings.FAKE_NODE_ID
            }]
        },
    }
    return _generate_manifest(settings.FAKE_NODE_NAME, k8s_csi_node_spec)


def get_fake_k8s_daemon_set_manifest(updated_pods, desired_updated_pods):
    k8s_daemon_set_status = {
        settings.STATUS_FIELD: {
            settings.UPDATED_PODS: updated_pods,
            settings.DESIRED_UPDATED_PODS: desired_updated_pods,
        }}
    return _generate_manifest(settings.FAKE_NODE_PODS_NAME, k8s_daemon_set_status)


def get_fake_k8s_pod_manifest():
    k8s_pod_spec = {
        settings.SPEC_FIELD: {
            settings.POD_NODE_NAME_FIELD: settings.FAKE_NODE_NAME
        }}
    return _generate_manifest(settings.FAKE_NODE_PODS_NAME, k8s_pod_spec)


def get_fake_k8s_host_definition_manifest(host_definition_phase):
    status_phase_manifest = get_status_phase_manifest(host_definition_phase)
    k8s_host_definition_body = {
        settings.SPEC_FIELD: {
            settings.HOST_DEFINITION_FIELD: {
                settings.SECRET_NAME_FIELD: settings.FAKE_SECRET,
                settings.SECRET_NAMESPACE_FIELD: settings.FAKE_SECRET_NAMESPACE,
                settings.HOST_DEFINITION_NODE_NAME_FIELD: settings.FAKE_NODE_NAME,
                settings.HOST_DEFINITION_NODE_ID_FIELD: settings.FAKE_NODE_ID
            }
        }}
    return _generate_manifest(settings.FAKE_NODE_NAME, status_phase_manifest, k8s_host_definition_body)


def get_status_phase_manifest(phase):
    return {
        settings.STATUS_FIELD: {
            settings.STATUS_PHASE_FIELD: phase
        }
    }


def get_fake_k8s_node_manifest(label):
    node_manifest = _generate_manifest(settings.FAKE_NODE_NAME)
    node_manifest[settings.METADATA_FIELD][settings.NODE_LABELS_FIELD] = {label: settings.TRUE_STRING}
    return node_manifest


def get_fake_k8s_secret_manifest():
    secret_data_manifest = {
        settings.SECRET_DATA_FIELD: {
            SECRET_ARRAY_PARAMETER: settings.FAKE_SECRET_ARRAY,
            SECRET_PASSWORD_PARAMETER: settings.FAKE_SECRET_PASSWORD,
            SECRET_USERNAME_PARAMETER: settings.FAKE_SECRET_USER_NAME
        }}
    secret_manifest = _generate_manifest(settings.FAKE_SECRET, secret_data_manifest)
    secret_manifest[settings.METADATA_FIELD][settings.NAMESPACE_FIELD] = settings.FAKE_SECRET_NAMESPACE
    return secret_manifest


def get_fake_k8s_storage_class_manifest(provisioner):
    k8s_storage_class_body = {
        settings.STORAGE_CLASS_PROVISIONER_FIELD: provisioner,
        settings.STORAGE_CLASS_PARAMETERS_FIELD: {
            settings.STORAGE_CLASS_SECRET_FIELD: settings.FAKE_SECRET,
            settings.STORAGE_CLASS_SECRET_NAMESPACE_FIELD: settings.FAKE_SECRET_NAMESPACE
        }}
    return _generate_manifest(settings.FAKE_STORAGE_CLASS, k8s_storage_class_body)


def _generate_manifest(object_name, *extra_dicts):
    metadata_manifest = _get_metadata_manifest()
    metadata_manifest[settings.METADATA_FIELD][settings.NAME_FIELD] = object_name
    if len(extra_dicts) > 0:
        merged_dicts = _merge_dicts(metadata_manifest, extra_dicts[0])
    else:
        return metadata_manifest
    for extra_dict in extra_dicts[1:]:
        merged_dicts = _merge_dicts(merged_dicts, extra_dict)
    return merged_dicts


def _get_metadata_manifest():
    return {
        settings.METADATA_FIELD: {
            settings.METADATA_RESOURCE_VERSION_FIELD: settings.FAKE_RESOURCE_VERSION,
            settings.METADATA_UID_FIELD: settings.FAKE_UID
        }}


def _merge_dicts(dict1, dict2):
    return {**dict1, **dict2}


def generate_watch_event(event_type, object_function):
    return {
        settings.EVENT_TYPE_FIELD: event_type,
        settings.EVENT_OBJECT_FIELD: object_function
    }
