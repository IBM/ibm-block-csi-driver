from controller.common.csi_logger import get_stdout_logger
import config
from controller.csi_general import csi_pb2

logger = get_stdout_logger()


def get_array_connection_info_from_secret(secrets):
    user = secrets[config.SECRET_USERNAME_PARAMETER]
    password = secrets[config.SECRET_PASSWORD_PARAMETER]
    array_addresses = secrets[config.SECRET_ARRAY_PARAMETER].split(",")
    return user, password, array_addresses


def get_vol_id(new_vol):
    return "{}:{}".format(new_vol.storage_type, new_vol.volume_name)


def validate_secret(secret):
    if secret:
        if not (config.SECRET_USERNAME_PARAMETER in secret and
                config.SECRET_PASSWORD_PARAMETER in secret and
                config.SECRET_ARRAY_PARAMETER in secret):
            return False, 'invalid secret was passed'

    else:
        return False, 'secret is missing'

    return True, ""


def validate_volume_capabilties(capabilities):
    logger.debug("all volume capabilies: {}".format(capabilities))
    if len(capabilities) == 0:
        return False, "capbilities were not set"

    for cap in capabilities:
        if cap.mount:
            if cap.mount.fs_type:
                if cap.mount.fs_type not in config.SUPPORTED_FS_TYPES:
                    logger.error("unsupported fs_type : {}".format(cap.mount.fs_type))
                    return False, "unsupported fs_type"

        else:
            logger.error("only mount volume capability is supported")
            return False, "only mount volume capability is supported"

        if cap.access_mode.mode not in config.SUPPORTED_ACCESS_MODE:
            logger.error("unsupported access mode : {}".format(cap.access_mode))
            return False, "unsupported access mode"
    return True, ""


def validate_create_volume_request(request):
    logger.debug("validating request")

    logger.debug("validating volume name")
    if request.name == '':
        return False, 'name should not be empty'

    logger.debug("validating volume capacity")
    if request.capacity_range:
        if request.capacity_range.required_bytes <= 0:
            return False, 'size should be bigger then 0'
    else:
        return False, 'no capacity range set'

    logger.debug("validating volume capabilities")
    res, msg = validate_volume_capabilties(request.volume_capabilities)
    if not res:
        return False, msg

    logger.debug("validating secrets")
    if request.secrets:
        res, msg = validate_secret(request.secrets)
        if not res:
            return False, msg

    logger.debug("validating storage class parameters")
    if request.parameters:
        if not (config.PARAMETERS_CAPABILITIES in request.parameters and
                config.PARAMETERS_CAPACITY in request.parameters):
            return False, 'wrong parameters passed'

    else:
        return False, 'parameters are missing'

    logger.debug("request validation finished.")
    return True, ""


def get_create_volume_response(new_vol):
    vol_context = {"volume_name": new_vol.volume_name,
                   "array_name": ",".join(new_vol.array_name),
                   "pool_name": new_vol.pool_name,
                   "storage_type": new_vol.storage_type
                   }
    try:
        return csi_pb2.CreateVolumeResponse(volume=csi_pb2.Volume(
            capacity_bytes=new_vol.capacity_bytes,
            volume_id=get_vol_id(new_vol),
            volume_context=vol_context))

    except Exception as ex:
        logger.exception(ex)
        return csi_pb2.CreateVolumeResponse()


def get_volume_id_info(volume_id):
    split_vol = volume_id.split(":")
    if len(split_vol) != 2:
        return False, None, None
    array_type, vol_id = split_vol
    return True, array_type, vol_id
