import grpc
from mock import Mock, MagicMock

from controller.csi_general import csi_pb2


class ProtoBufMock(MagicMock):
    def HasField(self, field):
        return hasattr(self, field)


def get_mock_mediator_response_volume(size, name, wwn, array_type, copy_source_id=None, space_efficiency=None):
    vol = Mock()
    vol.capacity_bytes = size
    vol.id = wwn
    vol.name = name
    vol.array_address = "arr1"
    vol.pool_name = "pool1"
    vol.array_type = array_type
    vol.copy_source_id = copy_source_id
    vol.space_efficiency = space_efficiency
    return vol


def get_mock_mediator_response_snapshot(capacity, name, wwn, volume_name, array_type):
    snapshot = Mock()
    snapshot.capacity_bytes = capacity
    snapshot.id = wwn
    snapshot.name = name
    snapshot.volume_name = volume_name
    snapshot.array_address = "arr1"
    snapshot.array_type = array_type
    snapshot.is_ready = True

    return snapshot


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


class FakeContext:

    def __init__(self):
        self.code = grpc.StatusCode.OK
        self.details = ""

    def set_code(self, code):
        self.code = code

    def set_details(self, details):
        self.details = details
