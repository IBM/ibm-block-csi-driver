from controller.common.csi_logger import get_stdout_logger
import config
from controller.csi_general import csi_pb2
from controller.controller_server.errors import ValidationException, VolumeIdError
import controller.controller_server.messages as messages

logger = get_stdout_logger()


def get_array_connection_info_from_secret(secrets):
    user = secrets[config.SECRET_USERNAME_PARAMETER]
    password = secrets[config.SECRET_PASSWORD_PARAMETER]
    array_addresses = secrets[config.SECRET_ARRAY_PARAMETER].split(",")
    return user, password, array_addresses


def get_vol_id(new_vol):
    return "{}:{}".format(new_vol.array_type, new_vol.volume_name)


def validate_secret(secret):
    if secret:
        if not (config.SECRET_USERNAME_PARAMETER in secret and
                config.SECRET_PASSWORD_PARAMETER in secret and
                config.SECRET_ARRAY_PARAMETER in secret):
            raise ValidationException(messages.invalid_secret_message)

    else:
        raise ValidationException(messages.secret_missing_message)


def validate_csi_volume_capabilties(capabilities):
    logger.debug("all volume capabilies: {}".format(capabilities))
    if len(capabilities) == 0:
        raise ValidationException(messages.capabilities_not_set_message)

    for cap in capabilities:
        if cap.mount:
            if cap.mount.fs_type:
                if cap.mount.fs_type not in config.SUPPORTED_FS_TYPES:
                    logger.error("unsupported fs type : {0}".format(cap.mount.fs_type))
                    raise ValidationException(messages.unsupported_fs_type_message)

        else:
            logger.error("only mount volume capability is supported")
            raise ValidationException(messages.only_mount_supported_message)

        if cap.access_mode.mode not in config.SUPPORTED_ACCESS_MODE:
            logger.error("unsupported access mode : {}".format(cap.access_mode))
            raise ValidationException(messages.unsupported_access_mode_message)


def validate_create_volume_request(request):
    logger.debug("validating request")

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
        if not (config.PARAMETERS_CAPACITY in request.parameters):
            raise ValidationException(messages.capacity_is_missing_message)

        if not len(request.parameters[config.PARAMETERS_CAPACITY].split(config.PARAMETERS_CAPACITY_DELIMITER)) == 2:
            raise ValidationException(messages.wrong_cpacity_passed_message)
    else:
        raise ValidationException(messages.params_are_missing_message)

    logger.debug("request validation finished.")


def generate_csi_create_volume_response(new_vol):
    vol_context = {"volume_name": new_vol.volume_name,
                   "array_name": ",".join(new_vol.array_name),
                   "pool_name": new_vol.pool_name,
                   "storage_type": new_vol.array_type
                   }
    return csi_pb2.CreateVolumeResponse(volume=csi_pb2.Volume(
        capacity_bytes=new_vol.capacity_bytes,
        volume_id=get_vol_id(new_vol),
        volume_context=vol_context))


def get_volume_id_info(volume_id):
    split_vol = volume_id.split(":")
    if len(split_vol) != 2:
        raise VolumeIdError(volume_id)

    array_type, vol_id = split_vol
    return True, array_type, vol_id
