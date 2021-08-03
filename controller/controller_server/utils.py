import json
import re
from hashlib import sha256
from operator import eq

import base58
from google.protobuf.timestamp_pb2 import Timestamp

import controller.array_action.errors as array_errors
import controller.controller_server.config as config
import controller.controller_server.messages as messages
from controller.array_action.config import FC_CONNECTIVITY_TYPE, ISCSI_CONNECTIVITY_TYPE
from controller.common.csi_logger import get_stdout_logger
from controller.common.settings import NAME_PREFIX_SEPARATOR
from controller.controller_server.controller_types import ArrayConnectionInfo, ObjectIdInfo, ObjectParameters
from controller.controller_server.errors import ObjectIdError, ValidationException
from controller.csi_general import csi_pb2

logger = get_stdout_logger()


def _parse_raw_json(raw_json):
    try:
        parsed_json = json.loads(raw_json)
    except json.decoder.JSONDecodeError as ex:
        raise ValidationException(messages.invalid_json_parameter_message.format(raw_json, ex))
    return parsed_json


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
    return None


def _get_system_info_for_topologies(secrets_config, node_topologies):
    for system_id, system_info in secrets_config.items():
        system_topologies = system_info.get(config.SECRET_SUPPORTED_TOPOLOGIES_PARAMETER)
        if _is_topology_match(system_topologies, node_topologies):
            return system_info, system_id
    raise ValidationException(messages.no_system_match_requested_topologies.format(node_topologies))


def _get_system_info_from_secrets(secrets, topologies=None, system_id=None):
    raw_secrets_config = secrets.get(config.SECRET_CONFIG_PARAMETER)
    system_info = secrets
    if raw_secrets_config:
        secrets_config = _parse_raw_json(raw_json=raw_secrets_config)
        if system_id:
            system_info = secrets_config.get(system_id)
        elif topologies:
            system_info, system_id = _get_system_info_for_topologies(secrets_config=secrets_config,
                                                                     node_topologies=topologies)
        else:
            raise ValidationException(messages.insufficient_data_to_choose_a_storage_system_message)
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
        parameters_by_system = _parse_raw_json(raw_json=raw_parameters_by_system)
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


def _is_system_id_valid(system_id):
    return system_id and re.match(config.SECRET_VALIDATION_REGEX, system_id)


def _validate_system_id(system_id):
    if not _is_system_id_valid(system_id):
        raise ValidationException(
            messages.invalid_system_id_message.format(system_id, config.SECRET_VALIDATION_REGEX))
    if len(system_id) > config.SECRET_SYSTEM_ID_MAX_LENGTH:
        raise ValidationException(
            messages.parameter_length_is_too_long.format("system id", system_id, config.SECRET_SYSTEM_ID_MAX_LENGTH))


def _validate_secrets(secrets):
    if not (config.SECRET_USERNAME_PARAMETER in secrets and
            config.SECRET_PASSWORD_PARAMETER in secrets and
            config.SECRET_ARRAY_PARAMETER in secrets):
        raise ValidationException(messages.secret_missing_connection_info_message)


def _validate_topologies(topologies):
    if topologies:
        if not all(topologies):
            raise ValidationException(messages.secret_missing_topologies_message)
    else:
        raise ValidationException(messages.secret_missing_topologies_message)


def _validate_secrets_config(secrets_config):
    for system_id, system_info in secrets_config.items():
        if system_id and system_info:
            _validate_system_id(system_id)
            _validate_secrets(system_info)
            supported_topologies = system_info.get(config.SECRET_SUPPORTED_TOPOLOGIES_PARAMETER)
            _validate_topologies(supported_topologies)
        else:
            raise ValidationException(messages.invalid_secret_config_message)


def validate_secrets(secrets):
    logger.debug("validating secrets")
    if not secrets:
        raise ValidationException(messages.secret_missing_message)
    raw_secrets_config = secrets.get(config.SECRET_CONFIG_PARAMETER)
    if raw_secrets_config:
        secrets_config = _parse_raw_json(raw_secrets_config)
        _validate_secrets_config(secrets_config)
    else:
        _validate_secrets(secrets)
    logger.debug("secrets validation finished")


def validate_csi_volume_capability(cap):
    logger.debug("validating csi volume capability")
    if cap.HasField(config.VOLUME_CAPABILITIES_FIELD_ACCESS_TYPE_MOUNT):
        if cap.mount.fs_type and (cap.mount.fs_type not in config.SUPPORTED_FS_TYPES):
            raise ValidationException(messages.unsupported_fs_type_message.format(cap.mount.fs_type))
        if cap.mount.mount_flags:
            raise ValidationException(messages.unsupported_mount_flags_message)

    elif not cap.HasField(config.VOLUME_CAPABILITIES_FIELD_ACCESS_TYPE_BLOCK):
        # should never get here since the value can be only mount (for fs volume) or block (for raw block)
        logger.error(messages.unsupported_volume_access_type_message)
        raise ValidationException(messages.unsupported_volume_access_type_message)

    if cap.access_mode.mode not in config.SUPPORTED_ACCESS_MODE:
        logger.error("unsupported access mode : {}".format(cap.access_mode))
        raise ValidationException(messages.unsupported_access_mode_message.format(cap.access_mode))

    logger.debug("csi volume capabilities validation finished.")


def validate_csi_volume_capabilities(capabilities):
    logger.debug("validating csi volume capabilities")
    if not capabilities:
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
    message = messages.volume_source_id_is_missing.format(source_type)
    _validate_object_id(source_object_id, object_type=source_type, message=message)


def _validate_pool_parameter(parameters):
    logger.debug("validating pool parameter")
    if config.PARAMETERS_POOL in parameters:
        if not parameters[config.PARAMETERS_POOL]:
            raise ValidationException(messages.pool_should_not_be_empty_message)
    elif not parameters.get(config.PARAMETERS_BY_SYSTEM):
        raise ValidationException(messages.pool_is_missing_message)


def _validate_object_id(object_id, object_type=config.VOLUME_TYPE_NAME,
                        message=messages.volume_id_should_not_be_empty_message):
    logger.debug("validating volume id")
    if not object_id:
        raise ValidationException(message)
    if config.PARAMETERS_OBJECT_ID_DELIMITER not in object_id:
        raise ObjectIdError(object_type, object_id)
    if len(object_id.split(config.PARAMETERS_OBJECT_ID_DELIMITER)) not in {config.MINIMUM_VOLUME_ID_PARTS,
                                                                           config.MAXIMUM_VOLUME_ID_PARTS}:
        raise ValidationException(messages.volume_id_wrong_format_message)


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

    validate_csi_volume_capabilities(request.volume_capabilities)

    validate_secrets(request.secrets)

    if request.parameters:
        _validate_pool_parameter(request.parameters)
    else:
        raise ValidationException(messages.pool_is_missing_message)

    logger.debug("validating volume copy source")
    validate_create_volume_source(request)

    logger.debug("request validation finished.")


def validate_create_snapshot_request(request):
    logger.debug("validating create snapshot request")
    logger.debug("validating snapshot name")
    if not request.name:
        raise ValidationException(messages.name_should_not_be_empty_message)

    validate_secrets(request.secrets)

    logger.debug("validating source volume id")
    if not request.source_volume_id:
        raise ValidationException(messages.snapshot_src_volume_id_is_missing)
    logger.debug("request validation finished.")


def validate_delete_snapshot_request(request):
    logger.debug("validating delete snapshot request")
    if not request.snapshot_id:
        raise ValidationException(messages.snapshot_id_should_not_be_empty_message)

    validate_secrets(request.secrets)

    logger.debug("request validation finished.")


def validate_validate_volume_capabilities_request(request):
    logger.debug("validating validate_volume_capabilities request")

    _validate_object_id(request.volume_id)

    if request.parameters:
        _validate_pool_parameter(request.parameters)
    else:
        raise ValidationException(messages.pool_is_missing_message)

    validate_csi_volume_capabilities(request.volume_capabilities)

    validate_secrets(request.secrets)


def validate_volume_context_match_volume(volume_context, volume):
    logger.debug("validate volume_context is matching volume")
    context_from_existing_volume = _get_context_from_volume(volume)

    if volume_context != context_from_existing_volume:
        raise ValidationException(
            messages.volume_context_not_match_volume_message.format(volume_context, context_from_existing_volume))
    logger.debug("volume_context validation finished.")


def validate_expand_volume_request(request):
    logger.debug("validating expand volume request")

    if not request.volume_id:
        raise ValidationException(messages.volume_id_should_not_be_empty_message)

    logger.debug("validating volume capacity")
    if request.capacity_range:
        if request.capacity_range.required_bytes < 0:
            raise ValidationException(messages.size_should_not_be_negative_message)
    else:
        raise ValidationException(messages.no_capacity_range_message)

    validate_secrets(request.secrets)

    logger.debug("expand volume validation finished")


def generate_csi_create_volume_response(new_volume, system_id=None, source_type=None):
    logger.debug("creating create volume response for volume : {0}".format(new_volume))

    volume_context = _get_context_from_volume(new_volume)

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
    logger.debug("creating create snapshot response for snapshot : {0}".format(new_snapshot))

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


def _get_supported_capability(volume_capability):
    access_mode = csi_pb2.VolumeCapability.AccessMode(mode=volume_capability.access_mode.mode)

    if volume_capability.HasField(config.VOLUME_CAPABILITIES_FIELD_ACCESS_TYPE_MOUNT):
        return csi_pb2.VolumeCapability(
            mount=csi_pb2.VolumeCapability.MountVolume(fs_type=volume_capability.mount.fs_type),
            access_mode=access_mode)

    if volume_capability.HasField(config.VOLUME_CAPABILITIES_FIELD_ACCESS_TYPE_BLOCK):
        return csi_pb2.VolumeCapability(
            mount=csi_pb2.VolumeCapability.BlockVolume(),
            access_mode=access_mode)


def generate_csi_validate_volume_capabilities_response(volume_context, volume_capabilities, parameters):
    logger.debug("creating validate volume capabilities response")

    capabilities = []
    for capability in volume_capabilities:
        supported_capability = _get_supported_capability(volume_capability=capability)
        capabilities.append(supported_capability)

    res = csi_pb2.ValidateVolumeCapabilitiesResponse(confirmed=csi_pb2.ValidateVolumeCapabilitiesResponse.Confirmed(
        volume_context=volume_context,
        volume_capabilities=capabilities,
        parameters=parameters))

    logger.debug("finished creating validate volume capabilities response")
    return res


def validate_delete_volume_request(request):
    logger.debug("validating delete volume request")

    if request.volume_id == "":
        raise ValidationException("Volume id cannot be empty")

    validate_secrets(request.secrets)

    logger.debug("delete volume validation finished")


def validate_publish_volume_request(request):
    logger.debug("validating publish volume request")

    logger.debug("validating readonly")
    if request.readonly:
        raise ValidationException(messages.readonly_not_supported_message)

    validate_csi_volume_capability(request.volume_capability)

    validate_secrets(request.secrets)

    logger.debug("publish volume request validation finished.")


def get_volume_id_info(volume_id):
    return get_object_id_info(volume_id, config.VOLUME_TYPE_NAME)


def get_snapshot_id_info(snapshot_id):
    return get_object_id_info(snapshot_id, config.SNAPSHOT_TYPE_NAME)


def _get_context_from_volume(volume):
    return {config.VOLUME_CONTEXT_VOLUME_NAME: volume.name,
            config.VOLUME_CONTEXT_ARRAY_ADDRESS: ",".join(
                volume.array_address if isinstance(volume.array_address, list) else [volume.array_address]),
            config.VOLUME_CONTEXT_POOL: volume.pool,
            config.VOLUME_CONTEXT_STORAGE_TYPE: volume.array_type
            }


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


def choose_connectivity_type(connectivity_types):
    # If connectivity type support FC and iSCSI at the same time, chose FC
    logger.debug("choosing connectivity type for connectivity types : {0}".format(connectivity_types))
    if FC_CONNECTIVITY_TYPE in connectivity_types:
        logger.debug("connectivity type is : {0}".format(FC_CONNECTIVITY_TYPE))
        return FC_CONNECTIVITY_TYPE
    if ISCSI_CONNECTIVITY_TYPE in connectivity_types:
        logger.debug("connectivity type is : {0}".format(ISCSI_CONNECTIVITY_TYPE))
        return ISCSI_CONNECTIVITY_TYPE
    return None


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

    _validate_object_id(request.volume_id)

    validate_secrets(request.secrets)

    logger.debug("unpublish volume request validation finished.")


def get_current_timestamp():
    res = Timestamp()
    res.GetCurrentTime()
    return res


def hash_string(string):
    return base58.b58encode(sha256(string.encode()).digest()).decode()


def _validate_parameter_match_volume(parameter_value, value_from_volume, error_message_format, cmp=eq):
    if parameter_value and not cmp(parameter_value, value_from_volume):
        raise ValidationException(error_message_format.format(parameter_value, value_from_volume))


def validate_parameters_match_volume(parameters, volume):
    logger.debug("validating space efficiency parameter matches volume's")
    space_efficiency = parameters.get(config.PARAMETERS_SPACE_EFFICIENCY)
    if space_efficiency:
        space_efficiency = space_efficiency.lower()
    else:
        space_efficiency = volume.default_space_efficiency
    _validate_parameter_match_volume(space_efficiency, volume.space_efficiency,
                                     messages.space_efficiency_not_match_volume_message)

    logger.debug("validating pool parameter matches volume's")
    pool = parameters.get(config.PARAMETERS_POOL)
    _validate_parameter_match_volume(pool, volume.pool, messages.pool_not_match_volume_message)

    logger.debug("validating prefix parameter matches volume's")
    prefix = parameters.get(config.PARAMETERS_VOLUME_NAME_PREFIX)
    _validate_parameter_match_volume(prefix, volume.name, messages.prefix_not_match_volume_message,
                                     lambda pref, name: name.startswith(pref + NAME_PREFIX_SEPARATOR))
