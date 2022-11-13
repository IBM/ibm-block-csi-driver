import json

import grpc
from mock import Mock, MagicMock

from controllers.array_action.array_action_types import Replication
from controllers.servers.csi.controller_types import ArrayConnectionInfo
from controllers.tests.common.test_settings import SECRET_USERNAME_VALUE as test_user, \
    SECRET_PASSWORD_VALUE as test_password, ARRAY as test_array, VOLUME_NAME, VOLUME_UID, DUMMY_POOL1, \
    VOLUME_GROUP_NAME, VOLUME_GROUP_UID, \
    INTERNAL_VOLUME_GROUP_ID, INTERNAL_VOLUME_ID, COPY_TYPE, \
    SNAPSHOT_NAME, SNAPSHOT_VOLUME_NAME, SNAPSHOT_VOLUME_UID


class ProtoBufMock(MagicMock):
    def HasField(self, field):  # pylint: disable=invalid-name
        return hasattr(self, field)


def get_mock_mediator_response_volume(size=10, name=VOLUME_NAME, volume_id=VOLUME_UID, array_type="a9k", source_id=None,
                                      space_efficiency='thick'):
    volume = Mock()
    volume.capacity_bytes = size
    volume.id = volume_id
    volume.internal_id = INTERNAL_VOLUME_ID
    volume.name = name
    volume.array_address = "arr1"
    volume.pool = DUMMY_POOL1
    volume.array_type = array_type
    volume.source_id = source_id
    volume.space_efficiency_aliases = space_efficiency if isinstance(space_efficiency, set) else {space_efficiency}
    return volume


def _get_mock_mediator_response_volumes(volumes):
    response_volumes = []
    for volume in volumes:
        response_volumes.append(get_mock_mediator_response_volume(name=volume.name, volume_id=volume.id))
    return response_volumes


def get_mock_mediator_response_volume_group(name=VOLUME_GROUP_NAME, volume_id=VOLUME_GROUP_UID, volumes=None):
    if not volumes:
        volumes = []
    volume_group = Mock()
    volume_group.id = volume_id
    volume_group.internal_id = INTERNAL_VOLUME_GROUP_ID
    volume_group.name = name
    volume_group.array_type = "a9k"
    volume_group.volumes = _get_mock_mediator_response_volumes(volumes)
    return volume_group


def get_mock_mediator_response_snapshot(capacity=10, name=SNAPSHOT_NAME, snapshot_id=SNAPSHOT_VOLUME_UID,
                                        volume_name=SNAPSHOT_VOLUME_NAME, array_type="xiv"):
    snapshot = Mock()
    snapshot.capacity_bytes = capacity
    snapshot.id = snapshot_id
    snapshot.internal_id = "0"
    snapshot.name = name
    snapshot.source_id = volume_name
    snapshot.array_address = "arr1"
    snapshot.array_type = array_type
    snapshot.is_ready = True

    return snapshot


def get_mock_mediator_response_replication(name, replication_type,
                                           copy_type=COPY_TYPE, is_primary=False, is_ready=True, volume_group_id=None):
    replication = Replication(name=name,
                              replication_type=replication_type,
                              copy_type=copy_type,
                              is_ready=is_ready,
                              is_primary=is_primary,
                              volume_group_id=volume_group_id)

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
