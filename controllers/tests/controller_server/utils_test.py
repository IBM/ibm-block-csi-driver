import json
import unittest

from csi_general import csi_pb2
from mock import patch, Mock
from munch import Munch

import controllers.common.utils
import controllers.servers.utils as utils
from controllers.array_action.settings import (NVME_OVER_FC_CONNECTIVITY_TYPE,
                                               FC_CONNECTIVITY_TYPE,
                                               ISCSI_CONNECTIVITY_TYPE)
from controllers.common.node_info import NodeIdInfo
from controllers.common.settings import SPACE_EFFICIENCY_DEDUPLICATED_COMPRESSED, SPACE_EFFICIENCY_NONE, \
    SPACE_EFFICIENCY_DEDUPLICATED, SPACE_EFFICIENCY_THIN
from controllers.servers import settings as controller_config
from controllers.servers.csi.csi_controller_server import CSIControllerServicer
from controllers.servers.errors import ObjectIdError, ValidationException, InvalidNodeId
from controllers.tests import utils as test_utils
from controllers.tests.common.test_settings import DUMMY_POOL1, SECRET_USERNAME_VALUE, SECRET_PASSWORD_VALUE, ARRAY
from controllers.tests.controller_server.csi_controller_server_test import ProtoBufMock
from controllers.tests.utils import get_fake_secret_config


class TestUtils(unittest.TestCase):

    def setUp(self):
        self.servicer = CSIControllerServicer()
        self.controller_config = Munch({"publish_context_lun_parameter": "lun",
                                        "publish_context_connectivity_parameter": "connectivity_type",
                                        "publish_context_separator": ",",
                                        "publish_context_array_iqn": "array_iqn",
                                        "publish_context_fc_initiators": "fc_wwns"})

    def _test_validation_exception(self, util_function, function_arg, str_in_msg="", raised_error=ValidationException):
        with self.assertRaises(raised_error) as context:
            util_function(function_arg)
        if str_in_msg:
            self.assertIn(str_in_msg, str(context.exception))

    def _test_validate_node_id_validation_exception(self, node_id):
        self._test_validation_exception(utils._validate_node_id, node_id, raised_error=InvalidNodeId)

    def test_validate_node_id_success(self):
        node_id = "test-host;nqn;fc"
        utils._validate_node_id(node_id)

    def test_validate_node_id_too_long(self):
        node_id = "test-host;nqn;fc;iqn;extra"
        self._test_validate_node_id_validation_exception(node_id)

    def test_validate_node_id_too_short(self):
        node_id = "test-host"
        self._test_validate_node_id_validation_exception(node_id)

    def _test_validate_secrets_validation_exception(self, secrets):
        self._test_validation_exception(utils.validate_secrets, secrets)

    def test_validate_secrets_success(self):
        secrets = {"username": SECRET_USERNAME_VALUE, "password": SECRET_PASSWORD_VALUE, "management_address": ARRAY}
        utils.validate_secrets(secrets)

    def test_validate_secrets_with_no_secret(self):
        self._test_validate_secrets_validation_exception(None)

    def test_validate_secrets_with_no_management_address(self):
        secrets = {"username": SECRET_USERNAME_VALUE, "password": SECRET_PASSWORD_VALUE}
        self._test_validate_secrets_validation_exception(secrets)

    def test_validate_secrets_with_no_password(self):
        secrets = {"username": SECRET_USERNAME_VALUE, "management_address": ARRAY}
        self._test_validate_secrets_validation_exception(secrets)

    def test_validate_secrets_with_no_username(self):
        secrets = {"password": SECRET_PASSWORD_VALUE, "management_address": ARRAY}
        self._test_validate_secrets_validation_exception(secrets)

    def test_validate_secrets_with_empty_dict(self):
        secrets = {}
        self._test_validate_secrets_validation_exception(secrets)

    def test_validate_secrets_with_config(self):
        secrets = get_fake_secret_config()
        utils.validate_secrets(secrets)

    def test_validate_secrets_with_config_invalid_secret(self):
        secrets = get_fake_secret_config(password=None)
        self._test_validate_secrets_validation_exception(secrets)

    def test_validate_secrets_with_config_no_topologies(self):
        secrets = get_fake_secret_config(supported_topologies=None)
        self._test_validate_secrets_validation_exception(secrets)
        secrets = get_fake_secret_config(supported_topologies=[])
        self._test_validate_secrets_validation_exception(secrets)
        secrets = get_fake_secret_config(supported_topologies=[{}])
        self._test_validate_secrets_validation_exception(secrets)

    def _test_validate_secrets_with_config_valid_system_id(self, system_id):
        secrets = get_fake_secret_config(system_id=system_id)
        utils.validate_secrets(secrets)

    def test_validate_secrets_with_config_valid_system_id(self):
        self._test_validate_secrets_with_config_valid_system_id("ui_.d")
        self._test_validate_secrets_with_config_valid_system_id("a" * controller_config.SECRET_SYSTEM_ID_MAX_LENGTH)

    def _test_validate_secrets_with_config_invalid_system_id(self, system_id):
        secrets = get_fake_secret_config(system_id=system_id)
        self._test_validate_secrets_validation_exception(secrets)

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
        self.assertEqual(SECRET_USERNAME_VALUE, array_connection_info.user)
        self.assertEqual(SECRET_PASSWORD_VALUE, array_connection_info.password)
        self.assertEqual(ARRAY, array_connection_info.array_addresses[0])
        if topologies or system_id:
            self.assertIsNotNone(array_connection_info.system_id)
        else:
            self.assertIsNone(array_connection_info.system_id)

    def test_get_array_connection_info_from_secrets(self):
        secrets = get_fake_secret_config()
        self._test_get_array_connection_info_from_secrets(secrets, system_id="u1")
        secrets = {"username": SECRET_USERNAME_VALUE, "password": SECRET_PASSWORD_VALUE, "management_address": ARRAY}
        self._test_get_array_connection_info_from_secrets(secrets)
        secrets = get_fake_secret_config(supported_topologies=[{"topology.block.csi.ibm.com/test1": "zone1"}])
        self._test_get_array_connection_info_from_secrets(secrets,
                                                          topologies={"topology.block.csi.ibm.com/test1": "zone1",
                                                                      "topology.block.csi.ibm.com/test2": "dev1"})

    def _test_get_pool_from_parameters(self, parameters, expected_pool=DUMMY_POOL1, system_id=None):
        volume_parameters = utils.get_volume_parameters(parameters, system_id)
        self.assertEqual(expected_pool, volume_parameters.pool)

    def test_get_pool_from_parameters(self):
        parameters = {controller_config.PARAMETERS_POOL: DUMMY_POOL1}
        self._test_get_pool_from_parameters(parameters)
        self._test_get_pool_from_parameters(parameters, system_id="u1")
        parameters = {controller_config.PARAMETERS_BY_SYSTEM: json.dumps(
            {"u1": {controller_config.PARAMETERS_POOL: DUMMY_POOL1},
             "u2": {controller_config.PARAMETERS_POOL: "other_pool"}})}
        self._test_get_pool_from_parameters(parameters, system_id="u1")
        self._test_get_pool_from_parameters(parameters, expected_pool="other_pool", system_id="u2")
        self._test_get_pool_from_parameters(parameters, expected_pool=None)

    def _test_validate_csi_volume_capabilities_validation_exception(self, capabilities):
        self._test_validation_exception(utils.validate_csi_volume_capabilities, capabilities)

    def test_validate_file_system_volume_capabilities(self):
        access_mode = csi_pb2.VolumeCapability.AccessMode

        cap = test_utils.get_mock_volume_capability()
        utils.validate_csi_volume_capabilities([cap])

        self._test_validate_csi_volume_capabilities_validation_exception([])

        cap.mount.fs_type = "ext4dummy"
        self._test_validate_csi_volume_capabilities_validation_exception([cap])

        cap.mount.fs_type = "ext4"
        cap.access_mode.mode = access_mode.SINGLE_NODE_READER_ONLY
        self._test_validate_csi_volume_capabilities_validation_exception([cap])

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

    def _test_validate_create_volume_request_validation_exception(self, request, msg):
        self._test_validation_exception(utils.validate_create_volume_request, request, str_in_msg=msg)

    @patch('controllers.servers.utils.validate_secrets')
    @patch('controllers.servers.utils.validate_csi_volume_capabilities')
    def test_validate_create_volume_request(self, validate_capabilities, validate_secrets):
        request = Mock()
        request.name = ""
        self._test_validate_create_volume_request_validation_exception(request, "name")

        request.name = "name"

        request.capacity_range.required_bytes = -1

        self._test_validate_create_volume_request_validation_exception(request, "size")

        request.capacity_range.required_bytes = 10
        validate_capabilities.side_effect = ValidationException("msg")

        self._test_validate_create_volume_request_validation_exception(request, "msg")

        validate_capabilities.side_effect = None

        validate_secrets.side_effect = ValidationException(" other msg")

        self._test_validate_create_volume_request_validation_exception(request, "other msg")

        validate_secrets.side_effect = None

        request.parameters = {"capabilities": ""}

        self._test_validate_create_volume_request_validation_exception(request, "parameter")

        request.parameters = {}

        self._test_validate_create_volume_request_validation_exception(request, "parameter")

        request.parameters = None

        self._test_validate_create_volume_request_validation_exception(request, "parameter")

        request.parameters = {controller_config.PARAMETERS_POOL: DUMMY_POOL1,
                              controller_config.PARAMETERS_SPACE_EFFICIENCY: "thin "}
        request.volume_content_source = None

        utils.validate_create_volume_request(request)

        request.parameters = {controller_config.PARAMETERS_POOL: DUMMY_POOL1}
        utils.validate_create_volume_request(request)

        request.capacity_range.required_bytes = 0
        utils.validate_create_volume_request(request)

    @patch('controllers.servers.utils.validate_secrets', Mock())
    def test_validate_delete_snapshot_request(self):
        request = Mock()
        request.snapshot_id = ""

        self._test_validation_exception(utils.validate_delete_snapshot_request, request)

    @patch("controllers.servers.utils.get_volume_id")
    def test_get_create_volume_response(self, get_volume_id):
        new_volume = Mock()
        new_volume.name = "name"
        new_volume.array_address = ["fqdn1", "fqdn2"]

        new_volume.pool = DUMMY_POOL1
        new_volume.array_type = "a9k"
        new_volume.capacity_bytes = 10
        new_volume.source_id = None

        get_volume_id.return_value = "a9k:name"
        response = utils.generate_csi_create_volume_response(new_volume)

        self.assertEqual(10, response.volume.capacity_bytes)

        get_volume_id.side_effect = [Exception("err")]

        with self.assertRaises(Exception):
            utils.generate_csi_create_volume_response(new_volume)

    @patch("controllers.servers.utils.get_volume_id")
    def test_get_create_volume_response_with_single_ip(self, get_volume_id):
        new_volume = Mock()
        new_volume.name = "name"
        new_volume.array_address = "9.1.1.1"

        new_volume.pool = DUMMY_POOL1
        new_volume.array_type = "svc"
        new_volume.capacity_bytes = 10
        new_volume.source_id = None

        get_volume_id.return_value = "svc:name"
        response = utils.generate_csi_create_volume_response(new_volume)

        self.assertEqual(10, response.volume.capacity_bytes)

    @patch("controllers.servers.utils.get_volume_id")
    def test_get_create_volume_response_with_multiple_ip(self, get_volume_id):
        new_volume = Mock()
        new_volume.name = "name"
        new_volume.array_address = ["9.1.1.1", "9.1.1.2"]

        new_volume.pool = DUMMY_POOL1
        new_volume.array_type = "svc"
        new_volume.capacity_bytes = 10
        new_volume.source_id = None

        get_volume_id.return_value = "svc:name"
        response = utils.generate_csi_create_volume_response(new_volume)

        self.assertEqual(10, response.volume.capacity_bytes)

    def _test_validate_publish_volume_request_validation_exception(self, request, msg):
        self._test_validation_exception(utils.validate_publish_volume_request, request, str_in_msg=msg)

    @patch('controllers.servers.utils.validate_secrets')
    @patch('controllers.servers.utils.validate_csi_volume_capability')
    @patch('controllers.servers.utils._validate_node_id')
    def test_validate_publish_volume_request(self, validate_node_id, validate_capabilities, validate_secrets):
        request = Mock()
        request.readonly = True

        self._test_validate_publish_volume_request_validation_exception(request, "readonly")

        request.readonly = False
        validate_capabilities.side_effect = [ValidationException("msg1")]

        self._test_validate_publish_volume_request_validation_exception(request, "msg1")

        validate_capabilities.side_effect = None
        validate_secrets.side_effect = [ValidationException("secrets")]

        self._test_validate_publish_volume_request_validation_exception(request, "secrets")

        validate_secrets.side_effect = None
        validate_node_id.side_effect = [ValidationException("node id")]

        self._test_validate_publish_volume_request_validation_exception(request, "node id")

        validate_node_id.side_effect = None

        utils.validate_publish_volume_request(request)

    def _test_validate_unpublish_volume_request_validation_exception(self, request, str_in_msg,
                                                                     raised_error=ValidationException):
        self._test_validation_exception(utils.validate_unpublish_volume_request, request, str_in_msg=str_in_msg,
                                        raised_error=raised_error)

    @patch('controllers.servers.utils._validate_node_id')
    @patch('controllers.servers.utils.validate_secrets')
    def test_validate_unpublish_volume_request(self, validate_secrets, validate_node_id):
        request = Mock()
        request.volume_id = "somebadvolumename"

        self._test_validate_unpublish_volume_request_validation_exception(request, "volume", raised_error=ObjectIdError)

        request.volume_id = "xiv:volume"

        validate_secrets.side_effect = [ValidationException("secret")]

        self._test_validate_unpublish_volume_request_validation_exception(request, "secret")

        validate_secrets.side_effect = None
        validate_node_id.side_effect = [ValidationException("node id")]

        self._test_validate_unpublish_volume_request_validation_exception(request, "node id")

        validate_node_id.side_effect = None

        utils.validate_unpublish_volume_request(request)

    def _test_get_volume_id_info(self, object_id, system_id=None, internal_id=None):
        system_id_field = ':{}'.format(system_id) if system_id else ''
        ids_field = '{};{}'.format(internal_id, object_id) if internal_id else object_id
        volume_id = '{}{}:{}'.format('xiv', system_id_field, ids_field)
        volume_id_info = utils.get_volume_id_info(volume_id)
        self.assertEqual("xiv", volume_id_info.array_type)
        self.assertEqual(system_id, volume_id_info.system_id)
        self.assertEqual(internal_id, volume_id_info.ids.internal_id)
        self.assertEqual(object_id, volume_id_info.ids.uid)

    def test_get_volume_id_info(self):
        self._test_get_volume_id_info(object_id="volume-id")

    def test_get_volume_id_info_with_system_id(self):
        self._test_get_volume_id_info(object_id="volume-id", system_id="system_id")

    def test_get_volume_id_info_with_internal_id(self):
        self._test_get_volume_id_info(object_id="volume-id", internal_id="0")

    def test_get_volume_id_info_with_internal_id_system_id(self):
        self._test_get_volume_id_info(object_id="volume-id", system_id="system_id", internal_id="0")

    def _test_get_volume_id_info_validation_exception(self, node_id, str_in_msg, raised_error):
        self._test_validation_exception(utils.get_volume_id_info, node_id, str_in_msg=str_in_msg,
                                        raised_error=raised_error)

    def test_get_volume_id_info_too_many_semicolons_fail(self):
        self._test_get_volume_id_info_validation_exception("xiv:0;volume;id", str_in_msg="Wrong volume id format",
                                                           raised_error=ObjectIdError)

    def test_get_volume_id_info_no_id_fail(self):
        self._test_get_volume_id_info_validation_exception("badvolumeformat", str_in_msg="Wrong volume id format",
                                                           raised_error=ObjectIdError)

    def _check_node_id_parameters(self, node_id_info, nvme_nqn, fc_wwns, iscsi_iqn):
        nvme_nqn = [nvme_nqn] if nvme_nqn else []
        fc_wwns = fc_wwns.split(":") if fc_wwns else []
        iscsi_iqn = [iscsi_iqn] if iscsi_iqn else []
        self.assertEqual("host-name", node_id_info.node_name)
        self.assertEqual(nvme_nqn, node_id_info.initiators.nvme_nqns)
        self.assertEqual(list(filter(None, fc_wwns)), node_id_info.initiators.fc_wwns)
        self.assertEqual(iscsi_iqn, node_id_info.initiators.iscsi_iqns)

    def test_get_node_id_info(self):
        self._test_validation_exception(controllers.common.utils.get_node_id_info,
                                        "bad-node-format", str_in_msg="node", raised_error=ValueError)
        host_name = "host-name"
        nvme_nqn = "nqn.ibm"
        fc_wwns = "wwn1:wwn2"
        iscsi_iqn = "iqn.ibm"

        node_id_info = NodeIdInfo("{};;;{}".format(host_name, iscsi_iqn))
        self._check_node_id_parameters(node_id_info, "", "", iscsi_iqn)

        node_id_info = NodeIdInfo("{};;{};{}".format(host_name, fc_wwns, iscsi_iqn))
        self._check_node_id_parameters(node_id_info, "", fc_wwns, iscsi_iqn)

        node_id_info = NodeIdInfo("{};{};{}".format(host_name, nvme_nqn, fc_wwns))
        self._check_node_id_parameters(node_id_info, nvme_nqn, fc_wwns, "")

        node_id_info = NodeIdInfo("{};{}".format(host_name, nvme_nqn))
        self._check_node_id_parameters(node_id_info, nvme_nqn, "", "")

        node_id_info = NodeIdInfo("{};;{}".format(host_name, fc_wwns))
        self._check_node_id_parameters(node_id_info, "", fc_wwns, "")

    def test_choose_connectivity_types(self):
        nvme = NVME_OVER_FC_CONNECTIVITY_TYPE
        fc = FC_CONNECTIVITY_TYPE
        iscsi = ISCSI_CONNECTIVITY_TYPE
        expected_chosen_by_connectivities_found = {
            (nvme, fc, iscsi): nvme, (fc, iscsi): fc,
            (nvme,): nvme, (fc,): fc, (iscsi,): iscsi
        }
        for connectivities_found, expected_chosen_connectivity in expected_chosen_by_connectivities_found.items():
            actual_chosen = utils.choose_connectivity_type(list(connectivities_found))
            self.assertEqual(expected_chosen_connectivity, actual_chosen)

    def _check_publish_volume_response_parameters(self, lun, connectivity_type, array_initiators):
        with patch("controllers.common.config.config.controller", new=self.controller_config):
            publish_volume_response = utils.generate_csi_publish_volume_response(lun, connectivity_type,
                                                                                 array_initiators)
            self.assertEqual(lun, publish_volume_response.publish_context["lun"])
            self.assertEqual(connectivity_type, publish_volume_response.publish_context["connectivity_type"])
            if connectivity_type == NVME_OVER_FC_CONNECTIVITY_TYPE:
                self.assertIsNone(publish_volume_response.publish_context.get("fc_wwns"))
                self.assertIsNone(publish_volume_response.publish_context.get("array_iqn"))
            elif connectivity_type == FC_CONNECTIVITY_TYPE:
                self.assertEqual(",".join(array_initiators), publish_volume_response.publish_context["fc_wwns"])
                self.assertIsNone(publish_volume_response.publish_context.get("array_iqn"))
            elif connectivity_type == ISCSI_CONNECTIVITY_TYPE:
                self.assertEqual(publish_volume_response.publish_context["array_iqn"],
                                 ",".join(array_initiators.keys()))
                for iqn, ips in array_initiators.items():
                    self.assertEqual(publish_volume_response.publish_context[iqn], ",".join(ips))
                self.assertIsNone(publish_volume_response.publish_context.get("fc_wwns"))

    def test_generate_publish_volume_response_success(self):
        self._check_publish_volume_response_parameters("", NVME_OVER_FC_CONNECTIVITY_TYPE, [])

        self._check_publish_volume_response_parameters("1", FC_CONNECTIVITY_TYPE, ["wwn1", "wwn2"])

        self._check_publish_volume_response_parameters("0", ISCSI_CONNECTIVITY_TYPE,
                                                       {"iqn": ["1.1.1.1", "2.2.2.2"], "iqn2": ["3.3.3.3", "::1"]})

    def _test_validate_parameters_match_volume(self, volume_field, volume_value, parameter_field, parameter_value):
        volume = test_utils.get_mock_mediator_response_volume(10, "vol", "wwn2", "a9k")
        setattr(volume, volume_field, volume_value)
        if parameter_field:
            parameters = {parameter_field: parameter_value}
        else:
            parameters = {}

        utils.validate_parameters_match_volume(parameters, volume)

    def test_validate_parameters_match_volume_se_fail(self):
        with self.assertRaises(ValidationException):
            self._test_validate_parameters_match_volume(volume_field="space_efficiency_aliases",
                                                        volume_value=SPACE_EFFICIENCY_NONE,
                                                        parameter_field=controller_config.PARAMETERS_SPACE_EFFICIENCY,
                                                        parameter_value="thin")

    def test_validate_parameters_match_volume_thin_se_success(self):
        self._test_validate_parameters_match_volume(volume_field="space_efficiency_aliases",
                                                    volume_value=SPACE_EFFICIENCY_THIN,
                                                    parameter_field=controller_config.PARAMETERS_SPACE_EFFICIENCY,
                                                    parameter_value="thin")

    def test_validate_parameters_match_volume_dedup_aliases_success(self):
        self._test_validate_parameters_match_volume(volume_field="space_efficiency_aliases",
                                                    volume_value=[
                                                        SPACE_EFFICIENCY_DEDUPLICATED_COMPRESSED,
                                                        SPACE_EFFICIENCY_DEDUPLICATED],
                                                    parameter_field=controller_config.PARAMETERS_SPACE_EFFICIENCY,
                                                    parameter_value="deduplicated")

    def test_validate_parameters_match_volume_default_se_success(self):
        self._test_validate_parameters_match_volume(volume_field="space_efficiency_aliases",
                                                    volume_value=SPACE_EFFICIENCY_NONE,
                                                    parameter_field=None, parameter_value=None)

    def test_validate_parameters_match_volume_pool_fail(self):
        with self.assertRaises(ValidationException):
            self._test_validate_parameters_match_volume(volume_field="pool", volume_value="test_pool",
                                                        parameter_field=controller_config.PARAMETERS_POOL,
                                                        parameter_value="fake_pool")

    def test_validate_parameters_match_volume_pool_success(self):
        self._test_validate_parameters_match_volume(volume_field="pool", volume_value="test_pool",
                                                    parameter_field=controller_config.PARAMETERS_POOL,
                                                    parameter_value="test_pool")

    def test_validate_parameters_match_volume_prefix_fail(self):
        with self.assertRaises(ValidationException):
            self._test_validate_parameters_match_volume(volume_field="name", volume_value="vol-with-no-prefix",
                                                        parameter_field=controller_config.PARAMETERS_VOLUME_NAME_PREFIX,
                                                        parameter_value="prefix")

    def test_validate_parameters_match_volume_prefix_success(self):
        self._test_validate_parameters_match_volume(volume_field="name", volume_value="prefix_vol",
                                                    parameter_field=controller_config.PARAMETERS_VOLUME_NAME_PREFIX,
                                                    parameter_value="prefix")

    def test_is_call_home_enabled_true(self):
        self._test_is_call_home_enabled('true', True)

    def test_is_call_home_enabled_false(self):
        self._test_is_call_home_enabled('false', False)

    def _test_is_call_home_enabled(self, get_env_return_value, expected_result):
        mock_getenv = patch('{}.getenv'.format('controllers.servers.utils')).start()
        mock_getenv.return_value = get_env_return_value
        result = utils.is_call_home_enabled()
        self.assertEqual(result, expected_result)
        mock_getenv.assert_called_once_with(controller_config.ENABLE_CALL_HOME_ENV_VAR, 'true')
