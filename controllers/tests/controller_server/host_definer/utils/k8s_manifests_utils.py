import controllers.tests.controller_server.host_definer.settings as settings
from controllers.servers.settings import (SECRET_ARRAY_PARAMETER,
                                          SECRET_PASSWORD_PARAMETER,
                                          SECRET_USERNAME_PARAMETER)


def get_k8s_csi_node_manifest(csi_provisioner):
    return {
        settings.METADATA: {
            settings.NAME: settings.FAKE_NODE_NAME
        },
        settings.SPEC: {
            settings.DRIVERS: [{
                settings.NAME: csi_provisioner,
                settings.NODE_ID_FIELD_IN_CSI_NODE: settings.FAKE_NODE_ID
            }]
        },
    }


def get_fake_k8s_daemon_set_manifest(updated_pods, desired_updated_pods):
    return {
        settings.METADATA: {
            settings.NAME: settings.FAKE_DAEMON_SET_NAME
        },
        settings.STATUS: {
            settings.UPDATED_PODS: updated_pods,
            settings.DESIRED_UPDATED_PODS: desired_updated_pods,
        }
    }


def get_fake_k8s_pod_manifest():
    return {
        settings.METADATA: {
            settings.NAME: settings.FAKE_NODE_PODS_NAME
        },
        settings.SPEC: {
            settings.NODE_NAME_FIELD_IN_PODS: settings.FAKE_NODE_NAME
        }
    }


def get_fake_k8s_host_manifest(host_definition_phase):
    return{
        settings.METADATA: {
            settings.NAME: settings.FAKE_NODE_NAME,
            settings.RESOURCE_VERSION: settings.FAKE_RESOURCE_VERSION,
            settings.UID: settings.FAKE_UID
        },
        settings.SPEC: {
            settings.HOST_DEFINITION_FIELD: {
                settings.SECRET_NAME_FIELD: settings.FAKE_SECRET,
                settings.SECRET_NAMESPACE_FIELD: settings.FAKE_SECRET_NAMESPACE,
                settings.NODE_NAME_FIELD_HOST_DEFINITION: settings.FAKE_NODE_NAME,
                settings.NODE_ID_FIELD_IN_HOST_DEFINITION: settings.FAKE_NODE_ID
            }
        },
        settings.STATUS: {
            settings.PHASE: host_definition_phase
        }
    }


def get_fake_k8s_node_manifest():
    return {
        settings.METADATA: {
            settings.NAME: settings.FAKE_NODE_NAME
        }
    }


def get_fake_k8s_secret_manifest():
    return {
        settings.METADATA: {
            settings.NAME: settings.FAKE_SECRET,
            settings.NAMESPACE: settings.FAKE_SECRET_NAMESPACE
        },
        settings.DATA: {
            SECRET_ARRAY_PARAMETER: settings.FAKE_SECRET_ARRAY,
            SECRET_PASSWORD_PARAMETER: settings.FAKE_SECRET_PASSWORD,
            SECRET_USERNAME_PARAMETER: settings.FAKE_SECRET_USER_NAME
        }
    }


def get_fake_k8s_storage_class_manifest(provisioner):
    return {
        settings.METADATA: {
            settings.NAME: settings.FAKE_STORAGE_CLASS,
        },
        settings.PROVISIONER_FIELD: provisioner,
        settings.PARAMETERS_FIELD: {
            settings.STORAGE_CLASS_SECRET_FIELD: settings.FAKE_SECRET,
            settings.STORAGE_CLASS_SECRET_NAMESPACE_FIELD: settings.FAKE_SECRET_NAMESPACE
        }
    }
