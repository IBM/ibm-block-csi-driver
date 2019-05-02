import bunch
import grpc


def get_mock_xiv_volume(size, name, wwn):
    vol = bunch.Bunch()
    vol.capacity = size
    vol.wwn = wwn
    vol.name = name
    vol.pool_name = "vol-name"
    return vol


def get_mock_mediator_response_volume(size, name, wwn):
    vol = bunch.Bunch()
    vol.capacity_bytes = size
    vol.volume_id = wwn
    vol.volume_name = name
    vol.array_name = "arr1"
    vol.pool_name = "pool1"
    vol.storage_type = "xiv"

    return vol


class FakeContext:
    def __init__(self):
        self.code = grpc.StatusCode.OK
        self.details = ""

    def set_code(self, code):
        self.code = code

    def set_details(self, details):
        self.details = details
