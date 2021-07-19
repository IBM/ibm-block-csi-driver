import json

import grpc
from mock import Mock

from controller.controller_server.controller_types import ArrayConnectionInfo
from controller.controller_server.test_settings import user as test_user, password as test_password, array as test_array


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


def get_fake_array_connection_info(user="user", password="pass", array_addresses=None, system_id="system_id_stub"):
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
