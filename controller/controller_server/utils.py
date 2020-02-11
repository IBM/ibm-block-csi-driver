from controller.common.csi_logger import get_stdout_logger
import controller.controller_server.config as config
from controller.csi_general import csi_pb2
from controller.controller_server.errors import ValidationException
import controller.controller_server.messages as messages
from controller.array_action.config import FC_CONNECTIVITY_TYPE, ISCSI_CONNECTIVITY_TYPE
from controller.array_action.errors import HostNotFoundError, VolumeNotFoundError

logger = get_stdout_logger()


def get_array_connection_info_from_secret(secrets):
    user = secrets[config.SECRET_USERNAME_PARAMETER]
    password = secrets[config.SECRET_PASSWORD_PARAMETER]
    array_addresses = secrets[config.SECRET_ARRAY_PARAMETER].split(",")
    return user, password, array_addresses


def get_vol_id(new_vol):
    logger.debug('getting vol id for vol : {0}'.format(new_vol))
    vol_id = "{0}{1}{2}".format(new_vol.array_type, config.PARAMETERS_VOLUME_ID_DELIMITER, new_vol.id)
    logger.debug("vol id is : {0}".format(vol_id))
    return vol_id


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


def validate_create_volume_request(request):
    logger.debug("validating create volume request")

    logger.debug("validating volume name")
    if request.name == '':
        raise ValidationException(messages.name_should_be_empty_message)

    logger.debug("validating volume capacity")
    if request.capacity_range:
        if request.capacity_range.required_bytes < 0:
            raise ValidationException(messages.size_bigget_then_0_message)

    else:
        raise ValidationException(messages.no_capacity_range_message)

    logger.debug("validating volume capabilities")
    validate_csi_volume_capabilties(request.volume_capabilities)

    logger.debug("validating secrets")
    if request.secrets:
        validate_secret(request.secrets)

    logger.debug("validating storage class parameters")
    if request.parameters:
        if not (config.PARAMETERS_POOL in request.parameters):
            raise ValidationException(messages.pool_is_missing_message)

        if not request.parameters[config.PARAMETERS_POOL]:
            raise ValidationException(messages.wrong_pool_passed_message)
    else:
        raise ValidationException(messages.params_are_missing_message)

    logger.debug("request validation finished.")


def generate_csi_create_volume_response(new_vol):
    logger.debug("creating volume response for vol : {0}".format(new_vol))

    vol_context = {"volume_name": new_vol.volume_name,
                   "array_address": ",".join(
                       new_vol.array_address if isinstance(new_vol.array_address, list) else [new_vol.array_address]),
                   "pool_name": new_vol.pool_name,
                   "storage_type": new_vol.array_type
                   }

    res = csi_pb2.CreateVolumeResponse(volume=csi_pb2.Volume(
        capacity_bytes=new_vol.capacity_bytes,
        volume_id=get_vol_id(new_vol),
        volume_context=vol_context))

    logger.debug("finished creating volume response : {0}".format(res))
    return res


def validate_delete_volume_request(request):
    logger.debug("validating delete volume request")

    if request.volume_id == "":
        raise ValidationException("Volume id cannot be empty")

    logger.debug("validating secrets")
    if request.secrets:
        validate_secret(request.secrets)

    logger.debug("delete volume validation finished")


def validate_publish_volume_request(request):
    logger.debug("validating publish volume request")

    logger.debug("validating readonly")
    if request.readonly:
        raise ValidationException(messages.readoly_not_supported_message)

    logger.debug("validating volume capabilities")
    validate_csi_volume_capability(request.volume_capability)

    logger.debug("validating secrets")
    if request.secrets:
        validate_secret(request.secrets)
    else:
        raise ValidationException(messages.secret_missing_message)

    logger.debug("publish volume request validation finished.")


def get_volume_id_info(volume_id):
    logger.debug("getting volume info for vol id : {0}".format(volume_id))
    split_vol = volume_id.split(config.PARAMETERS_VOLUME_ID_DELIMITER)
    if len(split_vol) != 2:
        raise VolumeNotFoundError(volume_id)

    array_type, vol_id = split_vol
    logger.debug("volume id : {0}, array type :{1}".format(vol_id, array_type))
    return array_type, vol_id


def get_node_id_info(node_id):
    logger.debug("getting node info for node id : {0}".format(node_id))
    split_node = node_id.split(config.PARAMETERS_NODE_ID_DELIMITER)
    if len(split_node) != config.SUPPORTED_CONNECTIVITY_TYPES + 1:  # the 1 is for the hostname
        raise HostNotFoundError(node_id)

    hostname, iscsi_iqn, fc_wwns = split_node
    logger.debug("node name : {0}, iscsi_iqn : {1}, fc_wwns : {2} ".format(
        hostname, iscsi_iqn, fc_wwns))
    return hostname, iscsi_iqn, fc_wwns


def choose_connectivity_type(connecitvity_types):
    # If connectivity type support FC and iSCSI at the same time, chose FC
    logger.debug("choosing connectivity type for connectivity types : {0}".format(connecitvity_types))
    res = None
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
    hash_by_connectivity = {
        'iscsi': config["controller"]["publish_context_array_iqn"],
        'fc': config["controller"]["publish_context_fc_initiators"]}

    array_initiators = ",".join(array_initiators)
    res = csi_pb2.ControllerPublishVolumeResponse(
        publish_context={lun_param: str(lun),
                         connectivity_param: connectivity_type,
                         hash_by_connectivity[connectivity_type]: array_initiators})

    logger.debug("publish volume response is :{0}".format(res))
    return res


def validate_unpublish_volume_request(request):
    logger.debug("validating unpublish volume request")

    logger.debug("validating volume id")
    if len(request.volume_id.split(config.PARAMETERS_VOLUME_ID_DELIMITER)) != 2:
        raise ValidationException(messages.volume_id_wrong_format_message)

    logger.debug("validating secrets")
    if request.secrets:
        validate_secret(request.secrets)
    else:
        raise ValidationException(messages.secret_missing_message)

    logger.debug("unpublish volume request validation finished.")
