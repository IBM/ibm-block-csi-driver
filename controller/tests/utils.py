from mock import Mock
import grpc

from controller.controller_server.controller_types import ArrayConnectionInfo


def get_mock_mediator_response_volume(size, name, wwn, array_type, copy_source_id=None):
    volume = Mock()
    volume.capacity_bytes = size
    volume.id = wwn
    volume.name = name
    volume.array_address = "arr1"
    volume.pool = "pool1"
    volume.array_type = array_type
    volume.copy_source_id = copy_source_id

    return volume


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


def get_mock_array_connection_info(user="user", password="pass", array_addresses=None, uid="u1"):
    if array_addresses is None:
        array_addresses = ["arr1"]
    return ArrayConnectionInfo(user=user, password=password, array_addresses=array_addresses, uid=uid)


class FakeContext:

    def __init__(self):
        self.code = grpc.StatusCode.OK
        self.details = ""

    def set_code(self, code):
        self.code = code

    def set_details(self, details):
        self.details = details
