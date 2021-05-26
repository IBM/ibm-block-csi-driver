import json
import re
from hashlib import sha256

import base58
from google.protobuf.timestamp_pb2 import Timestamp

import controller.array_action.errors as array_errors
import controller.controller_server.config as config
import controller.controller_server.messages as messages
from controller.array_action.config import FC_CONNECTIVITY_TYPE, ISCSI_CONNECTIVITY_TYPE
from controller.common.csi_logger import get_stdout_logger
from controller.controller_server.controller_types import ArrayConnectionInfo, ObjectIdInfo, ObjectParameters
from controller.controller_server.errors import ObjectIdError, ValidationException
from controller.csi_general import csi_pb2

logger = get_stdout_logger()


def _parse_raw_input(raw_input):
    try:
        parsed_input = json.loads(raw_input)
    except json.decoder.JSONDecodeError:
        raise ValidationException(messages.invalid_non_json_parameter_message.format(raw_input))
    return parsed_input


def _is_topology_match(system_topologies, node_topologies):
    for topologies in system_topologies:
        logger.debug(
            "Comparing topologies: system topologies: {},"
            " node topologies: {}".format(topologies, node_topologies))
        if topologies.items() <= node_topologies.items():
            return True
    return False


def get_volume_topologies(request):
    if request.HasField(config.REQUEST_ACCESSIBILITY_REQUIREMENTS_FIELD):
        accessibility_requirements = request.accessibility_requirements
        if accessibility_requirements.preferred:
            topologies = accessibility_requirements.preferred[0].segments
            logger.info("Chosen volume topologies: {}".format(topologies))
            return topologies


def get_system_info_by_topologies(secrets_config, node_topologies):
    for system_id, system_info in secrets_config.items():
        system_topologies = system_info.get(config.SECRET_SUPPORTED_TOPOLOGIES_PARAMETER)
        if _is_topology_match(system_topologies, node_topologies):
            return system_info, system_id


def _get_system_info_by_system_id(secrets_config, system_id):
    return secrets_config.get(system_id)


def _get_system_info_from_secrets(secrets, topologies=None, system_id=None):
    raw_secrets_config = secrets.get(config.SECRET_CONFIG_PARAMETER)
    system_info = secrets
    if raw_secrets_config:
        secrets_config = _parse_raw_input(raw_input=raw_secrets_config)
        if system_id:
            system_info = _get_system_info_by_system_id(secrets_config=secrets_config, system_id=system_id)
        elif topologies:
            system_info, system_id = get_system_info_by_topologies(secrets_config=secrets_config,
                                                                   node_topologies=topologies)
        else:
            raise ValidationException(messages.invalid_secrets_message)
    return system_info, system_id


def _get_array_connection_info_from_system_info(secrets, system_id):
    user = secrets[config.SECRET_USERNAME_PARAMETER]
    password = secrets[config.SECRET_PASSWORD_PARAMETER]
    array_addresses = secrets[config.SECRET_ARRAY_PARAMETER].split(config.PARAMETERS_ARRAY_ADDRESSES_DELIMITER)
    return ArrayConnectionInfo(array_addresses=array_addresses, user=user, password=password, system_id=system_id)


def get_array_connection_info_from_secrets(secrets, topologies=None, system_id=None):
    system_info, system_id = _get_system_info_from_secrets(secrets, topologies, system_id)
    return _get_array_connection_info_from_system_info(system_info, system_id)


def get_volume_parameters(parameters, system_id):
    return get_object_parameters(parameters, config.PARAMETERS_VOLUME_NAME_PREFIX, system_id)


def get_snapshot_parameters(parameters, system_id):
    return get_object_parameters(parameters, config.PARAMETERS_SNAPSHOT_NAME_PREFIX, system_id)


def get_object_parameters(parameters, prefix_param_name, system_id):
    raw_parameters_by_system = parameters.get(config.PARAMETERS_BY_SYSTEM)
    system_parameters = {}
    if raw_parameters_by_system and system_id:
        parameters_by_system = _parse_raw_input(raw_input=raw_parameters_by_system)
        system_parameters = parameters_by_system.get(system_id, {})
    default_pool = parameters.get(config.PARAMETERS_POOL)
    default_space_efficiency = parameters.get(config.PARAMETERS_SPACE_EFFICIENCY)
    default_prefix = parameters.get(prefix_param_name)
    return ObjectParameters(
        pool=system_parameters.get(config.PARAMETERS_POOL, default_pool),
        space_efficiency=system_parameters.get(config.PARAMETERS_SPACE_EFFICIENCY, default_space_efficiency),
        prefix=system_parameters.get(prefix_param_name, default_prefix))


def get_volume_id(new_volume, system_id):
    return _get_object_id(new_volume, system_id)


def get_snapshot_id(new_snapshot):
    return _get_object_id(new_snapshot)


def _get_object_id(obj, system_id=None):
    if system_id:
        return config.PARAMETERS_OBJECT_ID_DELIMITER.join((obj.array_type, system_id, obj.id))
    return config.PARAMETERS_OBJECT_ID_DELIMITER.join((obj.array_type, obj.id))


def _is_imported_string_valid(imported_string):
    return imported_string and re.match(config.SECRET_VALIDATION_REGEX, imported_string)


def _validate_system_id(system_id):
    if not _is_imported_string_valid(system_id):
        raise ValidationException(messages.invalid_system_id_value_message.format(system_id))
    if len(system_id) > config.SECRET_SYSTEM_ID_MAX_LENGTH:
        raise ValidationException(
            messages.parameter_length_is_too_long.format("system id", system_id, config.SECRET_SYSTEM_ID_MAX_LENGTH))


def _validate_secrets(secrets):
    if not (config.SECRET_USERNAME_PARAMETER in secrets and
            config.SECRET_PASSWORD_PARAMETER in secrets and
            config.SECRET_ARRAY_PARAMETER in secrets):
        raise ValidationException(messages.invalid_secrets_message)


def _validate_topologies(topologies):
    if topologies:
        for topology in topologies:
            if not topology:
                raise ValidationException(messages.invalid_secrets_message)
    else:
        raise ValidationException(messages.invalid_secrets_message)


def _validate_secrets_config(secrets_config):
    for system_id, system_info in secrets_config.items():
        if system_id and system_info:
            _validate_topologies(system_info.get(config.SECRET_SUPPORTED_TOPOLOGIES_PARAMETER))
            _validate_system_id(system_id)
            _validate_secrets(system_info)
        else:
            raise ValidationException(messages.invalid_secrets_message)


def validate_secrets(secrets):
    logger.debug("validating secrets")
    if not secrets:
        raise ValidationException(messages.secrets_missing_message)
    raw_secrets_config = secrets.get(config.SECRET_CONFIG_PARAMETER)
    if raw_secrets_config:
        secrets_config = _parse_raw_input(raw_secrets_config)
        _validate_secrets_config(secrets_config)
    else:
        _validate_secrets(secrets)
    logger.debug("secrets validation finished")


def validate_csi_volume_capability(cap):
    logger.debug("validating csi volume capability : {0}".format(cap))
    if cap.HasField(config.VOLUME_CAPABILITIES_FIELD_ACCESS_TYPE_MOUNT):
        if cap.mount.fs_type and (cap.mount.fs_type not in config.SUPPORTED_FS_TYPES):
            raise ValidationException(messages.unsupported_fs_type_message.format(cap.mount.fs_type))

    elif not cap.HasField(config.VOLUME_CAPABILITIES_FIELD_ACCESS_TYPE_BLOCK):
        # should never get here since the value can be only mount (for fs volume) or block (for raw block)
        logger.error(messages.unsupported_volume_access_type_message)
        raise ValidationException(messages.unsupported_volume_access_type_message)

    if cap.access_mode.mode not in config.SUPPORTED_ACCESS_MODE:
        logger.error("unsupported access mode : {}".format(cap.access_mode))
        raise ValidationException(messages.unsupported_access_mode_message.format(cap.access_mode))

    logger.debug("csi volume capabilities validation finished.")


def validate_csi_volume_capabilties(capabilities):
    logger.debug("validating csi volume capabilities: {}".format(capabilities))
    if len(capabilities) == 0:
        raise ValidationException(messages.capabilities_not_set_message)

    for cap in capabilities:
        validate_csi_volume_capability(cap)

    logger.debug("finished validating csi volume capabilities.")


def validate_create_volume_source(request):
    source = request.volume_content_source
    if source:
        logger.info(source)
        if source.HasField(config.SNAPSHOT_TYPE_NAME):
            _validate_source_info(source, config.SNAPSHOT_TYPE_NAME)
        elif source.HasField(config.VOLUME_TYPE_NAME):
            _validate_source_info(source, config.VOLUME_TYPE_NAME)


def _validate_source_info(source, source_type):
    source_object = getattr(source, source_type)
    logger.info("Source {0} specified: {1}".format(source_type, source_object))
    source_object_id = getattr(source_object, config.VOLUME_SOURCE_ID_FIELDS[source_type])
    if not source_object_id:
        raise ValidationException(messages.volume_source_id_is_missing.format(source_type))
    if config.PARAMETERS_OBJECT_ID_DELIMITER not in source_object_id:
        raise ObjectIdError(source_type, source_object_id)


def validate_create_volume_request(request):
    logger.debug("validating create volume request")

    logger.debug("validating volume name")
    if not request.name:
        raise ValidationException(messages.name_should_not_be_empty_message)

    logger.debug("validating volume capacity")
    if request.capacity_range:
        if request.capacity_range.required_bytes < 0:
            raise ValidationException(messages.size_should_not_be_negative_message)

    else:
        raise ValidationException(messages.no_capacity_range_message)

    logger.debug("validating volume capabilities")
    validate_csi_volume_capabilties(request.volume_capabilities)

    logger.debug("validating secrets")
    if request.secrets:
        validate_secrets(request.secrets)

    logger.debug("validating storage class parameters")
    if request.parameters:
        if config.PARAMETERS_POOL in request.parameters:
            if not request.parameters[config.PARAMETERS_POOL]:
                raise ValidationException(messages.wrong_pool_passed_message)
        elif not request.parameters.get(config.PARAMETERS_BY_SYSTEM):
            raise ValidationException(messages.pool_is_missing_message)
    else:
        raise ValidationException(messages.params_are_missing_message)

    logger.debug("validating volume copy source")
    validate_create_volume_source(request)

    logger.debug("request validation finished.")


def validate_create_snapshot_request(request):
    logger.debug("validating create snapshot request")
    logger.debug("validating snapshot name")
    if not request.name:
        raise ValidationException(messages.name_should_not_be_empty_message)
    logger.debug("validating secrets")
    if request.secrets:
        validate_secrets(request.secrets)
    logger.debug("validating source volume id")
    if not request.source_volume_id:
        raise ValidationException(messages.snapshot_src_volume_id_is_missing)
    logger.debug("request validation finished.")


def validate_delete_snapshot_request(request):
    logger.debug("validating delete snapshot request")
    if not request.snapshot_id:
        raise ValidationException(messages.name_should_not_be_empty_message)
    logger.debug("validating secrets")
    if request.secrets:
        validate_secrets(request.secrets)
    logger.debug("request validation finished.")


def validate_expand_volume_request(request):
    logger.debug("validating expand volume request")

    if not request.volume_id:
        raise ValidationException(messages.id_should_not_be_empty_message)

    logger.debug("validating volume capacity")
    if request.capacity_range:
        if request.capacity_range.required_bytes < 0:
            raise ValidationException(messages.size_should_not_be_negative_message)
    else:
        raise ValidationException(messages.no_capacity_range_message)

    validate_secrets(request.secrets)

    logger.debug("expand volume validation finished")


def generate_csi_create_volume_response(new_volume, system_id=None, source_type=None):
    logger.debug("creating volume response for volume : {0}".format(new_volume))

    volume_context = {"volume_name": new_volume.name,
                      "array_address": ",".join(
                          new_volume.array_address if isinstance(new_volume.array_address, list) else [
                              new_volume.array_address]),
                      "pool_name": new_volume.pool,
                      "storage_type": new_volume.array_type
                      }
    content_source = None
    if new_volume.copy_source_id:
        if source_type == config.SNAPSHOT_TYPE_NAME:
            snapshot_source = csi_pb2.VolumeContentSource.SnapshotSource(snapshot_id=new_volume.copy_source_id)
            content_source = csi_pb2.VolumeContentSource(snapshot=snapshot_source)
        else:
            volume_source = csi_pb2.VolumeContentSource.VolumeSource(volume_id=new_volume.copy_source_id)
            content_source = csi_pb2.VolumeContentSource(volume=volume_source)

    res = csi_pb2.CreateVolumeResponse(volume=csi_pb2.Volume(
        capacity_bytes=new_volume.capacity_bytes,
        volume_id=get_volume_id(new_volume, system_id),
        content_source=content_source,
        volume_context=volume_context))

    logger.debug("finished creating volume response : {0}".format(res))
    return res


def generate_csi_create_snapshot_response(new_snapshot, source_volume_id):
    logger.debug("creating snapshot response for snapshot : {0}".format(new_snapshot))

    res = csi_pb2.CreateSnapshotResponse(snapshot=csi_pb2.Snapshot(
        size_bytes=new_snapshot.capacity_bytes,
        snapshot_id=get_snapshot_id(new_snapshot),
        source_volume_id=source_volume_id,
        creation_time=get_current_timestamp(),
        ready_to_use=new_snapshot.is_ready))

    logger.debug("finished creating snapshot response : {0}".format(res))
    return res


def generate_csi_expand_volume_response(capacity_bytes, node_expansion_required=True):
    logger.debug("creating response for expand volume")
    res = csi_pb2.ControllerExpandVolumeResponse(
        capacity_bytes=capacity_bytes,
        node_expansion_required=node_expansion_required,
    )

    logger.debug("finished creating expand volume response")
    return res


def validate_delete_volume_request(request):
    logger.debug("validating delete volume request")

    if request.volume_id == "":
        raise ValidationException("Volume id cannot be empty")

    logger.debug("validating secrets")
    if request.secrets:
        validate_secrets(request.secrets)

    logger.debug("delete volume validation finished")


def validate_publish_volume_request(request):
    logger.debug("validating publish volume request")

    logger.debug("validating readonly")
    if request.readonly:
        raise ValidationException(messages.readonly_not_supported_message)

    logger.debug("validating volume capabilities")
    validate_csi_volume_capability(request.volume_capability)

    logger.debug("validating secrets")
    if request.secrets:
        validate_secrets(request.secrets)
    else:
        raise ValidationException(messages.secrets_missing_message)

    logger.debug("publish volume request validation finished.")


def get_volume_id_info(volume_id):
    return get_object_id_info(volume_id, config.VOLUME_TYPE_NAME)


def get_snapshot_id_info(snapshot_id):
    return get_object_id_info(snapshot_id, config.SNAPSHOT_TYPE_NAME)


def get_object_id_info(full_object_id, object_type):
    logger.debug("getting {0} info for id : {1}".format(object_type, full_object_id))
    splitted_object_id = full_object_id.split(config.PARAMETERS_OBJECT_ID_DELIMITER)
    system_id = None
    if len(splitted_object_id) == 2:
        array_type, object_id = splitted_object_id
    elif len(splitted_object_id) == 3:
        array_type, system_id, object_id = splitted_object_id
    else:
        raise ObjectIdError(object_type, full_object_id)
    logger.debug("volume id : {0}, array type :{1}".format(object_id, array_type))
    return ObjectIdInfo(array_type=array_type, system_id=system_id, object_id=object_id)


def get_node_id_info(node_id):
    logger.debug("getting node info for node id : {0}".format(node_id))
    split_node = node_id.split(config.PARAMETERS_NODE_ID_DELIMITER)
    hostname, fc_wwns, iscsi_iqn = "", "", ""
    if len(split_node) == config.SUPPORTED_CONNECTIVITY_TYPES + 1:
        hostname, fc_wwns, iscsi_iqn = split_node
    elif len(split_node) == 2:
        hostname, fc_wwns = split_node
    else:
        raise array_errors.HostNotFoundError(node_id)
    logger.debug("node name : {0}, iscsi_iqn : {1}, fc_wwns : {2} ".format(
        hostname, iscsi_iqn, fc_wwns))
    return hostname, fc_wwns, iscsi_iqn


def choose_connectivity_type(connecitvity_types):
    # If connectivity type support FC and iSCSI at the same time, chose FC
    logger.debug("choosing connectivity type for connectivity types : {0}".format(connecitvity_types))
    if FC_CONNECTIVITY_TYPE in connecitvity_types:
        logger.debug("connectivity type is : {0}".format(FC_CONNECTIVITY_TYPE))
        return FC_CONNECTIVITY_TYPE
    if ISCSI_CONNECTIVITY_TYPE in connecitvity_types:
        logger.debug("connectivity type is : {0}".format(ISCSI_CONNECTIVITY_TYPE))
        return ISCSI_CONNECTIVITY_TYPE


def generate_csi_publish_volume_response(lun, connectivity_type, config, array_initiators):
    logger.debug("generating publish volume response for lun :{0}, connectivity : {1}".format(lun, connectivity_type))

    lun_param = config["controller"]["publish_context_lun_parameter"]
    connectivity_param = config["controller"]["publish_context_connectivity_parameter"]
    separator = config["controller"]["publish_context_separator"]

    publish_context = {
        lun_param: str(lun),
        connectivity_param: connectivity_type
    }

    if connectivity_type == ISCSI_CONNECTIVITY_TYPE:
        for iqn, ips in array_initiators.items():
            publish_context[iqn] = separator.join(ips)

        array_initiators_param = config["controller"]["publish_context_array_iqn"]
        publish_context[array_initiators_param] = separator.join(array_initiators.keys())
    else:
        array_initiators_param = config["controller"]["publish_context_fc_initiators"]
        publish_context[array_initiators_param] = separator.join(array_initiators)

    res = csi_pb2.ControllerPublishVolumeResponse(publish_context=publish_context)

    logger.debug("publish volume response is :{0}".format(res))
    return res


def validate_unpublish_volume_request(request):
    logger.debug("validating unpublish volume request")

    logger.debug("validating volume id")
    if len(request.volume_id.split(config.PARAMETERS_OBJECT_ID_DELIMITER)) not in {config.MINIMUM_VOLUME_ID_PARTS,
                                                                                   config.MAXIMUM_VOLUME_ID_PARTS}:
        raise ValidationException(messages.volume_id_wrong_format_message)

    logger.debug("validating secrets")
    if request.secrets:
        validate_secrets(request.secrets)
    else:
        raise ValidationException(messages.secrets_missing_message)

    logger.debug("unpublish volume request validation finished.")


def get_current_timestamp():
    res = Timestamp()
    res.GetCurrentTime()
    return res


def hash_string(string):
    return base58.b58encode(sha256(string.encode()).digest()).decode()
