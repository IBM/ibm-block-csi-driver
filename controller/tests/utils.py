from mock import Mock
import grpc


def get_mock_mediator_response_volume(size, name, wwn, array_type):
    vol = Mock()
    vol.capacity_bytes = size
    vol.id = wwn
    vol.volume_name = name
    vol.array_name = "arr1"
    vol.pool_name = "pool1"
    vol.array_type = array_type

    return vol


class FakeContext:

    def __init__(self):
        self.code = grpc.StatusCode.OK
        self.details = ""

    def set_code(self, code):
        self.code = code

    def set_details(self, details):
        self.details = details
