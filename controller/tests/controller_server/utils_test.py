import unittest
from mock import patch, Mock
from controller.csi_general import csi_pb2
from controller.controller_server.csi_controller_server import ControllerServicer
import controller.controller_server.utils as utils


class TestUtils(unittest.TestCase):

    def setUp(self):
        self.fqdn = "fqdn"
        self.servicer = ControllerServicer(self.fqdn)

    def test_validate_secrets(self):
        username = "user"
        password = "pass"
        mgmt = "mg"
        secrets = {"username": username, "password": password, "management_address": mgmt}
        res, msg = utils.validate_secret(secrets)
        self.assertEqual(res, True)

        res, msg = utils.validate_secret(None)
        self.assertEqual(res, False)

        secrets = {"username": username, "password": password}
        res, msg = utils.validate_secret(secrets)
        self.assertEqual(res, False)

        secrets = {"username": username, "management_address": mgmt}
        res, msg = utils.validate_secret(secrets)
        self.assertEqual(res, False)

        secrets = {"password": password, "management_address": mgmt}
        res, msg = utils.validate_secret(secrets)
        self.assertEqual(res, False)

        secrets = {}
        res, msg = utils.validate_secret(secrets)
        self.assertEqual(res, False)

    def test_validate_volume_capabilities(self):
        caps = Mock()
        caps.mount = Mock()
        caps.mount.fs_type = "ext4"
        access_types = csi_pb2.VolumeCapability.AccessMode
        caps.access_mode.mode = access_types.SINGLE_NODE_WRITER

        res, msg = utils.validate_volume_capabilties([caps])
        self.assertEqual(res, True)

        res, msg = utils.validate_volume_capabilties([])
        self.assertEqual(res, False)

        caps.mount.fs_type = "ext41"
        res, msg = utils.validate_volume_capabilties([caps])
        self.assertEqual(res, False)

        caps.mount.fs_type = "ext4"
        caps.access_mode.mode = access_types.SINGLE_NODE_READER_ONLY
        res, msg = utils.validate_volume_capabilties([caps])
        self.assertEqual(res, False)

        caps = Mock()
        caps.mount = None
        caps.access_mode.mode = access_types.SINGLE_NODE_READER_ONLY
        res, msg = utils.validate_volume_capabilties([caps])
        self.assertEqual(res, False)

    @patch('controller.controller_server.utils.validate_secret')
    @patch('controller.controller_server.utils.validate_volume_capabilties')
    def test_validate_create_volume_request(self, valiate_capabilities, validate_secret):
        request = Mock()
        # request.name = "name"

        # request.parameters = {"capacity": "pool=pool1", "capabilities": ""}
        request.name = ""

        res, msg = utils.validate_create_volume_request(request)
        self.assertEqual(res, False)
        self.assertTrue("name" in msg)

        request.name = "name"

        request.capacity_range.required_bytes = -1

        res, msg = utils.validate_create_volume_request(request)
        self.assertEqual(res, False)
        self.assertTrue("size" in msg)


        request.capacity_range.required_bytes = 10
        valiate_capabilities.return_value = (False, "msg")

        res, msg = utils.validate_create_volume_request(request)
        self.assertEqual(res, False)
        self.assertTrue("msg" in msg)

        valiate_capabilities.return_value = (True, "")

        validate_secret.return_value = (False, "other msg")

        res, msg = utils.validate_create_volume_request(request)
        self.assertEqual(res, False)
        self.assertTrue("other msg" in msg)

        validate_secret.return_value = (True, "")

        # request.parameters = {"capacity": "pool={0}".format(self.pool), "capabilities": ""}
        request.parameters = {"capacity": "pool=pool1"}

        res, msg = utils.validate_create_volume_request(request)
        self.assertEqual(res, False)
        self.assertTrue("parameters" in msg)

        request.parameters = {"capabilities": ""}

        res, msg = utils.validate_create_volume_request(request)
        self.assertEqual(res, False)
        self.assertTrue("parameters" in msg)

        request.parameters = {}

        res, msg = utils.validate_create_volume_request(request)
        self.assertEqual(res, False)
        self.assertTrue("parameters" in msg)

        request.parameters = None

        res, msg = utils.validate_create_volume_request(request)
        self.assertEqual(res, False)
        self.assertTrue("parameters" in msg)

        request.parameters = {"capacity": "pool=pool1", "capabilities": ""}

        res, msg = utils.validate_create_volume_request(request)
        self.assertEqual(res, True)

        request.capacity_range.required_bytes = 0

        res, msg = utils.validate_create_volume_request(request)
        self.assertEqual(res, True)

    @patch("controller.controller_server.utils.get_vol_id")
    def test_get_create_volume_response(self, get_vol_id):
        new_vol = Mock()
        new_vol.volume_name = "name"
        new_vol.array_name = ["fqdn1", "fqdn2"]
        new_vol.pool_name = "pool"
        new_vol.storage_type = "a9k"
        new_vol.capacity_bytes = 10

        get_vol_id.return_value = "a9k:name"

        res = utils.get_create_volume_response(new_vol)
        self.assertEqual(10, res.volume.capacity_bytes)

        get_vol_id.side_effect = [Exception("err")]

        res = utils.get_create_volume_response(new_vol)
        self.assertNotEqual(10, res.volume.capacity_bytes)
