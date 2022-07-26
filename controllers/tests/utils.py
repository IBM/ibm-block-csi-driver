import json

import grpc
from mock import Mock, MagicMock

from controllers.servers.csi.controller_types import ArrayConnectionInfo
from controllers.tests.controller_server.test_settings import USER as test_user, \
    PASSWORD as test_password, \
    ARRAY as test_array, VOLUME_NAME


class ProtoBufMock(MagicMock):
    def HasField(self, field):  # pylint: disable=invalid-name
        return hasattr(self, field)


def get_mock_mediator_response_volume(size=10, name=VOLUME_NAME, wwn="wwn1", array_type="a9k", source_id=None,
                                      space_efficiency='thick'):
    volume = Mock()
    volume.capacity_bytes = size
    volume.id = wwn
    volume.internal_id = "0"
    volume.name = name
    volume.array_address = "arr1"
    volume.pool = "pool1"
    volume.array_type = array_type
    volume.source_id = source_id
    volume.space_efficiency_aliases = space_efficiency if isinstance(space_efficiency, set) else {space_efficiency}
    return volume


def get_mock_mediator_response_snapshot(capacity, name, wwn, volume_name, array_type):
    snapshot = Mock()
    snapshot.capacity_bytes = capacity
    snapshot.id = wwn
    snapshot.internal_id = "0"
    snapshot.name = name
    snapshot.volume_name = volume_name
    snapshot.array_address = "arr1"
    snapshot.array_type = array_type
    snapshot.is_ready = True

    return snapshot


def get_mock_mediator_response_replication(name, volume_internal_id, other_volume_internal_id,
                                           copy_type="sync", is_primary=True, is_ready=True):
    replication = Mock()
    replication.name = name
    replication.volume_internal_id = volume_internal_id
    replication.other_volume_internal_id = other_volume_internal_id
    replication.copy_type = copy_type
    replication.is_primary = is_primary
    replication.is_ready = is_ready

    return replication


def get_mock_volume_capability(mode=1, fs_type="ext4", mount_flags=None):
    capability = ProtoBufMock(spec=["mount", "access_mode"])
    mount = ProtoBufMock(spec=["fs_type", "mount_flags"])
    access_mode = ProtoBufMock(spec=["mode"])
    setattr(mount, "mount_flags", mount_flags)
    setattr(mount, "fs_type", fs_type)
    setattr(access_mode, "mode", mode)
    setattr(capability, "mount", mount)
    setattr(capability, "access_mode", access_mode)
    return capability


def get_fake_array_connection_info(user="user", password="pass", array_addresses=None, system_id="u1"):
    if array_addresses is None:
        array_addresses = ["arr1"]
    return ArrayConnectionInfo(array_addresses=array_addresses, user=user, password=password, system_id=system_id)


def get_fake_secret_config(system_id="u1", username=test_user, password=test_password, management_address=test_array,
                           supported_topologies="default"):
    if supported_topologies == "default":
        supported_topologies = [{"test": "test"}]
    system_info = {}
    if username:
        system_info.update({"username": username})
    if password:
        system_info.update({"password": password})
    if management_address:
        system_info.update({"management_address": management_address})
    if supported_topologies:
        system_info.update({"supported_topologies": supported_topologies})
    config = {system_id: system_info}
    return {"config": json.dumps(config)}


class FakeContext:

    def __init__(self):
        self.code = grpc.StatusCode.OK
        self.details = ""

    def set_code(self, code):
        self.code = code

    def set_details(self, details):
        self.details = details
