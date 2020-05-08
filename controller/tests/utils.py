from mock import Mock
import grpc


def get_mock_mediator_response_volume(size, name, wwn, array_type):
    vol = Mock()
    vol.capacity_bytes = size
    vol.id = wwn
    vol.volume_name = name
    vol.array_address = "arr1"
    vol.pool_name = "pool1"
    vol.array_type = array_type

    return vol


def get_mock_mediator_response_snapshot(size, name, wwn, volume_name, array_type):
    snapshot = Mock()
    snapshot.capacity_bytes = size
    snapshot.id = wwn
    snapshot.snapshot_name = name
    snapshot.volume_name = volume_name
    snapshot.array_address = "arr1"
    snapshot.array_type = array_type

    return snapshot


class FakeContext:

    def __init__(self):
        self.code = grpc.StatusCode.OK
        self.details = ""

    def set_code(self, code):
        self.code = code

    def set_details(self, details):
        self.details = details
