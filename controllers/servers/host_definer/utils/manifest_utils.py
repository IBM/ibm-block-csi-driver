from controllers.servers.host_definer import settings
import controllers.common.settings as common_settings


def get_host_definition_manifest(host_definition_info, response, node_id):
    manifest = generate_host_definition_response_fields_manifest(host_definition_info.name, response)
    manifest[common_settings.API_VERSION_FIELD] = common_settings.CSI_IBM_API_VERSION
    manifest[common_settings.KIND_FIELD] = common_settings.HOST_DEFINITION_KIND
    manifest[common_settings.SPEC_FIELD][common_settings.HOST_DEFINITION_FIELD][
        common_settings.HOST_DEFINITION_NODE_NAME_FIELD] = host_definition_info.node_name
    manifest[common_settings.SPEC_FIELD][common_settings.HOST_DEFINITION_FIELD][
        common_settings.HOST_DEFINITION_NODE_ID_FIELD] = node_id
    manifest[common_settings.SPEC_FIELD][common_settings.HOST_DEFINITION_FIELD][common_settings.SECRET_NAME_FIELD] = \
        host_definition_info.secret_name
    manifest[common_settings.SPEC_FIELD][common_settings.HOST_DEFINITION_FIELD][
        common_settings.SECRET_NAMESPACE_FIELD] = host_definition_info.secret_namespace
    return manifest


def get_host_definition_status_manifest(host_definition_phase):
    return {
        common_settings.STATUS_FIELD: {
            common_settings.STATUS_PHASE_FIELD: host_definition_phase,
        }
    }


def get_body_manifest_for_labels(label_value):
    return {
        common_settings.METADATA_FIELD: {
            common_settings.LABELS_FIELD: {
                common_settings.MANAGE_NODE_LABEL: label_value}
        }
    }


def get_finalizer_manifest(host_definition_name, finalizers):
    return {
        common_settings.METADATA_FIELD: {
            common_settings.NAME_FIELD: host_definition_name,
            common_settings.FINALIZERS_FIELD: finalizers,
        }
    }


def generate_host_definition_response_fields_manifest(host_definition_name, response):
    return {
        common_settings.METADATA_FIELD: {
            common_settings.NAME_FIELD: host_definition_name,
        },
        common_settings.SPEC_FIELD: {
            common_settings.HOST_DEFINITION_FIELD: {
                common_settings.CONNECTIVITY_TYPE_FIELD: response.connectivity_type,
                common_settings.PORTS_FIELD: response.ports,
                common_settings.NODE_NAME_ON_STORAGE_FIELD: response.node_name_on_storage,
                common_settings.IO_GROUP_FIELD: response.io_group,
                common_settings.MANAGEMENT_ADDRESS_FIELD: response.management_address
            },
        },
    }
