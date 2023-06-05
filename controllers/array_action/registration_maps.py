from controllers.array_action.settings import UNIQUE_KEY_KEY, METADATA_KEY


def _generate_plugin_type(unique_key, metadata=''):
    return {
        UNIQUE_KEY_KEY: unique_key,
        METADATA_KEY: metadata
    }


basic_plugin_type = _generate_plugin_type('basic')
replication_plugin_type = _generate_plugin_type('replication')
volume_group_plugin_type = _generate_plugin_type('volume_group')
snapshot_plugin_type = _generate_plugin_type('snapshot')
host_definition_plugin_type = _generate_plugin_type('host_definition')


SVC_REGISTRATION_MAP = {
    'create_volume': basic_plugin_type,
    'delete_volume': basic_plugin_type,
    'map_volume': basic_plugin_type,
    'unmap_volume': basic_plugin_type,
    'create_replication': replication_plugin_type,
    'delete_replication': replication_plugin_type,
    'promote_replication_volume': replication_plugin_type,
    'demote_replication_volume': replication_plugin_type,
    'create_volume_group': volume_group_plugin_type,
    'delete_volume_group': volume_group_plugin_type,
    'add_volume_to_volume_group': volume_group_plugin_type,
    'remove_volume_from_volume_group': volume_group_plugin_type,
    'create_snapshot': snapshot_plugin_type,
    'delete_snapshot': snapshot_plugin_type,
    'create_host': host_definition_plugin_type,
    'delete_host': host_definition_plugin_type,
    'add_ports_to_host': host_definition_plugin_type,
    'remove_ports_from_host': host_definition_plugin_type,
}
