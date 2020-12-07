import unittest

from mock import patch, Mock
from munch import Munch
from pysvc import errors as svc_errors
from pysvc.unified.response import CLIFailureError

import controller.array_action.config as config
import controller.array_action.errors as array_errors
from controller.array_action.array_mediator_svc import SVCArrayMediator, build_kwargs_from_capabilities, \
    HOST_ID_PARAM, HOST_NAME_PARAM, HOST_ISCSI_NAMES_PARAM, HOST_WWPNS_PARAM, FCMAP_STATUS_DONE
from controller.array_action.svc_cli_result_reader import SVCListResultsElement
from controller.common.node_info import Initiators

EMPTY_BYTES = b''


class TestArrayMediatorSVC(unittest.TestCase):

    def setUp(self):
        self.endpoint = ["IP_1"]
        with patch("controller.array_action.array_mediator_svc.SVCArrayMediator._connect"):
            self.svc = SVCArrayMediator("user", "password", self.endpoint)
        self.svc.client = Mock()
        node = Munch({'id': '1', 'name': 'node1', 'iscsi_name': 'iqn.1986-03.com.ibm:2145.v7k1.node1',
                      'status': 'online'})
        self.svc.client.svcinfo.lsnode.return_value = [node]
        port = Munch({'node_id': '1', 'IP_address': '1.1.1.1', 'IP_address_6': None})
        self.svc.client.svcinfo.lsportip.return_value = [port]
        self.fcmaps = [Munch(
            {'source_vdisk_name': 'source_name',
             'target_vdisk_name': 'target_name',
             'id': 'test_fc_id',
             'status': FCMAP_STATUS_DONE,
             'copy_rate': "non_zero_value"})]
        self.fcmaps_as_source = [Munch(
            {'source_vdisk_name': 'test_snap',
             'target_vdisk_name': 'target_name',
             'id': 'test_fc_id',
             'status': FCMAP_STATUS_DONE,
             'copy_rate': "non_zero_value"})]
        self.svc.client.svcinfo.lsfcmap.return_value = Mock(as_list=self.fcmaps)

    def test_raise_ManagementIPsNotSupportError_in_init(self):
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
        with self.assertRaises(array_errors.ObjectNotFoundError):
            self.svc.get_volume("volume_name")

    def test_get_volume_return_correct_value(self):
        vol_ret = Mock(as_single_element=Munch({'vdisk_UID': 'vol_id',
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
        with self.assertRaises(array_errors.ObjectNotFoundError):
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
        self.svc.client.svctask.mkvolume.side_effect = [
            CLIFailureError("CMMVC8710E")]
        with self.assertRaises(array_errors.NotEnoughSpaceInPool):
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
        vol_ret = Mock(as_single_element=Munch({'vdisk_UID': 'vol_id',
                                                'name': 'test_vol',
                                                'capacity': '1024',
                                                'mdisk_grp_name': 'pool_name'
                                                }))
        self.svc.client.svcinfo.lsvdisk.return_value = vol_ret
        volume = self.svc.create_volume("test_vol", 10, {}, "pool_name")
        self.assertEqual(volume.capacity_bytes, 1024)
        self.assertEqual(volume.array_type, 'SVC')
        self.assertEqual(volume.id, 'vol_id')

    def test_get_volume_name_by_wwn_return_error(self):
        vol_ret = Mock(as_single_element=Munch({}))
        self.svc.client.svcinfo.lsvdisk.return_value = vol_ret
        with self.assertRaises(array_errors.ObjectNotFoundError):
            self.svc._get_volume_name_by_wwn("vol")

    def test_get_volume_name_by_wwn_return_success(self):
        vol_ret = Mock(as_single_element=Munch({'vdisk_UID': 'vol_id',
                                                'name': 'test_vol',
                                                'capacity': '1024',
                                                'mdisk_grp_name': 'pool_name'
                                                }))
        self.svc.client.svcinfo.lsvdisk.return_value = vol_ret
        ret = self.svc._get_volume_name_by_wwn("vol_id")
        self.assertEqual(ret, 'test_vol')

    @patch("controller.array_action.array_mediator_svc.is_warning_message")
    def test_delete_volume_return_volume_not_found(self, mock_warning):
        mock_warning.return_value = False
        self.svc.client.svctask.rmvolume.side_effect = [
            CLIFailureError("CMMVC5753E")]
        with self.assertRaises(array_errors.ObjectNotFoundError):
            self.svc.delete_volume("vol")

    @patch("controller.array_action.array_mediator_svc.is_warning_message")
    def test_delete_volume_return_volume_delete_errors(self, mock_warning):
        mock_warning.return_value = False
        self.svc.client.svctask.rmvolume.side_effect = [
            CLIFailureError("CMMVC5753E")]
        with self.assertRaises(array_errors.ObjectNotFoundError):
            self.svc.delete_volume("vol")
        self.svc.client.svctask.rmvolume.side_effect = [
            CLIFailureError("CMMVC8957E")]
        with self.assertRaises(array_errors.ObjectNotFoundError):
            self.svc.delete_volume("vol")
        self.svc.client.svctask.rmvolume.side_effect = [
            CLIFailureError("Failed")]
        with self.assertRaises(CLIFailureError):
            self.svc.delete_volume("vol")

    def _prepare_mocks_for_object_still_in_use(self):
        cli_volume = self._get_source_cli_vol()
        cli_volume.FC_id = 'many'
        self.svc.client.svcinfo.lsvdisk.return_value = Mock(as_single_element=cli_volume)

    def test_delete_volume_has_snapshot_fcmaps_not_removed(self):
        self._prepare_mocks_for_object_still_in_use()
        fcmaps_as_target = Mock(as_list=[])
        fcmaps = self.fcmaps
        fcmaps[0].copy_rate = "0"
        fcmaps_as_source = Mock(as_list=fcmaps)
        self.svc.client.svcinfo.lsfcmap.side_effect = [fcmaps_as_target, fcmaps_as_source]
        with self.assertRaises(array_errors.ObjectIsStillInUseError):
            self.svc.delete_volume("vol")

    def test_delete_volume_still_copy_fcmaps_not_removed(self):
        self._prepare_mocks_for_object_still_in_use()
        fcmaps_as_target = Mock(as_list=[])
        fcmaps = self.fcmaps
        fcmaps[0].status = "not good"
        fcmaps_as_source = Mock(as_list=fcmaps)
        self.svc.client.svcinfo.lsfcmap.side_effect = [fcmaps_as_target, fcmaps_as_source]
        with self.assertRaises(array_errors.ObjectIsStillInUseError):
            self.svc.delete_volume("vol")

    def test_delete_volume_has_clone_fcmaps_removed(self):
        fcmaps_as_target = Mock(as_list=[])
        fcmaps_as_source = Mock(as_list=self.fcmaps_as_source)
        self.svc.client.svcinfo.lsfcmap.side_effect = [fcmaps_as_target, fcmaps_as_source]
        self.svc.delete_volume("vol")
        self.svc.client.svctask.rmfcmap.assert_called_once()

    def test_delete_volume_success(self):
        self.svc.client.svctask.rmvolume = Mock()
        self.svc.delete_volume("vol")

    def test_copy_to_existing_volume_from_source_success(self):
        self.svc.copy_to_existing_volume_from_source("a", "b", 1, 1)
        self.svc.client.svctask.mkfcmap.assert_called_once()
        self.svc.client.svctask.startfcmap.assert_called_once()

    @staticmethod
    def _mock_cli_object(cli_object):
        return Mock(as_single_element=cli_object)

    @staticmethod
    def _get_source_cli_vol():
        return Munch({'vdisk_UID': 'vol_id',
                      'name': 'source_vol',
                      'capacity': '1024',
                      'mdisk_grp_name': 'pool_name',
                      'FC_id': '',
                      'se_copy': 'yes',
                      'deduplicated_copy': 'no',
                      'compressed_copy': 'no'
                      })

    @classmethod
    def _get_mapless_target_cli_vol(cls):
        target_cli_vol = cls._get_source_cli_vol()
        target_cli_vol.vdisk_UID = 'snap_id'
        target_cli_vol.name = 'test_snap'
        return target_cli_vol

    @classmethod
    def _get_mapped_target_cli_vol(cls):
        target_cli_vol = cls._get_mapless_target_cli_vol()
        target_cli_vol.FC_id = 'test_fc_id'
        return target_cli_vol

    def _prepare_lsvdisk_to_raise_not_found_error(self, mock_warning):
        mock_warning.return_value = False
        self.svc.client.svcinfo.lsvdisk.side_effect = [
            CLIFailureError("CMMVC5753E")]

    def _prepare_lsvdisk_to_return_mapless_target_volume(self):
        mapless_target_cli_vol = self._get_mapless_target_cli_vol()
        mapless_target_cli_vol_mock = self._mock_cli_object(mapless_target_cli_vol)
        self.svc.client.svcinfo.lsvdisk.return_value = mapless_target_cli_vol_mock

    def _prepare_lsvdisk_to_return_none(self):
        self.svc.client.svcinfo.lsvdisk.return_value = self._mock_cli_object(None)

    def _prepare_mocks_for_delete_snapshot(self):
        target_cli_vol = self._get_mapped_target_cli_vol()
        target_cli_vol.FC_id = 'many'
        self.svc.client.svcinfo.lsvdisk.return_value = self._mock_cli_object(target_cli_vol)

    def _prepare_mocks_for_get_snapshot(self):
        self._prepare_mocks_for_delete_snapshot()
        self.fcmaps[0].copy_rate = "0"

    @patch("controller.array_action.array_mediator_svc.is_warning_message")
    def test_get_snapshot_not_exist_return_none(self, mock_warning):
        self._prepare_lsvdisk_to_raise_not_found_error(mock_warning)

        snapshot = self.svc.get_snapshot("test_snap")

        self.assertIsNone(snapshot)

    def test_get_snapshot_has_no_fc_id_raise_error(self):
        self._prepare_lsvdisk_to_return_mapless_target_volume()

        with self.assertRaises(array_errors.ExpectedSnapshotButFoundVolumeError):
            self.svc.get_snapshot("test_snap")

    @patch("controller.array_action.array_mediator_svc.is_warning_message")
    def test_get_snapshot_get_fcmap_not_exist_raise_error(self, mock_warning):
        target_cli_vol = self._get_mapped_target_cli_vol()
        self.svc.client.svcinfo.lsvdisk.return_value = self._mock_cli_object(target_cli_vol)
        mock_warning.return_value = False
        self.svc.client.svcinfo.lsfcmap.side_effect = [
            CLIFailureError("CMMVC5753E")]

        with self.assertRaises(CLIFailureError):
            self.svc.get_snapshot("test_snap")

    def test_get_snapshot_non_zero_copy_rate(self):
        self._prepare_mocks_for_get_snapshot()
        self.fcmaps[0].copy_rate = "non_zero_value"
        with self.assertRaises(array_errors.ExpectedSnapshotButFoundVolumeError):
            self.svc.get_snapshot("test_snap")

    def test_get_snapshot_no_fcmap_as_target(self):
        self._prepare_mocks_for_get_snapshot()
        self.svc.client.svcinfo.lsfcmap.return_value = Mock(as_list=[])
        with self.assertRaises(array_errors.ExpectedSnapshotButFoundVolumeError):
            self.svc.get_snapshot("test_snap")

    def test_get_snapshot_success(self):
        self._prepare_mocks_for_get_snapshot()
        snapshot = self.svc.get_snapshot("test_snap")
        self.assertEqual(snapshot.name, "test_snap")

    def test_get_object_by_id_snapshot_has_no_fcmap_id_raise_error(self):
        self._prepare_lsvdisk_to_return_mapless_target_volume()
        with self.assertRaises(array_errors.ExpectedSnapshotButFoundVolumeError):
            self.svc.get_object_by_id("snap_id", "snapshot")

    def test_get_object_by_id_return_none(self):
        self.svc.client.svcinfo.lsvdisk.return_value = Mock(as_single_element=None)
        returned_value = self.svc.get_object_by_id("snap_id", "snapshot")
        self.assertEqual(returned_value, None)

    def test_get_object_by_id_snapshot_success(self):
        self._prepare_mocks_for_get_snapshot()

        snapshot = self.svc.get_object_by_id("test_snap", "snapshot")
        self.assertEqual(snapshot.name, "test_snap")
        self.svc.client.svcinfo.lsvdisk.assert_called_once_with(bytes=True, filtervalue="vdisk_UID=test_snap")

    def test_get_object_by_id_volume_success(self):
        target_cli_vol = self._get_mapped_target_cli_vol()
        target_cli_vol.name = "volume_id"
        self.svc.client.svcinfo.lsvdisk.return_value = self._mock_cli_object(target_cli_vol)
        volume = self.svc.get_object_by_id("volume_id", "volume")
        self.assertEqual(volume.name, "volume_id")

    def _prepare_mocks_for_create_snapshot(self):
        self.svc.client.svctask.mkvolume.return_value = Mock()
        self.svc.client.svctask.mkfcmap.return_value = Mock()

        source_vol_to_copy_from = self._get_source_cli_vol()
        target_vol_after_creation = self._get_mapless_target_cli_vol()
        target_vol_after_mapping = self._get_mapped_target_cli_vol()
        target_vol_for_rollback = self._get_mapped_target_cli_vol()
        vols_to_return = [source_vol_to_copy_from, target_vol_after_creation,
                          target_vol_after_mapping, target_vol_for_rollback]
        return_values = map(self._mock_cli_object, vols_to_return)
        self.svc.client.svcinfo.lsvdisk.side_effect = return_values

        self.svc.client.svctask.startfcmap.return_value = Mock()

    @patch("controller.array_action.array_mediator_svc.is_warning_message")
    def test_create_snapshot_create_volume_error(self, mock_warning):
        source_cli_vol = self._get_source_cli_vol()
        self.svc.client.svcinfo.lsvdisk.return_value = self._mock_cli_object(source_cli_vol)
        mock_warning.return_value = False
        self.svc.client.svctask.mkvolume.side_effect = [
            CLIFailureError("Failed")]

        with self.assertRaises(CLIFailureError):
            self.svc.create_snapshot("test_snap", "source_vol")

    @patch("controller.array_action.array_mediator_svc.is_warning_message")
    def test_create_snapshot_create_fcmap_error(self, mock_warning):
        self._prepare_mocks_for_create_snapshot()
        mock_warning.return_value = False
        self.svc.client.svctask.mkfcmap.side_effect = [
            CLIFailureError("Failed")]

        with self.assertRaises(CLIFailureError):
            self.svc.create_snapshot("test_snap", "source_vol")

    @patch("controller.array_action.array_mediator_svc.is_warning_message")
    def test_create_snapshot_start_fcmap_error(self, mock_warning):
        self._prepare_mocks_for_create_snapshot()
        mock_warning.return_value = False
        self.svc.client.svctask.startfcmap.side_effect = [
            CLIFailureError("Failed")]

        with self.assertRaises(CLIFailureError):
            self.svc.create_snapshot("test_snap", "source_vol")

    def test_create_snapshot_success(self):
        self._prepare_mocks_for_create_snapshot()

        snapshot = self.svc.create_snapshot("test_snap", "source_vol")

        self.assertEqual(snapshot.capacity_bytes, 1024)
        self.assertEqual(snapshot.array_type, 'SVC')
        self.assertEqual(snapshot.id, 'snap_id')

    def test_delete_snapshot_no_volume_raise_snapshot_not_found(self):
        self._prepare_lsvdisk_to_return_none()

        with self.assertRaises(array_errors.ObjectNotFoundError):
            self.svc.delete_snapshot("test_snap")

    def test_delete_snapshot_no_fcmap_id_raise_snapshot_not_found(self):
        self._prepare_lsvdisk_to_return_mapless_target_volume()

        with self.assertRaises(array_errors.ObjectNotFoundError):
            self.svc.delete_snapshot("test_snap")

    def test_delete_snapshot_call_rmfcmap(self):
        self._prepare_mocks_for_delete_snapshot()
        fcmaps_as_target = self.fcmaps
        self.svc.client.svcinfo.lsfcmap.side_effect = [Mock(as_list=fcmaps_as_target), Mock(as_list=[])]
        self.svc.delete_snapshot("test_snap")

        self.svc.client.svctask.rmfcmap.assert_called_once_with(object_id="test_fc_id", force=True)

    @patch("controller.array_action.array_mediator_svc.is_warning_message")
    def test_delete_snapshot_rmvolume_error(self, mock_warning):
        self._prepare_mocks_for_delete_snapshot()
        mock_warning.return_value = False
        self.svc.client.svctask.rmvolume.side_effect = [
            CLIFailureError("Failed")]

        with self.assertRaises(CLIFailureError):
            self.svc.delete_snapshot("test_snap")

    def test_delete_snapshot_still_copy_fcmaps_not_removed(self):
        self._prepare_mocks_for_object_still_in_use()
        fcmaps_as_target = self.fcmaps
        fcmaps_as_source = self.fcmaps_as_source
        fcmaps_as_source[0].status = "not good"
        self.svc.client.svcinfo.lsfcmap.side_effect = [Mock(as_list=fcmaps_as_target), Mock(as_list=fcmaps_as_source)]
        with self.assertRaises(array_errors.ObjectIsStillInUseError):
            self.svc.delete_snapshot("test_snap")

    def test_delete_snapshot_success(self):
        self._prepare_mocks_for_delete_snapshot()
        self.svc.delete_snapshot("test_snap")
        self.assertEqual(self.svc.client.svctask.rmfcmap.call_count, 2)
        self.svc.client.svctask.rmvolume.assert_called_once_with(vdisk_id="test_snap")

    def test_validate_supported_capabilities_raise_error(self):
        capabilities_a = {"SpaceEfficiency": "Test"}
        with self.assertRaises(
                array_errors.StorageClassCapabilityNotSupported):
            self.svc.validate_supported_capabilities(capabilities_a)
        capabilities_b = {"SpaceEfficiency": ""}
        with self.assertRaises(
                array_errors.StorageClassCapabilityNotSupported):
            self.svc.validate_supported_capabilities(capabilities_b)
        capabilities_c = {}
        self.svc.validate_supported_capabilities(capabilities_c)

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
        result_a = build_kwargs_from_capabilities({'SpaceEfficiency': 'Thin'},
                                                  'P1', 'V1', size)
        self.assertDictEqual(result_a, {'name': 'V1', 'unit': 'b',
                                        'size': 1024, 'pool': 'P1',
                                        'thin': True})
        result_b = build_kwargs_from_capabilities(
            {'SpaceEfficiency': 'compressed'}, 'P2', 'V2', size)
        self.assertDictEqual(result_b, {'name': 'V2', 'unit': 'b',
                                        'size': 1024, 'pool': 'P2',
                                        'compressed': True})
        result_c = build_kwargs_from_capabilities({'SpaceEfficiency': 'Deduplicated'},
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
        self.assertEqual(SVCArrayMediator.max_volume_name_length, 63)
        self.assertEqual(SVCArrayMediator.max_connections, 2)
        self.assertEqual(SVCArrayMediator.max_lun_retries, 10)

    @patch("controller.array_action.svc_cli_result_reader.SVCListResultsReader.__iter__")
    def test_get_host_by_identifiers_returns_host_not_found(self, result_reader_iter):
        host_1 = self._get_host_as_dictionary('host_id_1', 'test_host_1', ['iqn.test.1'], [])
        host_2 = self._get_host_as_dictionary('host_id_2', 'test_host_2', ['iqn.test.2'], [])
        host_3 = self._get_host_as_dictionary('host_id_3', 'test_host_3', ['iqn.test.3'], [])
        hosts = [host_1, host_2, host_3]
        self.svc.client.svcinfo.lshost = Mock()
        self.svc.client.svcinfo.lshost.return_value = self._get_hosts_list_result(hosts)
        self.svc.client.send_raw_command = Mock()
        self.svc.client.send_raw_command.return_value = EMPTY_BYTES, EMPTY_BYTES
        result_reader_iter.return_value = self._get_detailed_hosts_list_result(hosts)
        with self.assertRaises(array_errors.HostNotFoundError):
            self.svc.get_host_by_host_identifiers(Initiators('Test_iqn', ['Test_wwn']))

    def test_get_host_by_identifier_return_host_not_found_when_no_hosts_exist(self):
        hosts = []
        self.svc.client.svcinfo.lshost = Mock()
        self.svc.client.svcinfo.lshost.return_value = self._get_hosts_list_result(hosts)
        with self.assertRaises(array_errors.HostNotFoundError):
            self.svc.get_host_by_host_identifiers(Initiators('Test_iqn', ['Test_wwn']))

    @patch("controller.array_action.svc_cli_result_reader.SVCListResultsReader.__iter__")
    def test_get_host_by_identifiers_raise_multiplehostsfounderror(self, result_reader_iter):
        host_1 = self._get_host_as_dictionary('host_id_1', 'test_host_1', ['iqn.test.1'], [])
        host_2 = self._get_host_as_dictionary('host_id_2', 'test_host_2', ['iqn.test.3'], [])
        host_3 = self._get_host_as_dictionary('host_id_3', 'test_host_3', [], ['Test_wwn'])
        hosts = [host_1, host_2, host_3]
        self.svc.client.svcinfo.lshost = Mock()
        self.svc.client.svcinfo.lshost.return_value = self._get_hosts_list_result(hosts)
        self.svc.client.send_raw_command = Mock()
        self.svc.client.send_raw_command.return_value = EMPTY_BYTES, EMPTY_BYTES
        result_reader_iter.return_value = self._get_detailed_hosts_list_result(hosts)
        with self.assertRaises(array_errors.MultipleHostsFoundError):
            self.svc.get_host_by_host_identifiers(Initiators('iqn.test.3', ['Test_wwn']))

    @patch("controller.array_action.svc_cli_result_reader.SVCListResultsReader.__iter__")
    def test_get_host_by_identifiers_return_iscsi_host(self, result_reader_iter):
        host_1 = self._get_host_as_dictionary('host_id_1', 'test_host_1', [], ['abc1'])
        host_2 = self._get_host_as_dictionary('host_id_2', 'test_host_2', ['iqn.test.2'], ['abc3'])
        host_3 = self._get_host_as_dictionary('host_id_3', 'test_host_3', ['iqn.test.3'], ['abc3'])
        hosts = [host_1, host_2, host_3]
        self.svc.client.svcinfo.lshost = Mock()
        self.svc.client.svcinfo.lshost.return_value = self._get_hosts_list_result(hosts)
        self.svc.client.send_raw_command = Mock()
        self.svc.client.send_raw_command.return_value = EMPTY_BYTES, EMPTY_BYTES
        result_reader_iter.return_value = self._get_detailed_hosts_list_result(hosts)
        host, connectivity_type = self.svc.get_host_by_host_identifiers(Initiators(
            'iqn.test.2', ['abcd3']))
        self.assertEqual('test_host_2', host)
        self.assertEqual([config.ISCSI_CONNECTIVITY_TYPE], connectivity_type)

    @patch("controller.array_action.svc_cli_result_reader.SVCListResultsReader.__iter__")
    def test_get_host_by_identifiers_return_iscsi_host_with_list_iqn(self, result_reader_iter):
        host_1 = self._get_host_as_dictionary('host_id_1', 'test_host_1', [], ['abc1'])
        host_2 = self._get_host_as_dictionary('host_id_2', 'test_host_2', ['iqn.test.2', 'iqn.test.22'], ['abc3'])
        host_3 = self._get_host_as_dictionary('host_id_3', 'test_host_3', ['iqn.test.3'], ['abc3'])
        hosts = [host_1, host_2, host_3]
        self.svc.client.svcinfo.lshost = Mock()
        self.svc.client.svcinfo.lshost.return_value = self._get_hosts_list_result(hosts)
        self.svc.client.send_raw_command = Mock()
        self.svc.client.send_raw_command.return_value = EMPTY_BYTES, EMPTY_BYTES
        result_reader_iter.return_value = self._get_detailed_hosts_list_result(hosts)
        host, connectivity_type = self.svc.get_host_by_host_identifiers(Initiators(
            'iqn.test.2', ['abcd3']))
        self.assertEqual('test_host_2', host)
        self.assertEqual([config.ISCSI_CONNECTIVITY_TYPE], connectivity_type)

    @patch("controller.array_action.svc_cli_result_reader.SVCListResultsReader.__iter__")
    def test_get_host_by_identifiers_return_fc_host(self, result_reader_iter):
        host_1 = self._get_host_as_dictionary('host_id_1', 'test_host_1', [], ['abc1'])
        host_2 = self._get_host_as_dictionary('host_id_2', 'test_host_2', [''], ['abc2'])
        host_3 = self._get_host_as_dictionary('host_id_3', 'test_host_3', ['iqn.test.3'], ['abc1', 'abc3'])
        hosts = [host_1, host_2, host_3]
        self.svc.client.svcinfo.lshost = Mock()
        self.svc.client.svcinfo.lshost.return_value = self._get_hosts_list_result(hosts)
        self.svc.client.send_raw_command = Mock()
        self.svc.client.send_raw_command.return_value = EMPTY_BYTES, EMPTY_BYTES
        result_reader_iter.return_value = self._get_detailed_hosts_list_result(hosts)
        host, connectivity_type = self.svc.get_host_by_host_identifiers(Initiators(
            'iqn.test.6', ['abc3', 'ABC1']))
        self.assertEqual('test_host_3', host)
        self.assertEqual([config.FC_CONNECTIVITY_TYPE], connectivity_type)

        result_reader_iter.return_value = self._get_detailed_hosts_list_result(hosts)
        host, connectivity_type = self.svc.get_host_by_host_identifiers(Initiators(
            'iqn.test.6', ['abc3']))
        self.assertEqual('test_host_3', host)
        self.assertEqual([config.FC_CONNECTIVITY_TYPE], connectivity_type)

    @patch("controller.array_action.svc_cli_result_reader.SVCListResultsReader.__iter__")
    def test_get_host_by_identifiers_with_wrong_fc_iscsi_raise_not_found(self, result_reader_iter):
        host_1 = self._get_host_as_dictionary('host_id_1', 'test_host_1', [], ['abc1'])
        host_2 = self._get_host_as_dictionary('host_id_2', 'test_host_2', ['iqn.test.2'], ['abc3'])
        host_3 = self._get_host_as_dictionary('host_id_3', 'test_host_3', ['iqn.test.3'], ['abc3'])
        hosts = [host_1, host_2, host_3]
        self.svc.client.svcinfo.lshost = Mock()
        self.svc.client.svcinfo.lshost.return_value = self._get_hosts_list_result(hosts)
        self.svc.client.send_raw_command = Mock()
        self.svc.client.send_raw_command.return_value = EMPTY_BYTES, EMPTY_BYTES
        result_reader_iter.return_value = self._get_detailed_hosts_list_result(hosts)
        with self.assertRaises(array_errors.HostNotFoundError):
            self.svc.get_host_by_host_identifiers(Initiators('', []))
        result_reader_iter.return_value = self._get_detailed_hosts_list_result(hosts)
        with self.assertRaises(array_errors.HostNotFoundError):
            self.svc.get_host_by_host_identifiers(Initiators('123', ['a', 'b']))

    @patch("controller.array_action.svc_cli_result_reader.SVCListResultsReader.__iter__")
    def test_get_host_by_identifiers_return_iscsi_and_fc_all_support(self, result_reader_iter):
        host_1 = self._get_host_as_dictionary('host_id_1', 'test_host_1', [], ['abc1'])
        host_2 = self._get_host_as_dictionary('host_id_2', 'test_host_2', ['iqn.test.6'], ['abcd3'])
        host_3 = self._get_host_as_dictionary('host_id_3', 'test_host_3', ['iqn.test.2'], ['abc3'])
        hosts = [host_1, host_2, host_3]
        self.svc.client.svcinfo.lshost = Mock()
        self.svc.client.svcinfo.lshost.return_value = self._get_hosts_list_result(hosts)
        self.svc.client.send_raw_command = Mock()
        self.svc.client.send_raw_command.return_value = EMPTY_BYTES, EMPTY_BYTES
        result_reader_iter.return_value = self._get_detailed_hosts_list_result(hosts)
        host, connectivity_type = self.svc.get_host_by_host_identifiers(Initiators('iqn.test.2', ['ABC3']))
        self.assertEqual('test_host_3', host)
        self.assertEqual([config.ISCSI_CONNECTIVITY_TYPE,
                          config.FC_CONNECTIVITY_TYPE], connectivity_type)

    def _get_host_as_dictionary(self, id, name, iscsi_names_list, wwpns_list):
        res = {HOST_ID_PARAM: id, HOST_NAME_PARAM: name}
        if iscsi_names_list:
            res[HOST_ISCSI_NAMES_PARAM] = iscsi_names_list
        if wwpns_list:
            res[HOST_WWPNS_PARAM] = wwpns_list
        return res

    def _get_hosts_list_result(self, hosts_dict):
        return [Munch(host_dict) for host_dict in hosts_dict]

    def _get_detailed_hosts_list_result(self, hosts_dict):
        detailed_hosts_list = []
        for host_dict in hosts_dict:
            current_element = SVCListResultsElement()
            current_element.add(HOST_ID_PARAM, host_dict.get(HOST_ID_PARAM))
            current_element.add(HOST_NAME_PARAM, host_dict.get(HOST_NAME_PARAM))
            iscsi_names_list = host_dict.get(HOST_ISCSI_NAMES_PARAM)
            if iscsi_names_list:
                for iscsi_name in iscsi_names_list:
                    current_element.add(HOST_ISCSI_NAMES_PARAM, iscsi_name)
            wwpns_list = host_dict.get(HOST_WWPNS_PARAM)
            if wwpns_list:
                for wwpn in wwpns_list:
                    current_element.add(HOST_WWPNS_PARAM, wwpn)
            detailed_hosts_list.append(current_element)
        return iter(detailed_hosts_list)

    def test_get_volume_mappings_empty_mapping_list(self):
        self.svc.client.svcinfo.lsvdiskhostmap.return_value = []
        mappings = self.svc.get_volume_mappings("vol")
        self.assertEqual(mappings, {})

    def test_get_volume_mappings_on_volume_not_found(self):
        self.svc.client.svcinfo.lsvdiskhostmap.side_effect = [
            svc_errors.CommandExecutionError('Failed')]

        with self.assertRaises(array_errors.ObjectNotFoundError):
            self.svc.get_volume_mappings('vol')

    def test_get_volume_mappings_success(self):
        map1 = Munch({'id': '51', 'name': 'peng', 'SCSI_id': '0',
                      'host_id': '12', 'host_name': 'Test_P'})
        map2 = Munch({'id': '52', 'name': 'peng', 'SCSI_id': '1',
                      'host_id': '18', 'host_name': 'Test_W'})
        self.svc.client.svcinfo.lsvdiskhostmap.return_value = [map1, map2]
        mappings = self.svc.get_volume_mappings("vol")
        self.assertEqual(mappings, {'Test_P': '0', 'Test_W': '1'})

    def test_get_first_free_lun_raises_host_not_found_error(self):
        self.svc.client.svcinfo.lshostvdiskmap.side_effect = [
            svc_errors.CommandExecutionError('Failed')]
        with self.assertRaises(array_errors.HostNotFoundError):
            self.svc.get_first_free_lun('host')

    def test_get_first_free_lun_with_no_host_mappings(self):
        self.svc.client.svcinfo.lshostvdiskmap.return_value = []
        lun = self.svc.get_first_free_lun('host')
        self.assertEqual(lun, '0')

    @patch.object(SVCArrayMediator, "MAX_LUN_NUMBER", 3)
    @patch.object(SVCArrayMediator, "MIN_LUN_NUMBER", 1)
    def test_get_first_free_lun_success(self):
        map1 = Munch({'id': '51', 'name': 'peng', 'SCSI_id': '0',
                      'host_id': '12', 'host_name': 'Test_P'})
        map2 = Munch({'id': '56', 'name': 'peng', 'SCSI_id': '1',
                      'host_id': '16', 'host_name': 'Test_W'})
        self.svc.client.svcinfo.lshostvdiskmap.return_value = [map1, map2]
        lun = self.svc.get_first_free_lun('Test_P')
        self.assertEqual(lun, '2')

    @patch.object(SVCArrayMediator, "MAX_LUN_NUMBER", 3)
    @patch.object(SVCArrayMediator, "MIN_LUN_NUMBER", 1)
    def test_first_free_lun_no_available_lun(self):
        map1 = Munch({'id': '51', 'name': 'peng', 'SCSI_id': '1',
                      'host_id': '12', 'host_name': 'Test_P'})
        map2 = Munch({'id': '56', 'name': 'peng', 'SCSI_id': '2',
                      'host_id': '16', 'host_name': 'Test_W'})
        map3 = Munch({'id': '58', 'name': 'Host', 'SCSI_id': '3',
                      'host_id': '18', 'host_name': 'Test_H'})
        self.svc.client.svcinfo.lshostvdiskmap.return_value = [map1, map2,
                                                               map3]
        with self.assertRaises(array_errors.NoAvailableLunError):
            self.svc.get_first_free_lun('Test_P')

    @patch("controller.array_action.array_mediator_svc.is_warning_message")
    @patch("controller.array_action.array_mediator_svc.SVCArrayMediator.get_first_free_lun")
    def test_map_volume_vol_not_found(self, mock_get_first_free_lun,
                                      mock_is_warning_message):
        mock_is_warning_message.return_value = False
        mock_get_first_free_lun.return_value = '1'
        self.svc.client.svctask.mkvdiskhostmap.side_effect = [
            svc_errors.CommandExecutionError('CMMVC5804E')]
        with self.assertRaises(array_errors.ObjectNotFoundError):
            self.svc.map_volume("vol", "host")

    @patch("controller.array_action.array_mediator_svc.is_warning_message")
    @patch("controller.array_action.array_mediator_svc.SVCArrayMediator.get_first_free_lun")
    def test_map_volume_host_not_found(self, mock_get_first_free_lun,
                                       mock_is_warning_message):
        mock_is_warning_message.return_value = False
        mock_get_first_free_lun.return_value = '2'
        self.svc.client.svctask.mkvdiskhostmap.side_effect = [
            svc_errors.CommandExecutionError('CMMVC5754E')]
        with self.assertRaises(array_errors.HostNotFoundError):
            self.svc.map_volume("vol", "host")

    @patch("controller.array_action.array_mediator_svc.is_warning_message")
    @patch("controller.array_action.array_mediator_svc.SVCArrayMediator.get_first_free_lun")
    def test_map_volume_vol_already_in_use(self, mock_get_first_free_lun,
                                           mock_is_warning_message):
        mock_is_warning_message.return_value = False
        mock_get_first_free_lun.return_value = '3'
        self.svc.client.svctask.mkvdiskhostmap.side_effect = [
            svc_errors.CommandExecutionError('CMMVC5878E')]
        with self.assertRaises(array_errors.LunAlreadyInUseError):
            self.svc.map_volume("vol", "host")

    @patch("controller.array_action.array_mediator_svc.is_warning_message")
    @patch("controller.array_action.array_mediator_svc.SVCArrayMediator.get_first_free_lun")
    def test_map_volume_raise_mapping_error(
            self, mock_get_first_free_lun, mock_is_warning_message):
        mock_is_warning_message.return_value = False
        mock_get_first_free_lun.return_value = '4'
        self.svc.client.svctask.mkvdiskhostmap.side_effect = [
            svc_errors.CommandExecutionError('Failed')]
        with self.assertRaises(array_errors.MappingError):
            self.svc.map_volume("vol", "host")

    def test_map_volume_raise_exception(self):
        self.svc.client.svctask.mkvdiskhostmap.side_effect = [Exception]
        with self.assertRaises(Exception):
            self.svc.map_volume("vol", "host")

    @patch("controller.array_action.array_mediator_svc.SVCArrayMediator.get_first_free_lun")
    def test_map_volume_success(self, mock_get_first_free_lun):
        mock_get_first_free_lun.return_value = '5'
        self.svc.client.svctask.mkvdiskhostmap.return_value = None
        lun = self.svc.map_volume("vol", "host")
        self.assertEqual(lun, '5')

    @patch("controller.array_action.array_mediator_svc.is_warning_message")
    def test_unmap_volume_vol_not_found(self, mock_is_warning_message):
        mock_is_warning_message.return_value = False
        self.svc.client.svctask.rmvdiskhostmap.side_effect = [
            svc_errors.CommandExecutionError('CMMVC5753E')]
        with self.assertRaises(array_errors.ObjectNotFoundError):
            self.svc.unmap_volume("vol", "host")

    @patch("controller.array_action.array_mediator_svc.is_warning_message")
    def test_unmap_volume_host_not_found(self, mock_is_warning_message):
        mock_is_warning_message.return_value = False
        self.svc.client.svctask.rmvdiskhostmap.side_effect = [
            svc_errors.CommandExecutionError('CMMVC5754E')]
        with self.assertRaises(array_errors.HostNotFoundError):
            self.svc.unmap_volume("vol", "host")

    @patch("controller.array_action.array_mediator_svc.is_warning_message")
    def test_unmap_volume_vol_already_unmapped(self, mock_is_warning_message):
        mock_is_warning_message.return_value = False
        self.svc.client.svctask.rmvdiskhostmap.side_effect = [
            svc_errors.CommandExecutionError('CMMVC5842E')]
        with self.assertRaises(array_errors.VolumeAlreadyUnmappedError):
            self.svc.unmap_volume("vol", "host")

    @patch("controller.array_action.array_mediator_svc.is_warning_message")
    def test_unmap_volume_raise_unmapped_error(self, mock_is_warning_message):
        mock_is_warning_message.return_value = False
        self.svc.client.svctask.rmvdiskhostmap.side_effect = [
            svc_errors.CommandExecutionError('Failed')]
        with self.assertRaises(array_errors.UnMappingError):
            self.svc.unmap_volume("vol", "host")

    def test_unmap_volume_raise_exception(self):
        self.svc.client.svctask.rmvdiskhostmap.side_effect = [Exception]
        with self.assertRaises(Exception):
            self.svc.unmap_volume("vol", "host")

    def test_unmap_volume_success(self):
        self.svc.client.svctask.rmvdiskhostmap.return_value = None
        self.svc.unmap_volume("vol", "host")

    def test_get_iscsi_targets_cmd_error_raise_no_targets_error(self):
        self.svc.client.svcinfo.lsportip.side_effect = [
            svc_errors.CommandExecutionError('Failed')]
        with self.assertRaises(array_errors.NoIscsiTargetsFoundError):
            self.svc.get_iscsi_targets_by_iqn()

    def test_get_iscsi_targets_cli_error_raise_no_targets_error(self):
        self.svc.client.svcinfo.lsportip.side_effect = [
            CLIFailureError("Failed")]
        with self.assertRaises(array_errors.NoIscsiTargetsFoundError):
            self.svc.get_iscsi_targets_by_iqn()

    def test_get_iscsi_targets_no_online_node_raise_no_targets_error(self):
        node = Munch({'id': '1',
                      'name': 'node1',
                      'iscsi_name': 'iqn.1986-03.com.ibm:2145.v7k1.node1',
                      'status': 'offline'})
        self.svc.client.svcinfo.lsnode.return_value = [node]
        with self.assertRaises(array_errors.NoIscsiTargetsFoundError):
            self.svc.get_iscsi_targets_by_iqn()

    def test_get_iscsi_targets_no_nodes_nor_ips_raise_no_targets_error(self):
        self.svc.client.svcinfo.lsnode.return_value = []
        self.svc.client.svcinfo.lsportip.return_value = []
        with self.assertRaises(array_errors.NoIscsiTargetsFoundError):
            self.svc.get_iscsi_targets_by_iqn()

    def test_get_iscsi_targets_no_port_with_ip_raise_no_targets_error(self):
        port_1 = Munch({'node_id': '1', 'IP_address': None, 'IP_address_6': ''})
        port_2 = Munch({'node_id': '2', 'IP_address': '', 'IP_address_6': None})
        self.svc.client.svcinfo.lsportip.return_value = [port_1, port_2]
        with self.assertRaises(array_errors.NoIscsiTargetsFoundError):
            self.svc.get_iscsi_targets_by_iqn()

    def test_get_iscsi_targets_no_ip_raise_no_targets_error(self):
        self.svc.client.svcinfo.lsportip.return_value = []
        with self.assertRaises(array_errors.NoIscsiTargetsFoundError):
            self.svc.get_iscsi_targets_by_iqn()

    def test_get_iscsi_targets_success(self):
        ips_by_iqn = self.svc.get_iscsi_targets_by_iqn()
        self.assertEqual(ips_by_iqn, {'iqn.1986-03.com.ibm:2145.v7k1.node1': ['1.1.1.1']})

    def test_get_iscsi_targets_with_exception(self):
        self.svc.client.svcinfo.lsnode.side_effect = [Exception]
        with self.assertRaises(Exception):
            self.svc.get_iscsi_targets_by_iqn()

    def test_get_iscsi_targets_with_multi_nodes(self):
        node1 = Munch({'id': '1',
                       'name': 'node1',
                       'iscsi_name': 'iqn.1986-03.com.ibm:2145.v7k1.node1',
                       'status': 'online'})
        node2 = Munch({'id': '2',
                       'name': 'node2',
                       'iscsi_name': 'iqn.1986-03.com.ibm:2145.v7k1.node2',
                       'status': 'online'})
        self.svc.client.svcinfo.lsnode.return_value = [node1, node2]
        port_1 = Munch({'node_id': '1', 'IP_address': '1.1.1.1', 'IP_address_6': None})
        port_2 = Munch({'node_id': '1', 'IP_address': '2.2.2.2', 'IP_address_6': None})
        port_3 = Munch({'node_id': '2', 'IP_address': '', 'IP_address_6': '1::1'})
        self.svc.client.svcinfo.lsportip.return_value = [port_1, port_2, port_3]

        ips_by_iqn = self.svc.get_iscsi_targets_by_iqn()

        self.assertEqual(ips_by_iqn, {'iqn.1986-03.com.ibm:2145.v7k1.node1': ['1.1.1.1', '2.2.2.2'],
                                      'iqn.1986-03.com.ibm:2145.v7k1.node2': ['[1::1]']})

    def test_get_array_fc_wwns_failed(self):
        self.svc.client.svcinfo.lsfabric.side_effect = [
            svc_errors.CommandExecutionError('Failed')]
        with self.assertRaises(svc_errors.CommandExecutionError):
            self.svc.get_array_fc_wwns('host')

    def test_get_array_fc_wwns_success(self):
        port_1 = Munch({'remote_wwpn': '21000024FF3A42E5',
                        'remote_nportid': '012F00', 'id': '1',
                        'node_name': 'node1', 'local_wwpn': '5005076810282CD8',
                        'local_port': '8', 'local_nportid': '010601',
                        'state': 'active', 'name': 'csi_host',
                        'cluster_name': '', 'type': 'host'})
        port_2 = Munch({'remote_wwpn': '21000024FF3A42E6',
                        'remote_nportid': '012F10', 'id': '2',
                        'node_name': 'node2', 'local_wwpn': '5005076810262CD8',
                        'local_port': '9', 'local_nportid': '010611',
                        'state': 'inactive', 'name': 'csi_host',
                        'cluster_name': '', 'type': 'host'})
        self.svc.client.svcinfo.lsfabric.return_value = [port_1, port_2]
        wwns = self.svc.get_array_fc_wwns('host')
        self.assertEqual(wwns, ['5005076810282CD8', '5005076810262CD8'])

    def _prepare_mocks_for_expand_volume(self):
        volume = Mock(as_single_element=Munch({'vdisk_UID': 'vol_id',
                                               'name': 'test_vol',
                                               'capacity': '512',
                                               'mdisk_grp_name': 'pool_name'
                                               }))
        self.svc.client.svcinfo.lsvdisk.return_value = volume
        self.svc.client.svcinfo.lsfcmap.return_value = Mock(as_list=[])

    def test_expand_volume_success(self):
        self._prepare_mocks_for_expand_volume()
        self.svc.expand_volume('vol_id', 1024)
        self.svc.client.svctask.expandvdisksize.assert_called_once_with(vdisk_id='test_vol', unit='b', size=512)

    def test_expand_volume_success_with_size_rounded_up(self):
        self._prepare_mocks_for_expand_volume()
        self.svc.expand_volume('vol_id', 513)
        self.svc.client.svctask.expandvdisksize.assert_called_once_with(vdisk_id='test_vol', unit='b', size=512)

    def test_expand_volume_raise_object_in_use(self):
        self._prepare_mocks_for_expand_volume()
        fcmaps = self.fcmaps_as_source
        fcmaps[0].status = 'not good'
        self.svc.client.svcinfo.lsfcmap.side_effect = [Mock(as_list=self.fcmaps), Mock(as_list=fcmaps)]
        with self.assertRaises(array_errors.ObjectIsStillInUseError):
            self.svc.expand_volume('vol_id', 2)
        self.svc.client.svctask.expandvdisksize.assert_not_called()

    def test_expand_volume_raise_object_not_found(self):
        self.svc.client.svcinfo.lsvdisk.return_value = Mock(as_single_element=None)
        with self.assertRaises(array_errors.ObjectNotFoundError):
            self.svc.expand_volume('vol_id', 2)
        self.svc.client.svctask.expandvdisksize.assert_not_called()

    @patch("controller.array_action.array_mediator_svc.is_warning_message")
    def _expand_volume_expandvdisksize_errors(self, mock_warning, returned_error, expected_raised):
        self._prepare_mocks_for_expand_volume()
        mock_warning.return_value = False

        self.svc.client.svctask.expandvdisksize.side_effect = [returned_error]

        with self.assertRaises(expected_raised):
            self.svc.expand_volume('vol_id', 2)

    def test_expand_volume_expandvdisksize_object_not_found_error(self):
        self._expand_volume_expandvdisksize_errors(returned_error=CLIFailureError("CMMVC5753E"),
                                                   expected_raised=array_errors.ObjectNotFoundError)

    def test_expand_volume_expandvdisksize_volume_not_found_error(self):
        self._expand_volume_expandvdisksize_errors(returned_error=CLIFailureError("CMMVC8957E"),
                                                   expected_raised=array_errors.ObjectNotFoundError)

    def test_expand_volume_expandvdisksize_not_enough_space_error(self):
        self._expand_volume_expandvdisksize_errors(returned_error=CLIFailureError("CMMVC5860E"),
                                                   expected_raised=array_errors.NotEnoughSpaceInPool)

    def test_expand_volume_expandvdisksize_cli_failure_error(self):
        self._expand_volume_expandvdisksize_errors(returned_error=CLIFailureError("Failed"),
                                                   expected_raised=CLIFailureError)

    def test_expand_volume_expandvdisksize_general_error(self):
        self._expand_volume_expandvdisksize_errors(returned_error=Exception("Failed"), expected_raised=Exception)
