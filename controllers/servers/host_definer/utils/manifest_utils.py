from controllers.servers.host_definer import settings
import controllers.common.settings as common_settings


def get_host_definition_manifest(host_definition_info, response, node_id):
    return {
        settings.API_VERSION: settings.CSI_IBM_API_VERSION,
        settings.KIND: settings.HOST_DEFINITION_KIND,
        settings.METADATA: {
            common_settings.NAME_FIELD: host_definition_info.name,
        },
        settings.SPEC: {
            settings.HOST_DEFINITION_FIELD: {
                settings.NODE_NAME_FIELD: host_definition_info.node_name,
                common_settings.HOST_DEFINITION_NODE_ID_FIELD: node_id,
                settings.SECRET_NAME_FIELD: host_definition_info.secret_name,
                settings.SECRET_NAMESPACE_FIELD: host_definition_info.secret_namespace,
                settings.CONNECTIVITY_TYPE_FIELD: response.connectivity_type,
                settings.PORTS_FIELD: response.ports,
                settings.NODE_NAME_ON_STORAGE_FIELD: response.node_name_on_storage,
                settings.IO_GROUP_FIELD: response.io_group,
                settings.MANAGEMENT_ADDRESS_FIELD: response.management_address
            },
        },
    }


def get_host_definition_status_manifest(host_definition_phase):
    return {
        settings.STATUS: {
            settings.PHASE: host_definition_phase,
        }
    }


def get_body_manifest_for_labels(label_value):
    return {
        settings.METADATA: {
            settings.LABELS: {
                settings.MANAGE_NODE_LABEL: label_value}
        }
    }


def get_finalizer_manifest(host_definition_name, finalizers):
    return {
        settings.METADATA: {
            common_settings.NAME_FIELD: host_definition_name,
            settings.FINALIZERS: finalizers,
        }
    }
