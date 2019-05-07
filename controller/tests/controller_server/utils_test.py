import unittest
from mock import patch, Mock
from controller.csi_general import csi_pb2
from controller.controller_server.csi_controller_server import ControllerServicer
import controller.controller_server.utils as utils
from controller.controller_server.errors import ValidationException


class TestUtils(unittest.TestCase):

    def setUp(self):
        self.fqdn = "fqdn"
        self.servicer = ControllerServicer(self.fqdn)

    def test_validate_secrets(self):
        username = "user"
        password = "pass"
        mgmt = "mg"
        secrets = {"username": username, "password": password, "management_address": mgmt}

        utils.validate_secret(secrets)

        with self.assertRaises(ValidationException):
            utils.validate_secret(None)

        secrets = {"username": username, "password": password}
        with self.assertRaises(ValidationException):
            utils.validate_secret(secrets)

        secrets = {"username": username, "management_address": mgmt}
        with self.assertRaises(ValidationException):
            utils.validate_secret(secrets)

        secrets = {"password": password, "management_address": mgmt}
        with self.assertRaises(ValidationException):
            utils.validate_secret(secrets)

        secrets = {}
        with self.assertRaises(ValidationException):
            utils.validate_secret(secrets)

    def test_validate_volume_capabilities(self):
        caps = Mock()
        caps.mount = Mock()
        caps.mount.fs_type = "ext4"
        access_types = csi_pb2.VolumeCapability.AccessMode
        caps.access_mode.mode = access_types.SINGLE_NODE_WRITER

        utils.validate_csi_volume_capabilties([caps])

        with self.assertRaises(ValidationException):
            utils.validate_csi_volume_capabilties([])

        caps.mount.fs_type = "ext41"
        with self.assertRaises(ValidationException):
            utils.validate_csi_volume_capabilties([caps])

        caps.mount.fs_type = "ext4"
        caps.access_mode.mode = access_types.SINGLE_NODE_READER_ONLY
        with self.assertRaises(ValidationException):
            utils.validate_csi_volume_capabilties([caps])

        caps = Mock()
        caps.mount = None
        caps.access_mode.mode = access_types.SINGLE_NODE_READER_ONLY
        with self.assertRaises(ValidationException):
            utils.validate_csi_volume_capabilties([caps])

    @patch('controller.controller_server.utils.validate_secret')
    @patch('controller.controller_server.utils.validate_csi_volume_capabilties')
    def test_validate_create_volume_request(self, valiate_capabilities, validate_secret):
        request = Mock()
        request.name = ""

        with self.assertRaises(ValidationException) as ex:
            utils.validate_create_volume_request(request)
            self.assertTrue("name" in ex.message)

        request.name = "name"

        request.capacity_range.required_bytes = 0

        with self.assertRaises(ValidationException) as ex:
            utils.validate_create_volume_request(request)
            self.assertTrue("size" in ex.message)

        request.capacity_range.required_bytes = 10
        valiate_capabilities.side_effect = ValidationException("msg")

        with self.assertRaises(ValidationException) as ex:
            utils.validate_create_volume_request(request)
            self.assertTrue("msg" in ex.message)

        valiate_capabilities.side_effect = None

        validate_secret.side_effect = ValidationException(" other msg")

        with self.assertRaises(ValidationException) as ex:
            utils.validate_create_volume_request(request)
            self.assertTrue("other msg" in ex.message)

        validate_secret.side_effect = None

        request.parameters = {"capabilities": ""}

        with self.assertRaises(ValidationException) as ex:
            utils.validate_create_volume_request(request)
            self.assertTrue("parameters" in ex.message)

        request.parameters = {}

        with self.assertRaises(ValidationException) as ex:
            utils.validate_create_volume_request(request)
            self.assertTrue("parameters" in ex.message)

        request.parameters = None

        with self.assertRaises(ValidationException) as ex:
            utils.validate_create_volume_request(request)
            self.assertTrue("parameters" in ex.message)

        request.parameters = {"capacity": "pool=pool1", "capabilities": ""}

        utils.validate_create_volume_request(request)

        request.parameters = {"capacity": "pool=pool1"}
        utils.validate_create_volume_request(request)


    def test_get_create_volume_response(self):
        new_vol = Mock()
        new_vol.volume_name = "name"
        new_vol.array_name = "array"
        new_vol.pool_name = "pool"
        new_vol.array_type = "a9k"
        new_vol.capacity_bytes = 10

        res = utils.generate_csi_create_volume_response(new_vol)
        self.assertEqual(10, res.volume.capacity_bytes)

        new_vol = Mock()
        new_vol.volume_name.side_effect = [Exception("err")]

        with self.assertRaises(Exception):
            utils.generate_csi_create_volume_response(new_vol)

