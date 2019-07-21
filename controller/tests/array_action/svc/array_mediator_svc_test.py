import unittest
from munch import Munch
from mock import patch, Mock
from controller.array_action.array_mediator_svc import SVCArrayMediator
from controller.array_action.array_mediator_svc import \
    build_kwargs_from_capabilities
import controller.array_action.errors as array_errors
from pysvc.unified.response import CLIFailureError
from pysvc import errors as svc_errors


class TestArrayMediatorSVC(unittest.TestCase):
    @patch(
        "controller.array_action.array_mediator_svc.SVCArrayMediator._connect")
    def setUp(self, connect):
        self.endpoint = ["IP_1"]
        self.svc = SVCArrayMediator("user", "password", self.endpoint)
        self.svc.client = Mock()

    @patch(
        "controller.array_action.array_mediator_svc.SVCArrayMediator._connect")
    def test_raise_ManagementIPsNotSupportError_in_init(self, connect):
        self.endpoint = ["IP_1", "IP_2"]
        with self.assertRaises(
                array_errors.StorageManagementIPsNotSupportError):
            SVCArrayMediator("user", "password", self.endpoint)

        self.endpoint = []
        with self.assertRaises(
                array_errors.StorageManagementIPsNotSupportError):
            SVCArrayMediator("user", "password", self.endpoint)

    @patch("pysvc.unified.client.connect")
    def test_connect_errors(self, mock_connect):
        mock_connect.side_effect = [
            svc_errors.IncorrectCredentials('Failed_a')]
        with self.assertRaises(array_errors.CredentialsError):
            self.svc._connect()

    def test_close(self):
        self.svc.disconnect()
        self.svc.client.close.assert_called_with()

    @patch("controller.array_action.array_mediator_svc.is_warning_message")
    def test_get_volume_return_CLI_Failure_errors(self, mock_warning):
        mock_warning.return_value = False
        self.svc.client.svcinfo.lsvdisk.side_effect = [
            CLIFailureError('CMMVC5753E')]
        with self.assertRaises(array_errors.VolumeNotFoundError):
            self.svc.get_volume("vol_name")

    def test_get_volume_return_correct_value(self):
        vol_ret = Mock(as_single_element=Munch({'id': 'vol_id',
                                                'name': 'test_vol',
                                                'capacity': '1024',
                                                'mdisk_grp_name': 'pool_name'
                                                }))
        self.svc.client.svcinfo.lsvdisk.return_value = vol_ret
        vol = self.svc.get_volume("test_vol")
        self.assertTrue(vol.capacity_bytes == 1024)
        self.assertTrue(vol.pool_name == 'pool_name')
        self.assertTrue(vol.array_type == 'SVC')

    @patch("controller.array_action.array_mediator_svc.is_warning_message")
    def test_get_volume_returns_Exception(self, mock_warning):
        mock_warning.return_value = False
        self.svc.client.svcinfo.lsvdisk.side_effect = [Exception]
        with self.assertRaises(Exception):
            self.svc.get_volume("vol")

    def test_get_volume_returns_nothing(self):
        vol_ret = Mock(as_single_element=Munch({}))
        self.svc.client.svcinfo.lsvdisk.return_value = vol_ret
        with self.assertRaises(array_errors.VolumeNotFoundError):
            self.svc.get_volume("vol")

    @patch("controller.array_action.array_mediator_svc.is_warning_message")
    def test_create_volume_return_exception(self, mock_warning):
        mock_warning.return_value = False
        self.svc.client.svctask.mkvolume.side_effect = [Exception]
        with self.assertRaises(Exception):
            self.svc.create_volume("vol", 10, {}, "pool")
        self.svc.client.svctask.mkvolume.side_effect = [
            CLIFailureError("Failed")]
        with self.assertRaises(CLIFailureError):
            self.svc.create_volume("vol", 10, {}, "pool")

    @patch("controller.array_action.array_mediator_svc.is_warning_message")
    def test_create_volume_return_volume_exists_error(self, mock_warning):
        mock_warning.return_value = False
        self.svc.client.svctask.mkvolume.side_effect = [
            CLIFailureError("CMMVC6035E")]
        with self.assertRaises(array_errors.VolumeAlreadyExists):
            self.svc.create_volume("vol", 10, {}, "pool")

    @patch("controller.array_action.array_mediator_svc.is_warning_message")
    def test_create_volume_return_pool_not_exists_error(self, mock_warning):
        mock_warning.return_value = False
        self.svc.client.svctask.mkvolume.side_effect = [
            CLIFailureError("CMMVC5754E")]
        with self.assertRaises(array_errors.PoolDoesNotExist):
            self.svc.create_volume("vol", 10, {}, "pool")

    @patch("controller.array_action.array_mediator_svc.is_warning_message")
    def test_create_volume_return_pool_not_match_capabilities_error(
            self, mock_warning):
        mock_warning.return_value = False
        self.svc.client.svctask.mkvolume.side_effect = [
            CLIFailureError("CMMVC9292E")]
        with self.assertRaises(array_errors.PoolDoesNotMatchCapabilities):
            self.svc.create_volume("vol", 10, {}, "pool")

        self.svc.client.svctask.mkvolume.side_effect = [
            CLIFailureError("CMMVC9301E")]
        with self.assertRaises(array_errors.PoolDoesNotMatchCapabilities):
            self.svc.create_volume("vol", 10, {}, "pool")

    def test_create_volume_success(self):
        self.svc.client.svctask.mkvolume.return_value = Mock()
        vol_ret = Mock(as_single_element=Munch({'id': 'vol_id',
                                                'name': 'test_vol',
                                                'capacity': '1024',
                                                'mdisk_grp_name': 'pool_name'
                                                }))
        self.svc.client.svcinfo.lsvdisk.return_value = vol_ret
        volume = self.svc.create_volume("test_vol", 10, {}, "pool_name")
        self.assertEqual(volume.capacity_bytes, 1024)
        self.assertEqual(volume.array_type, 'SVC')
        self.assertEqual(volume.id, 'vol_id')

    @patch("controller.array_action.array_mediator_svc.is_warning_message")
    def test_delete_volume_return_volume_not_found(self, mock_warning):
        mock_warning.return_value = False
        self.svc.client.svctask.rmvolume.side_effect = [
            CLIFailureError("CMMVC5753E")]
        with self.assertRaises(array_errors.VolumeNotFoundError):
            self.svc.delete_volume("vol")

    @patch("controller.array_action.array_mediator_svc.is_warning_message")
    def test_delete_volume_return_volume_delete_errors(self, mock_warning):
        mock_warning.return_value = False
        self.svc.client.svctask.rmvolume.side_effect = [
            CLIFailureError("CMMVC5753E")]
        with self.assertRaises(array_errors.VolumeNotFoundError):
            self.svc.delete_volume("vol")
        self.svc.client.svctask.rmvolume.side_effect = [
            CLIFailureError("CMMVC8957E")]
        with self.assertRaises(array_errors.VolumeNotFoundError):
            self.svc.delete_volume("vol")
        self.svc.client.svctask.rmvolume.side_effect = [
            CLIFailureError("Failed")]
        with self.assertRaises(CLIFailureError):
            self.svc.delete_volume("vol")

    def test_delete_volume_success(self):
        self.svc.client.svctask.rmvolume = Mock()
        self.svc.delete_volume("vol")

    def test_validate_supported_capabilities_raise_error(self):
        capabilities_a = {"Space": "Test"}
        with self.assertRaises(
                array_errors.StorageClassCapabilityNotSupported):
            self.svc.validate_supported_capabilities(capabilities_a)
        capabilities_b = {"SpaceEfficiency": "Test"}
        with self.assertRaises(
                array_errors.StorageClassCapabilityNotSupported):
            self.svc.validate_supported_capabilities(capabilities_b)
        capabilities_c = {"SpaceEfficiency": ""}
        with self.assertRaises(
                array_errors.StorageClassCapabilityNotSupported):
            self.svc.validate_supported_capabilities(capabilities_c)
        capabilities_d = {}
        self.svc.validate_supported_capabilities(capabilities_d)
        capabilities_e = None
        self.svc.validate_supported_capabilities(capabilities_e)

    def test_validate_supported_capabilities_success(self):
        capabilities = {"SpaceEfficiency": "thin"}
        self.svc.validate_supported_capabilities(capabilities)
        capabilities = {"SpaceEfficiency": "thick"}
        self.svc.validate_supported_capabilities(capabilities)
        capabilities = {"SpaceEfficiency": "compressed"}
        self.svc.validate_supported_capabilities(capabilities)
        capabilities = {"SpaceEfficiency": "deduplicated"}
        self.svc.validate_supported_capabilities(capabilities)

    def test_build_kwargs_from_capabilities(self):
        size = self.svc._convert_size_bytes(1000)
        result_a = build_kwargs_from_capabilities({'SpaceEfficiency': 'thin'},
                                                  'P1', 'V1', size)
        self.assertDictEqual(result_a, {'name': 'V1', 'unit': 'b',
                                        'size': 1024, 'pool': 'P1',
                                        'thin': True})
        result_b = build_kwargs_from_capabilities({'SpaceEfficiency': 'compressed'},
                                                  'P2', 'V2', size)
        self.assertDictEqual(result_b, {'name': 'V2', 'unit': 'b',
                                        'size': 1024, 'pool': 'P2',
                                        'compressed': True})
        result_c = build_kwargs_from_capabilities({'SpaceEfficiency': 'deduplicated'},
                                                  'P3', 'V3',
                                                  self.svc._convert_size_bytes(
                                                      2048))
        self.assertDictEqual(result_c, {'name': 'V3', 'unit': 'b',
                                        'size': 2048, 'pool': 'P3',
                                        'compressed': True,
                                        'deduplicated': True})

    def test_properties(self):
        self.assertEqual(SVCArrayMediator.port, 22)
        self.assertEqual(SVCArrayMediator.minimal_volume_size_in_bytes, 512)
        self.assertEqual(SVCArrayMediator.array_type, 'SVC')
        self.assertEqual(SVCArrayMediator.max_vol_name_length, 64)
        self.assertEqual(SVCArrayMediator.max_connections, 2)
