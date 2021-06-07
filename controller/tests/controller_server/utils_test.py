import json
import unittest

from mock import patch, Mock

import controller.array_action.errors as array_errors
import controller.controller_server.utils as utils
from controller.array_action import config as array_config
from controller.controller_server import config as controller_config
from controller.controller_server.csi_controller_server import ControllerServicer
from controller.controller_server.errors import ObjectIdError, ValidationException
from controller.controller_server.test_settings import pool, user, password, array
from controller.csi_general import csi_pb2
from controller.tests import utils as test_utils
from controller.tests.controller_server.csi_controller_server_test import ProtoBufMock
from controller.tests.utils import get_fake_secret_config


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

    def _test_validate_secrets_validation_errors(self, secrets):
        with self.assertRaises(ValidationException):
            utils.validate_secrets(secrets)

    def test_validate_secrets_success(self):
        secrets = {"username": user, "password": password, "management_address": array}
        utils.validate_secrets(secrets)

    def test_validate_secrets_with_no_secret(self):
        self._test_validate_secrets_validation_errors(None)

    def test_validate_secrets_with_no_management_address(self):
        secrets = {"username": user, "password": password}
        self._test_validate_secrets_validation_errors(secrets)

    def test_validate_secrets_with_no_password(self):
        secrets = {"username": user, "management_address": array}
        self._test_validate_secrets_validation_errors(secrets)

    def test_validate_secrets_with_no_username(self):
        secrets = {"password": password, "management_address": array}
        self._test_validate_secrets_validation_errors(secrets)

    def test_validate_secrets_with_empty_dict(self):
        secrets = {}
        self._test_validate_secrets_validation_errors(secrets)

    def test_validate_secrets_with_config(self):
        secrets = get_fake_secret_config()
        utils.validate_secrets(secrets)

    def test_validate_secrets_with_config_invalid_secret(self):
        secrets = get_fake_secret_config(password=None)
        self._test_validate_secrets_validation_errors(secrets)

    def test_validate_secrets_with_config_no_topologies(self):
        secrets = get_fake_secret_config(supported_topologies=None)
        self._test_validate_secrets_validation_errors(secrets)
        secrets = get_fake_secret_config(supported_topologies=[])
        self._test_validate_secrets_validation_errors(secrets)
        secrets = get_fake_secret_config(supported_topologies=[{}])
        self._test_validate_secrets_validation_errors(secrets)

    def _test_validate_secrets_with_config_valid_system_id(self, system_id):
        secrets = get_fake_secret_config(system_id=system_id)
        utils.validate_secrets(secrets)

    def test_validate_secrets_with_config_valid_system_id(self):
        self._test_validate_secrets_with_config_valid_system_id("ui_.d")
        self._test_validate_secrets_with_config_valid_system_id("a" * controller_config.SECRET_SYSTEM_ID_MAX_LENGTH)

    def _test_validate_secrets_with_config_invalid_system_id(self, system_id):
        secrets = get_fake_secret_config(system_id=system_id)
        self._test_validate_secrets_validation_errors(secrets)

    def test_validate_secrets_with_config_invalid_parameters(self):
        system_ids = ["-u1", "u:1", "u1+", "u1*", "u-1(", "u/1", "u=1", " ", "",
                      "a" * (controller_config.SECRET_SYSTEM_ID_MAX_LENGTH + 1)]
        for system_id in system_ids:
            self._test_validate_secrets_with_config_invalid_system_id(system_id=system_id)

    def _test_get_array_connection_info_from_secrets(self, secrets, topologies=None, system_id=None):
        array_connection_info = utils.get_array_connection_info_from_secrets(
            secrets=secrets,
            topologies=topologies,
            system_id=system_id)
        self.assertEqual(array_connection_info.user, user)
        self.assertEqual(array_connection_info.password, password)
        self.assertEqual(array_connection_info.array_addresses[0], array)
        if topologies or system_id:
            self.assertIsNotNone(array_connection_info.system_id)
        else:
            self.assertIsNone(array_connection_info.system_id)

    def test_get_array_connection_info_from_secrets(self):
        secrets = get_fake_secret_config()
        self._test_get_array_connection_info_from_secrets(secrets, system_id="u1")
        secrets = {"username": user, "password": password, "management_address": array}
        self._test_get_array_connection_info_from_secrets(secrets)
        secrets = get_fake_secret_config(supported_topologies=[{"topology.kubernetes.io/test": "zone1"}])
        self._test_get_array_connection_info_from_secrets(secrets,
                                                          topologies={"topology.kubernetes.io/test": "zone1",
                                                                      "topology.block.csi.ibm.com/test": "dev1"})

    def _test_get_pool_from_parameters(self, parameters, expected_pool=pool, system_id=None):
        volume_parameters = utils.get_volume_parameters(parameters, system_id)
        self.assertEqual(volume_parameters.pool, expected_pool)

    def test_get_pool_from_parameters(self):
        parameters = {controller_config.PARAMETERS_POOL: pool}
        self._test_get_pool_from_parameters(parameters)
        self._test_get_pool_from_parameters(parameters, system_id="u1")
        parameters = {controller_config.PARAMETERS_BY_SYSTEM: json.dumps(
            {"u1": {controller_config.PARAMETERS_POOL: pool}, "u2": {controller_config.PARAMETERS_POOL: "other_pool"}})}
        self._test_get_pool_from_parameters(parameters, system_id="u1")
        self._test_get_pool_from_parameters(parameters, expected_pool="other_pool", system_id="u2")
        self._test_get_pool_from_parameters(parameters, expected_pool=None)

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
            self.assertTrue("name" in str(ex))

        request.name = "name"

        request.capacity_range.required_bytes = -1

        with self.assertRaises(ValidationException) as ex:
            utils.validate_create_volume_request(request)
            self.assertTrue("size" in str(ex))

        request.capacity_range.required_bytes = 10
        validate_capabilities.side_effect = ValidationException("msg")

        with self.assertRaises(ValidationException) as ex:
            utils.validate_create_volume_request(request)
            self.assertTrue("msg" in str(ex))

        validate_capabilities.side_effect = None

        validate_secrets.side_effect = ValidationException(" other msg")

        with self.assertRaises(ValidationException) as ex:
            utils.validate_create_volume_request(request)
            self.assertTrue("other msg" in str(ex))

        validate_secrets.side_effect = None

        request.parameters = {"capabilities": ""}

        with self.assertRaises(ValidationException) as ex:
            utils.validate_create_volume_request(request)
            self.assertTrue("parameters" in str(ex))

        request.parameters = {}

        with self.assertRaises(ValidationException) as ex:
            utils.validate_create_volume_request(request)
            self.assertTrue("parameters" in str(ex))

        request.parameters = None

        with self.assertRaises(ValidationException) as ex:
            utils.validate_create_volume_request(request)
            self.assertTrue("parameters" in str(ex))

        request.parameters = {controller_config.PARAMETERS_POOL: pool,
                              controller_config.PARAMETERS_SPACE_EFFICIENCY: "thin "}
        request.volume_content_source = None

        utils.validate_create_volume_request(request)

        request.parameters = {controller_config.PARAMETERS_POOL: pool}
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

        new_volume.pool = pool
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

        new_volume.pool = pool
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

        new_volume.pool = pool
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
            self.assertTrue("readonly" in str(ex))

        request.readonly = False
        validate_capabilities.side_effect = [ValidationException("msg1")]

        with self.assertRaises(ValidationException) as ex:
            utils.validate_publish_volume_request(request)
            self.assertTrue("msg1" in str(ex))

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
            self.assertTrue("volume" in str(ex))

        request.volume_id = "xiv:volume"

        validate_secrets.side_effect = [ValidationException("secret")]

        with self.assertRaises(ValidationException) as ex:
            utils.validate_unpublish_volume_request(request)
            self.assertTrue("msg2" in str(ex))

        validate_secrets.side_effect = None

        utils.validate_unpublish_volume_request(request)

    def test_get_volume_id_info(self):
        with self.assertRaises(ObjectIdError) as ex:
            utils.get_volume_id_info("badvolumeformat")
            self.assertTrue("volume" in str(ex))

        volume_id_info = utils.get_volume_id_info("xiv:volume-id")
        self.assertEqual(volume_id_info.array_type, "xiv")
        self.assertEqual(volume_id_info.object_id, "volume-id")

    def test_get_node_id_info(self):
        with self.assertRaises(array_errors.HostNotFoundError) as ex:
            utils.get_node_id_info("badnodeformat")
            self.assertTrue("node" in str(ex))

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
            self._test_validate_parameters_match_volume(volume_field="pool",
                                                        volume_value="test_pool",
                                                        parameter_field=controller_config.PARAMETERS_POOL,
                                                        parameter_value="fake_pool")

    def test_validate_parameters_match_volume_pool_success(self):
        self._test_validate_parameters_match_volume(volume_field="pool",
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
