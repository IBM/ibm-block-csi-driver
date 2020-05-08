import unittest
from pyxcli import errors as xcli_errors
from controller.array_action.array_mediator_xiv import XIVArrayMediator
from mock import patch, Mock
import controller.array_action.errors as array_errors
from controller.tests.array_action.xiv import utils
from controller.array_action.config import ISCSI_CONNECTIVITY_TYPE
from controller.array_action.config import FC_CONNECTIVITY_TYPE
from controller.common.node_info import Initiators


class TestArrayMediatorXIV(unittest.TestCase):

    @patch("controller.array_action.array_mediator_xiv.XIVArrayMediator._connect")
    def setUp(self, connect):
        self.fqdn = "fqdn"
        self.mediator = XIVArrayMediator("user", "password", self.fqdn)
        self.mediator.client = Mock()

    def test_get_volume_return_correct_errors(self):
        error_msg = "ex"
        self.mediator.client.cmd.vol_list.side_effect = [Exception("ex")]
        with self.assertRaises(Exception) as ex:
            self.mediator.get_volume("some name")

        self.assertTrue(error_msg in str(ex.exception))

    def test_get_volume_return_correct_value(self):
        vol = utils.get_mock_xiv_volume(10, "vol_name", "wwn")
        ret = Mock()
        ret.as_single_element = vol
        self.mediator.client.cmd.vol_list.return_value = ret
        res = self.mediator.get_volume("some name")

        self.assertTrue(res.capacity_bytes == vol.capacity * 512)
        self.assertTrue(res.capacity_bytes == vol.capacity * 512)

    def test_get_volume_returns_illegal_object_name(self):
        self.mediator.client.cmd.vol_list.side_effect = [xcli_errors.IllegalNameForObjectError("", "vol", "")]
        with self.assertRaises(array_errors.IllegalObjectName):
            res = self.mediator.get_volume("vol")

    def test_get_volume_returns_nothing(self):
        ret = Mock()
        ret.as_single_element = None
        self.mediator.client.cmd.vol_list.return_value = ret
        with self.assertRaises(array_errors.VolumeNotFoundError):
            res = self.mediator.get_volume("vol")

    @patch("controller.array_action.array_mediator_xiv.XCLIClient")
    def test_connect_errors(self, client):
        client.connect_multiendpoint_ssl.return_value = Mock()
        client.connect_multiendpoint_ssl.side_effect = [xcli_errors.CredentialsError("a", "b", "c")]
        with self.assertRaises(array_errors.CredentialsError):
            self.mediator._connect()

        client.connect_multiendpoint_ssl.side_effect = [xcli_errors.XCLIError()]
        with self.assertRaises(array_errors.CredentialsError) as ex:
            self.mediator._connect()

    @patch("controller.array_action.array_mediator_xiv.XCLIClient")
    def test_close(self, client):
        self.mediator.client.is_connected = lambda: True
        self.mediator.disconnect()
        self.mediator.client.close.assert_called_once_with()

        self.mediator.client.is_connected = lambda: False
        self.mediator.disconnect()
        self.mediator.client.close.assert_called_once_with()

    def test_create_volume_return_illegal_name_for_object(self):
        self.mediator.client.cmd.vol_create.side_effect = [xcli_errors.IllegalNameForObjectError("", "vol", "")]
        with self.assertRaises(array_errors.IllegalObjectName):
            self.mediator.create_volume("vol", 10, [], "pool1")

    def test_create_volume_return_volume_exists_error(self):
        self.mediator.client.cmd.vol_create.side_effect = [xcli_errors.VolumeExistsError("", "vol", "")]
        with self.assertRaises(array_errors.VolumeAlreadyExists):
            self.mediator.create_volume("vol", 10, [], "pool1")

    def test_create_volume_return_pool_does_not_exists_error(self):
        self.mediator.client.cmd.vol_create.side_effect = [xcli_errors.PoolDoesNotExistError("", "pool", "")]
        with self.assertRaises(array_errors.PoolDoesNotExist):
            self.mediator.create_volume("vol", 10, [], "pool1")

    @patch.object(XIVArrayMediator, "_generate_volume_response")
    def test_create_volume__generate_volume_response_raise_exception(self, response):
        response.side_effect = Exception("err")
        with self.assertRaises(Exception):
            self.mediator.create_volume("vol", 10, [], "pool1")

    def test_delete_volume_return_volume_not_found(self):
        ret = Mock()
        ret.as_single_element = None
        self.mediator.client.cmd.vol_list.return_value = ret
        with self.assertRaises(array_errors.VolumeNotFoundError):
            self.mediator.delete_volume("vol-wwn")

    def test_delete_volume_return_bad_name_error(self):
        self.mediator.client.cmd.vol_delete.side_effect = [xcli_errors.VolumeBadNameError("", "vol", "")]
        with self.assertRaises(array_errors.VolumeNotFoundError):
            self.mediator.delete_volume("vol-wwn")

    def test_delete_volume_fails_on_permissions(self):
        self.mediator.client.cmd.vol_delete.side_effect = [
            xcli_errors.OperationForbiddenForUserCategoryError("", "vol", "")]
        with self.assertRaises(array_errors.PermissionDeniedError):
            self.mediator.delete_volume("vol-wwn")

    def test_delete_volume_succeeds(self):
        self.mediator.client.cmd.vol_delete = Mock()
        self.mediator.delete_volume("vol-wwn")

    def test_get_snapshot_return_correct_value(self):
        snap_name = "snap"
        snap_vol_name = "snap_vol"
        xcli_snap = self._get_single_snapshot_result_mock(snap_name, snap_vol_name)
        self.mediator.client.cmd.vol_list.return_value = xcli_snap
        res = self.mediator.get_snapshot(snap_name)
        self.assertTrue(res.snapshot_name == snap_name)
        self.assertTrue(res.volume_name == snap_vol_name)

    def test_get_snapshot_same_name_vol_exists_error(self):
        snap_name = "snap"
        snap_vol_name = ""
        xcli_snap = self._get_single_snapshot_result_mock(snap_name, snap_vol_name)
        self.mediator.client.cmd.vol_list.return_value = xcli_snap
        with self.assertRaises(array_errors.SnapshotNameBelongsToVolumeError):
            self.mediator.get_snapshot(snap_name)

    def test_get_snapshot_returns_illegal_object_name(self):
        snap_name = "snap"
        self.mediator.client.cmd.vol_list.side_effect = [xcli_errors.IllegalNameForObjectError("", snap_name, "")]
        with self.assertRaises(array_errors.IllegalObjectName):
            self.mediator.get_snapshot(snap_name)

    def test_create_snapshot_succeeds(self):
        snap_name = "snap"
        snap_vol_name = "snap_vol"
        size_in_blocks_string = "10"
        size_in_bytes = int(size_in_blocks_string) * XIVArrayMediator.BLOCK_SIZE_IN_BYTES
        xcli_snap = self._get_single_snapshot_result_mock(snap_name, snap_vol_name, snap_capacity=size_in_blocks_string)
        self.mediator.client.cmd.snapshot_create.return_value = xcli_snap
        res = self.mediator.create_snapshot(snap_name, snap_vol_name)
        self.assertTrue(res.snapshot_name == snap_name)
        self.assertTrue(res.volume_name == snap_vol_name)
        self.assertTrue(res.capacity_bytes == size_in_bytes)
        self.assertTrue(res.capacity_bytes == size_in_bytes)

    def test_create_snapshot_return_illegal_name_for_object(self):
        self._test_create_snapshot_error(xcli_errors.IllegalNameForObjectError, array_errors.IllegalObjectName)

    def test_create_snapshot_return_snapshot_exists_error(self):
        self._test_create_snapshot_error(xcli_errors.VolumeExistsError, array_errors.SnapshotAlreadyExists)

    def test_create_snapshot_return_volume_does_not_exists_error(self):
        self._test_create_snapshot_error(xcli_errors.VolumeBadNameError, array_errors.VolumeNotFoundError)

    def test_create_snapshot_return_permission_error(self):
        self._test_create_snapshot_error(xcli_errors.OperationForbiddenForUserCategoryError,
                                         array_errors.PermissionDeniedError)

    @patch.object(XIVArrayMediator, "_generate_snapshot_response")
    def test_create_snapshot_generate_snapshot_response_raise_exception(self, response):
        response.side_effect = Exception("err")
        with self.assertRaises(Exception):
            self.mediator.create_snapshot("snap", "vol")

    def _test_create_snapshot_error(self, xcli_exception, expected_exception):
        self.mediator.client.cmd.snapshot_create.side_effect = [xcli_exception("", "snap", "")]
        with self.assertRaises(expected_exception):
            self.mediator.create_snapshot("snap", "vol")

    def _get_single_snapshot_result_mock(self, snap_name, snap_vol_name, snap_capacity="17"):
        snap_wwn = "1235678"
        xcli_snap = Mock()
        xcli_snap.as_single_element = utils.get_mock_xiv_snapshot(snap_capacity, snap_name, snap_wwn, snap_vol_name)
        return xcli_snap

    def test_property(self):
        self.assertEqual(XIVArrayMediator.port, 7778)

    def test_get_host_by_identifiers_returns_host_not_found(self):
        iqn = "iqn"
        wwns = ['wwn1', 'wwn2']
        host1 = utils.get_mock_xiv_host("host1", "iqn1", "")
        host2 = utils.get_mock_xiv_host("host2", "iqn1", "")
        host3 = utils.get_mock_xiv_host("host3", "iqn2", "")
        ret = Mock()
        ret.as_list = [host1, host2, host3]

        self.mediator.client.cmd.host_list.return_value = ret
        with self.assertRaises(array_errors.HostNotFoundError):
            self.mediator.get_host_by_host_identifiers(Initiators(iqn, wwns))

    def test_get_host_by_identifiers_returns_host_not_found_when_no_hosts_exist(self):
        iqn = "iqn"
        ret = Mock()
        ret.as_list = []

        self.mediator.client.cmd.host_list.return_value = ret
        with self.assertRaises(array_errors.HostNotFoundError):
            self.mediator.get_host_by_host_identifiers(Initiators(iqn, []))

    def test_get_host_by_iscsi_identifiers_succeeds(self):
        iqn = "iqn1"
        wwns = []
        right_host = "host1"

        host1 = utils.get_mock_xiv_host(right_host, "iqn1,iqn4", "")
        host2 = utils.get_mock_xiv_host("host2", "iqn2", "")
        host3 = utils.get_mock_xiv_host("host3", "iqn2", "")
        host4 = utils.get_mock_xiv_host("host4", "iqn3", "")
        ret = Mock()
        ret.as_list = [host1, host2, host3, host4]

        self.mediator.client.cmd.host_list.return_value = ret
        host, connectivity_type = self.mediator.get_host_by_host_identifiers(Initiators(iqn, wwns))
        self.assertEqual(host, right_host)
        self.assertEqual(connectivity_type, [ISCSI_CONNECTIVITY_TYPE])

    def test_get_host_by_fc_identifiers_succeeds(self):
        iqn = "iqn5"
        wwns = ["wwn2", "wwn5"]
        right_host = "host2"

        host1 = utils.get_mock_xiv_host("host1", "iqn1", "wwn1")
        host2 = utils.get_mock_xiv_host(right_host, "iqn2", "wwn2")
        host3 = utils.get_mock_xiv_host("host3", "iqn2", "wwn3")
        host4 = utils.get_mock_xiv_host("host4", "iqn3", "wwn4")
        ret = Mock()
        ret.as_list = [host1, host2, host3, host4]

        self.mediator.client.cmd.host_list.return_value = ret
        host, connectivity_type = self.mediator.get_host_by_host_identifiers(Initiators(iqn, wwns))
        self.assertEqual(host, right_host)
        self.assertEqual(connectivity_type, [FC_CONNECTIVITY_TYPE])

    def test_get_host_by_iscsi_and_fc_identifiers_succeeds(self):
        iqn = "iqn2"
        wwns = ["wwn2", "wwn5"]
        right_host = "host2"

        host1 = utils.get_mock_xiv_host("host1", "iqn1", "wwn1")
        host2 = utils.get_mock_xiv_host(right_host, "iqn2", "wwn2")
        host3 = utils.get_mock_xiv_host("host3", "iqn3", "wwn3")
        host4 = utils.get_mock_xiv_host("host4", "iqn4", "wwn4")
        ret = Mock()
        ret.as_list = [host1, host2, host3, host4]

        self.mediator.client.cmd.host_list.return_value = ret
        host, connectivity_type = self.mediator.get_host_by_host_identifiers(Initiators(iqn, wwns))
        self.assertEqual(host, right_host)
        self.assertEqual(connectivity_type, [FC_CONNECTIVITY_TYPE, ISCSI_CONNECTIVITY_TYPE])

    def test_get_volume_mappings_empty_mapping_list(self):
        # host3 = utils.get_mock_xiv_mapping(2, "host1")
        ret = Mock()
        ret.as_list = []
        self.mediator.client.cmd.vol_mapping_list.return_value = ret
        mappings = self.mediator.get_volume_mappings("vol")
        self.assertEqual(mappings, {})

    def test_get_volume_mappings_success(self):
        host1 = "host1"
        host2 = "host2"
        map1 = utils.get_mock_xiv_vol_mapping(2, host1)
        map2 = utils.get_mock_xiv_vol_mapping(3, host2)
        ret = Mock()
        ret.as_list = [map1, map2]
        self.mediator.client.cmd.vol_mapping_list.return_value = ret
        mappings = self.mediator.get_volume_mappings("vol")
        self.assertEqual(mappings, {host1: 2, host2: 3})

    def test_get_volume_mappings_on_volume_not_found(self):
        vol = Mock()
        vol.as_single_element = None
        self.mediator.client.cmd.vol_list.return_value = vol

        with self.assertRaises(array_errors.VolumeNotFoundError):
            self.mediator.get_volume_mappings("vol")

    def test_get_next_available_lun_raises_host_bad_name(self):
        # mapping = get_mock_xiv_host_mapping(1)
        self.mediator.client.cmd.mapping_list.side_effect = [xcli_errors.HostBadNameError("", "host", "")]
        with self.assertRaises(array_errors.HostNotFoundError):
            self.mediator._get_next_available_lun("host")

    def test_get_next_available_with_no_host_mappings(self):
        res = Mock()
        res.as_list = []
        self.mediator.client.cmd.mapping_list.return_value = res
        lun = self.mediator._get_next_available_lun("host")
        self.assertTrue(lun <= self.mediator.MAX_LUN_NUMBER)
        self.assertTrue(lun >= self.mediator.MIN_LUN_NUMBER)

    @patch.object(XIVArrayMediator, "MAX_LUN_NUMBER", 3)
    @patch.object(XIVArrayMediator, "MIN_LUN_NUMBER", 1)
    def test_get_next_available_lun_success(self):
        mapping1 = utils.get_mock_xiv_host_mapping(1)
        mapping2 = utils.get_mock_xiv_host_mapping(3)
        res = Mock()
        res.as_list = [mapping1, mapping2]
        self.mediator.client.cmd.mapping_list.return_value = res
        lun = self.mediator._get_next_available_lun("host")
        self.assertEqual(lun, 2)

    @patch.object(XIVArrayMediator, "MAX_LUN_NUMBER", 3)
    @patch.object(XIVArrayMediator, "MIN_LUN_NUMBER", 1)
    def test_get_next_available_lun_no_available_lun(self):
        mapping1 = utils.get_mock_xiv_host_mapping(1)
        mapping2 = utils.get_mock_xiv_host_mapping(3)
        mapping3 = utils.get_mock_xiv_host_mapping(2)
        res = Mock()
        res.as_list = [mapping1, mapping2, mapping3]
        self.mediator.client.cmd.mapping_list.return_value = res
        with self.assertRaises(array_errors.NoAvailableLunError):
            self.mediator._get_next_available_lun("host")

    def test_map_volume_vol_bot_found(self):
        vol = Mock()
        vol.as_single_element = None
        self.mediator.client.cmd.vol_list.return_value = vol
        with self.assertRaises(array_errors.VolumeNotFoundError):
            self.mediator.map_volume("vol", "host")

    @patch.object(XIVArrayMediator, "MAX_LUN_NUMBER", 3)
    @patch.object(XIVArrayMediator, "MIN_LUN_NUMBER", 1)
    def test_map_volume_no_availabe_lun(self):
        mapping1 = utils.get_mock_xiv_host_mapping(1)
        mapping2 = utils.get_mock_xiv_host_mapping(3)
        mapping3 = utils.get_mock_xiv_host_mapping(2)
        res = Mock()
        res.as_list = [mapping1, mapping2, mapping3]
        self.mediator.client.cmd.mapping_list.return_value = res
        with self.assertRaises(array_errors.NoAvailableLunError):
            self.mediator.map_volume("vol", "host")

    @patch.object(XIVArrayMediator, "_get_next_available_lun")
    def map_volume_with_error(self, xcli_err, status, returned_err, get_next_lun):
        self.mediator.client.cmd.map_vol.side_effect = [xcli_err("", status, "")]
        with self.assertRaises(returned_err):
            self.mediator.map_volume("vol", "host")

    def test_map_volume_operation_forbidden(self):
        self.map_volume_with_error(xcli_errors.OperationForbiddenForUserCategoryError, "",
                                   array_errors.PermissionDeniedError)

    def test_map_volume_volume_bad_name(self):
        self.map_volume_with_error(xcli_errors.VolumeBadNameError, "",
                                   array_errors.VolumeNotFoundError)

    def test_map_volume_host_bad_name(self):
        self.map_volume_with_error(xcli_errors.HostBadNameError, "",
                                   array_errors.HostNotFoundError)

    def test_map_volume_command_runtime_lun_in_use_error(self):
        self.map_volume_with_error(xcli_errors.CommandFailedRuntimeError, "LUN is already in use 3",
                                   array_errors.LunAlreadyInUseError)

    def test_map_volume_other_command_runtime_error(self):
        self.map_volume_with_error(xcli_errors.CommandFailedRuntimeError, "",
                                   array_errors.MappingError)

    @patch.object(XIVArrayMediator, "_get_next_available_lun")
    def test_map_volume_success(self, next_lun):
        next_lun.return_value = 5
        self.mediator.client.cmd.map_vol.return_value = None
        lun = self.mediator.map_volume("vol", "host")
        self.assertEqual(lun, '5')

    def test_unmap_volume_volume_not_found(self):
        vol = Mock()
        vol.as_single_element = None
        self.mediator.client.cmd.vol_list.return_value = vol
        with self.assertRaises(array_errors.VolumeNotFoundError):
            self.mediator.unmap_volume("vol", "host")

    def unmap_volume_with_error(self, xcli_err, status, returned_err):
        self.mediator.client.cmd.unmap_vol.side_effect = [xcli_err("", status, "")]
        with self.assertRaises(returned_err):
            self.mediator.unmap_volume("vol", "host")

    def test_unmap_volume_vol_not_found(self):
        self.unmap_volume_with_error(xcli_errors.VolumeBadNameError, "", array_errors.VolumeNotFoundError)

    def test_unmap_volume_host_not_found(self):
        self.unmap_volume_with_error(xcli_errors.HostBadNameError, "", array_errors.HostNotFoundError)

    def test_unmap_volume_operation_forbidden(self):
        self.unmap_volume_with_error(xcli_errors.OperationForbiddenForUserCategoryError, "",
                                     array_errors.PermissionDeniedError)

    def test_unmap_volume_command_runtime_mapping_not_defined(self):
        self.unmap_volume_with_error(xcli_errors.CommandFailedRuntimeError, "The requested mapping is not defined",
                                     array_errors.VolumeAlreadyUnmappedError)

    def test_unmap_volume_command_runtime_other_error(self):
        self.unmap_volume_with_error(xcli_errors.CommandFailedRuntimeError, "", array_errors.UnMappingError)

    def test_unmap_volume_success(self):
        self.mediator.client.cmd.unmap_vol.return_value = None
        self.mediator.unmap_volume("vol", "host")

    def test_get_iscsi_targets_by_iqn_fail(self):
        self.mediator.client.cmd.config_get.return_value = Mock(as_list=[])
        self.mediator.client.cmd.ipinterface_list.return_value = []

        with self.assertRaises(Exception):
            self.mediator.get_iscsi_targets_by_iqn()

    def test_get_iscsi_targets_by_iqn_success(self):
        config_param = utils.get_mock_xiv_config_param(name="iscsi_name", value="iqn1")
        self.mediator.client.cmd.config_get.return_value = Mock(as_list=[config_param])
        ip_interface = utils.get_mock_xiv_ip_interface("iSCSI", address="1.2.3.4")
        ip_interface6 = utils.get_mock_xiv_ip_interface("iSCSI", address6="::1")
        self.mediator.client.cmd.ipinterface_list.return_value = [ip_interface, ip_interface6]

        targets_by_iqn = self.mediator.get_iscsi_targets_by_iqn()

        self.assertEqual(targets_by_iqn, {"iqn1": ["1.2.3.4", "[::1]"]})
