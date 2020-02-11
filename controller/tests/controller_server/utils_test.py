import unittest
from mock import patch, Mock
from controller.csi_general import csi_pb2
from controller.controller_server.csi_controller_server import ControllerServicer
import controller.controller_server.utils as utils
from controller.controller_server.errors import ValidationException
from controller.array_action.errors import VolumeNotFoundError, HostNotFoundError


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

    def test_validate_file_system_volume_capabilities(self):
        cap = Mock()
        cap.mount = Mock()
        cap.mount.fs_type = "ext4"
        access_mode = csi_pb2.VolumeCapability.AccessMode
        cap.access_mode.mode = access_mode.SINGLE_NODE_WRITER
        cap.HasField.return_value = True

        utils.validate_csi_volume_capabilties([cap])

        with self.assertRaises(ValidationException):
            utils.validate_csi_volume_capabilties([])

        cap.mount.fs_type = "ext4dummy"
        with self.assertRaises(ValidationException):
            utils.validate_csi_volume_capabilties([cap])

        cap.mount.fs_type = "ext4"
        cap.access_mode.mode = access_mode.SINGLE_NODE_READER_ONLY
        with self.assertRaises(ValidationException):
            utils.validate_csi_volume_capabilties([cap])

    def test_validate_raw_block_volume_capabilities(self):
        caps = Mock()
        caps.block = Mock()
        access_mode = csi_pb2.VolumeCapability.AccessMode
        caps.access_mode.mode = access_mode.SINGLE_NODE_WRITER
        is_mount = False
        is_block = True
        caps.HasField.side_effect = [is_mount, is_block]

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

        request.capacity_range.required_bytes = -1

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

        request.parameters = {"pool": "pool1", "SpaceEfficiency": "thin "}

        utils.validate_create_volume_request(request)

        request.parameters = {"pool": "pool1"}
        utils.validate_create_volume_request(request)

        request.capacity_range.required_bytes = 0
        utils.validate_create_volume_request(request)

    @patch("controller.controller_server.utils.get_vol_id")
    def test_get_create_volume_response(self, get_vol_id):
        new_vol = Mock()
        new_vol.volume_name = "name"
        new_vol.array_address = ["fqdn1", "fqdn2"]

        new_vol.pool_name = "pool"
        new_vol.array_type = "a9k"
        new_vol.capacity_bytes = 10

        get_vol_id.return_value = "a9k:name"
        res = utils.generate_csi_create_volume_response(new_vol)

        self.assertEqual(10, res.volume.capacity_bytes)

        get_vol_id.side_effect = [Exception("err")]

        with self.assertRaises(Exception):
            utils.generate_csi_create_volume_response(new_vol)

    @patch("controller.controller_server.utils.get_vol_id")
    def test_get_create_volume_response_with_single_IP(self, get_vol_id):
        new_vol = Mock()
        new_vol.volume_name = "name"
        new_vol.array_address = "9.1.1.1"

        new_vol.pool_name = "pool"
        new_vol.array_type = "svc"
        new_vol.capacity_bytes = 10

        get_vol_id.return_value = "svc:name"
        res = utils.generate_csi_create_volume_response(new_vol)

        self.assertEqual(10, res.volume.capacity_bytes)
        self.assertEqual("9.1.1.1", res.volume.volume_context['array_address'])

    @patch("controller.controller_server.utils.get_vol_id")
    def test_get_create_volume_response_with_Multiple_IP(self, get_vol_id):
        new_vol = Mock()
        new_vol.volume_name = "name"
        new_vol.array_address = ["9.1.1.1", "9.1.1.2"]

        new_vol.pool_name = "pool"
        new_vol.array_type = "svc"
        new_vol.capacity_bytes = 10

        get_vol_id.return_value = "svc:name"
        res = utils.generate_csi_create_volume_response(new_vol)

        self.assertEqual(10, res.volume.capacity_bytes)
        self.assertEqual("9.1.1.1,9.1.1.2", res.volume.volume_context['array_address'])

    @patch('controller.controller_server.utils.validate_secret')
    @patch('controller.controller_server.utils.validate_csi_volume_capability')
    def test_validate_publish_volume_request(self, validate_capabilities, validate_secrets):
        request = Mock()
        request.readonly = True

        with self.assertRaises(ValidationException) as ex:
            utils.validate_publish_volume_request(request)
            self.assertTrue("readonly" in ex.message)

        request.readonly = False
        validate_capabilities.side_effect = [ValidationException("msg1")]

        with self.assertRaises(ValidationException) as ex:
            utils.validate_publish_volume_request(request)
            self.assertTrue("msg1" in ex.message)

        validate_capabilities.side_effect = None
        request.secrets = []

        with self.assertRaises(ValidationException) as ex:
            utils.validate_publish_volume_request(request)
            self.assertTrue("secrets" in ex.message)

        request.secrets = ["secret"]
        validate_secrets.side_effect = [ValidationException("msg2")]

        with self.assertRaises(ValidationException) as ex:
            utils.validate_publish_volume_request(request)
            self.assertTrue("msg2" in ex.message)

        validate_secrets.side_effect = None

        utils.validate_publish_volume_request(request)

    @patch('controller.controller_server.utils.validate_secret')
    def test_validate_unpublish_volume_request(self, validate_secret):
        request = Mock()
        request.volume_id = "somebadvolumename"

        with self.assertRaises(ValidationException) as ex:
            utils.validate_unpublish_volume_request(request)
            self.assertTrue("volume" in ex.message)

        request.volume_id = "xiv:volume"

        request.secrets = []
        with self.assertRaises(ValidationException) as ex:
            utils.validate_unpublish_volume_request(request)
            self.assertTrue("secret" in ex.message)

        request.secrets = ["secret"]
        validate_secret.side_effect = [ValidationException("msg2")]
        with self.assertRaises(ValidationException) as ex:
            utils.validate_unpublish_volume_request(request)
            self.assertTrue("msg2" in ex.message)

        validate_secret.side_effect = None

        utils.validate_unpublish_volume_request(request)

    def test_get_volume_id_info(self):
        with self.assertRaises(VolumeNotFoundError) as ex:
            utils.get_volume_id_info("badvolumeformat")
            self.assertTrue("volume" in ex.message)

        arr_type, vol = utils.get_volume_id_info("xiv:vol")
        self.assertEqual(arr_type, "xiv")
        self.assertEqual(vol, "vol")

    def test_get_node_id_info(self):
        with self.assertRaises(HostNotFoundError) as ex:
            utils.get_node_id_info("badnodeformat")
            self.assertTrue("node" in ex.message)

        hostname, iscsi_iqn, fc_wwns = utils.get_node_id_info("hostabc;iqn.ibm;")
        self.assertEqual(hostname, "hostabc")
        self.assertEqual(iscsi_iqn, "iqn.ibm")
        self.assertEqual(fc_wwns, "")

        hostname, iscsi_iqn, fc_wwns = utils.get_node_id_info("hostabc;iqn.ibm;wwn1:wwn2")
        self.assertEqual(hostname, "hostabc")
        self.assertEqual(iscsi_iqn, "iqn.ibm")
        self.assertEqual(fc_wwns, "wwn1:wwn2")

        hostname, iscsi_iqn, fc_wwns = utils.get_node_id_info("hostabc;;wwn1:wwn2")
        self.assertEqual(hostname, "hostabc")
        self.assertEqual(iscsi_iqn, "")
        self.assertEqual(fc_wwns, "wwn1:wwn2")

    def test_choose_connectivity_types(self):
        res = utils.choose_connectivity_type(["iscsi"])
        self.assertEqual(res, "iscsi")

        res = utils.choose_connectivity_type(["fc"])
        self.assertEqual(res, "fc")

        res = utils.choose_connectivity_type(["iscsi", "fc"])
        self.assertEqual(res, "fc")

    def test_generate_publish_volume_response(self):
        config_a = {"controller": {"publish_context_lun_parameter": "lun",
                                   "publish_context_connectivity_parameter":
                                       "connectivity_type",
                                   "publish_context_array_iqn": "array_iqn",
                                   "publish_context_fc_initiators": "fc_wwns"}
                    }
        res = utils.generate_csi_publish_volume_response(0, "iscsi", config_a,
                                                         ["1"])
        self.assertEqual(res.publish_context["lun"], '0')
        self.assertEqual(res.publish_context["connectivity_type"], "iscsi")
        self.assertEqual(res.publish_context["array_iqn"], '1')

        config_b = {"controller": {"publish_context_lun_parameter": "lun",
                                   "publish_context_connectivity_parameter": "connectivity_type",
                                   "publish_context_array_iqn": "array_iqn",
                                   "publish_context_fc_initiators": "fc_wwns"}
                    }
        res = utils.generate_csi_publish_volume_response(1, "fc", config_b,
                                                         ["wwn1", "wwn2"])
        self.assertEqual(res.publish_context["lun"], '1')
        self.assertEqual(res.publish_context["connectivity_type"], "fc")
        self.assertEqual(res.publish_context["fc_wwns"], "wwn1,wwn2")
