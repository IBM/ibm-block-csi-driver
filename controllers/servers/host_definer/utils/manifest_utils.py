from controllers.servers.host_definer import settings
import controllers.common.settings as common_settings


def get_host_definition_manifest(host_definition_info, response, node_id):
    manifest = generate_host_definition_response_fields_manifest(host_definition_info.name, response)
    manifest[settings.API_VERSION] = settings.CSI_IBM_API_VERSION
    manifest[settings.KIND] = settings.HOST_DEFINITION_KIND
    manifest[settings.SPEC][settings.HOST_DEFINITION_FIELD][settings.NODE_NAME_FIELD] = host_definition_info.node_name
    manifest[settings.SPEC][settings.HOST_DEFINITION_FIELD][common_settings.HOST_DEFINITION_NODE_ID_FIELD] = node_id
    manifest[settings.SPEC][settings.HOST_DEFINITION_FIELD][settings.SECRET_NAME_FIELD] = \
        host_definition_info.secret_name
    manifest[settings.SPEC][settings.HOST_DEFINITION_FIELD][settings.SECRET_NAMESPACE_FIELD] = \
        host_definition_info.secret_namespace
    return manifest


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


def generate_host_definition_response_fields_manifest(host_definition_name, response):
    return {
        settings.METADATA: {
            common_settings.NAME_FIELD: host_definition_name,
        },
        settings.SPEC: {
            settings.HOST_DEFINITION_FIELD: {
                settings.CONNECTIVITY_TYPE_FIELD: response.connectivity_type,
                settings.PORTS_FIELD: response.ports,
                settings.NODE_NAME_ON_STORAGE_FIELD: response.node_name_on_storage,
                settings.IO_GROUP_FIELD: response.io_group,
                settings.MANAGEMENT_ADDRESS_FIELD: response.management_address
            },
        },
    }