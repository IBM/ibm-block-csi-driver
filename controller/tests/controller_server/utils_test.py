import unittest

from mock import patch, Mock

import controller.array_action.errors as array_errors
import controller.controller_server.utils as utils
from controller.controller_server.csi_controller_server import ControllerServicer
from controller.controller_server.errors import ObjectIdError, ValidationException
from controller.controller_server.test_settings import pool, user, password, array
from controller.csi_general import csi_pb2
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
        secrets = {"config": str({"u1": {"username": user, "password": password, "management_address": array}}
                                 )}
        utils.validate_secrets(secrets)

    def test_validate_secrets_with_config_invalid_secret(self):
        secrets = {"config": str({"u1": {"username": user, "management_address": array}})}
        self._test_validate_secrets_validation_errors(secrets)

    def _test_validate_secrets_with_config_valid_uid(self, uid):
        secrets = {"config": str({uid: {"username": user, "password": password, "management_address": array}}
                                 )}
        utils.validate_secrets(secrets)

    def test_validate_secrets_with_config_valid_uid(self):
        self._test_validate_secrets_with_config_valid_uid("ui_.d")
        self._test_validate_secrets_with_config_valid_uid("a" * 90)

    def test_validate_secrets_with_config_and_topologies(self):
        secrets = {"config": str({"u1": {"username": user, "password": password, "management_address": array,
                                         "supported_topologies": [{"topology.kubernetes.io/test": "zone1",
                                                                   "topology.block.csi.ibm.com/test": "dev1"}]}}
                                 )}
        utils.validate_secrets(secrets)

    def _test_validate_secrets_with_config_invalid_parameters(self, uid="u1", topology="topology.kubernetes.io/test",
                                                              value="test"):
        secrets = {"config": str({uid: {"username": user, "password": password, "management_address": array,
                                        "supported_topologies": [{topology: value}]}})}
        self._test_validate_secrets_validation_errors(secrets)

    def test_validate_secrets_with_config_invalid_parameters(self):
        uids = ["-u1", "u:1", "u1+", "u1*", "u-1(", "u/1", "u=1", " ", "", None, "a" * 91]
        for uid in uids:
            self._test_validate_secrets_with_config_invalid_parameters(uid=uid)

        topologies = ["topology.kubernetes.io", "topology.kubernetes/test", "topology.kubernetes.io/-test"]
        for topology in topologies:
            self._test_validate_secrets_with_config_invalid_parameters(topology=topology)

        self._test_validate_secrets_with_config_invalid_parameters(value="a*")

    def _test_get_array_connection_info_from_secrets(self, secrets, topologies=None, secret_uid=None):
        response = utils.get_array_connection_info_from_secrets(
            secrets=secrets,
            topologies=topologies,
            secret_uid=secret_uid)
        secret = response
        self.assertEqual(secret.user, user)
        self.assertEqual(secret.password, password)
        self.assertEqual(secret.array_addresses[0], array)
        if topologies or secret_uid:
            self.assertIsNotNone(secret.uid)
        else:
            self.assertIsNone(secret.uid)

    def test_get_array_connection_info_from_secrets(self):
        secrets = {"config": str({"u1": {"username": user, "password": password, "management_address": array}})}
        self._test_get_array_connection_info_from_secrets(secrets, secret_uid="u1")
        secrets = {"username": user, "password": password, "management_address": array}
        self._test_get_array_connection_info_from_secrets(secrets)
        secrets = {"config": str({"u1": {"username": user, "password": password, "management_address": array,
                                         "supported_topologies": [{"topology.kubernetes.io/test": "zone1"}]}})}
        self._test_get_array_connection_info_from_secrets(secrets,
                                                          topologies={"topology.kubernetes.io/test": "zone1",
                                                                      "topology.block.csi.ibm.com/test": "dev1"})

    def _test_get_pool_from_parameters(self, parameters, expected_pool=pool, uid=None):
        volume_parameters = utils.get_volume_parameters(parameters, uid)
        self.assertEqual(volume_parameters.pool, expected_pool)

    def test_get_pool_from_parameters(self):
        parameters = {"pool": pool}
        self._test_get_pool_from_parameters(parameters)
        self._test_get_pool_from_parameters(parameters, uid="u1")
        parameters = {"by_system": str({"u1": {"pool": pool}, "u2": {"pool": "other_pool"}})}
        self._test_get_pool_from_parameters(parameters, uid="u1")
        self._test_get_pool_from_parameters(parameters, expected_pool="other_pool", uid="u2")
        self._test_get_pool_from_parameters(parameters, expected_pool=None)

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

        utils.validate_csi_volume_capabilties([caps])

    @patch('controller.controller_server.utils.validate_secrets')
    @patch('controller.controller_server.utils.validate_csi_volume_capabilties')
    def test_validate_create_volume_request(self, valiate_capabilities, validate_secrets):
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
        valiate_capabilities.side_effect = ValidationException("msg")

        with self.assertRaises(ValidationException) as ex:
            utils.validate_create_volume_request(request)
            self.assertTrue("msg" in str(ex))

        valiate_capabilities.side_effect = None

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

        request.parameters = {"pool": pool, "SpaceEfficiency": "thin "}
        request.volume_content_source = None

        utils.validate_create_volume_request(request)

        request.parameters = {"pool": pool}
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
        request.secrets = []

        with self.assertRaises(ValidationException) as ex:
            utils.validate_publish_volume_request(request)
            self.assertTrue("secrets" in str(ex))

        request.secrets = ["secret"]
        validate_secrets.side_effect = [ValidationException("msg2")]

        with self.assertRaises(ValidationException) as ex:
            utils.validate_publish_volume_request(request)
            self.assertTrue("msg2" in str(ex))

        validate_secrets.side_effect = None

        utils.validate_publish_volume_request(request)

    @patch('controller.controller_server.utils.validate_secrets')
    def test_validate_unpublish_volume_request(self, validate_secrets):
        request = Mock()
        request.volume_id = "somebadvolumename"

        with self.assertRaises(ValidationException) as ex:
            utils.validate_unpublish_volume_request(request)
            self.assertTrue("volume" in str(ex))

        request.volume_id = "xiv:volume"

        request.secrets = []
        with self.assertRaises(ValidationException) as ex:
            utils.validate_unpublish_volume_request(request)
            self.assertTrue("secret" in str(ex))

        request.secrets = ["secret"]
        validate_secrets.side_effect = [ValidationException("msg2")]
        with self.assertRaises(ValidationException) as ex:
            utils.validate_unpublish_volume_request(request)
            self.assertTrue("msg2" in str(ex))

        validate_secrets.side_effect = None

        utils.validate_unpublish_volume_request(request)

        request.volume_id = "xiv:u2:volume"

        utils.validate_unpublish_volume_request(request)

    def test_get_volume_id_info(self):
        with self.assertRaises(ObjectIdError) as ex:
            utils.get_volume_id_info("badvolumeformat")
            self.assertTrue("volume" in str(ex))

        volume_id_info = utils.get_volume_id_info("xiv:volume-id")
        self.assertEqual(volume_id_info.array_type, "xiv")
        self.assertEqual(volume_id_info.volume_id, "volume-id")

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
