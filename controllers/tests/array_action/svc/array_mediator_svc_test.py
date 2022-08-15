import unittest
from unittest.mock import MagicMock

from mock import patch, Mock, call, PropertyMock
from munch import Munch
from pysvc import errors as svc_errors
from pysvc.unified.response import CLIFailureError, SVCResponse

import controllers.array_action.errors as array_errors
from controllers.array_action.array_mediator_svc import SVCArrayMediator, build_kwargs_from_parameters, \
    FCMAP_STATUS_DONE, YES
from controllers.common.node_info import Initiators
from controllers.common.settings import ARRAY_TYPE_SVC, SPACE_EFFICIENCY_THIN, SPACE_EFFICIENCY_COMPRESSED, \
    SPACE_EFFICIENCY_DEDUPLICATED_COMPRESSED, SPACE_EFFICIENCY_DEDUPLICATED_THIN, SPACE_EFFICIENCY_DEDUPLICATED, \
    SPACE_EFFICIENCY_THICK
import controllers.tests.array_action.svc.test_settings as svc_settings
from controllers.tests.array_action.test_settings import NVME_OVER_FC_CONNECTIVITY_TYPE, FC_CONNECTIVITY_TYPE, \
    ISCSI_CONNECTIVITY_TYPE, DUMMY_CONNECTIVITY_TYPE, DUMMY_ERROR_MESSAGE, DUMMY_HOST_NAME1, DUMMY_HOST_NAME2, \
    DUMMY_HOST_NAME3, DUMMY_HOST_ID1, DUMMY_HOST_ID2, DUMMY_HOST_ID3, DUMMY_NODE1_NAME, DUMMY_NODE1_IQN, \
    DUMMY_NODE2_NAME, DUMMY_NODE2_IQN, DUMMY_NODE3_IQN, DUMMY_NODE4_IQN, DUMMY_FC_WWN1, DUMMY_FC_WWN2, DUMMY_FC_WWN3, \
    DUMMY_FC_WWN4, DUMMY_NVME_NQN1, DUMMY_NVME_NQN2, DUMMY_NVME_NQN3, DUMMY_NVME_NQN4, VOLUME_ID_ATTR_KEY, \
    VOLUME_NAME_ATTR_KEY
from controllers.tests.common.test_settings import USER, PASSWORD, VOLUME_NAME, DUMMY_POOL1, VOLUME_UID, HOST_NAME, \
    SNAPSHOT_NAME, MANAGEMENT_ADDRESS, SOURCE_VOLUME_NAME, SOURCE_VOLUME_ID, INTERNAL_SNAPSHOT_ID, INTERNAL_VOLUME_ID, \
    TARGET_VOLUME_NAME, VOLUME_OBJECT_TYPE, SNAPSHOT_VOLUME_UID, SNAPSHOT_OBJECT_TYPE, STRETCHED_POOL, \
    DUMMY_POOL2, DUMMY_IO_GROUP, DUMMY_VOLUME_GROUP

EMPTY_BYTES = b""


class TestArrayMediatorSVC(unittest.TestCase):

    def setUp(self):
        self.endpoint = [MANAGEMENT_ADDRESS]
        with patch("controllers.array_action.array_mediator_svc.SVCArrayMediator._connect"):
            self.svc = SVCArrayMediator(USER, PASSWORD, self.endpoint)
        self.svc.client = Mock()
        self.svc.client.svcinfo.lssystem.return_value = [
            Munch({svc_settings.LSSYSTEM_LOCATION_ATTR_KEY: svc_settings.LOCAL_LOCATION,
                   svc_settings.LSSYSTEM_ID_ALIAS_ATTR_KEY: svc_settings.DUMMY_ID_ALIAS})]
        node = self._mock_node()
        self.svc.client.svcinfo.lsnode.return_value = [node]
        lsportip_port = Munch(
            {svc_settings.LSPORTIP_NODE_ID_ATTR_KEY: svc_settings.DUMMY_INTERNAL_ID1,
             svc_settings.LSPORTIP_IP_ADDRESS_ATTR_KEY: svc_settings.DUMMY_IP_ADDRESS1,
             svc_settings.LSPORTIP_IP_ADDRESS_6_ATTR_KEY: None})
        lsip_port = Munch({svc_settings.LSIP_NODE_ID_ATTR_KEY: svc_settings.DUMMY_INTERNAL_ID1,
                           svc_settings.LSIP_IP_ADDRESS_ATTR_KEY: svc_settings.DUMMY_IP_ADDRESS1,
                           svc_settings.LSIP_PORTSET_ID_ATTR_KEY: svc_settings.DUMMY_PORTSET_ID})
        self.svc.client.svcinfo.lsportip.return_value = [lsportip_port]
        self.svc.client.svcinfo.lsip.return_value = [lsip_port]
        self.fcmaps = [self._mock_fcmap(SOURCE_VOLUME_NAME, svc_settings.DUMMY_FCMAP_ID)]
        self.fcmaps_as_target = [self._mock_fcmap(SOURCE_VOLUME_NAME, svc_settings.DUMMY_FCMAP_ID)]
        self.fcmaps_as_source = [self._mock_fcmap(SNAPSHOT_NAME, svc_settings.DUMMY_FCMAP_ID)]
        self.svc.client.svcinfo.lsfcmap.return_value = Mock(as_list=self.fcmaps)
        del self.svc.client.svctask.addsnapshot

    def _mock_node(self, node_id=svc_settings.DUMMY_INTERNAL_ID1, name=DUMMY_NODE1_NAME,
                   iqn=DUMMY_NODE1_IQN, status=svc_settings.ONLINE_STATUS):
        return Munch({svc_settings.NODE_ID_ATTR_KEY: node_id,
                      svc_settings.NODE_NAME_ATTR_KEY: name,
                      svc_settings.NODE_ISCSI_NAME_ATTR_KEY: iqn,
                      svc_settings.NODE_STATUS_ATTR_KEY: status})

    def _mock_fcmap(self, source_name, id_value):
        return Munch({svc_settings.FCMAP_SOURCE_VDISK_NAME_ATTR_KEY: source_name,
                      svc_settings.FCMAP_TARGET_VDISK_NAME_ATTR_KEY: TARGET_VOLUME_NAME,
                      svc_settings.FCMAP_ID_ATTR_KEY: id_value,
                      svc_settings.FCMAP_STATUS_ATTR_KEY: FCMAP_STATUS_DONE,
                      svc_settings.FCMAP_COPY_RATE_ATTR_KEY: svc_settings.DUMMY_COPY_RATE,
                      svc_settings.FCMAP_RC_CONTROLLED_ATTR_KEY: svc_settings.NO_VALUE_ALIAS})

    @patch("controllers.array_action.array_mediator_svc.connect")
    def test_init_unsupported_system_version(self, connect_mock):
        code_level_below_min_supported = "7.7.77.77 (build 777.77.7777777777777)"
        svc_mock = Mock()
        system = Munch({svc_settings.LSSYSTEM_LOCATION_ATTR_KEY: svc_settings.LOCAL_LOCATION,
                        svc_settings.LSSYSTEM_CODE_LEVEL_ALIAS_ATTR_KEY: code_level_below_min_supported})
        svc_mock.svcinfo.lssystem.return_value = [system]
        connect_mock.return_value = svc_mock
        with self.assertRaises(array_errors.UnsupportedStorageVersionError):
            SVCArrayMediator(USER, PASSWORD, self.endpoint)

    def test_raise_management_ips_not_support_error_in_init(self):
        self.endpoint = ["IP_1", "IP_2"]
        with self.assertRaises(
                array_errors.StorageManagementIPsNotSupportError):
            SVCArrayMediator(USER, PASSWORD, self.endpoint)

        self.endpoint = []
        with self.assertRaises(
                array_errors.StorageManagementIPsNotSupportError):
            SVCArrayMediator(USER, PASSWORD, self.endpoint)

    @patch("controllers.array_action.array_mediator_svc.connect")
    def test_connect_errors(self, connect_mock):
        connect_mock.side_effect = [
            svc_errors.IncorrectCredentials("Failed_a")]
        with self.assertRaises(array_errors.CredentialsError):
            self.svc._connect()

    def test_close(self):
        self.svc.disconnect()
        self.svc.client.close.assert_called_with()

    def test_default_object_prefix_length_not_larger_than_max(self):
        prefix_length = len(self.svc.default_object_prefix)
        self.assertGreaterEqual(self.svc.max_object_prefix_length, prefix_length)
        self.assertGreaterEqual(self.svc.max_object_prefix_length, prefix_length)

    @patch("controllers.array_action.array_mediator_svc.is_warning_message")
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
        self._test_mediator_method_client_cli_failure_error(self.svc.get_volume, (volume_name, DUMMY_POOL1, False),
                                                            self.svc.client.svcinfo.lsvdisk, error_message_id,
                                                            expected_error)

    def test_get_volume_lsvdisk_cli_failure_errors(self):
        self._test_get_volume_lsvdisk_cli_failure_error(VOLUME_NAME, "CMMVC5753E", array_errors.ObjectNotFoundError)
        self._test_get_volume_lsvdisk_cli_failure_error("\xff", "CMMVC6017E", array_errors.InvalidArgumentError)
        self._test_get_volume_lsvdisk_cli_failure_error("12345", "CMMVC5703E", array_errors.InvalidArgumentError)
        self._test_get_volume_lsvdisk_cli_failure_error("", "other error", CLIFailureError)

    def _test_get_volume(self, get_cli_volume_args=None, is_virt_snap_func=False, lsvdisk_call_count=2):
        if get_cli_volume_args is None:
            get_cli_volume_args = {}
        cli_volume_mock = Mock(as_single_element=self._get_cli_volume(**get_cli_volume_args))
        self.svc.client.svcinfo.lsvdisk.return_value = cli_volume_mock
        volume = self.svc.get_volume(VOLUME_NAME, pool=DUMMY_POOL1, is_virt_snap_func=is_virt_snap_func)
        self.assertEqual(svc_settings.DUMMY_CAPACITY_INT, volume.capacity_bytes)
        self.assertEqual(DUMMY_POOL1, volume.pool)
        self.assertEqual(ARRAY_TYPE_SVC, volume.array_type)
        self.assertEqual(lsvdisk_call_count, self.svc.client.svcinfo.lsvdisk.call_count)
        return volume

    def test_get_volume_success(self):
        self._test_get_volume()

    def test_get_volume_with_source_success(self):
        volume = self._test_get_volume({"vdisk_uid": SOURCE_VOLUME_ID, "fc_id": svc_settings.DUMMY_INTERNAL_ID1})
        self.assertEqual(SOURCE_VOLUME_ID, volume.source_id)

    def test_get_volume_with_source_and_flashcopy_enabled(self):
        volume = self._test_get_volume({"vdisk_uid": SOURCE_VOLUME_ID, "fc_id": svc_settings.DUMMY_INTERNAL_ID1},
                                       is_virt_snap_func=True,
                                       lsvdisk_call_count=1)
        self.assertIsNone(volume.source_id)

    def test_get_volume_hyperswap_has_no_source(self):
        target_cli_volume = self._get_mapped_target_cli_volume()
        self.svc.client.svcinfo.lsvdisk.return_value = self._mock_cli_object(target_cli_volume)
        self._prepare_fcmaps_for_hyperswap()

        volume = self.svc.get_volume(VOLUME_NAME, pool=DUMMY_POOL1, is_virt_snap_func=False)

        self.assertIsNone(volume.source_id)

    def _prepare_stretched_volume_mock(self):
        cli_volume = self._get_cli_volume(pool_name=[svc_settings.POOL_MANY, DUMMY_POOL1, DUMMY_POOL2])
        self.svc.client.svcinfo.lsvdisk.return_value = Mock(as_single_element=cli_volume)

    def test_get_volume_stretched_return_correct_pools(self):
        self._prepare_stretched_volume_mock()

        volume = self.svc.get_volume(VOLUME_NAME, pool=DUMMY_POOL1, is_virt_snap_func=False)

        self.assertEqual(STRETCHED_POOL, volume.pool)

    def test_get_volume_raise_exception(self):
        self._test_mediator_method_client_error(self.svc.get_volume, (VOLUME_NAME,),
                                                self.svc.client.svcinfo.lsvdisk, Exception, Exception)

    def test_get_volume_returns_nothing(self):
        vol_ret = Mock(as_single_element=Munch({}))
        self.svc.client.svcinfo.lsvdisk.return_value = vol_ret
        with self.assertRaises(array_errors.ObjectNotFoundError):
            self.svc.get_volume(VOLUME_NAME, pool=DUMMY_POOL1, is_virt_snap_func=False)

    def _test_create_volume_mkvolume_cli_failure_error(self, error_message_id, expected_error, volume_name=VOLUME_NAME):
        self._test_mediator_method_client_cli_failure_error(self.svc.create_volume,
                                                            (volume_name, svc_settings.DUMMY_CAPACITY_INT,
                                                             SPACE_EFFICIENCY_THIN,
                                                             DUMMY_POOL1, None, None, None, None, False),
                                                            self.svc.client.svctask.mkvolume, error_message_id,
                                                            expected_error)

    def test_create_volume_raise_exceptions(self):
        self._test_mediator_method_client_error(self.svc.create_volume,
                                                (VOLUME_NAME, svc_settings.DUMMY_CAPACITY_INT, SPACE_EFFICIENCY_THIN,
                                                 DUMMY_POOL1,
                                                 None, None, None,
                                                 None, False),
                                                self.svc.client.svctask.mkvolume, Exception, Exception)
        self._test_create_volume_mkvolume_cli_failure_error(DUMMY_ERROR_MESSAGE, CLIFailureError)
        self._test_create_volume_mkvolume_cli_failure_error("CMMVC8710E", array_errors.NotEnoughSpaceInPool)
        self._test_create_volume_mkvolume_cli_failure_error("CMMVC6017E", array_errors.InvalidArgumentError, "\xff")
        self._test_create_volume_mkvolume_cli_failure_error("CMMVC6527E", array_errors.InvalidArgumentError, "1_volume")
        self._test_create_volume_mkvolume_cli_failure_error("CMMVC5738E", array_errors.InvalidArgumentError, "a" * 64)
        self._test_create_volume_mkvolume_cli_failure_error("CMMVC6035E", array_errors.VolumeAlreadyExists)
        self._test_create_volume_mkvolume_cli_failure_error("CMMVC5754E", array_errors.InvalidArgumentError)
        self._test_create_volume_mkvolume_cli_failure_error("CMMVC9292E", array_errors.PoolDoesNotMatchSpaceEfficiency)
        self._test_create_volume_mkvolume_cli_failure_error("CMMVC9301E", array_errors.PoolDoesNotMatchSpaceEfficiency)

    def _test_create_volume_success(self, space_efficiency=None, source_id=None, source_type=None, volume_group=None,
                                    is_virt_snap_func=False):
        self.svc.client.svctask.mkvolume.return_value = Mock()
        vol_ret = Mock(as_single_element=self._get_cli_volume())
        self.svc.client.svcinfo.lsvdisk.return_value = vol_ret
        volume = self.svc.create_volume(VOLUME_NAME, svc_settings.DUMMY_CAPACITY_INT, space_efficiency, DUMMY_POOL1,
                                        None,
                                        volume_group,
                                        self._mock_source_ids(source_id), source_type,
                                        is_virt_snap_func=is_virt_snap_func)

        self.assertEqual(svc_settings.DUMMY_CAPACITY_INT, volume.capacity_bytes)
        self.assertEqual(ARRAY_TYPE_SVC, volume.array_type)
        self.assertEqual(VOLUME_UID, volume.id)
        self.assertEqual(INTERNAL_VOLUME_ID, volume.internal_id)

    def test_create_volume_with_thin_space_efficiency_success(self):
        self._test_create_volume_success(SPACE_EFFICIENCY_THIN)
        self.svc.client.svctask.mkvolume.assert_called_with(name=VOLUME_NAME, unit=svc_settings.BYTE_UNIT_SYMBOL,
                                                            size=svc_settings.DUMMY_CAPACITY_INT,
                                                            pool=DUMMY_POOL1,
                                                            thin=True)

    def test_create_volume_with_compressed_space_efficiency_success(self):
        self._test_create_volume_success(SPACE_EFFICIENCY_COMPRESSED)
        self.svc.client.svctask.mkvolume.assert_called_with(name=VOLUME_NAME, unit=svc_settings.BYTE_UNIT_SYMBOL,
                                                            size=svc_settings.DUMMY_CAPACITY_INT,
                                                            pool=DUMMY_POOL1,
                                                            compressed=True)

    def test_create_volume_with_deduplicated_thin_space_efficiency_success(self):
        self._test_create_volume_success(SPACE_EFFICIENCY_DEDUPLICATED_THIN)
        self.svc.client.svctask.mkvolume.assert_called_with(name=VOLUME_NAME, unit=svc_settings.BYTE_UNIT_SYMBOL,
                                                            size=svc_settings.DUMMY_CAPACITY_INT,
                                                            pool=DUMMY_POOL1,
                                                            thin=True, deduplicated=True)

    def test_create_volume_with_deduplicated_compressed_space_efficiency_success(self):
        self._test_create_volume_success(SPACE_EFFICIENCY_DEDUPLICATED_COMPRESSED)
        self.svc.client.svctask.mkvolume.assert_called_with(name=VOLUME_NAME, unit=svc_settings.BYTE_UNIT_SYMBOL,
                                                            size=svc_settings.DUMMY_CAPACITY_INT,
                                                            pool=DUMMY_POOL1,
                                                            compressed=True, deduplicated=True)

    def test_create_volume_with_deduplicated_backward_compatibility_space_efficiency_success(self):
        self._test_create_volume_success(SPACE_EFFICIENCY_DEDUPLICATED)
        self.svc.client.svctask.mkvolume.assert_called_with(name=VOLUME_NAME, unit=svc_settings.BYTE_UNIT_SYMBOL,
                                                            size=svc_settings.DUMMY_CAPACITY_INT,
                                                            pool=DUMMY_POOL1,
                                                            compressed=True, deduplicated=True)

    def _test_create_volume_with_default_space_efficiency_success(self, space_efficiency):
        self._test_create_volume_success(space_efficiency)
        self.svc.client.svctask.mkvolume.assert_called_with(name=VOLUME_NAME, unit=svc_settings.BYTE_UNIT_SYMBOL,
                                                            size=svc_settings.DUMMY_CAPACITY_INT,
                                                            pool=DUMMY_POOL1)

    def _prepare_mocks_for_create_volume_mkvolumegroup(self):
        self.svc.client.svctask.addsnapshot = Mock()
        self.svc.client.svctask.mkvolumegroup = Mock()
        self.svc.client.svctask.mkvolumegroup.return_value = Mock(response=(b"id [0]\n", b""))
        vol_ret = Mock(as_single_element=self._get_cli_volume())
        self.svc.client.svcinfo.lsvdisk.return_value = vol_ret

    def _mock_source_ids(self, internal_id=""):
        if internal_id:
            source_ids = MagicMock(spec=[svc_settings.SOURCE_IDS_UID, svc_settings.SOURCE_IDS_INTERNAL_ID])
            source_ids.internal_id = internal_id
            return source_ids
        return None

    def test_create_volume_mkvolume_with_flashcopy_enable_no_source(self):
        self._test_create_volume_success(is_virt_snap_func=True)
        self.svc.client.svctask.mkvolume.assert_called_with(name=VOLUME_NAME, unit=svc_settings.BYTE_UNIT_SYMBOL,
                                                            size=svc_settings.DUMMY_CAPACITY_INT,
                                                            pool=DUMMY_POOL1)

    def _test_create_volume_mkvolumegroup_success(self, source_type):
        self._prepare_mocks_for_create_volume_mkvolumegroup()
        if source_type == VOLUME_OBJECT_TYPE:
            self._prepare_mocks_for_create_snapshot_addsnapshot(snapshot_id=INTERNAL_SNAPSHOT_ID)
        self._test_create_volume_success(source_id=INTERNAL_SNAPSHOT_ID, source_type=source_type,
                                         is_virt_snap_func=True)

        self.svc.client.svctask.mkvolumegroup.assert_called_with(type=svc_settings.MKVOLUMEGROUP_CLONE_TYPE,
                                                                 fromsnapshotid=INTERNAL_SNAPSHOT_ID,
                                                                 pool=DUMMY_POOL1, name=VOLUME_NAME)
        remove_from_volumegroup_call = call(vdisk_id=INTERNAL_VOLUME_ID, novolumegroup=True)
        rename_call = call(vdisk_id=INTERNAL_VOLUME_ID, name=VOLUME_NAME)
        self.svc.client.svctask.chvdisk.assert_has_calls([remove_from_volumegroup_call, rename_call])
        self.svc.client.svctask.rmvolumegroup.assert_called_with(object_id=VOLUME_NAME)

    def test_create_volume_mkvolumegroup_from_snapshot_success(self):
        self._test_create_volume_mkvolumegroup_success(source_type=SNAPSHOT_OBJECT_TYPE)

    def test_create_volume_mkvolumegroup_from_volume_success(self):
        self._test_create_volume_mkvolumegroup_success(source_type=VOLUME_OBJECT_TYPE)

    @patch("controllers.array_action.array_mediator_svc.is_warning_message")
    def test_create_volume_mkvolumegroup_with_rollback(self, mock_warning):
        mock_warning.return_value = False
        self._prepare_mocks_for_create_volume_mkvolumegroup()
        self.svc.client.svctask.chvdisk.side_effect = ["", CLIFailureError("CMMVC6035E")]
        with self.assertRaises(array_errors.VolumeAlreadyExists):
            self.svc.create_volume(VOLUME_NAME, svc_settings.DUMMY_CAPACITY_INT, svc_settings.DUMMY_SPACE_EFFICIENCY,
                                   DUMMY_POOL1, None, None,
                                   self._mock_source_ids(INTERNAL_SNAPSHOT_ID), SNAPSHOT_OBJECT_TYPE,
                                   is_virt_snap_func=True)
        self.svc.client.svctask.rmvolume.assert_called_with(vdisk_id=INTERNAL_VOLUME_ID)
        self.svc.client.svctask.rmvolumegroup.assert_called_with(object_id=VOLUME_NAME)

    def test_create_volume_with_empty_string_space_efficiency_success(self):
        self._test_create_volume_with_default_space_efficiency_success("")

    def test_create_volume_with_thick_space_efficiency_success(self):
        self._test_create_volume_with_default_space_efficiency_success(
            SPACE_EFFICIENCY_THICK)

    def _test_delete_volume_rmvolume_cli_failure_error(self, error_message_id, expected_error, volume_name=VOLUME_NAME):
        self._test_mediator_method_client_cli_failure_error(self.svc.delete_volume, (volume_name,),
                                                            self.svc.client.svctask.rmvolume, error_message_id,
                                                            expected_error)

    def test_delete_volume_return_volume_delete_errors(self):
        self._prepare_mocks_for_delete_volume()
        self._test_delete_volume_rmvolume_cli_failure_error("CMMVC5753E", array_errors.ObjectNotFoundError)
        self._test_delete_volume_rmvolume_cli_failure_error("CMMVC8957E", array_errors.ObjectNotFoundError)
        self._test_delete_volume_rmvolume_cli_failure_error(DUMMY_ERROR_MESSAGE, CLIFailureError)

    def test_delete_volume_has_snapshot_fcmaps_not_removed(self):
        self._prepare_mocks_for_delete_volume()
        fcmaps_as_target = Mock(as_list=[])
        fcmaps = self.fcmaps
        fcmaps[0].copy_rate = svc_settings.DUMMY_ZERO_COPY_RATE
        fcmaps_as_source = Mock(as_list=fcmaps)
        self.svc.client.svcinfo.lsfcmap.side_effect = [fcmaps_as_target, fcmaps_as_source]
        with self.assertRaises(array_errors.ObjectIsStillInUseError):
            self.svc.delete_volume(VOLUME_NAME)

    def test_delete_volume_still_copy_fcmaps_not_removed(self):
        self._prepare_mocks_for_delete_volume()
        fcmaps_as_target = Mock(as_list=[])
        fcmaps = self.fcmaps
        fcmaps[0].status = svc_settings.DUMMY_FCMAP_BAD_STATUS
        fcmaps_as_source = Mock(as_list=fcmaps)
        self.svc.client.svcinfo.lsfcmap.side_effect = [fcmaps_as_target, fcmaps_as_source]
        with self.assertRaises(array_errors.ObjectIsStillInUseError):
            self.svc.delete_volume(VOLUME_NAME)

    def _prepare_fcmaps_for_hyperswap(self):
        self.fcmaps_as_target[0].rc_controlled = svc_settings.YES_VALUE_ALIAS
        fcmaps_as_target = Mock(as_list=self.fcmaps_as_target)
        self.fcmaps[0].rc_controlled = svc_settings.YES_VALUE_ALIAS
        fcmaps_as_source = Mock(as_list=self.fcmaps)
        self.svc.client.svcinfo.lsfcmap.side_effect = [fcmaps_as_target, fcmaps_as_source]

    def test_delete_volume_does_not_remove_hyperswap_fcmap(self):
        self._prepare_mocks_for_delete_volume()
        self._prepare_fcmaps_for_hyperswap()
        self.svc.delete_volume(VOLUME_NAME)

        self.svc.client.svctask.rmfcmap.assert_not_called()

    def test_delete_volume_has_clone_fcmaps_removed(self):
        self._prepare_mocks_for_delete_volume()
        fcmaps_as_target = Mock(as_list=[])
        fcmaps_as_source = Mock(as_list=self.fcmaps_as_source)
        self.svc.client.svcinfo.lsfcmap.side_effect = [fcmaps_as_target, fcmaps_as_source]
        self.svc.delete_volume(VOLUME_NAME)
        self.svc.client.svctask.rmfcmap.assert_called_once()

    @patch("controllers.array_action.array_mediator_svc.is_warning_message")
    def test_delete_volume_has_clone_rmfcmap_raise_error(self, mock_warning):
        self._prepare_mocks_for_delete_volume()
        mock_warning.return_value = False
        fcmaps_as_target = Mock(as_list=[])
        fcmaps_as_source = Mock(as_list=self.fcmaps_as_source)
        self.svc.client.svcinfo.lsfcmap.side_effect = [fcmaps_as_target, fcmaps_as_source]
        self.svc.client.svctask.rmfcmap.side_effect = [CLIFailureError(DUMMY_ERROR_MESSAGE)]
        with self.assertRaises(CLIFailureError):
            self.svc.delete_volume(VOLUME_NAME)

    def _prepare_mocks_for_delete_volume(self):
        cli_volume = self._get_cli_volume()
        cli_volume.FC_id = svc_settings.VOLUME_FC_ID_MANY

        self.svc.client.svcinfo.lsvdisk.return_value = self._mock_cli_object(cli_volume)

    def test_delete_volume_success(self):
        self._prepare_mocks_for_delete_volume()
        self.svc.client.svctask.rmvolume = Mock()
        self.svc.delete_volume(VOLUME_NAME)

    def test_copy_to_existing_volume_from_source_success(self):
        self.svc.copy_to_existing_volume(VOLUME_UID, SOURCE_VOLUME_ID, svc_settings.DUMMY_CAPACITY_INT,
                                         svc_settings.DUMMY_SMALL_CAPACITY_INT)
        self.svc.client.svctask.mkfcmap.assert_called_once()
        self.svc.client.svctask.startfcmap.assert_called_once()

    @patch("controllers.array_action.array_mediator_svc.is_warning_message")
    def _test_copy_to_existing_volume_raise_errors(self, mock_warning, client_return_value, expected_error):
        mock_warning.return_value = False
        self.svc.client.svcinfo.lsvdisk.side_effect = [client_return_value, client_return_value]
        with self.assertRaises(expected_error):
            self.svc.copy_to_existing_volume(VOLUME_UID, SOURCE_VOLUME_ID, svc_settings.DUMMY_CAPACITY_INT,
                                             svc_settings.DUMMY_SMALL_CAPACITY_INT)

    def test_copy_to_existing_volume_raise_not_found(self):
        self._test_copy_to_existing_volume_raise_errors(client_return_value=Mock(as_single_element=None),
                                                        expected_error=array_errors.ObjectNotFoundError)

    def test_copy_to_existing_volume_raise_illegal_object_id(self):
        self._test_copy_to_existing_volume_raise_errors(client_return_value=CLIFailureError("CMMVC6017E"),
                                                        expected_error=array_errors.InvalidArgumentError)
        self._test_copy_to_existing_volume_raise_errors(client_return_value=CLIFailureError("CMMVC5741E"),
                                                        expected_error=array_errors.InvalidArgumentError)

    @staticmethod
    def _mock_cli_object(cli_object):
        return Mock(as_single_element=cli_object)

    @classmethod
    def _mock_cli_objects(cls, cli_objects):
        return map(cls._mock_cli_object, cli_objects)

    @staticmethod
    def _get_cli_volume(with_deduplicated_copy=True, name=SOURCE_VOLUME_NAME, pool_name=DUMMY_POOL1,
                        vdisk_uid=VOLUME_UID,
                        fc_id="", capacity=svc_settings.DUMMY_CAPACITY_STR, thick=False):

        deduplicated_copy = svc_settings.NO_VALUE_ALIAS
        compressed_copy = svc_settings.NO_VALUE_ALIAS
        se_copy = svc_settings.NO_VALUE_ALIAS
        if with_deduplicated_copy:
            deduplicated_copy = YES
            compressed_copy = YES
        elif not thick:
            se_copy = YES
        return Munch({svc_settings.VOLUME_VDISK_UID_ATTR_KEY: vdisk_uid,
                      VOLUME_ID_ATTR_KEY: INTERNAL_VOLUME_ID,
                      VOLUME_NAME_ATTR_KEY: name,
                      svc_settings.VOLUME_CAPACITY_ATTR_KEY: capacity,
                      svc_settings.VOLUME_MDISK_GRP_NAME_ATTR_KEY: pool_name,
                      svc_settings.VOLUME_IO_GROUP_NAME_ATTR_KEY: DUMMY_IO_GROUP,
                      svc_settings.VOLUME_FC_ID_ATTR_KEY: fc_id,
                      svc_settings.VOLUME_SE_COPY_ATTR_KEY: se_copy,
                      svc_settings.VOLUME_DEDUPLICATED_COPY_ATTR_KEY: deduplicated_copy,
                      svc_settings.VOLUME_COMPRESSED_COPY_ATTR_KEY: compressed_copy
                      })

    @staticmethod
    def _get_cli_snapshot(snapshot_id=INTERNAL_SNAPSHOT_ID):
        return Munch({svc_settings.SNAPSHOT_ID_ATTR_KEY: snapshot_id,
                      svc_settings.SNAPSHOT_NAME_ATTR_KEY: SNAPSHOT_NAME,
                      svc_settings.SNAPSHOT_VOLUME_ID_ATTR_KEY: INTERNAL_VOLUME_ID,
                      svc_settings.SNAPSHOT_VOLUME_NAME_ATTR_KEY: VOLUME_NAME,
                      })

    @classmethod
    def _get_mapless_target_cli_volume(cls):
        target_cli_volume = cls._get_cli_volume()
        target_cli_volume.vdisk_UID = SNAPSHOT_VOLUME_UID
        target_cli_volume.name = SNAPSHOT_NAME
        return target_cli_volume

    @classmethod
    def _get_mapped_target_cli_volume(cls):
        target_cli_volume = cls._get_mapless_target_cli_volume()
        target_cli_volume.FC_id = svc_settings.DUMMY_FCMAP_ID
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
        target_cli_volume.FC_id = svc_settings.VOLUME_FC_ID_MANY
        self.svc.client.svcinfo.lsvdisk.return_value = self._mock_cli_object(target_cli_volume)

    def _prepare_mocks_for_get_snapshot(self):
        self._prepare_mocks_for_delete_snapshot()
        self.fcmaps[0].copy_rate = svc_settings.DUMMY_ZERO_COPY_RATE

    @patch("controllers.array_action.array_mediator_svc.is_warning_message")
    def test_get_snapshot_not_exist_return_none(self, mock_warning):
        self._prepare_lsvdisk_to_raise_not_found_error(mock_warning)

        snapshot = self.svc.get_snapshot(VOLUME_UID, SNAPSHOT_NAME, pool=DUMMY_POOL1, is_virt_snap_func=False)

        self.assertIsNone(snapshot)

    def _test_get_snapshot_cli_failure_error(self, snapshot_name, client_method, error_message_id, expected_error,
                                             is_virt_snap_func=False):
        volume_id = VOLUME_UID
        self._test_mediator_method_client_cli_failure_error(self.svc.get_snapshot,
                                                            (volume_id, snapshot_name, DUMMY_POOL1, is_virt_snap_func),
                                                            client_method, error_message_id, expected_error)

    def _test_get_snapshot_illegal_name_cli_failure_errors(self, client_method, is_virt_snap_func=False):
        self._test_get_snapshot_cli_failure_error("\xff", client_method, "CMMVC6017E",
                                                  array_errors.InvalidArgumentError, is_virt_snap_func)
        self._test_get_snapshot_cli_failure_error("12345", client_method, "CMMVC5703E",
                                                  array_errors.InvalidArgumentError, is_virt_snap_func)

    def test_get_snapshot_lsvdisk_cli_failure_errors(self):
        client_method = self.svc.client.svcinfo.lsvdisk
        self._test_get_snapshot_illegal_name_cli_failure_errors(client_method)
        self.svc.client.svcinfo.lsvdisk.assert_called()

    def test_get_snapshot_has_no_fc_id_raise_error(self):
        self._prepare_lsvdisk_to_return_mapless_target_volume()

        with self.assertRaises(array_errors.ExpectedSnapshotButFoundVolumeError):
            self.svc.get_snapshot(VOLUME_UID, SNAPSHOT_NAME, pool=DUMMY_POOL1, is_virt_snap_func=False)

    @patch("controllers.array_action.array_mediator_svc.is_warning_message")
    def test_get_snapshot_get_fcmap_not_exist_raise_error(self, mock_warning):
        target_cli_volume = self._get_mapped_target_cli_volume()
        self.svc.client.svcinfo.lsvdisk.return_value = self._mock_cli_object(target_cli_volume)
        mock_warning.return_value = False
        self.svc.client.svcinfo.lsfcmap.side_effect = [
            CLIFailureError("CMMVC5753E")]

        with self.assertRaises(CLIFailureError):
            self.svc.get_snapshot(VOLUME_UID, SNAPSHOT_NAME, pool=DUMMY_POOL1, is_virt_snap_func=False)

    def test_get_snapshot_non_zero_copy_rate(self):
        self._prepare_mocks_for_get_snapshot()
        self.fcmaps[0].copy_rate = svc_settings.DUMMY_COPY_RATE
        with self.assertRaises(array_errors.ExpectedSnapshotButFoundVolumeError):
            self.svc.get_snapshot(VOLUME_UID, SNAPSHOT_NAME, pool=DUMMY_POOL1, is_virt_snap_func=False)

    def test_get_snapshot_no_fcmap_as_target(self):
        self._prepare_mocks_for_get_snapshot()
        self.svc.client.svcinfo.lsfcmap.return_value = Mock(as_list=[])
        with self.assertRaises(array_errors.ExpectedSnapshotButFoundVolumeError):
            self.svc.get_snapshot(VOLUME_UID, SNAPSHOT_NAME, pool=DUMMY_POOL1, is_virt_snap_func=False)

    def test_get_snapshot_lsvdisk_success(self):
        self._prepare_mocks_for_get_snapshot()
        snapshot = self.svc.get_snapshot(VOLUME_UID, SNAPSHOT_NAME, pool=DUMMY_POOL1, is_virt_snap_func=False)
        self.assertEqual(SNAPSHOT_NAME, snapshot.name)

    def test_get_snapshot_lsvolumesnapshot_cli_failure_errors(self):
        self.svc.client.svctask.addsnapshot = Mock()
        client_method = self.svc.client.svcinfo.lsvolumesnapshot
        self._test_get_snapshot_illegal_name_cli_failure_errors(client_method, True)
        self.svc.client.svcinfo.lsvolumesnapshot.assert_called()

    def _prepare_mocks_for_get_snapshot_lsvolumesnapshot(self):
        self.svc.client.svctask.addsnapshot = Mock()
        self.svc.client.svcinfo.lsvolumesnapshot.return_value = self._mock_cli_object(self._get_cli_snapshot())
        self.svc.client.svcinfo.lsvdisk.return_value = self._mock_cli_object(self._get_cli_volume())

    def _get_filtervalue(self, key, value):
        return "=".join([key, value])

    def test_get_snapshot_lsvolumesnapshot_success(self):
        self._prepare_mocks_for_get_snapshot_lsvolumesnapshot()
        snapshot = self.svc.get_snapshot(VOLUME_UID, SNAPSHOT_NAME, pool=DUMMY_POOL1, is_virt_snap_func=True)
        self.assertEqual(SNAPSHOT_NAME, snapshot.name)
        filtervalue = self._get_filtervalue(svc_settings.SNAPSHOT_NAME_ATTR_KEY, SNAPSHOT_NAME)
        self.svc.client.svcinfo.lsvolumesnapshot.assert_called_once_with(filtervalue=filtervalue)
        self.svc.client.svcinfo.lsvdisk.assert_called_once_with(bytes=True,
                                                                filtervalue=self._get_filtervalue(
                                                                    svc_settings.VOLUME_VDISK_UID_ATTR_KEY,
                                                                    VOLUME_UID))

    def test_get_snapshot_lsvolumesnapshot_not_supported_error(self):
        with self.assertRaises(array_errors.VirtSnapshotFunctionNotSupportedMessage):
            self.svc.get_snapshot(VOLUME_UID, SNAPSHOT_NAME, pool=DUMMY_POOL1, is_virt_snap_func=True)

    def test_get_object_by_id_snapshot_has_no_fcmap_id_raise_error(self):
        self._prepare_lsvdisk_to_return_mapless_target_volume()
        with self.assertRaises(array_errors.ExpectedSnapshotButFoundVolumeError):
            self.svc.get_object_by_id(SNAPSHOT_VOLUME_UID, SNAPSHOT_OBJECT_TYPE)

    def test_get_object_by_id_return_none(self):
        self.svc.client.svcinfo.lsvdisk.return_value = Mock(as_single_element=None)
        returned_value = self.svc.get_object_by_id(SNAPSHOT_VOLUME_UID, SNAPSHOT_OBJECT_TYPE)
        self.assertEqual(None, returned_value)

    def test_get_object_by_id_snapshot_success(self):
        self._prepare_mocks_for_get_snapshot()

        snapshot = self.svc.get_object_by_id(SNAPSHOT_VOLUME_UID, SNAPSHOT_OBJECT_TYPE)
        self.assertEqual(SNAPSHOT_NAME, snapshot.name)
        calls = [
            call(bytes=True,
                 filtervalue=self._get_filtervalue(svc_settings.VOLUME_VDISK_UID_ATTR_KEY, SNAPSHOT_VOLUME_UID)),
            call(bytes=True, object_id=SOURCE_VOLUME_NAME)]
        self.svc.client.svcinfo.lsvdisk.assert_has_calls(calls)

    def test_get_object_by_id_snapshot_virt_snap_func_enabled_success(self):
        self._prepare_mocks_for_get_snapshot()
        self._prepare_mocks_for_lsvolumesnapshot()
        snapshot = self.svc.get_object_by_id(SNAPSHOT_NAME, SNAPSHOT_OBJECT_TYPE, is_virt_snap_func=True)
        self.assertEqual(SNAPSHOT_NAME, snapshot.name)
        self.svc.client.svcinfo.lsvdisk.assert_called_once_with(bytes=True, object_id=VOLUME_NAME)
        self.svc.client.svcinfo.lsvolumesnapshot.assert_called_once_with(object_id=SNAPSHOT_NAME)

    def test_get_object_by_id_volume_success(self):
        target_cli_volume = self._get_mapped_target_cli_volume()
        target_cli_volume.name = VOLUME_NAME
        self.svc.client.svcinfo.lsvdisk.return_value = self._mock_cli_object(target_cli_volume)
        volume = self.svc.get_object_by_id(VOLUME_UID, VOLUME_OBJECT_TYPE)
        self.assertEqual(VOLUME_NAME, volume.name)

    def _get_custom_cli_volume(self, support_deduplicated_copy, with_deduplicated_copy, name=SOURCE_VOLUME_NAME,
                               pool_name=DUMMY_POOL1):
        volume = self._get_cli_volume(with_deduplicated_copy, name=name, pool_name=pool_name)
        if not support_deduplicated_copy:
            del volume.deduplicated_copy
        return volume

    def _prepare_mocks_for_create_snapshot_mkvolume(self, support_deduplicated_copy=True,
                                                    source_has_deduplicated_copy=False, different_pool_site=False,
                                                    is_source_stretched=False):
        self.svc.client.svctask.mkvolume.return_value = Mock()
        self.svc.client.svctask.mkfcmap.return_value = Mock()
        pool = [svc_settings.POOL_MANY, DUMMY_POOL1, DUMMY_POOL2] if is_source_stretched else DUMMY_POOL1
        source_volume_to_copy_from = self._get_custom_cli_volume(support_deduplicated_copy,
                                                                 source_has_deduplicated_copy,
                                                                 pool_name=pool)
        volumes_to_return = [source_volume_to_copy_from, source_volume_to_copy_from]

        if different_pool_site:
            if is_source_stretched:
                pools_to_return = [Munch({svc_settings.LSMDISKGRP_SITE_NAME_ATTR_KEY: svc_settings.DUMMY_POOL_SITE}),
                                   Munch({svc_settings.LSMDISKGRP_SITE_NAME_ATTR_KEY: svc_settings.DUMMY_VOLUME_SITE1}),
                                   Munch({svc_settings.LSMDISKGRP_SITE_NAME_ATTR_KEY: svc_settings.DUMMY_POOL_SITE})]
                self.svc.client.svcinfo.lsmdiskgrp.side_effect = self._mock_cli_objects(pools_to_return)
            else:
                pools_to_return = [Munch({svc_settings.LSMDISKGRP_SITE_NAME_ATTR_KEY: svc_settings.DUMMY_POOL_SITE}),
                                   Munch({svc_settings.LSMDISKGRP_SITE_NAME_ATTR_KEY: svc_settings.DUMMY_VOLUME_SITE1}),
                                   Munch({svc_settings.LSMDISKGRP_SITE_NAME_ATTR_KEY: svc_settings.DUMMY_VOLUME_SITE2}),
                                   Munch({svc_settings.LSMDISKGRP_SITE_NAME_ATTR_KEY: svc_settings.DUMMY_POOL_SITE})]
                self.svc.client.svcinfo.lsmdiskgrp.side_effect = self._mock_cli_objects(pools_to_return)

                auxiliary_volumes = [self._get_cli_volume(name=TARGET_VOLUME_NAME, pool_name=DUMMY_POOL2),
                                     self._get_custom_cli_volume(support_deduplicated_copy,
                                                                 source_has_deduplicated_copy,
                                                                 name=VOLUME_NAME,
                                                                 pool_name=DUMMY_POOL1)]
                volumes_to_return.extend(auxiliary_volumes)

                rcrelationships_to_return = [Munch({
                    svc_settings.LSRCRELATIONSHIP_AUX_VOLUME_ATTR_KEY: TARGET_VOLUME_NAME}),
                    Munch({svc_settings.LSRCRELATIONSHIP_AUX_VOLUME_ATTR_KEY: VOLUME_NAME})]
                self.svc.client.svcinfo.lsrcrelationship.return_value = Mock(as_list=rcrelationships_to_return)

        target_volume_after_creation = self._get_mapless_target_cli_volume()
        target_volume_after_mapping = self._get_mapped_target_cli_volume()
        target_volume_for_rollback = self._get_mapped_target_cli_volume()
        volumes_to_return.extend([target_volume_after_creation, target_volume_after_mapping,
                                  target_volume_for_rollback])

        self.svc.client.svcinfo.lsvdisk.side_effect = self._mock_cli_objects(volumes_to_return)
        self.svc.client.svctask.startfcmap.return_value = Mock()

    @patch("controllers.array_action.array_mediator_svc.is_warning_message")
    def test_create_snapshot_create_volume_error(self, mock_warning):
        source_cli_volume = self._get_cli_volume()
        self.svc.client.svcinfo.lsvdisk.return_value = self._mock_cli_object(source_cli_volume)
        mock_warning.return_value = False
        self.svc.client.svctask.mkvolume.side_effect = [
            CLIFailureError(DUMMY_ERROR_MESSAGE)]

        with self.assertRaises(CLIFailureError):
            self.svc.create_snapshot(SOURCE_VOLUME_ID, SNAPSHOT_NAME, space_efficiency=None, pool=DUMMY_POOL1,
                                     is_virt_snap_func=False)

    def _test_create_snapshot_lsvdisk_cli_failure_error(self, volume_id, snapshot_name, error_message_id,
                                                        expected_error, space_efficiency=None, pool=None):
        self._test_mediator_method_client_cli_failure_error(self.svc.create_snapshot,
                                                            (volume_id, snapshot_name, space_efficiency, pool, False),
                                                            self.svc.client.svcinfo.lsvdisk, error_message_id,
                                                            expected_error)

    def test_create_snapshot_lsvdisk_cli_failure_errors(self):
        self._test_create_snapshot_lsvdisk_cli_failure_error("\xff", SNAPSHOT_NAME, "CMMVC6017E",
                                                             array_errors.InvalidArgumentError)
        self._test_create_snapshot_lsvdisk_cli_failure_error("!@#", SNAPSHOT_NAME, "CMMVC5741E",
                                                             array_errors.InvalidArgumentError)

    def test_create_snapshot_source_not_found_error(self):
        self.svc.client.svcinfo.lsvdisk.side_effect = [Mock(as_single_element=None), Mock(as_single_element=None)]
        with self.assertRaises(array_errors.ObjectNotFoundError):
            self.svc.create_snapshot(SOURCE_VOLUME_ID, SNAPSHOT_NAME, space_efficiency=None, pool=DUMMY_POOL1,
                                     is_virt_snap_func=False)

    @patch("controllers.array_action.array_mediator_svc.is_warning_message")
    def test_create_snapshot_create_fcmap_error(self, mock_warning):
        self._prepare_mocks_for_create_snapshot_mkvolume()
        mock_warning.return_value = False
        self.svc.client.svctask.mkfcmap.side_effect = [
            CLIFailureError(DUMMY_ERROR_MESSAGE)]

        with self.assertRaises(CLIFailureError):
            self.svc.create_snapshot(SOURCE_VOLUME_ID, SNAPSHOT_NAME, space_efficiency=None, pool=DUMMY_POOL1,
                                     is_virt_snap_func=False)

    @patch("controllers.array_action.array_mediator_svc.is_warning_message")
    def test_create_snapshot_start_fcmap_error(self, mock_warning):
        self._prepare_mocks_for_create_snapshot_mkvolume()
        mock_warning.return_value = False
        self.svc.client.svctask.startfcmap.side_effect = [
            CLIFailureError(DUMMY_ERROR_MESSAGE)]

        with self.assertRaises(CLIFailureError):
            self.svc.create_snapshot(SOURCE_VOLUME_ID, SNAPSHOT_NAME, space_efficiency=None, pool=DUMMY_POOL1,
                                     is_virt_snap_func=False)

    def test_create_snapshot_mkvolume_success(self):
        self._prepare_mocks_for_create_snapshot_mkvolume()

        snapshot = self.svc.create_snapshot(SOURCE_VOLUME_ID, SNAPSHOT_NAME, space_efficiency=None, pool=DUMMY_POOL1,
                                            is_virt_snap_func=False)

        self.assertEqual(svc_settings.DUMMY_CAPACITY_INT, snapshot.capacity_bytes)
        self.assertEqual(ARRAY_TYPE_SVC, snapshot.array_type)
        self.assertEqual(SNAPSHOT_VOLUME_UID, snapshot.id)

    def test_create_snapshot_with_different_pool_success(self):
        self._prepare_mocks_for_create_snapshot_mkvolume()

        self.svc.create_snapshot(SOURCE_VOLUME_ID, SNAPSHOT_NAME, space_efficiency=None, pool=DUMMY_POOL2,
                                 is_virt_snap_func=False)
        self.svc.client.svctask.mkvolume.assert_called_once_with(name=SNAPSHOT_NAME, unit=svc_settings.BYTE_UNIT_SYMBOL,
                                                                 size=svc_settings.DUMMY_CAPACITY_INT,
                                                                 pool=DUMMY_POOL2,
                                                                 iogrp=DUMMY_IO_GROUP,
                                                                 thin=True)

    def test_create_snapshot_for_hyperswap_volume_with_different_site_success(self):
        self._prepare_mocks_for_create_snapshot_mkvolume(different_pool_site=True)

        self.svc.create_snapshot(SOURCE_VOLUME_ID, SNAPSHOT_NAME, space_efficiency=None, pool=DUMMY_POOL2,
                                 is_virt_snap_func=False)
        self.svc.client.svctask.mkfcmap.assert_called_once_with(source=VOLUME_NAME, target=SNAPSHOT_NAME,
                                                                copyrate=0)

    def test_create_snapshot_for_stretched_volume_with_different_site_success(self):
        self._prepare_mocks_for_create_snapshot_mkvolume(different_pool_site=True, is_source_stretched=True)

        self.svc.create_snapshot(SOURCE_VOLUME_ID, SNAPSHOT_NAME, space_efficiency=None, pool=DUMMY_POOL2,
                                 is_virt_snap_func=False)
        self.svc.client.svctask.mkfcmap.assert_called_once_with(source=SOURCE_VOLUME_NAME, target=SNAPSHOT_NAME,
                                                                copyrate=0)

    def test_create_snapshot_for_stretched_volume_implicit_pool_success(self):
        self._prepare_mocks_for_create_snapshot_mkvolume(is_source_stretched=True)

        self.svc.create_snapshot(SOURCE_VOLUME_ID, SNAPSHOT_NAME, space_efficiency=None, pool=None,
                                 is_virt_snap_func=False)
        self.svc.client.svctask.mkvolume.assert_called_once_with(name=SNAPSHOT_NAME, unit=svc_settings.BYTE_UNIT_SYMBOL,
                                                                 size=svc_settings.DUMMY_CAPACITY_INT,
                                                                 pool=DUMMY_POOL1, iogrp=DUMMY_IO_GROUP,
                                                                 thin=True)
        self.svc.client.svctask.mkfcmap.assert_called_once_with(source=SOURCE_VOLUME_NAME, target=SNAPSHOT_NAME,
                                                                copyrate=0)

    def test_create_snapshot_as_stretched_success(self):
        self._prepare_mocks_for_create_snapshot_mkvolume()

        self.svc.create_snapshot(SOURCE_VOLUME_ID, SNAPSHOT_NAME, space_efficiency=None, pool=STRETCHED_POOL,
                                 is_virt_snap_func=False)
        self.svc.client.svctask.mkvolume.assert_called_once_with(name=SNAPSHOT_NAME, unit=svc_settings.BYTE_UNIT_SYMBOL,
                                                                 size=svc_settings.DUMMY_CAPACITY_INT,
                                                                 pool=STRETCHED_POOL, iogrp=DUMMY_IO_GROUP,
                                                                 thin=True)
        self.svc.client.svctask.mkfcmap.assert_called_once_with(source=SOURCE_VOLUME_NAME, target=SNAPSHOT_NAME,
                                                                copyrate=0)

    def test_create_snapshot_with_specified_source_volume_space_efficiency_success(self):
        self._prepare_mocks_for_create_snapshot_mkvolume(source_has_deduplicated_copy=True)

        self.svc.create_snapshot(SOURCE_VOLUME_ID, SNAPSHOT_NAME, space_efficiency=None, pool=None,
                                 is_virt_snap_func=False)
        self.svc.client.svctask.mkvolume.assert_called_once_with(name=SNAPSHOT_NAME, unit=svc_settings.BYTE_UNIT_SYMBOL,
                                                                 size=svc_settings.DUMMY_CAPACITY_INT,
                                                                 pool=DUMMY_POOL1, iogrp=DUMMY_IO_GROUP,
                                                                 compressed=True, deduplicated=True)

    def test_create_snapshot_with_different_space_efficiency_success(self):
        self._prepare_mocks_for_create_snapshot_mkvolume(source_has_deduplicated_copy=True)

        self.svc.create_snapshot(SOURCE_VOLUME_ID, SNAPSHOT_NAME, space_efficiency=SPACE_EFFICIENCY_THIN, pool=None,
                                 is_virt_snap_func=False)
        self.svc.client.svctask.mkvolume.assert_called_once_with(name=SNAPSHOT_NAME, unit=svc_settings.BYTE_UNIT_SYMBOL,
                                                                 size=svc_settings.DUMMY_CAPACITY_INT,
                                                                 pool=DUMMY_POOL1, iogrp=DUMMY_IO_GROUP,
                                                                 thin=True)

    def test_create_snapshot_no_deduplicated_copy_success(self):
        self._prepare_mocks_for_create_snapshot_mkvolume(support_deduplicated_copy=False)

        snapshot = self.svc.create_snapshot(SOURCE_VOLUME_ID, SNAPSHOT_NAME, space_efficiency=None, pool=DUMMY_POOL1,
                                            is_virt_snap_func=False)

        self.assertEqual(svc_settings.DUMMY_CAPACITY_INT, snapshot.capacity_bytes)
        self.assertEqual(ARRAY_TYPE_SVC, snapshot.array_type)
        self.assertEqual(SNAPSHOT_VOLUME_UID, snapshot.id)

    def _prepare_mocks_for_lsvolumesnapshot(self, snapshot_id=INTERNAL_VOLUME_ID):
        self.svc.client.svcinfo.lsvolumesnapshot = Mock()
        self.svc.client.svcinfo.lsvolumesnapshot.return_value = self._mock_cli_object(
            self._get_cli_snapshot(snapshot_id))

    def _prepare_mocks_for_create_snapshot_addsnapshot(self, snapshot_id=INTERNAL_VOLUME_ID):
        self.svc.client.svctask.addsnapshot = Mock()
        source_volume_to_copy_from = self._get_custom_cli_volume(False, False, pool_name=DUMMY_POOL1)
        volumes_to_return = [source_volume_to_copy_from, source_volume_to_copy_from, source_volume_to_copy_from]
        self.svc.client.svcinfo.lsvdisk.side_effect = self._mock_cli_objects(volumes_to_return)
        self.svc.client.svctask.addsnapshot.return_value = Mock(
            response=(b"Snapshot, id [0], successfully created or triggered\n", b""))
        self._prepare_mocks_for_lsvolumesnapshot(snapshot_id)

    def _test_create_snapshot_addsnapshot_success(self, pool=DUMMY_POOL1):
        self._prepare_mocks_for_create_snapshot_addsnapshot()
        snapshot = self.svc.create_snapshot(SOURCE_VOLUME_ID, SNAPSHOT_NAME, space_efficiency=None, pool=pool,
                                            is_virt_snap_func=True)
        if not pool:
            pool = DUMMY_POOL1
        self.assertEqual(svc_settings.DUMMY_CAPACITY_INT, snapshot.capacity_bytes)
        self.svc.client.svctask.addsnapshot.assert_called_once_with(name=SNAPSHOT_NAME, volumes=INTERNAL_VOLUME_ID,
                                                                    pool=pool)
        self.svc.client.svcinfo.lsvolumesnapshot.assert_called_once_with(object_id=0)
        self.assertEqual(ARRAY_TYPE_SVC, snapshot.array_type)
        self.assertEqual("", snapshot.id)
        self.assertEqual(INTERNAL_VOLUME_ID, snapshot.internal_id)

    def test_create_snapshot_addsnapshot_success(self):
        self._test_create_snapshot_addsnapshot_success()

    def test_create_snapshot_addsnapshot_no_pool_success(self):
        self._test_create_snapshot_addsnapshot_success(pool="")

    def test_create_snapshot_addsnapshot_different_pool_success(self):
        self._test_create_snapshot_addsnapshot_success(pool=DUMMY_POOL2)

    def test_create_snapshot_addsnapshot_not_supported_error(self):
        with self.assertRaises(array_errors.VirtSnapshotFunctionNotSupportedMessage):
            self.svc.create_snapshot(SOURCE_VOLUME_ID, SNAPSHOT_NAME, space_efficiency=None, pool=DUMMY_POOL1,
                                     is_virt_snap_func=True)

    def _test_create_snapshot_addsnapshot_cli_failure_error(self, error_message_id, expected_error):
        self._prepare_mocks_for_create_snapshot_addsnapshot()
        self._test_mediator_method_client_cli_failure_error(self.svc.create_snapshot,
                                                            (SOURCE_VOLUME_ID, SNAPSHOT_NAME, "", DUMMY_POOL1, True),
                                                            self.svc.client.svctask.addsnapshot, error_message_id,
                                                            expected_error)

    def test_create_snapshot_addsnapshot_raise_exceptions(self):
        self.svc.client.svctask.addsnapshot = Mock()
        self._test_mediator_method_client_error(self.svc.create_snapshot,
                                                (SOURCE_VOLUME_NAME, SNAPSHOT_NAME, "", DUMMY_POOL1),
                                                self.svc.client.svctask.addsnapshot, Exception, Exception)
        self._test_create_snapshot_addsnapshot_cli_failure_error(DUMMY_ERROR_MESSAGE, CLIFailureError)
        self._test_create_snapshot_addsnapshot_cli_failure_error("CMMVC8710E", array_errors.NotEnoughSpaceInPool)
        self._test_create_snapshot_addsnapshot_cli_failure_error("CMMVC6017E", array_errors.InvalidArgumentError)
        self._test_create_snapshot_addsnapshot_cli_failure_error("CMMVC6035E", array_errors.SnapshotAlreadyExists)
        self._test_create_snapshot_addsnapshot_cli_failure_error("CMMVC5754E", array_errors.PoolDoesNotExist)

    def test_delete_snapshot_no_volume_raise_snapshot_not_found(self):
        self._prepare_lsvdisk_to_return_none()

        with self.assertRaises(array_errors.ObjectNotFoundError):
            self.svc.delete_snapshot(SNAPSHOT_NAME, INTERNAL_SNAPSHOT_ID)

    def test_delete_snapshot_no_fcmap_id_raise_snapshot_not_found(self):
        self._prepare_lsvdisk_to_return_mapless_target_volume()

        with self.assertRaises(array_errors.ObjectNotFoundError):
            self.svc.delete_snapshot(SNAPSHOT_NAME, INTERNAL_SNAPSHOT_ID)

    def test_delete_snapshot_call_rmfcmap(self):
        self._prepare_mocks_for_delete_snapshot()
        fcmaps_as_target = self.fcmaps
        self.svc.client.svcinfo.lsfcmap.side_effect = [Mock(as_list=fcmaps_as_target), Mock(as_list=[])]
        self.svc.delete_snapshot(SNAPSHOT_NAME, INTERNAL_SNAPSHOT_ID)

        self.svc.client.svctask.rmfcmap.assert_called_once_with(object_id=svc_settings.DUMMY_FCMAP_ID, force=True)

    def test_delete_snapshot_does_not_remove_hyperswap_fcmap(self):
        self._prepare_mocks_for_delete_snapshot()
        self._prepare_fcmaps_for_hyperswap()
        self.svc.delete_snapshot(SNAPSHOT_NAME, INTERNAL_SNAPSHOT_ID)

        self.svc.client.svctask.rmfcmap.assert_not_called()

    def _test_delete_snapshot_rmvolume_cli_failure_error(self, error_message_id, expected_error,
                                                         snapshot_id=SNAPSHOT_VOLUME_UID):
        self._test_mediator_method_client_cli_failure_error(self.svc.delete_snapshot,
                                                            (snapshot_id, INTERNAL_SNAPSHOT_ID),
                                                            self.svc.client.svctask.rmvolume, error_message_id,
                                                            expected_error)

    def test_delete_snapshot_rmvolume_errors(self):
        self._prepare_mocks_for_delete_snapshot()
        self._test_delete_snapshot_rmvolume_cli_failure_error("CMMVC5753E", array_errors.ObjectNotFoundError)
        self._test_delete_snapshot_rmvolume_cli_failure_error("CMMVC8957E", array_errors.ObjectNotFoundError)
        self._test_delete_snapshot_rmvolume_cli_failure_error(DUMMY_ERROR_MESSAGE, CLIFailureError)

    def test_delete_snapshot_still_copy_fcmaps_not_removed(self):
        self._prepare_mocks_for_delete_volume()
        fcmaps_as_target = self.fcmaps
        fcmaps_as_source = self.fcmaps_as_source
        fcmaps_as_source[0].status = svc_settings.DUMMY_FCMAP_BAD_STATUS
        self.svc.client.svcinfo.lsfcmap.side_effect = [Mock(as_list=fcmaps_as_target), Mock(as_list=fcmaps_as_source)]
        with self.assertRaises(array_errors.ObjectIsStillInUseError):
            self.svc.delete_snapshot(SNAPSHOT_NAME, INTERNAL_SNAPSHOT_ID)

    def test_delete_snapshot_rmvolume_success(self):
        self._prepare_mocks_for_delete_snapshot()
        self.svc.delete_snapshot(SNAPSHOT_NAME, INTERNAL_SNAPSHOT_ID)
        self.assertEqual(2, self.svc.client.svctask.rmfcmap.call_count)
        self.svc.client.svctask.rmvolume.assert_called_once_with(vdisk_id=SNAPSHOT_NAME)

    @patch("controllers.array_action.array_mediator_svc.is_warning_message")
    def test_delete_snapshot_with_fcmap_already_stopped_success(self, mock_warning):
        self._prepare_mocks_for_delete_snapshot()
        mock_warning.return_value = False
        self.svc.client.svctask.stopfcmap.side_effect = [CLIFailureError("CMMVC5912E")]
        self.svc.delete_snapshot(SNAPSHOT_NAME, INTERNAL_SNAPSHOT_ID)
        self.assertEqual(2, self.svc.client.svctask.rmfcmap.call_count)
        self.svc.client.svctask.rmvolume.assert_called_once_with(vdisk_id=SNAPSHOT_NAME)

    @patch("controllers.array_action.array_mediator_svc.is_warning_message")
    def test_delete_snapshot_with_stopfcmap_raise_error(self, mock_warning):
        self._prepare_mocks_for_delete_snapshot()
        mock_warning.return_value = False
        self.svc.client.svctask.stopfcmap.side_effect = [CLIFailureError(DUMMY_ERROR_MESSAGE)]
        with self.assertRaises(CLIFailureError):
            self.svc.delete_snapshot(SNAPSHOT_NAME, INTERNAL_SNAPSHOT_ID)

    def _prepare_mocks_for_delete_snapshot_addsnapshot(self):
        self.svc.client.svctask.addsnapshot = Mock()

    def _test_delete_snapshot_rmsnapshot_cli_failure_error(self, error_message_id, expected_error):
        self._test_mediator_method_client_cli_failure_error(self.svc.delete_snapshot, ("", INTERNAL_SNAPSHOT_ID),
                                                            self.svc.client.svctask.rmsnapshot, error_message_id,
                                                            expected_error)

    def test_delete_snapshot_rmsnapshot_errors(self):
        self._prepare_mocks_for_delete_snapshot_addsnapshot()
        self._test_delete_snapshot_rmsnapshot_cli_failure_error("CMMVC9755E", array_errors.ObjectNotFoundError)
        self._test_delete_snapshot_rmsnapshot_cli_failure_error(DUMMY_ERROR_MESSAGE, CLIFailureError)

    def test_delete_snapshot_rmsnapshot_success(self):
        self._prepare_mocks_for_delete_snapshot_addsnapshot()
        self.svc.delete_snapshot("", INTERNAL_SNAPSHOT_ID)
        self.svc.client.svctask.rmsnapshot.assert_called_once_with(snapshotid=INTERNAL_SNAPSHOT_ID)

    def test_validate_supported_space_efficiency_raise_error(self):
        space_efficiency = svc_settings.DUMMY_SPACE_EFFICIENCY
        with self.assertRaises(
                array_errors.SpaceEfficiencyNotSupported):
            self.svc.validate_supported_space_efficiency(space_efficiency)

    def test_validate_supported_space_efficiency_success(self):
        no_space_efficiency = ""
        self.svc.validate_supported_space_efficiency(no_space_efficiency)
        thin_space_efficiency = SPACE_EFFICIENCY_THIN
        self.svc.validate_supported_space_efficiency(thin_space_efficiency)
        thick_space_efficiency = SPACE_EFFICIENCY_THICK
        self.svc.validate_supported_space_efficiency(thick_space_efficiency)
        compressed_space_efficiency = SPACE_EFFICIENCY_COMPRESSED
        self.svc.validate_supported_space_efficiency(compressed_space_efficiency)
        deduplicated_space_efficiency = SPACE_EFFICIENCY_DEDUPLICATED
        self.svc.validate_supported_space_efficiency(deduplicated_space_efficiency)
        deduplicated_thin_space_efficiency = SPACE_EFFICIENCY_DEDUPLICATED_THIN
        self.svc.validate_supported_space_efficiency(deduplicated_thin_space_efficiency)
        deduplicated_compressed_space_efficiency = SPACE_EFFICIENCY_DEDUPLICATED_COMPRESSED
        self.svc.validate_supported_space_efficiency(deduplicated_compressed_space_efficiency)

    def _test_build_kwargs_from_parameters(self, space_efficiency, pool, io_group, volume_group, name, size,
                                           expected_extra_kwargs):
        expected_kwargs = {svc_settings.CREATE_VOLUME_NAME_ARGUMENT: name,
                           svc_settings.CREATE_VOLUME_SIZE_UNIT_ARGUMENT: svc_settings.BYTE_UNIT_SYMBOL,
                           svc_settings.CREATE_VOLUME_SIZE_ARGUMENT: size,
                           svc_settings.CREATE_VOLUME_POOL_ARGUMENT: pool}
        expected_kwargs.update(expected_extra_kwargs)
        if io_group:
            expected_kwargs[svc_settings.CREATE_VOLUME_IO_GROUP_ARGUMENT] = io_group
        if volume_group:
            expected_kwargs[svc_settings.CREATE_VOLUME_VOLUME_GROUP_ARGUMENT] = volume_group
        actual_kwargs = build_kwargs_from_parameters(space_efficiency, pool, io_group, volume_group, name, size)
        self.assertDictEqual(actual_kwargs, expected_kwargs)

    def test_build_kwargs_from_parameters(self):
        size = self.svc._convert_size_bytes(1000)
        second_size = self.svc._convert_size_bytes(2048)
        self._test_build_kwargs_from_parameters(SPACE_EFFICIENCY_THIN, DUMMY_POOL1, None, None, VOLUME_NAME, size,
                                                {SPACE_EFFICIENCY_THIN: True})
        self._test_build_kwargs_from_parameters(SPACE_EFFICIENCY_COMPRESSED, DUMMY_POOL1, None, None, VOLUME_NAME, size,
                                                {SPACE_EFFICIENCY_COMPRESSED: True})
        self._test_build_kwargs_from_parameters(SPACE_EFFICIENCY_DEDUPLICATED_THIN, DUMMY_POOL1, DUMMY_IO_GROUP,
                                                DUMMY_VOLUME_GROUP,
                                                VOLUME_NAME, second_size,
                                                {svc_settings.CREATE_VOLUME_IO_GROUP_ARGUMENT: DUMMY_IO_GROUP,
                                                 svc_settings.CREATE_VOLUME_VOLUME_GROUP_ARGUMENT: DUMMY_VOLUME_GROUP,
                                                 SPACE_EFFICIENCY_THIN: True, SPACE_EFFICIENCY_DEDUPLICATED: True})
        self._test_build_kwargs_from_parameters(SPACE_EFFICIENCY_DEDUPLICATED_COMPRESSED, DUMMY_POOL2, None, None,
                                                VOLUME_NAME, second_size,
                                                {SPACE_EFFICIENCY_COMPRESSED: True,
                                                 SPACE_EFFICIENCY_DEDUPLICATED: True})
        self._test_build_kwargs_from_parameters(SPACE_EFFICIENCY_DEDUPLICATED, DUMMY_POOL2, None, None, VOLUME_NAME,
                                                second_size,
                                                {SPACE_EFFICIENCY_COMPRESSED: True,
                                                 SPACE_EFFICIENCY_DEDUPLICATED: True})

    def test_properties(self):
        self.assertEqual(22, SVCArrayMediator.port)
        self.assertEqual(svc_settings.DUMMY_SMALL_CAPACITY_INT, SVCArrayMediator.minimal_volume_size_in_bytes)
        self.assertEqual(ARRAY_TYPE_SVC, SVCArrayMediator.array_type)
        self.assertEqual(63, SVCArrayMediator.max_object_name_length)
        self.assertEqual(2, SVCArrayMediator.max_connections)
        self.assertEqual(10, SVCArrayMediator.max_lun_retries)

    def _prepare_lsnvmefabric_mock(self, host_names, nvme_host_names, connectivity_types):
        nvme_host_mocks = []
        self.svc.client.svcinfo.lsnvmefabric.return_value = Mock(as_list=nvme_host_mocks)
        if NVME_OVER_FC_CONNECTIVITY_TYPE in connectivity_types:
            nvme_host_names = host_names if nvme_host_names is None else nvme_host_names
            if nvme_host_names:
                nvme_host_mocks = [Mock(object_name=host_name) for host_name in nvme_host_names]
                lsnvmefabric_return_values = [Mock(as_list=[host_mock] * 4) for host_mock in nvme_host_mocks]
                self.svc.client.svcinfo.lsnvmefabric.side_effect = lsnvmefabric_return_values

    def _prepare_lsfabric_mock_for_get_host(self, host_names, fc_host_names, connectivity_types):
        fc_host_mocks = []
        self.svc.client.svcinfo.lsfabric.return_value = Mock(as_list=fc_host_mocks)
        if FC_CONNECTIVITY_TYPE in connectivity_types:
            fc_host_names = host_names if fc_host_names is None else fc_host_names
            if fc_host_names:
                for host_name in fc_host_names:
                    mock = Mock()
                    mock.name = host_name
                    fc_host_mocks.append(mock)
                lsfabric_return_values = [Mock(as_list=[host_mock] * 4) for host_mock in fc_host_mocks]
                self.svc.client.svcinfo.lsfabric.side_effect = lsfabric_return_values

    def _prepare_lshostiplogin_mock(self, host_name, iscsi_host_name, connectivity_types):
        iscsi_host_name = host_name if iscsi_host_name is None else iscsi_host_name
        if ISCSI_CONNECTIVITY_TYPE in connectivity_types and iscsi_host_name:
            iscsi_host_mock = Mock(host_name=iscsi_host_name)
            self.svc.client.svcinfo.lshostiplogin.return_value = Mock(as_single_element=iscsi_host_mock)
        else:
            self.svc.client.svcinfo.lshostiplogin.side_effect = CLIFailureError("CMMVC5804E")

    def _prepare_mocks_for_get_host_by_identifiers(self, nvme_host_names=None, fc_host_names=None,
                                                   iscsi_host_name=None, connectivity_types=None):
        host_name = DUMMY_HOST_NAME1
        host_names = [host_name]

        if connectivity_types is None:
            connectivity_types = {NVME_OVER_FC_CONNECTIVITY_TYPE,
                                  FC_CONNECTIVITY_TYPE,
                                  ISCSI_CONNECTIVITY_TYPE}

        self._prepare_lsnvmefabric_mock(host_names, nvme_host_names, connectivity_types)
        self._prepare_lsfabric_mock_for_get_host(host_names, fc_host_names, connectivity_types)
        self._prepare_lshostiplogin_mock(host_name, iscsi_host_name, connectivity_types)

    def _prepare_mocks_for_get_host_by_identifiers_no_hosts(self):
        self._prepare_mocks_for_get_host_by_identifiers(nvme_host_names=[], fc_host_names=[], iscsi_host_name="")
        self.svc.client.svcinfo.lshost = Mock(return_value=[])

    def _prepare_mocks_for_get_host_by_identifiers_slow(self, svc_response, custom_host=None):
        self._prepare_mocks_for_get_host_by_identifiers_no_hosts()
        host_1 = self._get_host_as_munch(DUMMY_HOST_ID1, DUMMY_HOST_NAME1, nqn_list=[DUMMY_NVME_NQN1],
                                         wwpns_list=[DUMMY_FC_WWN1],
                                         iscsi_names_list=[DUMMY_NODE1_IQN])
        host_2 = self._get_host_as_munch(DUMMY_HOST_ID2, DUMMY_HOST_NAME2, nqn_list=[DUMMY_NVME_NQN2],
                                         wwpns_list=[DUMMY_FC_WWN2],
                                         iscsi_names_list=[DUMMY_NODE2_IQN])
        if custom_host:
            host_3 = custom_host
        else:
            host_3 = self._get_host_as_munch(DUMMY_HOST_ID3, DUMMY_HOST_NAME3, nqn_list=[DUMMY_NVME_NQN3],
                                             wwpns_list=[DUMMY_FC_WWN3], iscsi_names_list=[DUMMY_NODE3_IQN])
        hosts = [host_1, host_2, host_3]
        self.svc.client.svcinfo.lshost = Mock()
        self.svc.client.svcinfo.lshost.return_value = self._get_hosts_list_result(hosts)
        self.svc.client.send_raw_command = Mock()
        self.svc.client.send_raw_command.return_value = EMPTY_BYTES, EMPTY_BYTES
        svc_response.return_value = hosts

    def _prepare_mocks_for_get_host_by_identifiers_backward_compatible(self, svc_response):
        self._prepare_mocks_for_get_host_by_identifiers_slow(svc_response)
        del self.svc.client.svcinfo.lshostiplogin
        del self.svc.client.svcinfo.lsnvmefabric

    def test_get_host_by_name_success(self):
        self.svc.client.svcinfo.lshost.return_value = Mock(
            as_single_element=self._get_host_as_munch(DUMMY_HOST_ID1, DUMMY_HOST_NAME1, nqn_list=[DUMMY_NVME_NQN1],
                                                      wwpns_list=[DUMMY_FC_WWN1],
                                                      iscsi_names_list=[DUMMY_NODE1_IQN]))
        host = self.svc.get_host_by_name(DUMMY_HOST_NAME1)
        self.assertEqual(DUMMY_HOST_NAME1, host.name)
        self.assertEqual([NVME_OVER_FC_CONNECTIVITY_TYPE, FC_CONNECTIVITY_TYPE, ISCSI_CONNECTIVITY_TYPE],
                         host.connectivity_types)
        self.assertEqual([DUMMY_NVME_NQN1], host.initiators.nvme_nqns)
        self.assertEqual([DUMMY_FC_WWN1], host.initiators.fc_wwns)
        self.assertEqual([DUMMY_NODE1_IQN], host.initiators.iscsi_iqns)

    def test_get_host_by_name_raise_host_not_found(self):
        self.svc.client.svcinfo.lshost.return_value = Mock(as_single_element=None)
        with self.assertRaises(array_errors.HostNotFoundError):
            self.svc.get_host_by_name(DUMMY_HOST_NAME1)

    @patch.object(SVCResponse, svc_settings.SVC_RESPONSE_AS_LIST, new_callable=PropertyMock)
    def test_get_host_by_identifiers_returns_host_not_found(self, svc_response):
        self._prepare_mocks_for_get_host_by_identifiers_slow(svc_response)
        with self.assertRaises(array_errors.HostNotFoundError):
            self.svc.get_host_by_host_identifiers(Initiators([DUMMY_NVME_NQN4], [DUMMY_FC_WWN4], [DUMMY_NODE4_IQN]))

    def test_get_host_by_identifier_return_host_not_found_when_no_hosts_exist(self):
        self._prepare_mocks_for_get_host_by_identifiers_no_hosts()
        with self.assertRaises(array_errors.HostNotFoundError):
            self.svc.get_host_by_host_identifiers(Initiators([DUMMY_NVME_NQN4], [DUMMY_FC_WWN4], [DUMMY_NODE4_IQN]))

    @patch.object(SVCResponse, svc_settings.SVC_RESPONSE_AS_LIST, new_callable=PropertyMock)
    def test_get_host_by_identifiers_slow_raise_multiplehostsfounderror(self, svc_response):
        self._prepare_mocks_for_get_host_by_identifiers_slow(svc_response)
        with self.assertRaises(array_errors.MultipleHostsFoundError):
            self.svc.get_host_by_host_identifiers(Initiators([DUMMY_NVME_NQN4], [DUMMY_FC_WWN2], [DUMMY_NODE3_IQN]))

    def test_get_host_by_identifiers_raise_multiplehostsfounderror(self):
        self._prepare_mocks_for_get_host_by_identifiers(nvme_host_names=[DUMMY_HOST_NAME1],
                                                        fc_host_names=[DUMMY_HOST_NAME2])
        with self.assertRaises(array_errors.MultipleHostsFoundError):
            self.svc.get_host_by_host_identifiers(Initiators([DUMMY_NVME_NQN4], [DUMMY_FC_WWN2], [DUMMY_NODE3_IQN]))

    @patch.object(SVCResponse, svc_settings.SVC_RESPONSE_AS_LIST, new_callable=PropertyMock)
    def test_get_host_by_identifiers_slow_return_iscsi_host(self, svc_response):
        self._prepare_mocks_for_get_host_by_identifiers_slow(svc_response)
        hostname, connectivity_types = self.svc.get_host_by_host_identifiers(
            Initiators([DUMMY_NVME_NQN4], [DUMMY_FC_WWN4], [DUMMY_NODE2_IQN]))
        self.assertEqual(DUMMY_HOST_NAME2, hostname)
        self.assertEqual([ISCSI_CONNECTIVITY_TYPE], connectivity_types)

    def test_get_host_by_identifiers_return_iscsi_host(self):
        self._prepare_mocks_for_get_host_by_identifiers(iscsi_host_name=DUMMY_HOST_NAME1,
                                                        connectivity_types=[ISCSI_CONNECTIVITY_TYPE])
        hostname, connectivity_types = self.svc.get_host_by_host_identifiers(
            Initiators([DUMMY_NVME_NQN4], [DUMMY_FC_WWN4], [DUMMY_NODE2_IQN]))
        self.assertEqual(DUMMY_HOST_NAME1, hostname)
        self.assertEqual({ISCSI_CONNECTIVITY_TYPE}, connectivity_types)
        self.svc.client.svcinfo.lshostiplogin.assert_called_once_with(object_id=DUMMY_NODE2_IQN)

    @patch.object(SVCResponse, svc_settings.SVC_RESPONSE_AS_LIST, new_callable=PropertyMock)
    def test_get_host_by_identifiers_slow_no_other_ports_return_iscsi_host(self, svc_response):
        host_with_iqn = self._get_host_as_munch(DUMMY_HOST_ID1, HOST_NAME, iscsi_names_list=[DUMMY_NODE1_IQN])
        self._prepare_mocks_for_get_host_by_identifiers_slow(svc_response, custom_host=host_with_iqn)
        hostname, connectivity_types = self.svc.get_host_by_host_identifiers(
            Initiators([DUMMY_NVME_NQN4], [DUMMY_FC_WWN4], [DUMMY_NODE1_IQN]))
        self.assertEqual(HOST_NAME, hostname)
        self.assertEqual([ISCSI_CONNECTIVITY_TYPE], connectivity_types)

    @patch.object(SVCResponse, svc_settings.SVC_RESPONSE_AS_LIST, new_callable=PropertyMock)
    def test_get_host_by_identifiers_slow_return_iscsi_host_with_list_iqn(self, svc_response):
        host_with_iqn_list = self._get_host_as_munch(DUMMY_HOST_ID1, HOST_NAME, wwpns_list=[DUMMY_FC_WWN1],
                                                     iscsi_names_list=[DUMMY_NODE1_IQN, DUMMY_NODE2_IQN])
        self._prepare_mocks_for_get_host_by_identifiers_slow(svc_response, custom_host=host_with_iqn_list)
        hostname, connectivity_types = self.svc.get_host_by_host_identifiers(
            Initiators([DUMMY_NVME_NQN4], [DUMMY_FC_WWN4], [DUMMY_NODE1_IQN]))
        self.assertEqual(HOST_NAME, hostname)
        self.assertEqual([ISCSI_CONNECTIVITY_TYPE], connectivity_types)

    @patch.object(SVCResponse, svc_settings.SVC_RESPONSE_AS_LIST, new_callable=PropertyMock)
    def test_get_host_by_identifiers_slow_return_nvme_host(self, svc_response):
        self._prepare_mocks_for_get_host_by_identifiers_slow(svc_response)
        hostname, connectivity_types = self.svc.get_host_by_host_identifiers(
            Initiators([DUMMY_NVME_NQN3], [DUMMY_FC_WWN4], [DUMMY_NODE4_IQN]))
        self.assertEqual(DUMMY_HOST_NAME3, hostname)
        self.assertEqual([NVME_OVER_FC_CONNECTIVITY_TYPE], connectivity_types)

    def test_get_host_by_identifiers_return_nvme_host(self):
        self._prepare_mocks_for_get_host_by_identifiers(nvme_host_names=[DUMMY_HOST_NAME3],
                                                        connectivity_types=[NVME_OVER_FC_CONNECTIVITY_TYPE])
        hostname, connectivity_types = self.svc.get_host_by_host_identifiers(
            Initiators([DUMMY_NVME_NQN1], [DUMMY_FC_WWN4], [DUMMY_NODE4_IQN]))
        self.assertEqual(DUMMY_HOST_NAME3, hostname)
        self.assertEqual({NVME_OVER_FC_CONNECTIVITY_TYPE}, connectivity_types)
        self.svc.client.svcinfo.lsnvmefabric.assert_called_once_with(remotenqn=DUMMY_NVME_NQN1)

    @patch.object(SVCResponse, svc_settings.SVC_RESPONSE_AS_LIST, new_callable=PropertyMock)
    def test_get_host_by_identifiers_slow_no_other_ports_return_nvme_host(self, svc_response):
        host_with_nqn = self._get_host_as_munch(DUMMY_HOST_ID1, HOST_NAME,
                                                nqn_list=[DUMMY_NVME_NQN4])
        self._prepare_mocks_for_get_host_by_identifiers_slow(svc_response, custom_host=host_with_nqn)
        hostname, connectivity_types = self.svc.get_host_by_host_identifiers(
            Initiators([DUMMY_NVME_NQN4], [DUMMY_FC_WWN4], [DUMMY_NODE4_IQN]))
        self.assertEqual(HOST_NAME, hostname)
        self.assertEqual([NVME_OVER_FC_CONNECTIVITY_TYPE], connectivity_types)

    @patch.object(SVCResponse, svc_settings.SVC_RESPONSE_AS_LIST, new_callable=PropertyMock)
    def test_get_host_by_identifiers_slow_return_fc_host(self, svc_response):
        host_1 = self._get_host_as_munch(DUMMY_HOST_ID1, DUMMY_HOST_NAME1, wwpns_list=[DUMMY_FC_WWN1],
                                         iscsi_names_list=[])
        host_2 = self._get_host_as_munch(DUMMY_HOST_ID2, DUMMY_HOST_NAME2, wwpns_list=[DUMMY_FC_WWN2],
                                         iscsi_names_list=[])
        host_3 = self._get_host_as_munch(DUMMY_HOST_ID3, DUMMY_HOST_NAME3, wwpns_list=[DUMMY_FC_WWN3, DUMMY_FC_WWN4],
                                         iscsi_names_list=[DUMMY_NODE3_IQN])
        hosts = [host_1, host_2, host_3]
        self._prepare_mocks_for_get_host_by_identifiers_slow(svc_response)
        hostname, connectivity_types = self.svc.get_host_by_host_identifiers(
            Initiators([DUMMY_NVME_NQN4], [DUMMY_FC_WWN4, DUMMY_FC_WWN3], [DUMMY_NODE4_IQN]))
        self.assertEqual(DUMMY_HOST_NAME3, hostname)
        self.assertEqual([FC_CONNECTIVITY_TYPE], connectivity_types)

        svc_response.return_value = hosts
        hostname, connectivity_types = self.svc.get_host_by_host_identifiers(
            Initiators([DUMMY_NVME_NQN4], [DUMMY_FC_WWN3], [DUMMY_NODE4_IQN]))
        self.assertEqual(DUMMY_HOST_NAME3, hostname)
        self.assertEqual([FC_CONNECTIVITY_TYPE], connectivity_types)

    def test_get_host_by_identifiers_return_fc_host(self):
        self._prepare_mocks_for_get_host_by_identifiers(fc_host_names=[DUMMY_HOST_NAME3],
                                                        connectivity_types=[FC_CONNECTIVITY_TYPE])
        hostname, connectivity_types = self.svc.get_host_by_host_identifiers(
            Initiators([DUMMY_NVME_NQN1], [DUMMY_FC_WWN4], [DUMMY_NODE4_IQN]))
        self.assertEqual(DUMMY_HOST_NAME3, hostname)
        self.assertEqual({FC_CONNECTIVITY_TYPE}, connectivity_types)
        self.svc.client.svcinfo.lsfabric.assert_called_once_with(wwpn=DUMMY_FC_WWN4)

    @patch.object(SVCResponse, svc_settings.SVC_RESPONSE_AS_LIST, new_callable=PropertyMock)
    def test_get_host_by_identifiers_slow_no_other_ports_return_fc_host(self, svc_response):
        host_with_wwpn = self._get_host_as_munch(DUMMY_HOST_ID1, HOST_NAME, wwpns_list=[DUMMY_FC_WWN1])
        self._prepare_mocks_for_get_host_by_identifiers_slow(svc_response, custom_host=host_with_wwpn)
        hostname, connectivity_types = self.svc.get_host_by_host_identifiers(
            Initiators([DUMMY_NVME_NQN4], [DUMMY_FC_WWN4, DUMMY_FC_WWN1], [DUMMY_NODE4_IQN]))
        self.assertEqual(HOST_NAME, hostname)
        self.assertEqual([FC_CONNECTIVITY_TYPE], connectivity_types)

    def test_get_host_by_identifiers_no_other_ports_return_fc_host(self):
        self._prepare_mocks_for_get_host_by_identifiers(fc_host_names=["", DUMMY_HOST_NAME2],
                                                        connectivity_types=[FC_CONNECTIVITY_TYPE])
        hostname, connectivity_types = self.svc.get_host_by_host_identifiers(
            Initiators([DUMMY_NVME_NQN4], [DUMMY_FC_WWN4, DUMMY_FC_WWN1], [DUMMY_NODE4_IQN]))
        self.assertEqual(DUMMY_HOST_NAME2, hostname)
        self.assertEqual({FC_CONNECTIVITY_TYPE}, connectivity_types)

    @patch.object(SVCResponse, svc_settings.SVC_RESPONSE_AS_LIST, new_callable=PropertyMock)
    def test_get_host_by_identifiers_slow_with_wrong_fc_iscsi_raise_not_found(self, svc_response):
        host_1 = self._get_host_as_munch(DUMMY_HOST_ID1, DUMMY_HOST_NAME1, wwpns_list=[DUMMY_FC_WWN1],
                                         iscsi_names_list=[])
        host_2 = self._get_host_as_munch(DUMMY_HOST_ID2, DUMMY_HOST_NAME2, wwpns_list=[DUMMY_FC_WWN3],
                                         iscsi_names_list=[DUMMY_NODE2_IQN])
        host_3 = self._get_host_as_munch(DUMMY_HOST_ID3, DUMMY_HOST_NAME3, wwpns_list=[DUMMY_FC_WWN3],
                                         iscsi_names_list=[DUMMY_NODE3_IQN])
        hosts = [host_1, host_2, host_3]
        self._prepare_mocks_for_get_host_by_identifiers_slow(svc_response)
        with self.assertRaises(array_errors.HostNotFoundError):
            self.svc.get_host_by_host_identifiers(Initiators([DUMMY_NVME_NQN4], [], []))
        svc_response.return_value = hosts
        with self.assertRaises(array_errors.HostNotFoundError):
            self.svc.get_host_by_host_identifiers(Initiators([DUMMY_NVME_NQN4],
                                                             [DUMMY_FC_WWN4, DUMMY_FC_WWN2],
                                                             [DUMMY_NODE1_IQN]))

    @patch.object(SVCResponse, svc_settings.SVC_RESPONSE_AS_LIST, new_callable=PropertyMock)
    def test_get_host_by_identifiers_slow_backward_compatible_return_nvme_fc_and_iscsi(self, svc_response):
        self._prepare_mocks_for_get_host_by_identifiers_backward_compatible(svc_response)
        hostname, connectivity_types = self.svc.get_host_by_host_identifiers(
            Initiators([DUMMY_NVME_NQN2], [DUMMY_FC_WWN2], [DUMMY_NODE2_IQN]))
        self.assertEqual(DUMMY_HOST_NAME2, hostname)
        self.assertEqual(
            {NVME_OVER_FC_CONNECTIVITY_TYPE, FC_CONNECTIVITY_TYPE, ISCSI_CONNECTIVITY_TYPE},
            set(connectivity_types))

    @patch.object(SVCResponse, svc_settings.SVC_RESPONSE_AS_LIST, new_callable=PropertyMock)
    def test_get_host_by_identifiers_slow_return_nvme_fc_and_iscsi(self, svc_response):
        self._prepare_mocks_for_get_host_by_identifiers_slow(svc_response)
        hostname, connectivity_types = self.svc.get_host_by_host_identifiers(
            Initiators([DUMMY_NVME_NQN2], [DUMMY_FC_WWN2], [DUMMY_NODE2_IQN]))
        self.assertEqual(DUMMY_HOST_NAME2, hostname)
        self.assertEqual(
            {NVME_OVER_FC_CONNECTIVITY_TYPE, FC_CONNECTIVITY_TYPE, ISCSI_CONNECTIVITY_TYPE},
            set(connectivity_types))

    def test_get_host_by_identifiers_return_nvme_fc_and_iscsi(self):
        self._prepare_mocks_for_get_host_by_identifiers()
        hostname, connectivity_types = self.svc.get_host_by_host_identifiers(
            Initiators([DUMMY_NVME_NQN1], [DUMMY_FC_WWN1], [DUMMY_NODE1_IQN]))
        self.assertEqual(DUMMY_HOST_NAME1, hostname)
        self.assertEqual(
            {NVME_OVER_FC_CONNECTIVITY_TYPE, FC_CONNECTIVITY_TYPE, ISCSI_CONNECTIVITY_TYPE},
            connectivity_types)

    def _get_host_as_munch(self, host_id, host_name, nqn_list=None, wwpns_list=None, iscsi_names_list=None,
                           portset_id=None):
        host = Munch(id=host_id, name=host_name)
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
        mappings = self.svc.get_volume_mappings(VOLUME_UID)
        self.assertEqual({}, mappings)

    def _test_get_volume_mappings_lsvdisk_cli_failure_error(self, volume_name, error_message_id, expected_error):
        self._test_mediator_method_client_cli_failure_error(self.svc.get_volume_mappings, (volume_name,),
                                                            self.svc.client.svcinfo.lsvdisk, error_message_id,
                                                            expected_error)

    def test_get_volume_mappings_lsvdisk_cli_failure_errors(self):
        self._test_get_volume_mappings_lsvdisk_cli_failure_error("\xff", "CMMVC6017E",
                                                                 array_errors.InvalidArgumentError)
        self._test_get_volume_mappings_lsvdisk_cli_failure_error("!@#", "CMMVC5741E", array_errors.InvalidArgumentError)

    def test_get_volume_mappings_on_volume_not_found(self):
        self.svc.client.svcinfo.lsvdiskhostmap.side_effect = [
            svc_errors.CommandExecutionError(DUMMY_ERROR_MESSAGE)]

        with self.assertRaises(array_errors.ObjectNotFoundError):
            self.svc.get_volume_mappings(VOLUME_UID)

    def _mock_cli_host_map(self, hostmap_id, scsi_id, host_id, host_name=HOST_NAME, hostmap_name="host_map_name"):
        return Munch({svc_settings.HOST_MAP_ID_ATTR_KEY: hostmap_id,
                      svc_settings.HOST_MAP_NAME_ATTR_KEY: hostmap_name,
                      svc_settings.HOST_MAP_LUN_ATTR_KEY: scsi_id,
                      svc_settings.HOST_MAP_HOST_ID_ATTR_KEY: host_id,
                      svc_settings.HOST_MAP_HOST_NAME_ATTR_KEY: host_name})

    def test_get_volume_mappings_success(self):
        maps = self._get_mock_host_list(("0", "1"))
        self.svc.client.svcinfo.lsvdiskhostmap.return_value = maps
        mappings = self.svc.get_volume_mappings(VOLUME_UID)
        self.assertEqual({"host_0": "0", "host_1": "1"}, mappings)

    def test_get_free_lun_raises_host_not_found_error(self):
        self.svc.client.svcinfo.lshostvdiskmap.side_effect = [
            svc_errors.CommandExecutionError(DUMMY_ERROR_MESSAGE)]
        with self.assertRaises(array_errors.HostNotFoundError):
            self.svc._get_free_lun(HOST_NAME)

    def _get_mock_host_list(self, lun_list):
        maps = []
        for index, lun in enumerate(lun_list):
            maps.append(self._mock_cli_host_map(hostmap_id=index,
                                                hostmap_name="host_map_{}".format(index),
                                                scsi_id=str(lun),
                                                host_id=index,
                                                host_name="host_{}".format(index)))
        return maps

    def _test_get_free_lun_host_mappings(self, lun_list, expected_lun="0"):
        maps = self._get_mock_host_list(lun_list)
        self.svc.client.svcinfo.lshostvdiskmap.return_value = maps
        lun = self.svc._get_free_lun(HOST_NAME)
        if lun_list:
            self.assertNotIn(lun, lun_list)
        self.assertEqual(lun, expected_lun)

    @patch("controllers.array_action.array_mediator_svc.choice")
    def test_get_free_lun_with_no_host_mappings(self, random_choice):
        random_choice.return_value = "0"
        self._test_get_free_lun_host_mappings([])

    @patch.object(SVCArrayMediator, "MAX_LUN_NUMBER", 2)
    @patch.object(SVCArrayMediator, "MIN_LUN_NUMBER", 0)
    def test_get_free_lun_success(self):
        self._test_get_free_lun_host_mappings(("1", "2"))

    @patch.object(SVCArrayMediator, "MAX_LUN_NUMBER", 4)
    @patch.object(SVCArrayMediator, "MIN_LUN_NUMBER", 0)
    @patch("controllers.array_action.array_mediator_svc.LUN_INTERVAL", 1)
    def test_get_free_lun_in_interval_success(self):
        self._test_get_free_lun_host_mappings(("0", "1"), expected_lun="2")

    @patch.object(SVCArrayMediator, "MAX_LUN_NUMBER", 3)
    @patch.object(SVCArrayMediator, "MIN_LUN_NUMBER", 1)
    def test_free_lun_no_available_lun(self):
        maps = self._get_mock_host_list(("1", "2", "3"))
        self.svc.client.svcinfo.lshostvdiskmap.return_value = maps
        with self.assertRaises(array_errors.NoAvailableLunError):
            self.svc._get_free_lun(HOST_NAME)

    @patch("controllers.array_action.array_mediator_svc.SVCArrayMediator._get_free_lun")
    def _test_map_volume_mkvdiskhostmap_error(self, client_error, expected_error, mock_get_free_lun):
        mock_get_free_lun.return_value = svc_settings.DUMMY_LUN
        self._test_mediator_method_client_error(self.svc.map_volume, (VOLUME_UID, HOST_NAME, DUMMY_CONNECTIVITY_TYPE),
                                                self.svc.client.svctask.mkvdiskhostmap, client_error,
                                                expected_error)

    def test_map_volume_mkvdiskhostmap_errors(self):
        self._test_map_volume_mkvdiskhostmap_error(svc_errors.CommandExecutionError("CMMVC5804E"),
                                                   array_errors.ObjectNotFoundError)
        self._test_map_volume_mkvdiskhostmap_error(svc_errors.CommandExecutionError("CMMVC5754E"),
                                                   array_errors.HostNotFoundError)
        self._test_map_volume_mkvdiskhostmap_error(svc_errors.CommandExecutionError("CMMVC5879E"),
                                                   array_errors.LunAlreadyInUseError)
        self._test_map_volume_mkvdiskhostmap_error(svc_errors.CommandExecutionError(DUMMY_ERROR_MESSAGE),
                                                   array_errors.MappingError)
        self._test_map_volume_mkvdiskhostmap_error(Exception, Exception)

    @patch("controllers.array_action.array_mediator_svc.SVCArrayMediator._get_free_lun")
    def test_map_volume_success(self, mock_get_free_lun):
        mock_get_free_lun.return_value = svc_settings.DUMMY_LUN
        self.svc.client.svctask.mkvdiskhostmap.return_value = None
        self.svc.client.svcinfo.lsvdisk.return_value = Mock(as_single_element=self._get_cli_volume(name=VOLUME_NAME))
        lun = self.svc.map_volume(VOLUME_UID, HOST_NAME, DUMMY_CONNECTIVITY_TYPE)
        self.assertEqual(svc_settings.DUMMY_LUN, lun)
        self.svc.client.svctask.mkvdiskhostmap.assert_called_once_with(host=HOST_NAME, object_id=VOLUME_NAME,
                                                                       force=True,
                                                                       scsi=svc_settings.DUMMY_LUN)

    def test_map_volume_nvme_success(self):
        self.svc.client.svctask.mkvdiskhostmap.return_value = None
        self.svc.client.svcinfo.lsvdisk.return_value = Mock(as_single_element=self._get_cli_volume(name=VOLUME_NAME))
        lun = self.svc.map_volume(VOLUME_UID, HOST_NAME, NVME_OVER_FC_CONNECTIVITY_TYPE)
        self.assertEqual("", lun)
        self.svc.client.svctask.mkvdiskhostmap.assert_called_once_with(host=HOST_NAME, object_id=VOLUME_NAME,
                                                                       force=True)

    def _test_unmap_volume_rmvdiskhostmap_error(self, client_error, expected_error):
        self._test_mediator_method_client_error(self.svc.unmap_volume, (VOLUME_UID, HOST_NAME),
                                                self.svc.client.svctask.rmvdiskhostmap, client_error,
                                                expected_error)

    def test_unmap_volume_rmvdiskhostmap_errors(self):
        self._test_unmap_volume_rmvdiskhostmap_error(svc_errors.CommandExecutionError("CMMVC5753E"),
                                                     array_errors.ObjectNotFoundError)
        self._test_unmap_volume_rmvdiskhostmap_error(svc_errors.CommandExecutionError("CMMVC5754E"),
                                                     array_errors.HostNotFoundError)
        self._test_unmap_volume_rmvdiskhostmap_error(svc_errors.CommandExecutionError("CMMVC5842E"),
                                                     array_errors.VolumeAlreadyUnmappedError)
        self._test_unmap_volume_rmvdiskhostmap_error(svc_errors.CommandExecutionError(DUMMY_ERROR_MESSAGE),
                                                     array_errors.UnmappingError)
        self._test_unmap_volume_rmvdiskhostmap_error(Exception, Exception)

    def test_unmap_volume_success(self):
        self.svc.client.svctask.rmvdiskhostmap.return_value = None
        self.svc.unmap_volume(VOLUME_UID, HOST_NAME)

    def _prepare_mocks_for_get_iscsi_targets(self, portset_id=None):
        host = self._get_host_as_munch(DUMMY_HOST_ID1, HOST_NAME, wwpns_list=[DUMMY_FC_WWN1],
                                       iscsi_names_list=[DUMMY_NODE1_IQN, DUMMY_NODE2_IQN],
                                       portset_id=portset_id)
        self.svc.client.svcinfo.lshost = Mock()
        self.svc.client.svcinfo.lshost.return_value = Mock(as_single_element=host)

    def test_get_iscsi_targets_cmd_error_raise_host_not_found(self):
        self.svc.client.svcinfo.lshost.return_value = Mock(as_single_element=[])
        with self.assertRaises(array_errors.HostNotFoundError):
            self.svc.get_iscsi_targets_by_iqn(HOST_NAME)

    def test_get_iscsi_targets_cmd_error_raise_no_targets_error(self):
        self._prepare_mocks_for_get_iscsi_targets()
        self.svc.client.svcinfo.lsportip.side_effect = [
            svc_errors.CommandExecutionError(DUMMY_ERROR_MESSAGE)]
        with self.assertRaises(array_errors.NoIscsiTargetsFoundError):
            self.svc.get_iscsi_targets_by_iqn(HOST_NAME)

    def test_get_iscsi_targets_cli_error_raise_no_targets_error(self):
        self._prepare_mocks_for_get_iscsi_targets()
        self.svc.client.svcinfo.lsportip.side_effect = [
            CLIFailureError(DUMMY_ERROR_MESSAGE)]
        with self.assertRaises(array_errors.NoIscsiTargetsFoundError):
            self.svc.get_iscsi_targets_by_iqn(HOST_NAME)

    def test_get_iscsi_targets_no_online_node_raise_no_targets_error(self):
        self._prepare_mocks_for_get_iscsi_targets()
        node = self._mock_node(status=svc_settings.OFFLINE_STATUS)
        self.svc.client.svcinfo.lsnode.return_value = [node]
        with self.assertRaises(array_errors.NoIscsiTargetsFoundError):
            self.svc.get_iscsi_targets_by_iqn(HOST_NAME)

    def test_get_iscsi_targets_no_nodes_nor_ips_raise_no_targets_error(self):
        self._prepare_mocks_for_get_iscsi_targets()
        self.svc.client.svcinfo.lsnode.return_value = []
        self.svc.client.svcinfo.lsportip.return_value = []
        with self.assertRaises(array_errors.NoIscsiTargetsFoundError):
            self.svc.get_iscsi_targets_by_iqn(HOST_NAME)

    def test_get_iscsi_targets_no_port_with_ip_raise_no_targets_error(self):
        self._prepare_mocks_for_get_iscsi_targets()
        port_1 = self._mock_cli_port_ip(ip_addr=None, ip_addr6="")
        port_2 = self._mock_cli_port_ip(node_id=svc_settings.DUMMY_INTERNAL_ID2, ip_addr="", ip_addr6=None)
        self.svc.client.svcinfo.lsportip.return_value = [port_1, port_2]
        with self.assertRaises(array_errors.NoIscsiTargetsFoundError):
            self.svc.get_iscsi_targets_by_iqn(HOST_NAME)

    def test_get_iscsi_targets_no_ip_raise_no_targets_error(self):
        self._prepare_mocks_for_get_iscsi_targets()
        self.svc.client.svcinfo.lsportip.return_value = []
        with self.assertRaises(array_errors.NoIscsiTargetsFoundError):
            self.svc.get_iscsi_targets_by_iqn(HOST_NAME)

    def test_get_iscsi_targets_with_lsportip_success(self):
        self._prepare_mocks_for_get_iscsi_targets()
        ips_by_iqn = self.svc.get_iscsi_targets_by_iqn(HOST_NAME)
        self.svc.client.svcinfo.lsportip.assert_called_once()
        self.assertEqual({DUMMY_NODE1_IQN: [svc_settings.DUMMY_IP_ADDRESS1]}, ips_by_iqn)

    def test_get_iscsi_targets_with_lsip_success(self):
        self._prepare_mocks_for_get_iscsi_targets(portset_id=svc_settings.DUMMY_INTERNAL_ID1)
        ips_by_iqn = self.svc.get_iscsi_targets_by_iqn(HOST_NAME)
        filtervalue = self._get_filtervalue(svc_settings.LSIP_PORTSET_ID_ATTR_KEY, svc_settings.DUMMY_INTERNAL_ID1)
        self.svc.client.svcinfo.lsip.assert_called_once_with(filtervalue=filtervalue)
        self.svc.client.svcinfo.lsportip.not_called()
        self.assertEqual({DUMMY_NODE1_IQN: [svc_settings.DUMMY_IP_ADDRESS1]}, ips_by_iqn)

    def test_get_iscsi_targets_with_exception(self):
        self.svc.client.svcinfo.lsnode.side_effect = [Exception]
        with self.assertRaises(Exception):
            self.svc.get_iscsi_targets_by_iqn(HOST_NAME)

    def _mock_cli_port_ip(self, node_id=svc_settings.DUMMY_INTERNAL_ID1, ip_addr=svc_settings.DUMMY_IP_ADDRESS1,
                          ip_addr6=None):
        return Munch({svc_settings.NODE_ID_KEY: node_id,
                      svc_settings.IP_ADDRESS_KEY: ip_addr,
                      svc_settings.ADDRESS_6_ATTR_KEY: ip_addr6})

    def test_get_iscsi_targets_with_multi_nodes(self):
        self._prepare_mocks_for_get_iscsi_targets()
        node1 = self._mock_node()
        node2 = self._mock_node(svc_settings.DUMMY_INTERNAL_ID2, DUMMY_NODE2_NAME,
                                DUMMY_NODE2_IQN)
        self.svc.client.svcinfo.lsnode.return_value = [node1, node2]
        port_1 = self._mock_cli_port_ip()
        port_2 = self._mock_cli_port_ip(ip_addr=svc_settings.DUMMY_IP_ADDRESS2)
        port_3 = self._mock_cli_port_ip(svc_settings.DUMMY_INTERNAL_ID2, "", ip_addr6=svc_settings.DUMMY_IP_ADDRESS_6_1)
        self.svc.client.svcinfo.lsportip.return_value = [port_1, port_2, port_3]

        ips_by_iqn = self.svc.get_iscsi_targets_by_iqn(HOST_NAME)

        self.assertEqual(ips_by_iqn, {
            DUMMY_NODE1_IQN: [svc_settings.DUMMY_IP_ADDRESS1, svc_settings.DUMMY_IP_ADDRESS2],
            DUMMY_NODE2_IQN: ["[{}]".format(svc_settings.DUMMY_IP_ADDRESS_6_1)]})

    def test_get_array_fc_wwns_failed(self):
        self.svc.client.svcinfo.lsfabric.side_effect = [
            svc_errors.CommandExecutionError(DUMMY_ERROR_MESSAGE)]
        with self.assertRaises(svc_errors.CommandExecutionError):
            self.svc.get_array_fc_wwns(HOST_NAME)

    def _mock_lsfabric_port(self, remote_wwpn, remote_nportid, port_id, node_name, local_wwpn, local_port,
                            local_nportid, state):
        return Munch({svc_settings.LSFABRIC_PORT_REMOTE_WWPN_ATTR_KEY: remote_wwpn,
                      svc_settings.LSFABRIC_PORT_REMOTE_NPORTID_ATTR_KEY: remote_nportid,
                      svc_settings.LSFABRIC_PORT_ID_ATTR_KEY: port_id,
                      svc_settings.LSFABRIC_PORT_NODE_NAME_ATTR_KEY: node_name,
                      svc_settings.LSFABRIC_PORT_LOCAL_WWPN_ATTR_KEY: local_wwpn,
                      svc_settings.LSFABRIC_PORT_LOCAL_PORT_ATTR_KEY: local_port,
                      svc_settings.LSFABRIC_PORT_LOCAL_NPORTID_ATTR_KEY: local_nportid,
                      svc_settings.LSFABRIC_PORT_STATE_ATTR_KEY: state,
                      svc_settings.LSFABRIC_PORT_NAME_ATTR_KEY: svc_settings.DUMMY_PORT_NAME,
                      svc_settings.LSFABRIC_PORT_CLUSTER_NAME_ATTR_KEY: "",
                      svc_settings.LSFABRIC_PORT_TYPE_ATTR_KEY: HOST_NAME})

    def test_get_array_fc_wwns_success(self):
        port_1 = self._mock_lsfabric_port(svc_settings.DUMMY_REMOTE_WWPN1,
                                          svc_settings.DUMMY_REMOTE_NPORTID1,
                                          svc_settings.DUMMY_INTERNAL_ID1,
                                          DUMMY_NODE1_NAME,
                                          svc_settings.DUMMY_LOCAL_WWPN1,
                                          svc_settings.DUMMY_LOCAL_PORT1,
                                          svc_settings.DUMMY_LOCAL_NPORTID1,
                                          svc_settings.ACTIVE_STATE)
        port_2 = self._mock_lsfabric_port(svc_settings.DUMMY_REMOTE_WWPN2,
                                          svc_settings.DUMMY_REMOTE_NPORTID2,
                                          svc_settings.DUMMY_INTERNAL_ID2,
                                          DUMMY_NODE2_NAME,
                                          svc_settings.DUMMY_LOCAL_WWPN2,
                                          svc_settings.DUMMY_LOCAL_PORT2,
                                          svc_settings.DUMMY_LOCAL_NPORTID2,
                                          svc_settings.INACTIVE_STATE)
        self.svc.client.svcinfo.lsfabric.return_value = [port_1, port_2]
        wwns = self.svc.get_array_fc_wwns(HOST_NAME)
        self.assertEqual([svc_settings.DUMMY_LOCAL_WWPN1, svc_settings.DUMMY_LOCAL_WWPN2], wwns)

    def _prepare_mocks_for_expand_volume(self):
        volume = Mock(
            as_single_element=self._get_cli_volume(name=VOLUME_NAME, capacity=svc_settings.DUMMY_SMALL_CAPACITY_STR))
        self.svc.client.svcinfo.lsvdisk.return_value = volume
        self.svc.client.svcinfo.lsfcmap.return_value = Mock(as_list=[])

    def test_expand_volume_success(self):
        self._prepare_mocks_for_expand_volume()
        self.svc.expand_volume(VOLUME_UID, svc_settings.DUMMY_CAPACITY_INT)
        self.svc.client.svctask.expandvdisksize.assert_called_once_with(vdisk_id=VOLUME_NAME,
                                                                        unit=svc_settings.BYTE_UNIT_SYMBOL,
                                                                        size=svc_settings.DUMMY_SMALL_CAPACITY_INT)

    def test_expand_volume_success_with_size_rounded_up(self):
        self._prepare_mocks_for_expand_volume()
        self.svc.expand_volume(VOLUME_UID, 513)
        self.svc.client.svctask.expandvdisksize.assert_called_once_with(vdisk_id=VOLUME_NAME,
                                                                        unit=svc_settings.BYTE_UNIT_SYMBOL,
                                                                        size=svc_settings.DUMMY_SMALL_CAPACITY_INT)

    def test_expand_volume_raise_object_in_use(self):
        self._prepare_mocks_for_expand_volume()
        fcmaps = self.fcmaps_as_source
        fcmaps[0].status = svc_settings.DUMMY_FCMAP_BAD_STATUS
        self.svc.client.svcinfo.lsfcmap.side_effect = [Mock(as_list=self.fcmaps), Mock(as_list=fcmaps)]
        with self.assertRaises(array_errors.ObjectIsStillInUseError):
            self.svc.expand_volume(VOLUME_UID, svc_settings.DUMMY_CAPACITY_INT)
        self.svc.client.svctask.expandvdisksize.assert_not_called()

    def test_expand_volume_in_hyperswap(self):
        self._prepare_mocks_for_expand_volume()
        self._prepare_fcmaps_for_hyperswap()
        self.svc.expand_volume(VOLUME_UID, svc_settings.DUMMY_CAPACITY_INT)

        self.svc.client.svctask.expandvolume.assert_called_once_with(object_id=VOLUME_NAME,
                                                                     unit=svc_settings.BYTE_UNIT_SYMBOL,
                                                                     size=svc_settings.DUMMY_SMALL_CAPACITY_INT)
        self.svc.client.svctask.rmfcmap.assert_not_called()

    def test_expand_volume_raise_object_not_found(self):
        self.svc.client.svcinfo.lsvdisk.return_value = Mock(as_single_element=None)
        with self.assertRaises(array_errors.ObjectNotFoundError):
            self.svc.expand_volume(VOLUME_UID, svc_settings.DUMMY_CAPACITY_INT)
        self.svc.client.svctask.expandvdisksize.assert_not_called()

    def _test_expand_volume_expandvdisksize_errors(self, client_error, expected_error):
        self._prepare_mocks_for_expand_volume()
        self._test_mediator_method_client_error(self.svc.expand_volume, (VOLUME_UID, 2),
                                                self.svc.client.svctask.expandvdisksize, client_error, expected_error)

    def test_expand_volume_expandvdisksize_errors(self):
        self._test_expand_volume_expandvdisksize_errors(CLIFailureError("CMMVC5753E"), array_errors.ObjectNotFoundError)
        self._test_expand_volume_expandvdisksize_errors(CLIFailureError("CMMVC8957E"), array_errors.ObjectNotFoundError)
        self._test_expand_volume_expandvdisksize_errors(CLIFailureError("CMMVC5860E"),
                                                        array_errors.NotEnoughSpaceInPool)
        self._test_expand_volume_expandvdisksize_errors(CLIFailureError(DUMMY_ERROR_MESSAGE), CLIFailureError)
        self._test_expand_volume_expandvdisksize_errors(Exception(DUMMY_ERROR_MESSAGE), Exception)

    def _expand_volume_lsvdisk_errors(self, client_error, expected_error, volume_id=VOLUME_UID):
        self._test_mediator_method_client_error(self.svc.expand_volume, (volume_id, svc_settings.DUMMY_CAPACITY_INT),
                                                self.svc.client.svcinfo.lsvdisk, client_error, expected_error)

    def test_expand_volume_lsvdisk_errors(self):
        self._expand_volume_lsvdisk_errors(CLIFailureError("CMMVC6017E"), array_errors.InvalidArgumentError, "\xff")
        self._expand_volume_lsvdisk_errors(CLIFailureError("CMMVC5741E"), array_errors.InvalidArgumentError, "!@#")
        self._expand_volume_lsvdisk_errors(CLIFailureError(DUMMY_ERROR_MESSAGE), CLIFailureError)
        self._expand_volume_lsvdisk_errors(Exception(DUMMY_ERROR_MESSAGE), Exception)

    def test_create_host_nvme_success(self):
        self.svc.create_host(HOST_NAME,
                             Initiators([DUMMY_NVME_NQN1], [DUMMY_FC_WWN1,
                                                            DUMMY_FC_WWN2], [
                                            DUMMY_NODE1_IQN]), "")
        self.svc.client.svctask.mkhost.assert_called_once_with(name=HOST_NAME, nqn=DUMMY_NVME_NQN1,
                                                               protocol="nvme")

    def test_create_host_fc_success(self):
        self.svc.create_host(HOST_NAME, Initiators([], [DUMMY_FC_WWN1, DUMMY_FC_WWN2], [
            DUMMY_NODE1_IQN]), "")
        self.svc.client.svctask.mkhost.assert_called_once_with(name=HOST_NAME,
                                                               fcwwpn=":".join([DUMMY_FC_WWN1, DUMMY_FC_WWN2]))

    def test_create_host_iscsi_success(self):
        self.svc.create_host(HOST_NAME, Initiators([], [], [DUMMY_NODE1_IQN]), "")
        self.svc.client.svctask.mkhost.assert_called_once_with(name=HOST_NAME, iscsiname=DUMMY_NODE1_IQN)
