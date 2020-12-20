from hashlib import sha256

import base58
from google.protobuf.timestamp_pb2 import Timestamp

import controller.controller_server.config as config
import controller.controller_server.messages as messages
from controller.array_action.config import FC_CONNECTIVITY_TYPE, ISCSI_CONNECTIVITY_TYPE
from controller.array_action.errors import HostNotFoundError
from controller.common.csi_logger import get_stdout_logger
from controller.controller_server.errors import ValidationException, ObjectIdError
from controller.csi_general import csi_pb2

logger = get_stdout_logger()


def get_array_connection_info_from_secret(secrets):
    user = secrets[config.SECRET_USERNAME_PARAMETER]
    password = secrets[config.SECRET_PASSWORD_PARAMETER]
    array_addresses = secrets[config.SECRET_ARRAY_PARAMETER].split(",")
    return user, password, array_addresses


def get_vol_id(new_vol):
    return _get_object_id(new_vol)


def get_snapshot_id(new_snapshot):
    return _get_object_id(new_snapshot)


def _get_object_id(obj):
    return config.PARAMETERS_OBJECT_ID_DELIMITER.join((obj.array_type, obj.id))


def validate_secret(secret):
    logger.debug("validating secrets")
    if secret:
        if not (config.SECRET_USERNAME_PARAMETER in secret and
                config.SECRET_PASSWORD_PARAMETER in secret and
                config.SECRET_ARRAY_PARAMETER in secret):
            raise ValidationException(messages.invalid_secret_message)

    else:
        raise ValidationException(messages.secret_missing_message)

    logger.debug("secret validation finished")


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
    if not source_object_id:
        raise ValidationException(messages.volume_source_id_is_missing.format(source_type))
    if config.PARAMETERS_OBJECT_ID_DELIMITER not in source_object_id:
        raise ObjectIdError(source_type, source_object_id)


def _validate_create_volume_parameters(parameters):
    if config.PARAMETERS_POOL not in parameters:
        raise ValidationException(messages.pool_is_missing_message)

    if not parameters[config.PARAMETERS_POOL]:
        raise ValidationException(messages.wrong_pool_passed_message)


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
    validate_csi_volume_capabilities(request.volume_capabilities)

    validate_secret(request.secrets)

    logger.debug("validating storage class parameters")
    if request.parameters:
        _validate_create_volume_parameters(request.parameters)
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

    validate_secret(request.secrets)

    logger.debug("validating source volume id")
    if not request.source_volume_id:
        raise ValidationException(messages.snapshot_src_volume_id_is_missing)
    logger.debug("request validation finished.")


def validate_delete_snapshot_request(request):
    logger.debug("validating delete snapshot request")
    if not request.snapshot_id:
        raise ValidationException(messages.snapshot_id_should_not_be_empty_message)

    validate_secret(request.secrets)

    logger.debug("request validation finished.")


def validate_validate_volume_capabilities_request(request):
    logger.debug("validating validate_volume_capabilities request")

    logger.debug("validating volume id")
    volume_id = request.volume_id
    if not volume_id:
        raise ValidationException(messages.volume_id_should_not_be_empty_message)
    if config.PARAMETERS_OBJECT_ID_DELIMITER not in volume_id:
        raise ObjectIdError(config.VOLUME_TYPE_NAME, volume_id)

    logger.debug("validating parameters")
    if request.parameters:
        _validate_create_volume_parameters(request.parameters)

    validate_csi_volume_capabilities(request.volume_capabilities)

    validate_secret(request.secrets)


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

    validate_secret(request.secrets)

    logger.debug("expand volume validation finished")


def generate_csi_create_volume_response(new_volume, source_type=None):
    logger.debug("creating create volume response for vol : {0}".format(new_volume))

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
        volume_id=get_vol_id(new_volume),
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

    elif volume_capability.HasField(config.VOLUME_CAPABILITIES_FIELD_ACCESS_TYPE_BLOCK):
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

    validate_secret(request.secrets)

    logger.debug("delete volume validation finished")


def validate_publish_volume_request(request):
    logger.debug("validating publish volume request")

    logger.debug("validating readonly")
    if request.readonly:
        raise ValidationException(messages.readonly_not_supported_message)

    logger.debug("validating volume capabilities")
    validate_csi_volume_capability(request.volume_capability)

    validate_secret(request.secrets)

    logger.debug("publish volume request validation finished.")


def get_volume_id_info(volume_id):
    return get_object_id_info(volume_id, config.VOLUME_TYPE_NAME)


def get_snapshot_id_info(snapshot_id):
    return get_object_id_info(snapshot_id, config.SNAPSHOT_TYPE_NAME)


def _get_context_from_volume(volume):
    return {config.VOLUME_CONTEXT_VOLUME_NAME: volume.name,
            config.VOLUME_CONTEXT_ARRAY_ADDRESS: ",".join(
                volume.array_address if isinstance(volume.array_address, list) else [volume.array_address]),
            config.VOLUME_CONTEXT_POOL: volume.pool_name,
            config.VOLUME_CONTEXT_STORAGE_TYPE: volume.array_type
            }


def get_object_id_info(full_object_id, object_type):
    logger.debug("getting {0} info for id : {1}".format(object_type, full_object_id))
    splitted_object_id = full_object_id.split(config.PARAMETERS_OBJECT_ID_DELIMITER)
    if len(splitted_object_id) != 2:
        raise ObjectIdError(object_type, full_object_id)

    array_type, object_id = splitted_object_id
    logger.debug("volume id : {0}, array type :{1}".format(object_id, array_type))
    return array_type, object_id


def get_node_id_info(node_id):
    logger.debug("getting node info for node id : {0}".format(node_id))
    split_node = node_id.split(config.PARAMETERS_NODE_ID_DELIMITER)
    hostname, fc_wwns, iscsi_iqn = "", "", ""
    if len(split_node) == config.SUPPORTED_CONNECTIVITY_TYPES + 1:
        hostname, fc_wwns, iscsi_iqn = split_node
    elif len(split_node) == 2:
        hostname, fc_wwns = split_node
    else:
        raise HostNotFoundError(node_id)
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
    if len(request.volume_id.split(config.PARAMETERS_OBJECT_ID_DELIMITER)) != 2:
        raise ValidationException(messages.volume_id_wrong_format_message)

    validate_secret(request.secrets)

    logger.debug("unpublish volume request validation finished.")


def get_current_timestamp():
    res = Timestamp()
    res.GetCurrentTime()
    return res


def hash_string(string):
    return base58.b58encode(sha256(string.encode()).digest()).decode()


def validate_parameters_match_volume(parameters, volume):
    space_efficiency = parameters.get(config.PARAMETERS_SPACEEFFICIENCY)
    volume_space_efficiency = volume.space_efficiency
    if space_efficiency:
        if space_efficiency != volume_space_efficiency:
            raise ValidationException(
                messages.space_efficiency_not_match_volume_message.format(space_efficiency, volume_space_efficiency))
    else:
        if volume_space_efficiency is not None and volume_space_efficiency != 'none':
            raise ValidationException(
                messages.space_efficiency_not_match_volume_message.format(space_efficiency, volume_space_efficiency))

    pool = parameters.get(config.PARAMETERS_POOL)
    volume_pool = volume.pool_name
    if pool:
        if pool != volume_pool:
            raise ValidationException(
                messages.pool_not_match_volume_message.format(pool, volume_pool))
