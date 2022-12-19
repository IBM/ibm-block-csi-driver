import json
import re
from hashlib import sha256
from operator import eq

import base58
from csi_general import csi_pb2
from google.protobuf.timestamp_pb2 import Timestamp

import controllers.servers.messages as messages
import controllers.servers.settings as servers_settings
from controllers.array_action.array_action_types import ReplicationRequest
from controllers.array_action.settings import NVME_OVER_FC_CONNECTIVITY_TYPE, FC_CONNECTIVITY_TYPE, \
    ISCSI_CONNECTIVITY_TYPE, REPLICATION_COPY_TYPE_SYNC, REPLICATION_COPY_TYPE_ASYNC, REPLICATION_TYPE_MIRROR, \
    REPLICATION_TYPE_EAR, REPLICATION_DEFAULT_COPY_TYPE
from controllers.common import settings
from controllers.common.config import config as common_config
from controllers.common.csi_logger import get_stdout_logger
from controllers.common.settings import NAME_PREFIX_SEPARATOR
from controllers.servers.csi.controller_types import (ArrayConnectionInfo,
                                                      ObjectIdInfo,
                                                      ObjectParameters, VolumeGroupParameters)
from controllers.servers.errors import ObjectIdError, ValidationException, InvalidNodeId

logger = get_stdout_logger()


def _parse_raw_json(raw_json):
    try:
        parsed_json = json.loads(raw_json)
    except json.decoder.JSONDecodeError as ex:
        raise ValidationException(messages.INVALID_JSON_PARAMETER_MESSAGE.format(raw_json, ex))
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
    if request.HasField(servers_settings.REQUEST_ACCESSIBILITY_REQUIREMENTS_FIELD):
        accessibility_requirements = request.accessibility_requirements
        if accessibility_requirements.preferred:
            topologies = accessibility_requirements.preferred[0].segments
            logger.info("Chosen volume topologies: {}".format(topologies))
            return topologies
    return None


def _get_system_info_for_topologies(secrets_config, node_topologies):
    for system_id, system_info in secrets_config.items():
        system_topologies = system_info.get(servers_settings.SECRET_SUPPORTED_TOPOLOGIES_PARAMETER)
        if _is_topology_match(system_topologies, node_topologies):
            return system_info, system_id
    raise ValidationException(messages.NO_SYSTEM_MATCH_REQUESTED_TOPOLOGIES.format(node_topologies))


def _get_system_info_from_secrets(secrets, topologies=None, system_id=None):
    raw_secrets_config = secrets.get(servers_settings.SECRET_CONFIG_PARAMETER)
    system_info = secrets
    if raw_secrets_config:
        secrets_config = _parse_raw_json(raw_json=raw_secrets_config)
        if system_id:
            system_info = secrets_config.get(system_id)
        elif topologies:
            system_info, system_id = _get_system_info_for_topologies(secrets_config=secrets_config,
                                                                     node_topologies=topologies)
        else:
            raise ValidationException(messages.INSUFFICIENT_DATA_TO_CHOOSE_A_STORAGE_SYSTEM_MESSAGE)
    return system_info, system_id


def _get_array_connection_info_from_system_info(secrets, system_id):
    user = secrets[servers_settings.SECRET_USERNAME_PARAMETER]
    password = secrets[servers_settings.SECRET_PASSWORD_PARAMETER]
    array_addresses = secrets[servers_settings.SECRET_ARRAY_PARAMETER].split(
        servers_settings.PARAMETERS_ARRAY_ADDRESSES_DELIMITER)
    return ArrayConnectionInfo(array_addresses=array_addresses, user=user, password=password, system_id=system_id)


def get_array_connection_info_from_secrets(secrets, topologies=None, system_id=None):
    system_info, system_id = _get_system_info_from_secrets(secrets, topologies, system_id)
    return _get_array_connection_info_from_system_info(system_info, system_id)


def get_volume_parameters(parameters, system_id=None):
    return get_object_parameters(parameters, servers_settings.PARAMETERS_VOLUME_NAME_PREFIX, system_id)


def get_snapshot_parameters(parameters, system_id):
    return get_object_parameters(parameters, servers_settings.PARAMETERS_SNAPSHOT_NAME_PREFIX, system_id)


def get_volume_group_parameters(parameters):
    return VolumeGroupParameters(prefix=parameters.get(servers_settings.PARAMETERS_VOLUME_GROUP_NAME_PREFIX))


def _str_to_bool(parameter):
    if parameter and parameter.lower() == "true":
        return True
    return False


def get_object_parameters(parameters, prefix_param_name, system_id):
    raw_parameters_by_system = parameters.get(servers_settings.PARAMETERS_BY_SYSTEM)
    system_parameters = {}
    if raw_parameters_by_system and system_id:
        parameters_by_system = _parse_raw_json(raw_json=raw_parameters_by_system)
        system_parameters = parameters_by_system.get(system_id, {})
    default_pool = parameters.get(servers_settings.PARAMETERS_POOL)
    default_space_efficiency = parameters.get(servers_settings.PARAMETERS_SPACE_EFFICIENCY)
    default_prefix = parameters.get(prefix_param_name)
    default_io_group = parameters.get(servers_settings.PARAMETERS_IO_GROUP)
    default_volume_group = parameters.get(servers_settings.PARAMETERS_VOLUME_GROUP)
    default_virt_snap_func = parameters.get(servers_settings.PARAMETERS_VIRT_SNAP_FUNC)
    virt_snap_func_str = system_parameters.get(servers_settings.PARAMETERS_VIRT_SNAP_FUNC, default_virt_snap_func)
    is_virt_snap_func = _str_to_bool(virt_snap_func_str)
    return ObjectParameters(
        pool=system_parameters.get(servers_settings.PARAMETERS_POOL, default_pool),
        space_efficiency=system_parameters.get(servers_settings.PARAMETERS_SPACE_EFFICIENCY, default_space_efficiency),
        prefix=system_parameters.get(prefix_param_name, default_prefix),
        io_group=system_parameters.get(servers_settings.PARAMETERS_IO_GROUP, default_io_group),
        volume_group=system_parameters.get(servers_settings.PARAMETERS_VOLUME_GROUP, default_volume_group),
        virt_snap_func=is_virt_snap_func)


def get_volume_id(new_volume, system_id):
    return _get_object_id(new_volume, system_id)


def get_snapshot_id(new_snapshot, system_id):
    return _get_object_id(new_snapshot, system_id)


def _get_object_id(obj, system_id):
    object_ids_delimiter = servers_settings.PARAMETERS_OBJECT_IDS_DELIMITER
    object_ids_value = object_ids_delimiter.join((obj.internal_id, obj.id))
    object_id_info_delimiter = servers_settings.PARAMETERS_OBJECT_ID_INFO_DELIMITER
    if system_id:
        return object_id_info_delimiter.join((obj.array_type, system_id, object_ids_value))
    return object_id_info_delimiter.join((obj.array_type, object_ids_value))


def _is_system_id_valid(system_id):
    return system_id and re.match(servers_settings.SECRET_VALIDATION_REGEX, system_id)


def _validate_system_id(system_id):
    if not _is_system_id_valid(system_id):
        raise ValidationException(
            messages.INVALID_SYSTEM_ID_MESSAGE.format(system_id, servers_settings.SECRET_VALIDATION_REGEX))
    if len(system_id) > servers_settings.SECRET_SYSTEM_ID_MAX_LENGTH:
        raise ValidationException(
            messages.PARAMETER_LENGTH_IS_TOO_LONG.format("system id", system_id,
                                                         servers_settings.SECRET_SYSTEM_ID_MAX_LENGTH))


def _validate_secrets(secrets):
    if not (servers_settings.SECRET_USERNAME_PARAMETER in secrets and
            servers_settings.SECRET_PASSWORD_PARAMETER in secrets and
            servers_settings.SECRET_ARRAY_PARAMETER in secrets):
        raise ValidationException(messages.SECRET_MISSING_CONNECTION_INFO_MESSAGE)


def _validate_topologies(topologies):
    if topologies:
        if not all(topologies):
            raise ValidationException(messages.SECRET_MISSING_TOPOLOGIES_MESSAGE)
    else:
        raise ValidationException(messages.SECRET_MISSING_TOPOLOGIES_MESSAGE)


def _validate_secrets_config(secrets_config):
    for system_id, system_info in secrets_config.items():
        if system_id and system_info:
            _validate_system_id(system_id)
            _validate_secrets(system_info)
            supported_topologies = system_info.get(servers_settings.SECRET_SUPPORTED_TOPOLOGIES_PARAMETER)
            _validate_topologies(supported_topologies)
        else:
            raise ValidationException(messages.INVALID_SECRET_CONFIG_MESSAGE)


def validate_secrets(secrets):
    logger.debug("validating secrets")
    if not secrets:
        raise ValidationException(messages.SECRET_MISSING_MESSAGE)
    raw_secrets_config = secrets.get(servers_settings.SECRET_CONFIG_PARAMETER)
    if raw_secrets_config:
        secrets_config = _parse_raw_json(raw_secrets_config)
        _validate_secrets_config(secrets_config)
    else:
        _validate_secrets(secrets)
    logger.debug("secrets validation finished")


def validate_csi_volume_capability(cap):
    logger.debug("validating csi volume capability")
    if cap.HasField(servers_settings.VOLUME_CAPABILITIES_FIELD_ACCESS_TYPE_MOUNT):
        if cap.mount.fs_type and (cap.mount.fs_type not in servers_settings.SUPPORTED_FS_TYPES):
            raise ValidationException(messages.UNSUPPORTED_FS_TYPE_MESSAGE.format(cap.mount.fs_type))
        if cap.mount.mount_flags:
            raise ValidationException(messages.UNSUPPORTED_MOUNT_FLAGS_MESSAGE)

    elif not cap.HasField(servers_settings.VOLUME_CAPABILITIES_FIELD_ACCESS_TYPE_BLOCK):
        # should never get here since the value can be only mount (for fs volume) or block (for raw block)
        logger.error(messages.UNSUPPORTED_VOLUME_ACCESS_TYPE_MESSAGE)
        raise ValidationException(messages.UNSUPPORTED_VOLUME_ACCESS_TYPE_MESSAGE)

    if cap.access_mode.mode not in servers_settings.SUPPORTED_ACCESS_MODE:
        logger.error("unsupported access mode : {}".format(cap.access_mode))
        raise ValidationException(messages.UNSUPPORTED_ACCESS_MODE_MESSAGE.format(cap.access_mode))

    logger.debug("csi volume capabilities validation finished.")


def validate_csi_volume_capabilities(capabilities):
    logger.debug("validating csi volume capabilities")
    if not capabilities:
        raise ValidationException(messages.CAPABILITIES_NOT_SET_MESSAGE)

    for cap in capabilities:
        validate_csi_volume_capability(cap)

    logger.debug("finished validating csi volume capabilities.")


def validate_create_volume_source(request):
    source = request.volume_content_source
    if source:
        logger.info(source)
        if source.HasField(servers_settings.SNAPSHOT_TYPE_NAME):
            _validate_source_info(source, servers_settings.SNAPSHOT_TYPE_NAME)
        elif source.HasField(servers_settings.VOLUME_TYPE_NAME):
            _validate_source_info(source, servers_settings.VOLUME_TYPE_NAME)


def _validate_source_info(source, source_type):
    source_object = getattr(source, source_type)
    logger.info("Source {0} specified: {1}".format(source_type, source_object))
    source_object_id = getattr(source_object, servers_settings.VOLUME_SOURCE_ID_FIELDS[source_type])
    message = messages.VOLUME_SOURCE_ID_IS_MISSING.format(source_type)
    _validate_object_id(source_object_id, object_type=source_type, message=message)


def _validate_pool_parameter(parameters):
    logger.debug("validating pool parameter")
    if servers_settings.PARAMETERS_POOL in parameters:
        if not parameters[servers_settings.PARAMETERS_POOL]:
            raise ValidationException(messages.POOL_SHOULD_NOT_BE_EMPTY_MESSAGE)
    elif not parameters.get(servers_settings.PARAMETERS_BY_SYSTEM):
        raise ValidationException(messages.POOL_IS_MISSING_MESSAGE)


def _validate_object_id(object_id, object_type=servers_settings.VOLUME_TYPE_NAME,
                        message=messages.VOLUME_ID_SHOULD_NOT_BE_EMPTY_MESSAGE):
    logger.debug("validating volume id")
    object_id_info_delimiter = servers_settings.PARAMETERS_OBJECT_ID_INFO_DELIMITER
    if not object_id:
        raise ValidationException(message)
    if object_id_info_delimiter not in object_id:
        raise ObjectIdError(object_type, object_id)
    if len(object_id.split(object_id_info_delimiter)) not in {servers_settings.MINIMUM_VOLUME_ID_PARTS,
                                                              servers_settings.MAXIMUM_VOLUME_ID_PARTS}:
        raise ValidationException(messages.WRONG_FORMAT_MESSAGE.format("volume id"))


def _validate_request_required_field(field_value, field_name):
    logger.debug("validating request {}".format(field_name))
    if not field_value:
        raise ValidationException(messages.PARAMETER_SHOULD_NOT_BE_EMPTY_MESSAGE.format(field_name))


def _validate_minimum_request_fields(request, required_field_names):
    for required_field_name in required_field_names:
        _validate_request_required_field(getattr(request, required_field_name), required_field_name)
    validate_secrets(request.secrets)


def validate_create_volume_request(request):
    logger.debug("validating create volume request")

    _validate_minimum_request_fields(request, ["name"])

    logger.debug("validating volume capacity")
    if request.capacity_range:
        if request.capacity_range.required_bytes < 0:
            raise ValidationException(messages.SIZE_SHOULD_NOT_BE_NEGATIVE_MESSAGE)

    else:
        raise ValidationException(messages.NO_CAPACITY_RANGE_MESSAGE)

    validate_csi_volume_capabilities(request.volume_capabilities)

    if request.parameters:
        _validate_pool_parameter(request.parameters)
    else:
        raise ValidationException(messages.POOL_IS_MISSING_MESSAGE)

    logger.debug("validating volume copy source")
    validate_create_volume_source(request)

    logger.debug("request validation finished.")


def validate_create_volume_group_request(request):
    logger.debug("validating create volume group request")

    _validate_minimum_request_fields(request, ["name"])

    logger.debug("request validation finished.")


def validate_create_snapshot_request(request):
    logger.debug("validating create snapshot request")
    _validate_minimum_request_fields(request, ["name"])

    logger.debug("validating source volume id")
    if not request.source_volume_id:
        raise ValidationException(messages.SNAPSHOT_SOURCE_VOLUME_ID_IS_MISSING)
    logger.debug("request validation finished.")


def validate_delete_snapshot_request(request):
    logger.debug("validating delete snapshot request")

    _validate_minimum_request_fields(request, ["snapshot_id"])

    logger.debug("request validation finished.")


def validate_validate_volume_capabilities_request(request):
    logger.debug("validating validate_volume_capabilities request")
    _validate_minimum_request_fields(request, ["volume_id"])
    _validate_object_id(request.volume_id)

    if request.parameters:
        _validate_pool_parameter(request.parameters)

    validate_csi_volume_capabilities(request.volume_capabilities)


def validate_volume_context_match_volume(volume_context, volume):
    logger.debug("validate volume_context is matching volume")
    context_from_existing_volume = _get_context_from_volume(volume)

    if volume_context and volume_context != context_from_existing_volume:
        raise ValidationException(
            messages.VOLUME_CONTEXT_NOT_MATCH_VOLUME_MESSAGE.format(volume_context, context_from_existing_volume))
    logger.debug("volume_context validation finished.")


def validate_expand_volume_request(request):
    logger.debug("validating expand volume request")

    _validate_minimum_request_fields(request, ["volume_id"])

    logger.debug("validating volume capacity")
    if request.capacity_range:
        if request.capacity_range.required_bytes < 0:
            raise ValidationException(messages.SIZE_SHOULD_NOT_BE_NEGATIVE_MESSAGE)
    else:
        raise ValidationException(messages.NO_CAPACITY_RANGE_MESSAGE)

    logger.debug("expand volume validation finished")


def _generate_volumes_response(new_volumes):
    volumes = []
    for volume in new_volumes:
        volumes.append(_generate_volume_response(volume))
    return volumes


def _generate_volume_response(new_volume, system_id=None, source_type=None):
    content_source = None
    if new_volume.source_id:
        if source_type == servers_settings.SNAPSHOT_TYPE_NAME:
            snapshot_source = csi_pb2.VolumeContentSource.SnapshotSource(snapshot_id=new_volume.source_id)
            content_source = csi_pb2.VolumeContentSource(snapshot=snapshot_source)
        else:
            volume_source = csi_pb2.VolumeContentSource.VolumeSource(volume_id=new_volume.source_id)
            content_source = csi_pb2.VolumeContentSource(volume=volume_source)

    return csi_pb2.Volume(
        capacity_bytes=new_volume.capacity_bytes,
        volume_id=get_volume_id(new_volume, system_id),
        content_source=content_source)


def generate_csi_create_volume_response(new_volume, system_id=None, source_type=None):
    logger.debug("creating create volume response for volume : {0}".format(new_volume))

    response = csi_pb2.CreateVolumeResponse(volume=_generate_volume_response(new_volume, system_id, source_type))

    logger.debug("finished creating volume response : {0}".format(response))
    return response


def generate_csi_create_volume_group_response(volume_group):
    logger.debug("creating create volume group response for volume group : {0}".format(volume_group))

    response = csi_pb2.CreateVolumeGroupResponse(volume_group=csi_pb2.VolumeGroup(
        volume_group_id=_get_object_id(volume_group, None),
        volumes=[]))
    logger.debug("finished creating volume group response : {0}".format(response))

    return response


def generate_csi_modify_volume_group_response(volume_group):
    logger.debug("creating modify volume group response for volume group : {0}".format(volume_group))

    response = csi_pb2.ModifyVolumeGroupMembershipResponse(volume_group=csi_pb2.VolumeGroup(
        volume_group_id=_get_object_id(volume_group, None),
        volumes=_generate_volumes_response(volume_group.volumes)))
    logger.debug("finished creating volume group response : {0}".format(response))

    return response


def generate_csi_create_snapshot_response(new_snapshot, system_id, source_volume_id):
    logger.debug("creating create snapshot response for snapshot : {0}".format(new_snapshot))

    response = csi_pb2.CreateSnapshotResponse(snapshot=csi_pb2.Snapshot(
        size_bytes=new_snapshot.capacity_bytes,
        snapshot_id=get_snapshot_id(new_snapshot, system_id),
        source_volume_id=source_volume_id,
        creation_time=get_current_timestamp(),
        ready_to_use=new_snapshot.is_ready))

    logger.debug("finished creating snapshot response : {0}".format(response))
    return response


def generate_csi_expand_volume_response(capacity_bytes, node_expansion_required=True):
    logger.debug("creating response for expand volume")
    response = csi_pb2.ControllerExpandVolumeResponse(
        capacity_bytes=capacity_bytes,
        node_expansion_required=node_expansion_required,
    )

    logger.debug("finished creating expand volume response")
    return response


def _get_supported_capability(volume_capability):
    access_mode = csi_pb2.VolumeCapability.AccessMode(mode=volume_capability.access_mode.mode)

    if volume_capability.HasField(servers_settings.VOLUME_CAPABILITIES_FIELD_ACCESS_TYPE_MOUNT):
        return csi_pb2.VolumeCapability(
            mount=csi_pb2.VolumeCapability.MountVolume(fs_type=volume_capability.mount.fs_type),
            access_mode=access_mode)

    return csi_pb2.VolumeCapability(
        mount=csi_pb2.VolumeCapability.BlockVolume(),
        access_mode=access_mode)


def generate_csi_validate_volume_capabilities_response(volume_context, volume_capabilities, parameters):
    logger.debug("creating validate volume capabilities response")

    capabilities = []
    for capability in volume_capabilities:
        supported_capability = _get_supported_capability(volume_capability=capability)
        capabilities.append(supported_capability)

    confirmed = csi_pb2.ValidateVolumeCapabilitiesResponse.Confirmed(
        volume_context=volume_context,
        volume_capabilities=capabilities,
        parameters=parameters)
    response = csi_pb2.ValidateVolumeCapabilitiesResponse(confirmed=confirmed)

    logger.debug("finished creating validate volume capabilities response")
    return response


def validate_delete_volume_request(request):
    logger.debug("validating delete volume request")

    _validate_minimum_request_fields(request, ["volume_id"])

    logger.debug("delete volume validation finished")


def _validate_node_id(node_id):
    logger.debug("validating node id")

    delimiter_count = node_id.count(settings.PARAMETERS_NODE_ID_DELIMITER)

    if not 1 <= delimiter_count <= 3:
        raise InvalidNodeId(node_id)

    logger.debug("node id validation finished")


def validate_publish_volume_request(request):
    logger.debug("validating publish volume request")

    logger.debug("validating readonly")
    if request.readonly:
        raise ValidationException(messages.READONLY_NOT_SUPPORTED_MESSAGE)

    validate_csi_volume_capability(request.volume_capability)

    _validate_minimum_request_fields(request, ["node_id"])

    _validate_node_id(request.node_id)

    logger.debug("publish volume request validation finished.")


def get_volume_id_info(volume_id):
    return get_object_id_info(volume_id, servers_settings.VOLUME_TYPE_NAME)


def get_snapshot_id_info(snapshot_id):
    return get_object_id_info(snapshot_id, servers_settings.SNAPSHOT_TYPE_NAME)


def get_volume_group_id_info(volume_group_id):
    return get_object_id_info(volume_group_id, servers_settings.VOLUME_GROUP_TYPE_NAME)


def _get_context_from_volume(volume):
    return {servers_settings.VOLUME_CONTEXT_VOLUME_NAME: volume.name,
            servers_settings.VOLUME_CONTEXT_ARRAY_ADDRESS: ",".join(
                volume.array_address if isinstance(volume.array_address, list) else [volume.array_address]),
            servers_settings.VOLUME_CONTEXT_POOL: volume.pool,
            servers_settings.VOLUME_CONTEXT_STORAGE_TYPE: volume.array_type
            }


def get_object_id_info(full_object_id, object_type):
    logger.debug("getting {0} info for id : {1}".format(object_type, full_object_id))
    splitted_object_id = full_object_id.split(servers_settings.PARAMETERS_OBJECT_ID_INFO_DELIMITER)
    system_id, wwn, internal_id = None, None, None
    if len(splitted_object_id) == 2:
        array_type, object_id = splitted_object_id
    elif len(splitted_object_id) == 3:
        array_type, system_id, object_id = splitted_object_id
    else:
        raise ObjectIdError(object_type, full_object_id)
    splitted_id = object_id.split(servers_settings.PARAMETERS_OBJECT_IDS_DELIMITER)
    if len(splitted_id) == 1:
        wwn = splitted_id[0]
    elif len(splitted_id) == 2:
        internal_id, wwn = splitted_id
    else:
        raise ObjectIdError(object_type, full_object_id)
    logger.debug("{0} id : {1}, array type :{2}".format(object_type, object_id, array_type))
    return ObjectIdInfo(array_type=array_type, system_id=system_id, internal_id=internal_id, uid=wwn)


def choose_connectivity_type(connectivity_types):
    logger.debug("choosing connectivity type for connectivity types : {0}".format(connectivity_types))
    if NVME_OVER_FC_CONNECTIVITY_TYPE in connectivity_types:
        logger.debug("connectivity type is : {0}".format(NVME_OVER_FC_CONNECTIVITY_TYPE))
        return NVME_OVER_FC_CONNECTIVITY_TYPE
    if FC_CONNECTIVITY_TYPE in connectivity_types:
        logger.debug("connectivity type is : {0}".format(FC_CONNECTIVITY_TYPE))
        return FC_CONNECTIVITY_TYPE
    if ISCSI_CONNECTIVITY_TYPE in connectivity_types:
        logger.debug("connectivity type is : {0}".format(ISCSI_CONNECTIVITY_TYPE))
        return ISCSI_CONNECTIVITY_TYPE
    return None


def generate_csi_publish_volume_response(lun, connectivity_type, array_initiators):
    logger.debug("generating publish volume response for lun :{0}, connectivity : {1}".format(lun, connectivity_type))

    lun_param = common_config.controller.publish_context_lun_parameter
    connectivity_param = common_config.controller.publish_context_connectivity_parameter
    separator = common_config.controller.publish_context_separator

    publish_context = {
        lun_param: str(lun),
        connectivity_param: connectivity_type
    }

    if connectivity_type == FC_CONNECTIVITY_TYPE:
        array_initiators_param = common_config.controller.publish_context_fc_initiators
        publish_context[array_initiators_param] = separator.join(array_initiators)
    elif connectivity_type == ISCSI_CONNECTIVITY_TYPE:
        for iqn, ips in array_initiators.items():
            publish_context[iqn] = separator.join(ips)

        array_initiators_param = common_config.controller.publish_context_array_iqn
        publish_context[array_initiators_param] = separator.join(array_initiators.keys())

    response = csi_pb2.ControllerPublishVolumeResponse(publish_context=publish_context)

    logger.debug("publish volume response is :{0}".format(response))
    return response


def validate_unpublish_volume_request(request):
    logger.debug("validating unpublish volume request")

    _validate_object_id(request.volume_id)

    _validate_minimum_request_fields(request, ["volume_id"])

    _validate_node_id(request.node_id)

    logger.debug("unpublish volume request validation finished.")


def validate_addons_request(request, replication_type):
    logger.debug("validating addons request")
    minimum_request_fields = ["volume_id"]
    if replication_type == REPLICATION_TYPE_MIRROR:
        minimum_request_fields.append("replication_id")
    _validate_minimum_request_fields(request, minimum_request_fields)

    if replication_type == REPLICATION_TYPE_EAR:
        logger.debug("validating obsolete non-EAR parameters")
        _validate_addons_request_for_replication_id(request)
        _validate_addons_request_for_system_id(request)

    logger.debug("validating copy type")
    if servers_settings.PARAMETERS_COPY_TYPE in request.parameters:
        copy_type = request.parameters.get(servers_settings.PARAMETERS_COPY_TYPE)
        if copy_type not in (REPLICATION_COPY_TYPE_SYNC, REPLICATION_COPY_TYPE_ASYNC):
            raise ValidationException(messages.INVALID_REPLICATION_COPY_TYPE_MESSAGE.format(copy_type))

    logger.debug("addons request validation finished")


def _validate_addons_request_for_replication_id(request):
    if request.replication_id != "":
        raise ValidationException(messages.INVALID_EAR_PARAMETER_MESSAGE.format(
            servers_settings.PARAMETERS_REPLICATION_HANDLE))


def _validate_addons_request_for_system_id(request):
    if request.parameters.get(servers_settings.PARAMETERS_SYSTEM_ID):
        raise ValidationException(messages.INVALID_EAR_PARAMETER_MESSAGE.format(
            servers_settings.PARAMETERS_SYSTEM_ID))


def get_addons_replication_type(request):
    if servers_settings.PARAMETERS_REPLICATION_POLICY in request.parameters:
        replication_type = REPLICATION_TYPE_EAR
    else:
        replication_type = REPLICATION_TYPE_MIRROR

    logger.info("replication type is {}".format(replication_type))
    return replication_type


def generate_addons_replication_request(request, replication_type, object_type):
    volume_internal_id = _get_volume_internal_id(request, object_type)
    other_volume_internal_id = _get_other_volume_internal_id(request, replication_type)

    other_system_id = request.parameters.get(servers_settings.PARAMETERS_SYSTEM_ID)
    copy_type = request.parameters.get(servers_settings.PARAMETERS_COPY_TYPE, REPLICATION_DEFAULT_COPY_TYPE)
    replication_policy = request.parameters.get(servers_settings.PARAMETERS_REPLICATION_POLICY)
    return ReplicationRequest(volume_internal_id=volume_internal_id,
                              other_volume_internal_id=other_volume_internal_id,
                              other_system_id=other_system_id,
                              copy_type=copy_type,
                              replication_type=replication_type,
                              replication_policy=replication_policy)


def _get_volume_internal_id(request, object_type):
    if object_type == servers_settings.VOLUME_GROUP_TYPE_NAME:
        volume_id_info = get_volume_group_id_info(request.volume_id)
        volume_internal_id = volume_id_info.ids.uid
    else:
        volume_id_info = get_volume_id_info(request.volume_id)
        volume_internal_id = volume_id_info.ids.internal_id
    return volume_internal_id


def _get_other_volume_internal_id(request, replication_type):
    if replication_type == REPLICATION_TYPE_MIRROR:
        other_volume_id_info = get_volume_id_info(request.replication_id)
        other_volume_internal_id = other_volume_id_info.ids.internal_id
    else:
        other_volume_internal_id = None
    return other_volume_internal_id


def get_current_timestamp():
    timestamp = Timestamp()
    timestamp.GetCurrentTime()
    return timestamp


def hash_string(string):
    return base58.b58encode(sha256(string.encode()).digest()).decode()


def _validate_parameter_matches_volume(parameter_value, value_from_volume, error_message_format, cmp=eq):
    if parameter_value and not cmp(parameter_value, value_from_volume):
        raise ValidationException(error_message_format.format(parameter_value, value_from_volume))


def _validate_space_efficiency_match(space_efficiency, volume):
    if space_efficiency:
        space_efficiency = space_efficiency.lower()
    _validate_parameter_matches_volume(space_efficiency, volume.space_efficiency_aliases,
                                       messages.SPACE_EFFICIENCY_NOT_MATCH_VOLUME_MESSAGE,
                                       lambda se, se_aliases: se in se_aliases)


def validate_parameters_match_volume(parameters, volume):
    logger.debug("validating space efficiency parameter matches volume's")
    space_efficiency = parameters.get(servers_settings.PARAMETERS_SPACE_EFFICIENCY)
    _validate_space_efficiency_match(space_efficiency, volume)

    logger.debug("validating pool parameter matches volume's")
    pool = parameters.get(servers_settings.PARAMETERS_POOL)
    _validate_parameter_matches_volume(pool, volume.pool, messages.POOL_NOT_MATCH_VOLUME_MESSAGE)

    logger.debug("validating prefix parameter matches volume's")
    prefix = parameters.get(servers_settings.PARAMETERS_VOLUME_NAME_PREFIX)
    _validate_parameter_matches_volume(prefix, volume.name, messages.PREFIX_NOT_MATCH_VOLUME_MESSAGE,
                                       lambda pref, name: name.startswith(pref + NAME_PREFIX_SEPARATOR))


def join_object_prefix_with_name(prefix, name):
    if prefix:
        return settings.NAME_PREFIX_SEPARATOR.join((prefix, name))
    return name


def validate_parameters_match_source_volume(space_efficiency, required_bytes, volume):
    _validate_space_efficiency_match(space_efficiency, volume)
    volume_capacity_bytes = volume.capacity_bytes
    if volume_capacity_bytes < required_bytes:
        raise ValidationException(messages.REQUIRED_BYTES_MISMATCH_MESSAGE.format(
            required_bytes, volume_capacity_bytes))


def get_volume_group_from_request(request_volume_group, parameters_volume_group, array_mediator):
    storage_class_volume_group = parameters_volume_group
    if request_volume_group:
        volume_group_id = get_volume_group_id_info(request_volume_group)
        volume_group = array_mediator.get_volume_group(volume_group_id.ids.internal_id)
        volume_group_name = volume_group.name
        if storage_class_volume_group != "" and volume_group_name != "":
            raise ValidationException(messages.UNSUPPORTED_STORAGECLASS_VOLUME_GROUP)
        return volume_group_name
    return storage_class_volume_group


def validate_delete_volume_group_request(request):
    logger.debug("validating delete volume group request")

    _validate_minimum_request_fields(request, ["volume_group_id"])

    logger.debug("delete volume group validation finished")


def validate_modify_volume_group_request(request):
    logger.debug("validating modify volume group request")

    _validate_minimum_request_fields(request, ["volume_group_id"])

    logger.debug("modify volume group validation finished")


def get_replication_object_type_and_id_info(request):
    object_type = servers_settings.VOLUME_GROUP_TYPE_NAME
    object_id = request.volume_id
    object_info = None
    if object_info:
        logger.info(object_info)
        if object_info.HasField(servers_settings.VOLUME_TYPE_NAME):
            object_id = object_info.volume.volume_id
            object_type = servers_settings.VOLUME_TYPE_NAME
        elif object_info.HasField(servers_settings.VOLUME_GROUP_TYPE_NAME):
            object_id = object_info.volume.volume_group_id
            object_type = servers_settings.VOLUME_GROUP_TYPE_NAME
        else:
            return object_type, object_id
    object_id_info = get_object_id_info(object_id, object_type)
    return object_type, object_id_info
