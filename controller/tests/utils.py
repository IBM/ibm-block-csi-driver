from mock import Mock
import grpc
from controller.csi_general import csi_pb2


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


def get_mock_volume_capability_object(mode=csi_pb2.VolumeCapability.AccessMode.SINGLE_NODE_WRITER, fs_type="ext4",
                                      mount_flags=None):
    if mount_flags is None:
        mount_flags = []
    return csi_pb2.VolumeCapability(
        mount=csi_pb2.VolumeCapability.MountVolume(fs_type=fs_type, mount_flags=mount_flags),
        access_mode=csi_pb2.VolumeCapability.AccessMode(mode=mode))


class FakeContext:

    def __init__(self):
        self.code = grpc.StatusCode.OK
        self.details = ""

    def set_code(self, code):
        self.code = code

    def set_details(self, details):
        self.details = details
