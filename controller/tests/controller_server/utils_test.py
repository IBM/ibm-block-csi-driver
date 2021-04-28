import unittest

from mock import patch, Mock

import controller.array_action.errors as array_errors
import controller.controller_server.utils as utils
from controller.array_action import config as array_config
from controller.controller_server import config as controller_config
from controller.controller_server.csi_controller_server import ControllerServicer
from controller.controller_server.errors import ObjectIdError, ValidationException
from controller.csi_general import csi_pb2
from controller.tests import utils as test_utils
from controller.tests.controller_server.csi_controller_server_test import ProtoBufMock


class TestUtils(unittest.TestCase):

    def setUp(self):
        self.fqdn = "fqdn"
        self.servicer = ControllerServicer(self.fqdn)
        self.config = {"controller": {"publish_context_lun_parameter": "lun",
                                      "publish_context_connectivity_parameter": "connectivity_type",
                                      "publish_context_separator": ",",
                                      "publish_context_array_iqn": "array_iqn",
                                      "publish_context_fc_initiators": "fc_wwns"}
                       }

    def test_validate_secrets(self):
        username = "user"
        password = "pass"
        mgmt = "mg"
        secrets = {"username": username, "password": password, "management_address": mgmt}

        utils.validate_secrets(secrets)

        with self.assertRaises(ValidationException):
            utils.validate_secrets(None)

        secrets = {"username": username, "password": password}
        with self.assertRaises(ValidationException):
            utils.validate_secrets(secrets)

        secrets = {"username": username, "management_address": mgmt}
        with self.assertRaises(ValidationException):
            utils.validate_secrets(secrets)

        secrets = {"password": password, "management_address": mgmt}
        with self.assertRaises(ValidationException):
            utils.validate_secrets(secrets)

        secrets = {}
        with self.assertRaises(ValidationException):
            utils.validate_secrets(secrets)

    def test_validate_file_system_volume_capabilities(self):
        access_mode = csi_pb2.VolumeCapability.AccessMode

        cap = test_utils.get_mock_volume_capability()
        utils.validate_csi_volume_capabilities([cap])

        with self.assertRaises(ValidationException):
            utils.validate_csi_volume_capabilities([])

        cap.mount.fs_type = "ext4dummy"
        with self.assertRaises(ValidationException):
            utils.validate_csi_volume_capabilities([cap])

        cap.mount.fs_type = "ext4"
        cap.access_mode.mode = access_mode.SINGLE_NODE_READER_ONLY
        with self.assertRaises(ValidationException):
            utils.validate_csi_volume_capabilities([cap])

    def test_validate_create_volume_source_empty(self):
        request = Mock()
        source = ProtoBufMock(spec=[])
        request.volume_content_source = source
        utils.validate_create_volume_source(request)

    def test_validate_create_volume_source_snapshot(self):
        request = Mock()
        snapshot_source = ProtoBufMock(spec=["snapshot"])
        request.volume_content_source = snapshot_source
        snapshot_source.snapshot.snapshot_id = "A9000:snap_id"
        utils.validate_create_volume_source(request)

    def test_validate_create_volume_source_volume(self):
        request = Mock()
        volume_source = ProtoBufMock(spec=["volume"])
        request.volume_content_source = volume_source
        volume_source.volume.volume_id = "A9000:vol_id"
        utils.validate_create_volume_source(request)

    def test_validate_raw_block_volume_capabilities(self):
        caps = Mock()
        caps.block = Mock()
        access_mode = csi_pb2.VolumeCapability.AccessMode
        caps.access_mode.mode = access_mode.SINGLE_NODE_WRITER
        is_mount = False
        is_block = True
        caps.HasField.side_effect = [is_mount, is_block]

        utils.validate_csi_volume_capabilities([caps])

    @patch('controller.controller_server.utils.validate_secrets')
    @patch('controller.controller_server.utils.validate_csi_volume_capabilities')
    def test_validate_create_volume_request(self, validate_capabilities, validate_secrets):
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
        validate_capabilities.side_effect = ValidationException("msg")

        with self.assertRaises(ValidationException) as ex:
            utils.validate_create_volume_request(request)
            self.assertTrue("msg" in ex.message)

        validate_capabilities.side_effect = None

        validate_secrets.side_effect = ValidationException(" other msg")

        with self.assertRaises(ValidationException) as ex:
            utils.validate_create_volume_request(request)
            self.assertTrue("other msg" in ex.message)

        validate_secrets.side_effect = None

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
        request.volume_content_source = None

        utils.validate_create_volume_request(request)

        request.parameters = {"pool": "pool1"}
        utils.validate_create_volume_request(request)

        request.capacity_range.required_bytes = 0
        utils.validate_create_volume_request(request)

    @patch('controller.controller_server.utils.validate_secrets', Mock())
    def test_validate_delete_snapshot_request(self):
        request = Mock()
        request.snapshot_id = ""

        with self.assertRaises(ValidationException):
            utils.validate_delete_snapshot_request(request)

    @patch("controller.controller_server.utils.get_volume_id")
    def test_get_create_volume_response(self, get_volume_id):
        new_volume = Mock()
        new_volume.name = "name"
        new_volume.array_address = ["fqdn1", "fqdn2"]

        new_volume.pool_name = "pool"
        new_volume.array_type = "a9k"
        new_volume.capacity_bytes = 10
        new_volume.copy_source_id = None

        get_volume_id.return_value = "a9k:name"
        res = utils.generate_csi_create_volume_response(new_volume)

        self.assertEqual(10, res.volume.capacity_bytes)

        get_volume_id.side_effect = [Exception("err")]

        with self.assertRaises(Exception):
            utils.generate_csi_create_volume_response(new_volume)

    @patch("controller.controller_server.utils.get_volume_id")
    def test_get_create_volume_response_with_single_IP(self, get_volume_id):
        new_volume = Mock()
        new_volume.name = "name"
        new_volume.array_address = "9.1.1.1"

        new_volume.pool_name = "pool"
        new_volume.array_type = "svc"
        new_volume.capacity_bytes = 10
        new_volume.copy_source_id = None

        get_volume_id.return_value = "svc:name"
        res = utils.generate_csi_create_volume_response(new_volume)

        self.assertEqual(10, res.volume.capacity_bytes)
        self.assertEqual("9.1.1.1", res.volume.volume_context['array_address'])

    @patch("controller.controller_server.utils.get_volume_id")
    def test_get_create_volume_response_with_Multiple_IP(self, get_volume_id):
        new_volume = Mock()
        new_volume.name = "name"
        new_volume.array_address = ["9.1.1.1", "9.1.1.2"]

        new_volume.pool_name = "pool"
        new_volume.array_type = "svc"
        new_volume.capacity_bytes = 10
        new_volume.copy_source_id = None

        get_volume_id.return_value = "svc:name"
        res = utils.generate_csi_create_volume_response(new_volume)

        self.assertEqual(10, res.volume.capacity_bytes)
        self.assertEqual("9.1.1.1,9.1.1.2", res.volume.volume_context['array_address'])

    @patch('controller.controller_server.utils.validate_secrets')
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
        validate_secrets.side_effect = [ValidationException("secrets")]

        with self.assertRaises(ValidationException) as ex:
            utils.validate_publish_volume_request(request)
            self.assertTrue("secrets" in ex.message)

        validate_secrets.side_effect = None

        utils.validate_publish_volume_request(request)

    @patch('controller.controller_server.utils.validate_secrets')
    def test_validate_unpublish_volume_request(self, validate_secrets):
        request = Mock()
        request.volume_id = "somebadvolumename"

        with self.assertRaises(ObjectIdError) as ex:
            utils.validate_unpublish_volume_request(request)
            self.assertTrue("volume" in ex.message)

        request.volume_id = "xiv:volume"

        validate_secrets.side_effect = [ValidationException("secret")]

        with self.assertRaises(ValidationException) as ex:
            utils.validate_unpublish_volume_request(request)
            self.assertTrue("secret" in ex.message)

        validate_secrets.side_effect = None

        utils.validate_unpublish_volume_request(request)

    def test_get_volume_id_info(self):
        with self.assertRaises(ObjectIdError) as ex:
            utils.get_volume_id_info("badvolumeformat")
            self.assertTrue("volume" in ex.message)

        arr_type, volume_id = utils.get_volume_id_info("xiv:volume-id")
        self.assertEqual(arr_type, "xiv")
        self.assertEqual(volume_id, "volume-id")

    def test_get_node_id_info(self):
        with self.assertRaises(array_errors.HostNotFoundError) as ex:
            utils.get_node_id_info("badnodeformat")
            self.assertTrue("node" in ex.message)

        hostname, fc_wwns, iscsi_iqn = utils.get_node_id_info("hostabc;;iqn.ibm")
        self.assertEqual(hostname, "hostabc")
        self.assertEqual(iscsi_iqn, "iqn.ibm")
        self.assertEqual(fc_wwns, "")

        hostname, fc_wwns, iscsi_iqn = utils.get_node_id_info("hostabc;wwn1:wwn2;iqn.ibm")
        self.assertEqual(hostname, "hostabc")
        self.assertEqual(iscsi_iqn, "iqn.ibm")
        self.assertEqual(fc_wwns, "wwn1:wwn2")

        hostname, fc_wwns, iscsi_iqn = utils.get_node_id_info("hostabc;wwn1:wwn2")
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

    def test_generate_publish_volume_response_success(self):
        res = utils.generate_csi_publish_volume_response(0, "iscsi", self.config,
                                                         {"iqn": ["1.1.1.1", "2.2.2.2"],
                                                          "iqn2": ["3.3.3.3", "::1"]})
        self.assertEqual(res.publish_context["lun"], '0')
        self.assertEqual(res.publish_context["connectivity_type"], "iscsi")
        self.assertEqual(res.publish_context["array_iqn"], "iqn,iqn2")
        self.assertEqual(res.publish_context["iqn"], "1.1.1.1,2.2.2.2")
        self.assertEqual(res.publish_context["iqn2"], "3.3.3.3,::1")

        res = utils.generate_csi_publish_volume_response(1, "fc", self.config,
                                                         ["wwn1", "wwn2"])
        self.assertEqual(res.publish_context["lun"], '1')
        self.assertEqual(res.publish_context["connectivity_type"], "fc")
        self.assertEqual(res.publish_context["fc_wwns"], "wwn1,wwn2")

    def _test_validate_parameters_match_volume(self, volume_field, volume_value, parameter_field, parameter_value,
                                               default_space_efficiency=None):
        volume = test_utils.get_mock_mediator_response_volume(10, "vol", "wwn2", "a9k")
        setattr(volume, volume_field, volume_value)
        volume.default_space_efficiency = default_space_efficiency
        if parameter_field:
            parameters = {parameter_field: parameter_value}
        else:
            parameters = {}

        utils.validate_parameters_match_volume(parameters, volume)

    def test_validate_parameters_match_volume_se_fail(self):
        with self.assertRaises(ValidationException):
            self._test_validate_parameters_match_volume(volume_field="space_efficiency",
                                                        volume_value=array_config.SPACE_EFFICIENCY_NONE,
                                                        parameter_field=controller_config.PARAMETERS_SPACE_EFFICIENCY,
                                                        parameter_value="thin")

    def test_validate_parameters_match_volume_thin_se_success(self):
        self._test_validate_parameters_match_volume(volume_field="space_efficiency",
                                                    volume_value=array_config.SPACE_EFFICIENCY_THIN,
                                                    parameter_field=controller_config.PARAMETERS_SPACE_EFFICIENCY,
                                                    parameter_value="thin")

    def test_validate_parameters_match_volume_default_se_success(self):
        self._test_validate_parameters_match_volume(volume_field="space_efficiency",
                                                    volume_value=array_config.SPACE_EFFICIENCY_NONE,
                                                    parameter_field=None,
                                                    parameter_value=None,
                                                    default_space_efficiency='none')

    def test_validate_parameters_match_volume_pool_fail(self):
        with self.assertRaises(ValidationException):
            self._test_validate_parameters_match_volume(volume_field="pool_name",
                                                        volume_value="test_pool",
                                                        parameter_field=controller_config.PARAMETERS_POOL,
                                                        parameter_value="fake_pool")

    def test_validate_parameters_match_volume_pool_success(self):
        self._test_validate_parameters_match_volume(volume_field="pool_name",
                                                    volume_value="test_pool",
                                                    parameter_field=controller_config.PARAMETERS_POOL,
                                                    parameter_value="test_pool")

    def test_validate_parameters_match_volume_prefix_fail(self):
        with self.assertRaises(ValidationException):
            self._test_validate_parameters_match_volume(volume_field="name",
                                                        volume_value="vol-with-no-prefix",
                                                        parameter_field=controller_config.PARAMETERS_VOLUME_NAME_PREFIX,
                                                        parameter_value="prefix")

    def test_validate_parameters_match_volume_prefix_success(self):
        self._test_validate_parameters_match_volume(volume_field="name",
                                                    volume_value="prefix_vol",
                                                    parameter_field=controller_config.PARAMETERS_VOLUME_NAME_PREFIX,
                                                    parameter_value="prefix")
