import os
import ast
import base64
import json
from munch import Munch

from controllers.common.csi_logger import get_stdout_logger
from controllers.servers.utils import validate_secrets, get_array_connection_info_from_secrets
from controllers.servers.errors import ValidationException
import controllers.servers.host_definer.settings as host_definer_settings
import controllers.common.settings as common_settings
from controllers.servers.host_definer import settings


logger = get_stdout_logger()


def generate_io_group_from_labels(labels):
    io_group = ''
    for io_group_index in range(host_definer_settings.POSSIBLE_NUMBER_OF_IO_GROUP):
        label_content = labels.get(common_settings.IO_GROUP_LABEL_PREFIX + str(io_group_index))
        if label_content == host_definer_settings.TRUE_STRING:
            if io_group:
                io_group += common_settings.IO_GROUP_DELIMITER
            io_group += str(io_group_index)
    return io_group


def get_k8s_object_resource_version(k8s_object):
    if hasattr(k8s_object.metadata, 'resource_version'):
        return k8s_object.metadata.resource_version
    return k8s_object.metadata.resourceVersion


def change_decode_base64_secret_config(secret_data):
    if settings.SECRET_CONFIG_FIELD in secret_data.keys():
        secret_data[settings.SECRET_CONFIG_FIELD] = _decode_base64_to_dict(
            secret_data[settings.SECRET_CONFIG_FIELD])
    return secret_data


def _decode_base64_to_dict(content_with_base64):
    decoded_string_content = decode_base64_to_string(content_with_base64)
    my_dict_again = ast.literal_eval(decoded_string_content)
    return my_dict_again


def get_secret_config(secret_data):
    secret_data = _convert_secret_config_to_dict(secret_data)
    return secret_data.get(settings.SECRET_CONFIG_FIELD, {})


def _convert_secret_config_to_dict(secret_data):
    if settings.SECRET_CONFIG_FIELD in secret_data.keys():
        if type(secret_data[settings.SECRET_CONFIG_FIELD]) is str:
            secret_data[settings.SECRET_CONFIG_FIELD] = json.loads(secret_data[settings.SECRET_CONFIG_FIELD])
    return secret_data


def munch(watch_event):
    return Munch.fromDict(watch_event)


def loop_forever():
    return True


def validate_secret(secret_data):
    secret_data = _convert_secret_config_to_string(secret_data)
    try:
        validate_secrets(secret_data)
    except ValidationException as ex:
        logger.error(str(ex))


def get_prefix():
    return os.getenv(settings.PREFIX_ENV_VAR)


def get_connectivity_type_from_user(connectivity_type_label_on_node):
    if connectivity_type_label_on_node in settings.SUPPORTED_CONNECTIVITY_TYPES:
        return connectivity_type_label_on_node
    return os.getenv(settings.CONNECTIVITY_ENV_VAR)


def is_topology_label(label):
    for prefix in settings.TOPOLOGY_PREFIXES:
        if label.startswith(prefix):
            return True
    return False


def get_array_connection_info_from_secret_data(secret_data, labels):
    try:
        secret_data = _convert_secret_config_to_string(secret_data)
        array_connection_info = get_array_connection_info_from_secrets(secret_data, labels)
        return decode_array_connectivity_info(array_connection_info)
    except ValidationException as ex:
        logger.error(str(ex))
    return None


def _convert_secret_config_to_string(secret_data):
    if settings.SECRET_CONFIG_FIELD in secret_data.keys():
        if type(secret_data[settings.SECRET_CONFIG_FIELD]) is dict:
            secret_data[settings.SECRET_CONFIG_FIELD] = json.dumps(secret_data[settings.SECRET_CONFIG_FIELD])
    return secret_data


def decode_array_connectivity_info(array_connection_info):
    array_connection_info.array_addresses = _decode_list_base64_to_list_string(
        array_connection_info.array_addresses)
    array_connection_info.user = decode_base64_to_string(array_connection_info.user)
    array_connection_info.password = decode_base64_to_string(array_connection_info.password)
    return array_connection_info


def _decode_list_base64_to_list_string(list_with_base64):
    for index, base64_content in enumerate(list_with_base64):
        list_with_base64[index] = decode_base64_to_string(base64_content)
    return list_with_base64


def decode_base64_to_string(content_with_base64):
    try:
        base64_bytes = content_with_base64.encode('ascii')
        decoded_string_in_bytes = base64.b64decode(base64_bytes)
        return decoded_string_in_bytes.decode('ascii')
    except Exception:
        return content_with_base64
