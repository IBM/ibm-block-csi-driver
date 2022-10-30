import controllers.tests.controller_server.host_definer.settings as settings
from controllers.servers.settings import (SECRET_ARRAY_PARAMETER,
                                          SECRET_PASSWORD_PARAMETER,
                                          SECRET_USERNAME_PARAMETER)


def get_k8s_csi_node_manifest(csi_provisioner):
    metadata_manifest = _generate_metadata_manifest(settings.FAKE_NODE_NAME)
    k8s_csi_node_spec = {
        settings.SPEC: {
            settings.DRIVERS: [{
                settings.NAME: csi_provisioner,
                settings.NODE_ID_FIELD_IN_CSI_NODE: settings.FAKE_NODE_ID
            }]
        },
    }
    return _merge_dicts(metadata_manifest, k8s_csi_node_spec)


def get_fake_k8s_daemon_set_manifest(updated_pods, desired_updated_pods):
    metadata_manifest = _generate_metadata_manifest(settings.FAKE_NODE_PODS_NAME)
    k8s_daemon_set_status = {
        settings.STATUS: {
            settings.UPDATED_PODS: updated_pods,
            settings.DESIRED_UPDATED_PODS: desired_updated_pods,
        }}
    return _merge_dicts(metadata_manifest, k8s_daemon_set_status)


def get_fake_k8s_pod_manifest():
    metadata_manifest = _generate_metadata_manifest(settings.FAKE_NODE_PODS_NAME)
    k8s_pod_spec = {
        settings.SPEC: {
            settings.NODE_NAME_FIELD_IN_PODS: settings.FAKE_NODE_NAME
        }}
    return _merge_dicts(metadata_manifest, k8s_pod_spec)


def get_fake_k8s_host_definition_manifest(host_definition_phase):
    metadata_manifest = _generate_metadata_manifest(settings.FAKE_NODE_NAME)
    status_phase_manifest = get_status_phase_manifest(host_definition_phase)
    merged_dicts = _merge_dicts(metadata_manifest, status_phase_manifest)
    k8s_host_definition_body = {
        settings.SPEC: {
            settings.HOST_DEFINITION_FIELD: {
                settings.SECRET_NAME_FIELD: settings.FAKE_SECRET,
                settings.SECRET_NAMESPACE_FIELD: settings.FAKE_SECRET_NAMESPACE,
                settings.NODE_NAME_FIELD_HOST_DEFINITION: settings.FAKE_NODE_NAME,
                settings.NODE_ID_FIELD_IN_HOST_DEFINITION: settings.FAKE_NODE_ID
            }
        }}
    return _merge_dicts(merged_dicts, k8s_host_definition_body)


def get_status_phase_manifest(phase):
    return {
        settings.STATUS: {
            settings.PHASE: phase
        }
    }


def get_fake_k8s_node_manifest(label):
    metadata_manifest = _generate_metadata_manifest(settings.FAKE_NODE_NAME)
    metadata_manifest[settings.METADATA][settings.LABELS] = {label: settings.TRUE_STRING}
    return metadata_manifest


def get_fake_k8s_secret_manifest():
    metadata_manifest = _generate_metadata_manifest(settings.FAKE_SECRET)
    metadata_manifest[settings.METADATA][settings.NAMESPACE] = settings.FAKE_SECRET_NAMESPACE
    secret_data_manifest = {
        settings.DATA: {
            SECRET_ARRAY_PARAMETER: settings.FAKE_SECRET_ARRAY,
            SECRET_PASSWORD_PARAMETER: settings.FAKE_SECRET_PASSWORD,
            SECRET_USERNAME_PARAMETER: settings.FAKE_SECRET_USER_NAME
        }}
    return _merge_dicts(metadata_manifest, secret_data_manifest)


def get_fake_k8s_storage_class_manifest(provisioner):
    metadata_manifest = _generate_metadata_manifest(settings.FAKE_STORAGE_CLASS)
    k8s_storage_class_body = {
        settings.PROVISIONER_FIELD: provisioner,
        settings.PARAMETERS_FIELD: {
            settings.STORAGE_CLASS_SECRET_FIELD: settings.FAKE_SECRET,
            settings.STORAGE_CLASS_SECRET_NAMESPACE_FIELD: settings.FAKE_SECRET_NAMESPACE
        }}
    return _merge_dicts(metadata_manifest, k8s_storage_class_body)


def _generate_metadata_manifest(object_name):
    metadata_manifest = _get_metadata_manifest()
    metadata_manifest[settings.METADATA][settings.NAME] = object_name
    return metadata_manifest


def _get_metadata_manifest():
    return {
        settings.METADATA: {
            settings.RESOURCE_VERSION: settings.FAKE_RESOURCE_VERSION,
            settings.UID: settings.FAKE_UID
        }}


def _merge_dicts(dict1, dict2):
    return {**dict1, **dict2}


def generate_watch_event(event_type, object_function):
    return {
        settings.TYPE: event_type,
        settings.OBJECT: object_function
    }
