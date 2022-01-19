import unittest

from mock import patch, Mock, call, PropertyMock
from munch import Munch
from pysvc import errors as svc_errors
from pysvc.unified.response import CLIFailureError, SVCResponse

import controller.array_action.config as config
import controller.array_action.errors as array_errors
from controller.array_action.array_mediator_svc import SVCArrayMediator, build_kwargs_from_parameters, \
    FCMAP_STATUS_DONE, YES
from controller.common.node_info import Initiators

EMPTY_BYTES = b''


class TestArrayMediatorSVC(unittest.TestCase):

    def setUp(self):
        self.endpoint = ["IP_1"]
        with patch("controller.array_action.array_mediator_svc.SVCArrayMediator._connect"):
            self.svc = SVCArrayMediator("user", "password", self.endpoint)
        self.svc.client = Mock()
        self.svc.client.svcinfo.lssystem.return_value = [Munch({'location': 'local',
                                                                'id_alias': 'fake_identifier'})]
        node = Munch({'id': '1', 'name': 'node1', 'iscsi_name': 'iqn.1986-03.com.ibm:2145.v7k1.node1',
                      'status': 'online'})
        self.svc.client.svcinfo.lsnode.return_value = [node]
        lsportip_port = Munch({'node_id': '1', 'IP_address': '1.1.1.1', 'IP_address_6': None})
        lsip_port = Munch({'node_id': '1', 'IP_address': '1.1.1.1', 'portset_id': 'demo_id'})
        self.svc.client.svcinfo.lsportip.return_value = [lsportip_port]
        self.svc.client.svcinfo.lsip.return_value = [lsip_port]
        self.fcmaps = [self._create_dummy_fcmap('source_name', 'test_fc_id')]
        self.fcmaps_as_target = [self._create_dummy_fcmap('source_name', 'test_fc_as_target_id')]
        self.fcmaps_as_source = [self._create_dummy_fcmap('test_snapshot', 'test_fc_id')]
        self.svc.client.svcinfo.lsfcmap.return_value = Mock(as_list=self.fcmaps)

    def _create_dummy_fcmap(self, source_name, id_value):
        return Munch(
            {'source_vdisk_name': source_name,
             'target_vdisk_name': 'target_name',
             'id': id_value,
             'status': FCMAP_STATUS_DONE,
             'copy_rate': 'non_zero_value',
             'rc_controlled': 'no'})

    @patch("controller.array_action.array_mediator_svc.connect")
    def test_init_unsupported_system_version(self, connect_mock):
        code_level_below_min_supported = '7.7.77.77 (build 777.77.7777777777777)'
        svc_mock = Mock()
        svc_mock.svcinfo.lssystem.return_value = [Munch({'location': 'local',
                                                         'code_level': code_level_below_min_supported})]
        connect_mock.return_value = svc_mock
        with self.assertRaises(array_errors.UnsupportedStorageVersionError):
            SVCArrayMediator("user", "password", self.endpoint)

    def test_raise_ManagementIPsNotSupportError_in_init(self):
        self.endpoint = ["IP_1", "IP_2"]
        with self.assertRaises(
                array_errors.StorageManagementIPsNotSupportError):
            SVCArrayMediator("user", "password", self.endpoint)

        self.endpoint = []
        with self.assertRaises(
                array_errors.StorageManagementIPsNotSupportError):
            SVCArrayMediator("user", "password", self.endpoint)

    @patch("controller.array_action.array_mediator_svc.connect")
    def test_connect_errors(self, connect_mock):
        connect_mock.side_effect = [
            svc_errors.IncorrectCredentials('Failed_a')]
        with self.assertRaises(array_errors.CredentialsError):
            self.svc._connect()

    def test_close(self):
        self.svc.disconnect()
        self.svc.client.close.assert_called_with()

    def test_default_object_prefix_length_not_larger_than_max(self):
        prefix_length = len(self.svc.default_object_prefix)
        self.assertGreaterEqual(self.svc.max_object_prefix_length, prefix_length)
        self.assertGreaterEqual(self.svc.max_object_prefix_length, prefix_length)

    @patch("controller.array_action.array_mediator_svc.is_warning_message")
    def _test_mediator_method_client_error(self, mediator_method, args, client_method, client_error, expected_error,
                                           mock_warning):
        mock_warning.return_value = False
        client_method.side_effect = [client_error]
        with self.assertRaises(expected_error):
            mediator_method(*args)

    def _test_mediator_method_client_cli_failure_error(self, mediator_method, args, client_method, error_message_id,
                                                       expected_error):
        self._test_mediator_method_client_error(mediator_method, args, client_method, CLIFailureError(error_message_id),
                                                expected_error)

    def _test_get_volume_lsvdisk_cli_failure_error(self, volume_name, error_message_id, expected_error):
        self._test_mediator_method_client_cli_failure_error(self.svc.get_volume, (volume_name,),
                                                            self.svc.client.svcinfo.lsvdisk, error_message_id,
                                                            expected_error)

    def test_get_volume_lsvdisk_cli_failure_errors(self):
        self._test_get_volume_lsvdisk_cli_failure_error("volume_name", 'CMMVC5753E', array_errors.ObjectNotFoundError)
        self._test_get_volume_lsvdisk_cli_failure_error("\xff", 'CMMVC6017E', array_errors.IllegalObjectName)
        self._test_get_volume_lsvdisk_cli_failure_error("12345", 'CMMVC5703E', array_errors.IllegalObjectName)
        self._test_get_volume_lsvdisk_cli_failure_error("", 'other error', CLIFailureError)

    def test_get_volume_return_correct_value(self):
        cli_volume_mock = Mock(as_single_element=self._get_cli_volume())
        self.svc.client.svcinfo.lsvdisk.return_value = cli_volume_mock
        volume = self.svc.get_volume("test_volume")
        self.assertEqual(volume.capacity_bytes, 1024)
        self.assertEqual(volume.pool, 'pool_name')
        self.assertEqual(volume.array_type, 'SVC')

    def test_get_volume_hyperswap_has_no_source(self):
        target_cli_volume = self._get_mapped_target_cli_volume()
        self.svc.client.svcinfo.lsvdisk.return_value = self._mock_cli_object(target_cli_volume)
        self._prepare_fcmaps_for_hyperswap()

        volume = self.svc.get_volume("volume_name")

        self.assertIsNone(volume.copy_source_id)

    def _prepare_stretched_volume_mock(self):
        cli_volume = self._get_cli_volume(pool_name=['many', 'pool1', 'pool2'])
        self.svc.client.svcinfo.lsvdisk.return_value = Mock(as_single_element=cli_volume)

    def test_get_volume_stretched_return_correct_pools(self):
        self._prepare_stretched_volume_mock()

        volume = self.svc.get_volume("volume_name")

        self.assertEqual(volume.pool, 'pool1:pool2')

    def test_get_volume_raise_exception(self):
        self._test_mediator_method_client_error(self.svc.get_volume, ("volume",),
                                                self.svc.client.svcinfo.lsvdisk, Exception, Exception)

    def test_get_volume_returns_nothing(self):
        vol_ret = Mock(as_single_element=Munch({}))
        self.svc.client.svcinfo.lsvdisk.return_value = vol_ret
        with self.assertRaises(array_errors.ObjectNotFoundError):
            self.svc.get_volume("volume")

    def _test_create_volume_mkvolume_cli_failure_error(self, error_message_id, expected_error, volume_name="volume"):
        self._test_mediator_method_client_cli_failure_error(self.svc.create_volume, (volume_name, 10, "thin", "pool"),
                                                            self.svc.client.svctask.mkvolume, error_message_id,
                                                            expected_error)

    def test_create_volume_raise_exceptions(self):
        self._test_mediator_method_client_error(self.svc.create_volume, ("volume", 10, "thin", "pool"),
                                                self.svc.client.svctask.mkvolume, Exception, Exception)
        self._test_create_volume_mkvolume_cli_failure_error("Failed", CLIFailureError)
        self._test_create_volume_mkvolume_cli_failure_error("CMMVC8710E", array_errors.NotEnoughSpaceInPool)
        self._test_create_volume_mkvolume_cli_failure_error("CMMVC6017E", array_errors.IllegalObjectName, "\xff")
        self._test_create_volume_mkvolume_cli_failure_error("CMMVC6527E", array_errors.IllegalObjectName, "1_volume")
        self._test_create_volume_mkvolume_cli_failure_error("CMMVC5738E", array_errors.IllegalObjectName, "a" * 64)
        self._test_create_volume_mkvolume_cli_failure_error("CMMVC6035E", array_errors.VolumeAlreadyExists)
        self._test_create_volume_mkvolume_cli_failure_error("CMMVC5754E", array_errors.PoolDoesNotExist)
        self._test_create_volume_mkvolume_cli_failure_error("CMMVC9292E", array_errors.PoolDoesNotMatchSpaceEfficiency)
        self._test_create_volume_mkvolume_cli_failure_error("CMMVC9301E", array_errors.PoolDoesNotMatchSpaceEfficiency)

    def _test_create_volume_success(self, space_efficiency):
        self.svc.client.svctask.mkvolume.return_value = Mock()
        vol_ret = Mock(as_single_element=self._get_cli_volume())
        self.svc.client.svcinfo.lsvdisk.return_value = vol_ret
        volume = self.svc.create_volume("test_volume", 1024, space_efficiency, "pool_name")

        self.assertEqual(volume.capacity_bytes, 1024)
        self.assertEqual(volume.array_type, 'SVC')
        self.assertEqual(volume.id, 'vol_id')
        self.assertEqual(volume.internal_id, 'test_id')

    def test_create_volume_with_thin_space_efficiency_success(self):
        self._test_create_volume_success(config.SPACE_EFFICIENCY_THIN)
        self.svc.client.svctask.mkvolume.assert_called_with(name="test_volume", unit="b", size=1024, pool="pool_name",
                                                            thin=True)

    def test_create_volume_with_compressed_space_efficiency_success(self):
        self._test_create_volume_success(config.SPACE_EFFICIENCY_COMPRESSED)
        self.svc.client.svctask.mkvolume.assert_called_with(name="test_volume", unit="b", size=1024, pool="pool_name",
                                                            compressed=True)

    def test_create_volume_with_deduplicated_thin_space_efficiency_success(self):
        self._test_create_volume_success(config.SPACE_EFFICIENCY_DEDUPLICATED_THIN)
        self.svc.client.svctask.mkvolume.assert_called_with(name="test_volume", unit="b", size=1024, pool="pool_name",
                                                            thin=True, deduplicated=True)

    def test_create_volume_with_deduplicated_compressed_space_efficiency_success(self):
        self._test_create_volume_success(config.SPACE_EFFICIENCY_DEDUPLICATED_COMPRESSED)
        self.svc.client.svctask.mkvolume.assert_called_with(name="test_volume", unit="b", size=1024, pool="pool_name",
                                                            compressed=True, deduplicated=True)

    def test_create_volume_with_deduplicated_backward_compatibility_space_efficiency_success(self):
        self._test_create_volume_success(config.SPACE_EFFICIENCY_DEDUPLICATED)
        self.svc.client.svctask.mkvolume.assert_called_with(name="test_volume", unit="b", size=1024, pool="pool_name",
                                                            compressed=True, deduplicated=True)

    def _test_create_volume_with_default_space_efficiency_success(self, space_efficiency):
        self._test_create_volume_success(space_efficiency)
        self.svc.client.svctask.mkvolume.assert_called_with(name="test_volume", unit="b", size=1024, pool="pool_name")

    def test_create_volume_with_empty_string_space_efficiency_success(self):
        self._test_create_volume_with_default_space_efficiency_success("")

    def test_create_volume_with_thick_space_efficiency_success(self):
        self._test_create_volume_with_default_space_efficiency_success(config.SPACE_EFFICIENCY_THICK)

    def _test_delete_volume_rmvolume_cli_failure_error(self, error_message_id, expected_error, volume_name="volume"):
        self._test_mediator_method_client_cli_failure_error(self.svc.delete_volume, (volume_name,),
                                                            self.svc.client.svctask.rmvolume, error_message_id,
                                                            expected_error)

    def test_delete_volume_return_volume_delete_errors(self):
        self._test_delete_volume_rmvolume_cli_failure_error("CMMVC5753E", array_errors.ObjectNotFoundError)
        self._test_delete_volume_rmvolume_cli_failure_error("CMMVC8957E", array_errors.ObjectNotFoundError)
        self._test_delete_volume_rmvolume_cli_failure_error("Failed", CLIFailureError)

    def _prepare_mocks_for_object_still_in_use(self):
        cli_volume = self._get_cli_volume()
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
            self.svc.delete_volume("volume")

    def test_delete_volume_still_copy_fcmaps_not_removed(self):
        self._prepare_mocks_for_object_still_in_use()
        fcmaps_as_target = Mock(as_list=[])
        fcmaps = self.fcmaps
        fcmaps[0].status = "not good"
        fcmaps_as_source = Mock(as_list=fcmaps)
        self.svc.client.svcinfo.lsfcmap.side_effect = [fcmaps_as_target, fcmaps_as_source]
        with self.assertRaises(array_errors.ObjectIsStillInUseError):
            self.svc.delete_volume("volume")

    def _prepare_fcmaps_for_hyperswap(self):
        self.fcmaps_as_target[0].rc_controlled = "yes"
        fcmaps_as_target = Mock(as_list=self.fcmaps_as_target)
        self.fcmaps[0].rc_controlled = "yes"
        fcmaps_as_source = Mock(as_list=self.fcmaps)
        self.svc.client.svcinfo.lsfcmap.side_effect = [fcmaps_as_target, fcmaps_as_source]

    def test_delete_volume_does_not_remove_hyperswap_fcmap(self):
        self._prepare_fcmaps_for_hyperswap()
        self.svc.delete_volume("volume")

        self.svc.client.svctask.rmfcmap.assert_not_called()

    def test_delete_volume_has_clone_fcmaps_removed(self):
        fcmaps_as_target = Mock(as_list=[])
        fcmaps_as_source = Mock(as_list=self.fcmaps_as_source)
        self.svc.client.svcinfo.lsfcmap.side_effect = [fcmaps_as_target, fcmaps_as_source]
        self.svc.delete_volume("volume")
        self.svc.client.svctask.rmfcmap.assert_called_once()

    @patch("controller.array_action.array_mediator_svc.is_warning_message")
    def test_delete_volume_has_clone_rmfcmap_raise_error(self, mock_warning):
        mock_warning.return_value = False
        fcmaps_as_target = Mock(as_list=[])
        fcmaps_as_source = Mock(as_list=self.fcmaps_as_source)
        self.svc.client.svcinfo.lsfcmap.side_effect = [fcmaps_as_target, fcmaps_as_source]
        self.svc.client.svctask.rmfcmap.side_effect = [CLIFailureError('error')]
        with self.assertRaises(CLIFailureError):
            self.svc.delete_volume("volume")

    def test_delete_volume_success(self):
        self.svc.client.svctask.rmvolume = Mock()
        self.svc.delete_volume("volume")

    def test_copy_to_existing_volume_from_source_success(self):
        self.svc.copy_to_existing_volume_from_source("a", "b", 1, 1)
        self.svc.client.svctask.mkfcmap.assert_called_once()
        self.svc.client.svctask.startfcmap.assert_called_once()

    @patch("controller.array_action.array_mediator_svc.is_warning_message")
    def _test_copy_to_existing_volume_raise_errors(self, mock_warning, client_return_value, expected_error):
        mock_warning.return_value = False
        self.svc.client.svcinfo.lsvdisk.side_effect = [client_return_value, client_return_value]
        with self.assertRaises(expected_error):
            self.svc.copy_to_existing_volume_from_source("a", "b", 1, 1)

    def test_copy_to_existing_volume_raise_not_found(self):
        self._test_copy_to_existing_volume_raise_errors(client_return_value=Mock(as_single_element=None),
                                                        expected_error=array_errors.ObjectNotFoundError)

    def test_copy_to_existing_volume_raise_illegal_object_id(self):
        self._test_copy_to_existing_volume_raise_errors(client_return_value=CLIFailureError('CMMVC6017E'),
                                                        expected_error=array_errors.IllegalObjectID)
        self._test_copy_to_existing_volume_raise_errors(client_return_value=CLIFailureError('CMMVC5741E'),
                                                        expected_error=array_errors.IllegalObjectID)

    @staticmethod
    def _mock_cli_object(cli_object):
        return Mock(as_single_element=cli_object)

    @classmethod
    def _mock_cli_objects(cls, cli_objects):
        return map(cls._mock_cli_object, cli_objects)

    @staticmethod
    def _get_cli_volume(with_deduplicated_copy=True, name='source_volume', pool_name='pool_name'):
        se_copy = YES
        deduplicated_copy = 'no'
        compressed_copy = 'no'
        if with_deduplicated_copy:
            se_copy = 'no'
            deduplicated_copy = YES
            compressed_copy = YES
        return Munch({'vdisk_UID': 'vol_id',
                      'id': 'test_id',
                      'name': name,
                      'capacity': '1024',
                      'mdisk_grp_name': pool_name,
                      'FC_id': '',
                      'se_copy': se_copy,
                      'deduplicated_copy': deduplicated_copy,
                      'compressed_copy': compressed_copy
                      })

    @classmethod
    def _get_mapless_target_cli_volume(cls):
        target_cli_volume = cls._get_cli_volume()
        target_cli_volume.vdisk_UID = 'snap_id'
        target_cli_volume.name = 'test_snapshot'
        return target_cli_volume

    @classmethod
    def _get_mapped_target_cli_volume(cls):
        target_cli_volume = cls._get_mapless_target_cli_volume()
        target_cli_volume.FC_id = 'test_fc_id'
        return target_cli_volume

    def _prepare_lsvdisk_to_raise_not_found_error(self, mock_warning):
        mock_warning.return_value = False
        self.svc.client.svcinfo.lsvdisk.side_effect = [
            CLIFailureError("CMMVC5753E")]

    def _prepare_lsvdisk_to_return_mapless_target_volume(self):
        mapless_target_cli_volume = self._get_mapless_target_cli_volume()
        mapless_target_cli_volume_mock = self._mock_cli_object(mapless_target_cli_volume)
        self.svc.client.svcinfo.lsvdisk.return_value = mapless_target_cli_volume_mock

    def _prepare_lsvdisk_to_return_none(self):
        self.svc.client.svcinfo.lsvdisk.return_value = self._mock_cli_object(None)

    def _prepare_mocks_for_delete_snapshot(self):
        target_cli_volume = self._get_mapped_target_cli_volume()
        target_cli_volume.FC_id = 'many'
        self.svc.client.svcinfo.lsvdisk.return_value = self._mock_cli_object(target_cli_volume)

    def _prepare_mocks_for_get_snapshot(self):
        self._prepare_mocks_for_delete_snapshot()
        self.fcmaps[0].copy_rate = "0"

    @patch("controller.array_action.array_mediator_svc.is_warning_message")
    def test_get_snapshot_not_exist_return_none(self, mock_warning):
        self._prepare_lsvdisk_to_raise_not_found_error(mock_warning)

        snapshot = self.svc.get_snapshot("volume_id", "test_snapshot")

        self.assertIsNone(snapshot)

    def _test_get_snapshot_lsvdisk_cli_failure_error(self, snapshot_name, error_message_id, expected_error):
        volume_id = "volume_id"
        self._test_mediator_method_client_cli_failure_error(self.svc.get_snapshot, (volume_id, snapshot_name),
                                                            self.svc.client.svcinfo.lsvdisk, error_message_id,
                                                            expected_error)

    def test_get_snapshot_lsvdisk_cli_failure_errors(self):
        self._test_get_snapshot_lsvdisk_cli_failure_error("\xff", 'CMMVC6017E', array_errors.IllegalObjectName)
        self._test_get_snapshot_lsvdisk_cli_failure_error("12345", 'CMMVC5703E', array_errors.IllegalObjectName)

    def test_get_snapshot_has_no_fc_id_raise_error(self):
        self._prepare_lsvdisk_to_return_mapless_target_volume()

        with self.assertRaises(array_errors.ExpectedSnapshotButFoundVolumeError):
            self.svc.get_snapshot("volume_id", "test_snapshot")

    @patch("controller.array_action.array_mediator_svc.is_warning_message")
    def test_get_snapshot_get_fcmap_not_exist_raise_error(self, mock_warning):
        target_cli_volume = self._get_mapped_target_cli_volume()
        self.svc.client.svcinfo.lsvdisk.return_value = self._mock_cli_object(target_cli_volume)
        mock_warning.return_value = False
        self.svc.client.svcinfo.lsfcmap.side_effect = [
            CLIFailureError("CMMVC5753E")]

        with self.assertRaises(CLIFailureError):
            self.svc.get_snapshot("volume_id", "test_snapshot")

    def test_get_snapshot_non_zero_copy_rate(self):
        self._prepare_mocks_for_get_snapshot()
        self.fcmaps[0].copy_rate = "non_zero_value"
        with self.assertRaises(array_errors.ExpectedSnapshotButFoundVolumeError):
            self.svc.get_snapshot("volume_id", "test_snapshot")

    def test_get_snapshot_no_fcmap_as_target(self):
        self._prepare_mocks_for_get_snapshot()
        self.svc.client.svcinfo.lsfcmap.return_value = Mock(as_list=[])
        with self.assertRaises(array_errors.ExpectedSnapshotButFoundVolumeError):
            self.svc.get_snapshot("volume_id", "test_snapshot")

    def test_get_snapshot_success(self):
        self._prepare_mocks_for_get_snapshot()
        snapshot = self.svc.get_snapshot("volume_id", "test_snapshot")
        self.assertEqual(snapshot.name, "test_snapshot")

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

        snapshot = self.svc.get_object_by_id("test_snapshot", "snapshot")
        self.assertEqual(snapshot.name, "test_snapshot")
        calls = [call(bytes=True, filtervalue='vdisk_UID=test_snapshot'),
                 call(bytes=True, object_id='source_name')]
        self.svc.client.svcinfo.lsvdisk.assert_has_calls(calls)

    def test_get_object_by_id_volume_success(self):
        target_cli_volume = self._get_mapped_target_cli_volume()
        target_cli_volume.name = "volume_id"
        self.svc.client.svcinfo.lsvdisk.return_value = self._mock_cli_object(target_cli_volume)
        volume = self.svc.get_object_by_id("volume_id", "volume")
        self.assertEqual(volume.name, "volume_id")

    def _get_custom_cli_volume(self, support_deduplicated_copy, with_deduplicated_copy, name='source_volume',
                               pool_name='pool_name'):
        volume = self._get_cli_volume(with_deduplicated_copy, name=name, pool_name=pool_name)
        if not support_deduplicated_copy:
            del volume.deduplicated_copy
        return volume

    def _prepare_mocks_for_create_snapshot(self, support_deduplicated_copy=True, source_has_deduplicated_copy=False,
                                           different_pool_site=False, is_source_stretched=False):
        self.svc.client.svctask.mkvolume.return_value = Mock()
        self.svc.client.svctask.mkfcmap.return_value = Mock()
        pool = ['many', 'pool1', 'pool2'] if is_source_stretched else 'pool_name'
        source_volume_to_copy_from = self._get_custom_cli_volume(support_deduplicated_copy,
                                                                 source_has_deduplicated_copy,
                                                                 pool_name=pool)
        volumes_to_return = [source_volume_to_copy_from, source_volume_to_copy_from]

        if different_pool_site:
            if is_source_stretched:
                pools_to_return = [Munch({'site_name': 'pool_site'}),
                                   Munch({'site_name': 'source_volume_site'}),
                                   Munch({'site_name': 'pool_site'})]
                self.svc.client.svcinfo.lsmdiskgrp.side_effect = self._mock_cli_objects(pools_to_return)
            else:
                pools_to_return = [Munch({'site_name': 'pool_site'}),
                                   Munch({'site_name': 'source_volume_site'}),
                                   Munch({'site_name': 'other_volume_site'}),
                                   Munch({'site_name': 'pool_site'})]
                self.svc.client.svcinfo.lsmdiskgrp.side_effect = self._mock_cli_objects(pools_to_return)

                auxiliary_volumes = [self._get_cli_volume(name='other_volume', pool_name='other_volume_pool'),
                                     self._get_custom_cli_volume(support_deduplicated_copy,
                                                                 source_has_deduplicated_copy,
                                                                 name='relevant_volume',
                                                                 pool_name='relevant_volume_pool')]
                volumes_to_return.extend(auxiliary_volumes)

                rcrelationships_to_return = [Munch({'aux_vdisk_name': 'other_volume'}),
                                             Munch({'aux_vdisk_name': 'relevant_volume'})]
                self.svc.client.svcinfo.lsrcrelationship.return_value = Mock(as_list=rcrelationships_to_return)

        target_volume_after_creation = self._get_mapless_target_cli_volume()
        target_volume_after_mapping = self._get_mapped_target_cli_volume()
        target_volume_for_rollback = self._get_mapped_target_cli_volume()
        volumes_to_return.extend([target_volume_after_creation, target_volume_after_mapping,
                                  target_volume_for_rollback])

        self.svc.client.svcinfo.lsvdisk.side_effect = self._mock_cli_objects(volumes_to_return)
        self.svc.client.svctask.startfcmap.return_value = Mock()

    @patch("controller.array_action.array_mediator_svc.is_warning_message")
    def test_create_snapshot_create_volume_error(self, mock_warning):
        source_cli_volume = self._get_cli_volume()
        self.svc.client.svcinfo.lsvdisk.return_value = self._mock_cli_object(source_cli_volume)
        mock_warning.return_value = False
        self.svc.client.svctask.mkvolume.side_effect = [
            CLIFailureError("Failed")]

        with self.assertRaises(CLIFailureError):
            self.svc.create_snapshot("source_volume_id", "test_snapshot", space_efficiency=None, pool="pool1")

    def _test_create_snapshot_lsvdisk_cli_failure_error(self, volume_id, snapshot_name, error_message_id,
                                                        expected_error, space_efficiency=None, pool=None):
        self._test_mediator_method_client_cli_failure_error(self.svc.create_snapshot,
                                                            (volume_id, snapshot_name, space_efficiency, pool),
                                                            self.svc.client.svcinfo.lsvdisk, error_message_id,
                                                            expected_error)

    def test_create_snapshot_lsvdisk_cli_failure_errors(self):
        self._test_create_snapshot_lsvdisk_cli_failure_error("\xff", "snapshot_name", 'CMMVC6017E',
                                                             array_errors.IllegalObjectID)
        self._test_create_snapshot_lsvdisk_cli_failure_error("!@#", "snapshot_name", 'CMMVC5741E',
                                                             array_errors.IllegalObjectID)

    def test_create_snapshot_source_not_found_error(self):
        self.svc.client.svcinfo.lsvdisk.side_effect = [Mock(as_single_element=None), Mock(as_single_element=None)]
        with self.assertRaises(array_errors.ObjectNotFoundError):
            self.svc.create_snapshot("source_volume_id", "test_snapshot", space_efficiency=None, pool="pool1")

    @patch("controller.array_action.array_mediator_svc.is_warning_message")
    def test_create_snapshot_create_fcmap_error(self, mock_warning):
        self._prepare_mocks_for_create_snapshot()
        mock_warning.return_value = False
        self.svc.client.svctask.mkfcmap.side_effect = [
            CLIFailureError("Failed")]

        with self.assertRaises(CLIFailureError):
            self.svc.create_snapshot("source_volume_id", "test_snapshot", space_efficiency=None, pool="pool1")

    @patch("controller.array_action.array_mediator_svc.is_warning_message")
    def test_create_snapshot_start_fcmap_error(self, mock_warning):
        self._prepare_mocks_for_create_snapshot()
        mock_warning.return_value = False
        self.svc.client.svctask.startfcmap.side_effect = [
            CLIFailureError("Failed")]

        with self.assertRaises(CLIFailureError):
            self.svc.create_snapshot("source_volume_id", "test_snapshot", space_efficiency=None, pool="pool1")

    def test_create_snapshot_success(self):
        self._prepare_mocks_for_create_snapshot()

        snapshot = self.svc.create_snapshot("source_volume_id", "test_snapshot", space_efficiency=None, pool="pool1")

        self.assertEqual(snapshot.capacity_bytes, 1024)
        self.assertEqual(snapshot.array_type, 'SVC')
        self.assertEqual(snapshot.id, 'snap_id')

    def test_create_snapshot_with_different_pool_success(self):
        self._prepare_mocks_for_create_snapshot()

        self.svc.create_snapshot("source_volume_id", "test_snapshot", space_efficiency=None, pool="different_pool")
        self.svc.client.svctask.mkvolume.assert_called_once_with(name='test_snapshot', unit='b', size=1024,
                                                                 pool='different_pool', thin=True)

    def test_create_snapshot_for_hyperswap_volume_with_different_site_success(self):
        self._prepare_mocks_for_create_snapshot(different_pool_site=True)

        self.svc.create_snapshot("source_volume_id", "test_snapshot", space_efficiency=None, pool="different_pool")
        self.svc.client.svctask.mkfcmap.assert_called_once_with(source="relevant_volume", target="test_snapshot",
                                                                copyrate=0)

    def test_create_snapshot_for_stretched_volume_with_different_site_success(self):
        self._prepare_mocks_for_create_snapshot(different_pool_site=True, is_source_stretched=True)

        self.svc.create_snapshot("source_volume_id", "test_snapshot", space_efficiency=None, pool="different_pool")
        self.svc.client.svctask.mkfcmap.assert_called_once_with(source="source_volume", target="test_snapshot",
                                                                copyrate=0)

    def test_create_snapshot_for_stretched_volume_implicit_pool_success(self):
        self._prepare_mocks_for_create_snapshot(is_source_stretched=True)

        self.svc.create_snapshot("source_volume_id", "test_snapshot", space_efficiency=None, pool=None)
        self.svc.client.svctask.mkvolume.assert_called_once_with(name='test_snapshot', unit='b', size=1024,
                                                                 pool='pool1', thin=True)
        self.svc.client.svctask.mkfcmap.assert_called_once_with(source="source_volume", target="test_snapshot",
                                                                copyrate=0)

    def test_create_snapshot_as_stretched_success(self):
        self._prepare_mocks_for_create_snapshot()

        self.svc.create_snapshot("source_volume_id", "test_snapshot", space_efficiency=None, pool="pool1:pool2")
        self.svc.client.svctask.mkvolume.assert_called_once_with(name='test_snapshot', unit='b', size=1024,
                                                                 pool='pool1:pool2', thin=True)
        self.svc.client.svctask.mkfcmap.assert_called_once_with(source="source_volume", target="test_snapshot",
                                                                copyrate=0)

    def test_create_snapshot_with_specified_source_volume_space_efficiency_success(self):
        self._prepare_mocks_for_create_snapshot(source_has_deduplicated_copy=True)

        self.svc.create_snapshot("source_volume_id", "test_snapshot", space_efficiency=None, pool=None)
        self.svc.client.svctask.mkvolume.assert_called_once_with(name='test_snapshot', unit='b', size=1024,
                                                                 pool='pool_name', compressed=True, deduplicated=True)

    def test_create_snapshot_with_different_space_efficiency_success(self):
        self._prepare_mocks_for_create_snapshot(source_has_deduplicated_copy=True)

        self.svc.create_snapshot("source_volume_id", "test_snapshot", space_efficiency="thin", pool=None)
        self.svc.client.svctask.mkvolume.assert_called_once_with(name='test_snapshot', unit='b', size=1024,
                                                                 pool='pool_name', thin=True)

    def test_create_snapshot_no_deduplicated_copy_success(self):
        self._prepare_mocks_for_create_snapshot(support_deduplicated_copy=False)

        snapshot = self.svc.create_snapshot("source_volume_id", "test_snapshot", space_efficiency=None, pool="pool1")

        self.assertEqual(snapshot.capacity_bytes, 1024)
        self.assertEqual(snapshot.array_type, 'SVC')
        self.assertEqual(snapshot.id, 'snap_id')

    def test_delete_snapshot_no_volume_raise_snapshot_not_found(self):
        self._prepare_lsvdisk_to_return_none()

        with self.assertRaises(array_errors.ObjectNotFoundError):
            self.svc.delete_snapshot("test_snapshot")

    def test_delete_snapshot_no_fcmap_id_raise_snapshot_not_found(self):
        self._prepare_lsvdisk_to_return_mapless_target_volume()

        with self.assertRaises(array_errors.ObjectNotFoundError):
            self.svc.delete_snapshot("test_snapshot")

    def test_delete_snapshot_call_rmfcmap(self):
        self._prepare_mocks_for_delete_snapshot()
        fcmaps_as_target = self.fcmaps
        self.svc.client.svcinfo.lsfcmap.side_effect = [Mock(as_list=fcmaps_as_target), Mock(as_list=[])]
        self.svc.delete_snapshot("test_snapshot")

        self.svc.client.svctask.rmfcmap.assert_called_once_with(object_id="test_fc_id", force=True)

    def test_delete_snapshot_does_not_remove_hyperswap_fcmap(self):
        self._prepare_mocks_for_delete_snapshot()
        self._prepare_fcmaps_for_hyperswap()
        self.svc.delete_snapshot("test_snapshot")

        self.svc.client.svctask.rmfcmap.assert_not_called()

    def _test_delete_snapshot_rmvolume_cli_failure_error(self, error_message_id, expected_error, snapshot_id="snap_id"):
        self._test_mediator_method_client_cli_failure_error(self.svc.delete_snapshot, (snapshot_id,),
                                                            self.svc.client.svctask.rmvolume, error_message_id,
                                                            expected_error)

    def test_delete_snapshot_rmvolume_errors(self):
        self._prepare_mocks_for_delete_snapshot()
        self._test_delete_snapshot_rmvolume_cli_failure_error("CMMVC5753E", array_errors.ObjectNotFoundError)
        self._test_delete_snapshot_rmvolume_cli_failure_error("CMMVC8957E", array_errors.ObjectNotFoundError)
        self._test_delete_snapshot_rmvolume_cli_failure_error("Failed", CLIFailureError)

    def test_delete_snapshot_still_copy_fcmaps_not_removed(self):
        self._prepare_mocks_for_object_still_in_use()
        fcmaps_as_target = self.fcmaps
        fcmaps_as_source = self.fcmaps_as_source
        fcmaps_as_source[0].status = "not good"
        self.svc.client.svcinfo.lsfcmap.side_effect = [Mock(as_list=fcmaps_as_target), Mock(as_list=fcmaps_as_source)]
        with self.assertRaises(array_errors.ObjectIsStillInUseError):
            self.svc.delete_snapshot("test_snapshot")

    def test_delete_snapshot_success(self):
        self._prepare_mocks_for_delete_snapshot()
        self.svc.delete_snapshot("test_snapshot")
        self.assertEqual(self.svc.client.svctask.rmfcmap.call_count, 2)
        self.svc.client.svctask.rmvolume.assert_called_once_with(vdisk_id="test_snapshot")

    @patch("controller.array_action.array_mediator_svc.is_warning_message")
    def test_delete_snapshot_with_fcmap_already_stopped_success(self, mock_warning):
        self._prepare_mocks_for_delete_snapshot()
        mock_warning.return_value = False
        self.svc.client.svctask.stopfcmap.side_effect = [CLIFailureError('CMMVC5912E')]
        self.svc.delete_snapshot("test_snapshot")
        self.assertEqual(self.svc.client.svctask.rmfcmap.call_count, 2)
        self.svc.client.svctask.rmvolume.assert_called_once_with(vdisk_id="test_snapshot")

    @patch("controller.array_action.array_mediator_svc.is_warning_message")
    def test_delete_snapshot_with_stopfcmap_raise_error(self, mock_warning):
        self._prepare_mocks_for_delete_snapshot()
        mock_warning.return_value = False
        self.svc.client.svctask.stopfcmap.side_effect = [CLIFailureError('error')]
        with self.assertRaises(CLIFailureError):
            self.svc.delete_snapshot("test_snapshot")

    def test_validate_supported_space_efficiency_raise_error(self):
        space_efficiency = "Test"
        with self.assertRaises(
                array_errors.SpaceEfficiencyNotSupported):
            self.svc.validate_supported_space_efficiency(space_efficiency)

    def test_validate_supported_space_efficiency_success(self):
        no_space_efficiency = ""
        self.svc.validate_supported_space_efficiency(no_space_efficiency)
        thin_space_efficiency = config.SPACE_EFFICIENCY_THIN
        self.svc.validate_supported_space_efficiency(thin_space_efficiency)
        thick_space_efficiency = config.SPACE_EFFICIENCY_THICK
        self.svc.validate_supported_space_efficiency(thick_space_efficiency)
        compressed_space_efficiency = config.SPACE_EFFICIENCY_COMPRESSED
        self.svc.validate_supported_space_efficiency(compressed_space_efficiency)
        deduplicated_space_efficiency = config.SPACE_EFFICIENCY_DEDUPLICATED
        self.svc.validate_supported_space_efficiency(deduplicated_space_efficiency)
        deduplicated_thin_space_efficiency = config.SPACE_EFFICIENCY_DEDUPLICATED_THIN
        self.svc.validate_supported_space_efficiency(deduplicated_thin_space_efficiency)
        deduplicated_compressed_space_efficiency = config.SPACE_EFFICIENCY_DEDUPLICATED_COMPRESSED
        self.svc.validate_supported_space_efficiency(deduplicated_compressed_space_efficiency)

    def _test_build_kwargs_from_parameters(self, space_efficiency, pool, name, size, expected_space_efficiency_kwargs):
        expected_kwargs = {'name': name, 'unit': 'b', 'size': size, 'pool': pool}
        expected_kwargs.update(expected_space_efficiency_kwargs)
        actual_kwargs = build_kwargs_from_parameters(space_efficiency, pool, name, size)
        self.assertDictEqual(actual_kwargs, expected_kwargs)

    def test_build_kwargs_from_parameters(self):
        size = self.svc._convert_size_bytes(1000)

        self._test_build_kwargs_from_parameters('Thin', 'P1', 'V1', size, {'thin': True})
        self._test_build_kwargs_from_parameters('compressed', 'P2', 'V2', size, {'compressed': True})
        self._test_build_kwargs_from_parameters('dedup_thin', 'P3', 'V3', self.svc._convert_size_bytes(2048),
                                                {'thin': True, 'deduplicated': True})
        self._test_build_kwargs_from_parameters('dedup_compressed', 'P3', 'V3', self.svc._convert_size_bytes(2048),
                                                {'compressed': True, 'deduplicated': True})
        self._test_build_kwargs_from_parameters('Deduplicated', 'P3', 'V3', self.svc._convert_size_bytes(2048),
                                                {'compressed': True, 'deduplicated': True})

    def test_properties(self):
        self.assertEqual(SVCArrayMediator.port, 22)
        self.assertEqual(SVCArrayMediator.minimal_volume_size_in_bytes, 512)
        self.assertEqual(SVCArrayMediator.array_type, 'SVC')
        self.assertEqual(SVCArrayMediator.max_object_name_length, 63)
        self.assertEqual(SVCArrayMediator.max_connections, 2)
        self.assertEqual(SVCArrayMediator.max_lun_retries, 10)

    def _prepare_mocks_for_get_host_by_identifiers(self, svc_response, custom_host=None):
        host_1 = self._get_host_as_munch('host_id_1', 'test_host_1', nqn_list=['nqn.test.1'], wwpns_list=['wwn1'],
                                         iscsi_names_list=['iqn.test.1'])
        host_2 = self._get_host_as_munch('host_id_2', 'test_host_2', nqn_list=['nqn.test.2'], wwpns_list=['wwn2'],
                                         iscsi_names_list=['iqn.test.2'])
        if custom_host:
            host_3 = custom_host
        else:
            host_3 = self._get_host_as_munch('host_id_3', 'test_host_3', nqn_list=['nqn.test.3'],
                                             wwpns_list=['wwn3'], iscsi_names_list=['iqn.test.3'])
        hosts = [host_1, host_2, host_3]
        self.svc.client.svcinfo.lshost = Mock()
        self.svc.client.svcinfo.lshost.return_value = self._get_hosts_list_result(hosts)
        self.svc.client.send_raw_command = Mock()
        self.svc.client.send_raw_command.return_value = EMPTY_BYTES, EMPTY_BYTES
        svc_response.return_value = hosts

    @patch.object(SVCResponse, 'as_list', new_callable=PropertyMock)
    def test_get_host_by_identifiers_returns_host_not_found(self, svc_response):
        self._prepare_mocks_for_get_host_by_identifiers(svc_response)
        with self.assertRaises(array_errors.HostNotFoundError):
            self.svc.get_host_by_host_identifiers(Initiators('Test_nqn', ['Test_wwn'], 'Test_iqn'))

    def test_get_host_by_identifier_return_host_not_found_when_no_hosts_exist(self):
        hosts = []
        self.svc.client.svcinfo.lshost = Mock()
        self.svc.client.svcinfo.lshost.return_value = self._get_hosts_list_result(hosts)
        with self.assertRaises(array_errors.HostNotFoundError):
            self.svc.get_host_by_host_identifiers(Initiators('Test_nqn', ['Test_wwn'], 'Test_iqn'))

    @patch.object(SVCResponse, 'as_list', new_callable=PropertyMock)
    def test_get_host_by_identifiers_raise_multiplehostsfounderror(self, svc_response):
        self._prepare_mocks_for_get_host_by_identifiers(svc_response)
        with self.assertRaises(array_errors.MultipleHostsFoundError):
            self.svc.get_host_by_host_identifiers(Initiators('Test_nqn', ['wwn2'], 'iqn.test.3'))

    @patch.object(SVCResponse, 'as_list', new_callable=PropertyMock)
    def test_get_host_by_identifiers_return_iscsi_host(self, svc_response):
        self._prepare_mocks_for_get_host_by_identifiers(svc_response)
        hostname, connectivity_types = self.svc.get_host_by_host_identifiers(
            Initiators('Test_nqn', ['Test_wwn'], 'iqn.test.2'))
        self.assertEqual('test_host_2', hostname)
        self.assertEqual([config.ISCSI_CONNECTIVITY_TYPE], connectivity_types)

    @patch.object(SVCResponse, 'as_list', new_callable=PropertyMock)
    def test_get_host_by_identifiers_no_other_ports_return_iscsi_host(self, svc_response):
        host_with_iqn = self._get_host_as_munch('costume_host_id', 'test_costume_host',
                                                iscsi_names_list=['iqn.test.costume'])
        self._prepare_mocks_for_get_host_by_identifiers(svc_response, custom_host=host_with_iqn)
        hostname, connectivity_types = self.svc.get_host_by_host_identifiers(
            Initiators('Test_nqn', ['Test_wwn'], 'iqn.test.costume'))
        self.assertEqual('test_costume_host', hostname)
        self.assertEqual([config.ISCSI_CONNECTIVITY_TYPE], connectivity_types)

    @patch.object(SVCResponse, 'as_list', new_callable=PropertyMock)
    def test_get_host_by_identifiers_return_iscsi_host_with_list_iqn(self, svc_response):
        host_with_iqn_list = self._get_host_as_munch('costume_host_id', 'test_costume_host', wwpns_list=['wwns'],
                                                     iscsi_names_list=['iqn.test.s1', 'iqn.test.s2'])
        self._prepare_mocks_for_get_host_by_identifiers(svc_response, custom_host=host_with_iqn_list)
        hostname, connectivity_types = self.svc.get_host_by_host_identifiers(
            Initiators('Test_nqn', ['Test_wwn'], 'iqn.test.s1'))
        self.assertEqual('test_costume_host', hostname)
        self.assertEqual([config.ISCSI_CONNECTIVITY_TYPE], connectivity_types)

    @patch.object(SVCResponse, 'as_list', new_callable=PropertyMock)
    def test_get_host_by_identifiers_return_nvme_host(self, svc_response):
        self._prepare_mocks_for_get_host_by_identifiers(svc_response)
        hostname, connectivity_types = self.svc.get_host_by_host_identifiers(
            Initiators('nqn.test.3', ['Test_wwn'], 'iqn.test.6'))
        self.assertEqual('test_host_3', hostname)
        self.assertEqual([config.NVME_OVER_FC_CONNECTIVITY_TYPE], connectivity_types)

    @patch.object(SVCResponse, 'as_list', new_callable=PropertyMock)
    def test_get_host_by_identifiers_no_other_ports_return_nvme_host(self, svc_response):
        host_with_nqn = self._get_host_as_munch('costume_host_id', 'test_costume_host',
                                                nqn_list=['nqn.test.costume'])
        self._prepare_mocks_for_get_host_by_identifiers(svc_response, custom_host=host_with_nqn)
        hostname, connectivity_types = self.svc.get_host_by_host_identifiers(
            Initiators('nqn.test.costume', ['Test_wwn'], 'Test_iqn'))
        self.assertEqual('test_costume_host', hostname)
        self.assertEqual([config.NVME_OVER_FC_CONNECTIVITY_TYPE], connectivity_types)

    @patch.object(SVCResponse, 'as_list', new_callable=PropertyMock)
    def test_get_host_by_identifiers_return_fc_host(self, svc_response):
        host_1 = self._get_host_as_munch('host_id_1', 'test_host_1', wwpns_list=['wwn1'], iscsi_names_list=[])
        host_2 = self._get_host_as_munch('host_id_2', 'test_host_2', wwpns_list=['wwn2'], iscsi_names_list=[''])
        host_3 = self._get_host_as_munch('host_id_3', 'test_host_3', wwpns_list=['wwn3', 'wwn4'],
                                         iscsi_names_list=['iqn.test.3'])
        hosts = [host_1, host_2, host_3]
        self._prepare_mocks_for_get_host_by_identifiers(svc_response)
        hostname, connectivity_types = self.svc.get_host_by_host_identifiers(
            Initiators('Test_nqn', ['wwn4', 'WWN3'], 'iqn.test.6'))
        self.assertEqual('test_host_3', hostname)
        self.assertEqual([config.FC_CONNECTIVITY_TYPE], connectivity_types)

        svc_response.return_value = hosts
        hostname, connectivity_types = self.svc.get_host_by_host_identifiers(
            Initiators('Test_nqn', ['wwn3'], 'iqn.test.6'))
        self.assertEqual('test_host_3', hostname)
        self.assertEqual([config.FC_CONNECTIVITY_TYPE], connectivity_types)

    @patch.object(SVCResponse, 'as_list', new_callable=PropertyMock)
    def test_get_host_by_identifiers_no_other_ports_return_fc_host(self, svc_response):
        host_with_wwpn = self._get_host_as_munch('costume_host_id', 'test_costume_host', wwpns_list=['WWNs'])
        self._prepare_mocks_for_get_host_by_identifiers(svc_response, custom_host=host_with_wwpn)
        hostname, connectivity_types = self.svc.get_host_by_host_identifiers(
            Initiators('Test_nqn', ['Test_wwn', 'WWNs'], 'Test_iqn'))
        self.assertEqual('test_costume_host', hostname)
        self.assertEqual([config.FC_CONNECTIVITY_TYPE], connectivity_types)

    @patch.object(SVCResponse, 'as_list', new_callable=PropertyMock)
    def test_get_host_by_identifiers_with_wrong_fc_iscsi_raise_not_found(self, svc_response):
        host_1 = self._get_host_as_munch('host_id_1', 'test_host_1', wwpns_list=['wwn1'], iscsi_names_list=[])
        host_2 = self._get_host_as_munch('host_id_2', 'test_host_2', wwpns_list=['wwn3'],
                                         iscsi_names_list=['iqn.test.2'])
        host_3 = self._get_host_as_munch('host_id_3', 'test_host_3', wwpns_list=['wwn3'],
                                         iscsi_names_list=['iqn.test.3'])
        hosts = [host_1, host_2, host_3]
        self._prepare_mocks_for_get_host_by_identifiers(svc_response)
        with self.assertRaises(array_errors.HostNotFoundError):
            self.svc.get_host_by_host_identifiers(Initiators('Test_nqn', [], ''))
        svc_response.return_value = hosts
        with self.assertRaises(array_errors.HostNotFoundError):
            self.svc.get_host_by_host_identifiers(Initiators('Test_nqn', ['a', 'b'], '123'))

    @patch.object(SVCResponse, 'as_list', new_callable=PropertyMock)
    def test_get_host_by_identifiers_return_nvme_fc_and_iscsi(self, svc_response):
        self._prepare_mocks_for_get_host_by_identifiers(svc_response)
        hostname, connectivity_types = self.svc.get_host_by_host_identifiers(
            Initiators('nqn.test.2', ['WWN2'], 'iqn.test.2'))
        self.assertEqual('test_host_2', hostname)
        self.assertEqual(
            {config.NVME_OVER_FC_CONNECTIVITY_TYPE, config.FC_CONNECTIVITY_TYPE, config.ISCSI_CONNECTIVITY_TYPE},
            set(connectivity_types))

    def _get_host_as_munch(self, id, name, nqn_list=None, wwpns_list=None, iscsi_names_list=None, portset_id=None):
        host = Munch(id=id, name=name)
        if nqn_list:
            host.nqn = nqn_list
        if wwpns_list:
            host.WWPN = wwpns_list
        if iscsi_names_list:
            host.iscsi_name = iscsi_names_list
        if portset_id:
            host.portset_id = portset_id
        return host

    def _get_hosts_list_result(self, hosts_dict):
        return [Munch(host_dict) for host_dict in hosts_dict]

    def test_get_volume_mappings_empty_mapping_list(self):
        self.svc.client.svcinfo.lsvdiskhostmap.return_value = []
        mappings = self.svc.get_volume_mappings("volume")
        self.assertEqual(mappings, {})

    def _test_get_volume_mappings_lsvdisk_cli_failure_error(self, volume_name, error_message_id, expected_error):
        self._test_mediator_method_client_cli_failure_error(self.svc.get_volume_mappings, (volume_name,),
                                                            self.svc.client.svcinfo.lsvdisk, error_message_id,
                                                            expected_error)

    def test_get_volume_mappings_lsvdisk_cli_failure_errors(self):
        self._test_get_volume_mappings_lsvdisk_cli_failure_error("\xff", 'CMMVC6017E', array_errors.IllegalObjectID)
        self._test_get_volume_mappings_lsvdisk_cli_failure_error("!@#", 'CMMVC5741E', array_errors.IllegalObjectID)

    def test_get_volume_mappings_on_volume_not_found(self):
        self.svc.client.svcinfo.lsvdiskhostmap.side_effect = [
            svc_errors.CommandExecutionError('Failed')]

        with self.assertRaises(array_errors.ObjectNotFoundError):
            self.svc.get_volume_mappings('volume')

    def test_get_volume_mappings_success(self):
        map1 = Munch({'id': '51', 'name': 'peng', 'SCSI_id': '0',
                      'host_id': '12', 'host_name': 'Test_P'})
        map2 = Munch({'id': '52', 'name': 'peng', 'SCSI_id': '1',
                      'host_id': '18', 'host_name': 'Test_W'})
        self.svc.client.svcinfo.lsvdiskhostmap.return_value = [map1, map2]
        mappings = self.svc.get_volume_mappings("volume")
        self.assertEqual(mappings, {'Test_P': '0', 'Test_W': '1'})

    def test_get_free_lun_raises_host_not_found_error(self):
        self.svc.client.svcinfo.lshostvdiskmap.side_effect = [
            svc_errors.CommandExecutionError('Failed')]
        with self.assertRaises(array_errors.HostNotFoundError):
            self.svc._get_free_lun('host')

    def _test_get_free_lun_host_mappings(self, lun_list, expected_lun='0'):
        maps = []
        for index, lun in enumerate(lun_list):
            maps.append(Munch({'id': index, 'name': 'peng{}'.format(index), 'SCSI_id': lun,
                               'host_id': index, 'host_name': 'Test_{}'.format(index)}))
        self.svc.client.svcinfo.lshostvdiskmap.return_value = maps
        lun = self.svc._get_free_lun('host')
        if lun_list:
            self.assertNotIn(lun, lun_list)
        self.assertEqual(lun, expected_lun)

    @patch("controller.array_action.array_mediator_svc.choice")
    def test_get_free_lun_with_no_host_mappings(self, random_choice):
        random_choice.return_value = '0'
        self._test_get_free_lun_host_mappings([])

    @patch.object(SVCArrayMediator, "MAX_LUN_NUMBER", 2)
    @patch.object(SVCArrayMediator, "MIN_LUN_NUMBER", 0)
    def test_get_free_lun_success(self):
        self._test_get_free_lun_host_mappings(('1', '2'))

    @patch.object(SVCArrayMediator, "MAX_LUN_NUMBER", 4)
    @patch.object(SVCArrayMediator, "MIN_LUN_NUMBER", 0)
    @patch("controller.array_action.array_mediator_svc.LUN_INTERVAL", 1)
    def test_get_free_lun_in_interval_success(self):
        self._test_get_free_lun_host_mappings(('0', '1'), expected_lun='2')

    @patch.object(SVCArrayMediator, "MAX_LUN_NUMBER", 3)
    @patch.object(SVCArrayMediator, "MIN_LUN_NUMBER", 1)
    def test_free_lun_no_available_lun(self):
        map1 = Munch({'id': '51', 'name': 'peng', 'SCSI_id': '1',
                      'host_id': '12', 'host_name': 'Test_P'})
        map2 = Munch({'id': '56', 'name': 'peng', 'SCSI_id': '2',
                      'host_id': '16', 'host_name': 'Test_W'})
        map3 = Munch({'id': '58', 'name': 'Host', 'SCSI_id': '3',
                      'host_id': '18', 'host_name': 'Test_H'})
        self.svc.client.svcinfo.lshostvdiskmap.return_value = [map1, map2,
                                                               map3]
        with self.assertRaises(array_errors.NoAvailableLunError):
            self.svc._get_free_lun('Test_P')

    @patch("controller.array_action.array_mediator_svc.SVCArrayMediator._get_free_lun")
    def _test_map_volume_mkvdiskhostmap_error(self, client_error, expected_error, mock_get_free_lun):
        mock_get_free_lun.return_value = '1'
        self._test_mediator_method_client_error(self.svc.map_volume, ("volume", "host", "connectivity_type"),
                                                self.svc.client.svctask.mkvdiskhostmap, client_error,
                                                expected_error)

    def test_map_volume_mkvdiskhostmap_errors(self):
        self._test_map_volume_mkvdiskhostmap_error(svc_errors.CommandExecutionError('CMMVC5804E'),
                                                   array_errors.ObjectNotFoundError)
        self._test_map_volume_mkvdiskhostmap_error(svc_errors.CommandExecutionError('CMMVC5754E'),
                                                   array_errors.HostNotFoundError)
        self._test_map_volume_mkvdiskhostmap_error(svc_errors.CommandExecutionError('CMMVC5879E'),
                                                   array_errors.LunAlreadyInUseError)
        self._test_map_volume_mkvdiskhostmap_error(svc_errors.CommandExecutionError('Failed'),
                                                   array_errors.MappingError)
        self._test_map_volume_mkvdiskhostmap_error(Exception, Exception)

    @patch("controller.array_action.array_mediator_svc.SVCArrayMediator._get_free_lun")
    def test_map_volume_success(self, mock_get_free_lun):
        mock_get_free_lun.return_value = '5'
        self.svc.client.svctask.mkvdiskhostmap.return_value = None
        self.svc.client.svcinfo.lsvdisk.return_value = Mock(as_single_element=self._get_cli_volume(name='volume'))
        lun = self.svc.map_volume("volume_id", "host", "connectivity_type")
        self.assertEqual(lun, '5')
        self.svc.client.svctask.mkvdiskhostmap.assert_called_once_with(host='host', object_id='volume', force=True,
                                                                       scsi='5')

    def test_map_volume_nvme_success(self):
        self.svc.client.svctask.mkvdiskhostmap.return_value = None
        self.svc.client.svcinfo.lsvdisk.return_value = Mock(as_single_element=self._get_cli_volume(name='volume'))
        lun = self.svc.map_volume("volume", "host", config.NVME_OVER_FC_CONNECTIVITY_TYPE)
        self.assertEqual(lun, "")
        self.svc.client.svctask.mkvdiskhostmap.assert_called_once_with(host='host', object_id='volume', force=True)

    def _test_unmap_volume_rmvdiskhostmap_error(self, client_error, expected_error):
        self._test_mediator_method_client_error(self.svc.unmap_volume, ("volume", "host"),
                                                self.svc.client.svctask.rmvdiskhostmap, client_error,
                                                expected_error)

    def test_unmap_volume_rmvdiskhostmap_errors(self):
        self._test_unmap_volume_rmvdiskhostmap_error(svc_errors.CommandExecutionError('CMMVC5753E'),
                                                     array_errors.ObjectNotFoundError)
        self._test_unmap_volume_rmvdiskhostmap_error(svc_errors.CommandExecutionError('CMMVC5754E'),
                                                     array_errors.HostNotFoundError)
        self._test_unmap_volume_rmvdiskhostmap_error(svc_errors.CommandExecutionError('CMMVC5842E'),
                                                     array_errors.VolumeAlreadyUnmappedError)
        self._test_unmap_volume_rmvdiskhostmap_error(svc_errors.CommandExecutionError('Failed'),
                                                     array_errors.UnmappingError)
        self._test_unmap_volume_rmvdiskhostmap_error(Exception, Exception)

    def test_unmap_volume_success(self):
        self.svc.client.svctask.rmvdiskhostmap.return_value = None
        self.svc.unmap_volume("volume", "host")

    def _prepare_mocks_for_get_iscsi_targets(self, portset_id=None):
        host = self._get_host_as_munch('host_id', 'test_host', wwpns_list=['wwn0'],
                                       iscsi_names_list=['iqn.test.0', 'iqn.test.00'], portset_id=portset_id)
        self.svc.client.svcinfo.lshost = Mock()
        self.svc.client.svcinfo.lshost.return_value = Mock(as_single_element=host)

    def test_get_iscsi_targets_cmd_error_raise_host_not_found(self):
        self.svc.client.svcinfo.lshost.return_value = Mock(as_single_element=[])
        with self.assertRaises(array_errors.HostNotFoundError):
            self.svc.get_iscsi_targets_by_iqn('test_host')

    def test_get_iscsi_targets_cmd_error_raise_no_targets_error(self):
        self._prepare_mocks_for_get_iscsi_targets()
        self.svc.client.svcinfo.lsportip.side_effect = [
            svc_errors.CommandExecutionError('Failed')]
        with self.assertRaises(array_errors.NoIscsiTargetsFoundError):
            self.svc.get_iscsi_targets_by_iqn('test_host')

    def test_get_iscsi_targets_cli_error_raise_no_targets_error(self):
        self._prepare_mocks_for_get_iscsi_targets()
        self.svc.client.svcinfo.lsportip.side_effect = [
            CLIFailureError("Failed")]
        with self.assertRaises(array_errors.NoIscsiTargetsFoundError):
            self.svc.get_iscsi_targets_by_iqn('test_host')

    def test_get_iscsi_targets_no_online_node_raise_no_targets_error(self):
        self._prepare_mocks_for_get_iscsi_targets()
        node = Munch({'id': '1',
                      'name': 'node1',
                      'iscsi_name': 'iqn.1986-03.com.ibm:2145.v7k1.node1',
                      'status': 'offline'})
        self.svc.client.svcinfo.lsnode.return_value = [node]
        with self.assertRaises(array_errors.NoIscsiTargetsFoundError):
            self.svc.get_iscsi_targets_by_iqn('test_host')

    def test_get_iscsi_targets_no_nodes_nor_ips_raise_no_targets_error(self):
        self._prepare_mocks_for_get_iscsi_targets()
        self.svc.client.svcinfo.lsnode.return_value = []
        self.svc.client.svcinfo.lsportip.return_value = []
        with self.assertRaises(array_errors.NoIscsiTargetsFoundError):
            self.svc.get_iscsi_targets_by_iqn('test_host')

    def test_get_iscsi_targets_no_port_with_ip_raise_no_targets_error(self):
        self._prepare_mocks_for_get_iscsi_targets()
        port_1 = Munch({'node_id': '1', 'IP_address': None, 'IP_address_6': ''})
        port_2 = Munch({'node_id': '2', 'IP_address': '', 'IP_address_6': None})
        self.svc.client.svcinfo.lsportip.return_value = [port_1, port_2]
        with self.assertRaises(array_errors.NoIscsiTargetsFoundError):
            self.svc.get_iscsi_targets_by_iqn('test_host')

    def test_get_iscsi_targets_no_ip_raise_no_targets_error(self):
        self._prepare_mocks_for_get_iscsi_targets()
        self.svc.client.svcinfo.lsportip.return_value = []
        with self.assertRaises(array_errors.NoIscsiTargetsFoundError):
            self.svc.get_iscsi_targets_by_iqn('test_host')

    def test_get_iscsi_targets_with_lsportip_success(self):
        self._prepare_mocks_for_get_iscsi_targets()
        ips_by_iqn = self.svc.get_iscsi_targets_by_iqn('test_host')
        self.svc.client.svcinfo.lsportip.assert_called_once()
        self.assertEqual(ips_by_iqn, {'iqn.1986-03.com.ibm:2145.v7k1.node1': ['1.1.1.1']})

    def test_get_iscsi_targets_with_lsip_success(self):
        self._prepare_mocks_for_get_iscsi_targets(portset_id='demo_id')
        ips_by_iqn = self.svc.get_iscsi_targets_by_iqn('test_host')
        self.svc.client.svcinfo.lsip.assert_called_once_with(filtervalue='portset_id=demo_id')
        self.svc.client.svcinfo.lsportip.not_called()
        self.assertEqual(ips_by_iqn, {'iqn.1986-03.com.ibm:2145.v7k1.node1': ['1.1.1.1']})

    def test_get_iscsi_targets_with_exception(self):
        self.svc.client.svcinfo.lsnode.side_effect = [Exception]
        with self.assertRaises(Exception):
            self.svc.get_iscsi_targets_by_iqn('test_host')

    def test_get_iscsi_targets_with_multi_nodes(self):
        self._prepare_mocks_for_get_iscsi_targets()
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

        ips_by_iqn = self.svc.get_iscsi_targets_by_iqn('test_host')

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
                                               'name': 'test_volume',
                                               'capacity': '512',
                                               'mdisk_grp_name': 'pool_name'
                                               }))
        self.svc.client.svcinfo.lsvdisk.return_value = volume
        self.svc.client.svcinfo.lsfcmap.return_value = Mock(as_list=[])

    def test_expand_volume_success(self):
        self._prepare_mocks_for_expand_volume()
        self.svc.expand_volume('vol_id', 1024)
        self.svc.client.svctask.expandvdisksize.assert_called_once_with(vdisk_id='test_volume', unit='b', size=512)

    def test_expand_volume_success_with_size_rounded_up(self):
        self._prepare_mocks_for_expand_volume()
        self.svc.expand_volume('vol_id', 513)
        self.svc.client.svctask.expandvdisksize.assert_called_once_with(vdisk_id='test_volume', unit='b', size=512)

    def test_expand_volume_raise_object_in_use(self):
        self._prepare_mocks_for_expand_volume()
        fcmaps = self.fcmaps_as_source
        fcmaps[0].status = 'not good'
        self.svc.client.svcinfo.lsfcmap.side_effect = [Mock(as_list=self.fcmaps), Mock(as_list=fcmaps)]
        with self.assertRaises(array_errors.ObjectIsStillInUseError):
            self.svc.expand_volume('vol_id', 2)
        self.svc.client.svctask.expandvdisksize.assert_not_called()

    def test_expand_volume_in_hyperswap(self):
        self._prepare_mocks_for_expand_volume()
        self._prepare_fcmaps_for_hyperswap()
        self.svc.expand_volume('vol_id', 1024)

        self.svc.client.svctask.expandvolume.assert_called_once_with(object_id='test_volume', unit='b', size=512)
        self.svc.client.svctask.rmfcmap.assert_not_called()

    def test_expand_volume_raise_object_not_found(self):
        self.svc.client.svcinfo.lsvdisk.return_value = Mock(as_single_element=None)
        with self.assertRaises(array_errors.ObjectNotFoundError):
            self.svc.expand_volume('vol_id', 2)
        self.svc.client.svctask.expandvdisksize.assert_not_called()

    def _test_expand_volume_expandvdisksize_errors(self, client_error, expected_error):
        self._prepare_mocks_for_expand_volume()
        self._test_mediator_method_client_error(self.svc.expand_volume, ("vol_id", 2),
                                                self.svc.client.svctask.expandvdisksize, client_error, expected_error)

    def test_expand_volume_expandvdisksize_errors(self):
        self._test_expand_volume_expandvdisksize_errors(CLIFailureError("CMMVC5753E"), array_errors.ObjectNotFoundError)
        self._test_expand_volume_expandvdisksize_errors(CLIFailureError("CMMVC8957E"), array_errors.ObjectNotFoundError)
        self._test_expand_volume_expandvdisksize_errors(CLIFailureError("CMMVC5860E"),
                                                        array_errors.NotEnoughSpaceInPool)
        self._test_expand_volume_expandvdisksize_errors(CLIFailureError("Failed"), CLIFailureError)
        self._test_expand_volume_expandvdisksize_errors(Exception("Failed"), Exception)

    def _expand_volume_lsvdisk_errors(self, client_error, expected_error, volume_id="vol_id"):
        self._test_mediator_method_client_error(self.svc.expand_volume, (volume_id, 2),
                                                self.svc.client.svcinfo.lsvdisk, client_error, expected_error)

    def test_expand_volume_lsvdisk_errors(self):
        self._expand_volume_lsvdisk_errors(CLIFailureError("CMMVC6017E"), array_errors.IllegalObjectID, "\xff")
        self._expand_volume_lsvdisk_errors(CLIFailureError("CMMVC5741E"), array_errors.IllegalObjectID, "!@#")
        self._expand_volume_lsvdisk_errors(CLIFailureError("Failed"), CLIFailureError)
        self._expand_volume_lsvdisk_errors(Exception("Failed"), Exception)
