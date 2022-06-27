import unittest

from mock import patch, Mock, call
from munch import Munch
from pyxcli import errors as xcli_errors

import controllers.array_action.errors as array_errors
from controllers.array_action.array_mediator_xiv import XIVArrayMediator
from controllers.array_action.config import FC_CONNECTIVITY_TYPE
from controllers.array_action.config import ISCSI_CONNECTIVITY_TYPE
from controllers.common.node_info import Initiators
from controllers.tests.array_action.xiv import utils


class TestArrayMediatorXIV(unittest.TestCase):

    def setUp(self):
        self.fqdn = "fqdn"
        with patch("controllers.array_action.array_mediator_xiv.XIVArrayMediator._connect"):
            self.mediator = XIVArrayMediator("user", "password", self.fqdn)
        self.mediator.client = Mock()
        self.required_bytes = 2000

    def test_get_volume_raise_correct_errors(self):
        error_msg = "ex"
        self.mediator.client.cmd.vol_list.side_effect = [Exception("ex")]
        with self.assertRaises(Exception) as ex:
            self.mediator.get_volume("some name", None, False)

        self.assertIn(error_msg, str(ex.exception))

    def test_get_volume_return_correct_value(self):
        xcli_volume = utils.get_mock_xiv_volume(10, "volume_name", "wwn")
        self.mediator.client.cmd.vol_list.return_value = Mock(as_single_element=xcli_volume)
        volume = self.mediator.get_volume("some name", None, False)

        self.assertEqual(xcli_volume.capacity * 512, volume.capacity_bytes)
        self.assertEqual(xcli_volume.capacity * 512, volume.capacity_bytes)

    def test_get_volume_raise_illegal_object_name(self):
        self.mediator.client.cmd.vol_list.side_effect = [xcli_errors.IllegalNameForObjectError("", "volume", "")]
        with self.assertRaises(array_errors.InvalidArgumentError):
            self.mediator.get_volume("volume", None, False)

    def test_get_volume_returns_nothing(self):
        self.mediator.client.cmd.vol_list.return_value = Mock(as_single_element=None)
        with self.assertRaises(array_errors.ObjectNotFoundError):
            self.mediator.get_volume("volume", None, False)

    @patch("controllers.array_action.array_mediator_xiv.XCLIClient")
    def test_connect_errors(self, client):
        client.connect_multiendpoint_ssl.return_value = Mock()
        client.connect_multiendpoint_ssl.side_effect = [xcli_errors.CredentialsError("a", "b", "c")]
        with self.assertRaises(array_errors.CredentialsError):
            self.mediator._connect()

        client.connect_multiendpoint_ssl.side_effect = [xcli_errors.XCLIError()]
        with self.assertRaises(array_errors.CredentialsError):
            self.mediator._connect()

    def test_close(self):
        self.mediator.client.is_connected = lambda: True
        self.mediator.disconnect()
        self.mediator.client.close.assert_called_once_with()

        self.mediator.client.is_connected = lambda: False
        self.mediator.disconnect()
        self.mediator.client.close.assert_called_once_with()

    @staticmethod
    def _get_cli_volume(name='mock_volume', wwn='123'):
        return Munch({
            'wwn': wwn,
            'name': name,
            'id': 'test_id',
            'pool_name': 'fake_pool',
            'capacity': '512',
            'copy_master_wwn': wwn})

    def _test_create_volume_with_space_efficiency_success(self, space_efficiency):
        self.mediator.client.cmd.vol_create = Mock()
        self.mediator.client.cmd.vol_create.return_value = Mock(as_single_element=self._get_cli_volume())
        volume = self.mediator.create_volume("mock_volume", 512, space_efficiency, "fake_pool", None, None, None, None,
                                             False)
        self.mediator.client.cmd.vol_create.assert_called_once_with(vol='mock_volume', size_blocks=1,
                                                                    pool='fake_pool')
        self.assertEqual("mock_volume", volume.name)
        self.assertEqual('test_id', volume.internal_id)

    def test_create_volume_success(self):
        self._test_create_volume_with_space_efficiency_success(None)

    def test_create_volume_with_empty_space_efficiency_success(self):
        self._test_create_volume_with_space_efficiency_success("")

    def test_create_volume_with_not_available_wwn(self):
        self.mediator.client.cmd.vol_create = Mock()
        self.mediator.client.cmd.vol_create.return_value = Mock(
            as_single_element=self._get_cli_volume(wwn="Not Available"))
        volume = self.mediator.create_volume("mock_volume", 512, None, "fake_pool", None, None, None, None, False)

        self.assertIsNone(volume.source_id)

    def test_create_volume_raise_illegal_name_for_object(self):
        self.mediator.client.cmd.vol_create.side_effect = [xcli_errors.IllegalNameForObjectError("", "volume", "")]
        with self.assertRaises(array_errors.InvalidArgumentError):
            self.mediator.create_volume("volume", 10, None, "pool1", None, None, None, None, False)

    def test_create_volume_raise_volume_exists_error(self):
        self.mediator.client.cmd.vol_create.side_effect = [xcli_errors.VolumeExistsError("", "volume", "")]
        with self.assertRaises(array_errors.VolumeAlreadyExists):
            self.mediator.create_volume("volume", 10, None, "pool1", None, None, None, None, False)

    def test_create_volume_raise_pool_does_not_exists_error(self):
        self.mediator.client.cmd.vol_create.side_effect = [xcli_errors.PoolDoesNotExistError("", "pool", "")]
        with self.assertRaises(array_errors.PoolDoesNotExist):
            self.mediator.create_volume("volume", 10, None, "pool1", None, None, None, None, False)

    def test_create_volume_raise_no_space_error(self):
        self.mediator.client.cmd.vol_create.side_effect = [
            xcli_errors.CommandFailedRuntimeError("", "No space to allocate to the volume", "")]
        with self.assertRaises(array_errors.NotEnoughSpaceInPool):
            self.mediator.create_volume("volume", 10, None, "pool1", None, None, None, None, False)

    def test_create_volume_raise_runtime_error(self):
        self.mediator.client.cmd.vol_create.side_effect = [
            xcli_errors.CommandFailedRuntimeError("", "other error", "")]
        with self.assertRaises(xcli_errors.CommandFailedRuntimeError):
            self.mediator.create_volume("volume", 10, None, "pool1", None, None, None, None, False)

    @patch.object(XIVArrayMediator, "_generate_volume_response")
    def test_create_volume__generate_volume_response_raise_exception(self, response):
        response.side_effect = Exception("err")
        with self.assertRaises(Exception):
            self.mediator.create_volume("volume", 10, None, "pool1", None, None, None, None, False)

    def _test_copy_to_existing_volume_from_snapshot(self, source_snapshot_capacity_in_bytes,
                                                    min_volume_size_in_bytes):
        volume_id = "volume_id"
        source_id = "source_id"
        volume_name = "volume"
        source_snapshot_name = "snapshot"
        self.mediator.client.cmd.vol_format = Mock()
        self.mediator.client.cmd.vol_copy = Mock()
        self.mediator.client.cmd.vol_resize = Mock()
        target_volume = self._get_cli_volume(name=volume_name)
        source_volume = self._get_cli_volume(name=source_snapshot_name)
        self.mediator.client.cmd.vol_list.side_effect = [Mock(as_single_element=target_volume),
                                                         Mock(as_single_element=source_volume)]
        self.mediator.copy_to_existing_volume(volume_id, source_id,
                                              source_snapshot_capacity_in_bytes, min_volume_size_in_bytes)
        calls = [call(wwn=volume_id), call(wwn=source_id)]
        self.mediator.client.cmd.vol_list.assert_has_calls(calls, any_order=False)
        self.mediator.client.cmd.vol_format.assert_called_once_with(vol=volume_name)
        self.mediator.client.cmd.vol_copy.assert_called_once_with(vol_src=source_snapshot_name, vol_trg=volume_name)

    def test_copy_to_existing_volume_from_snapshot_succeeds_with_resize(self):
        volume_size_in_blocks = 1
        volume_name = "volume"
        self._test_copy_to_existing_volume_from_snapshot(source_snapshot_capacity_in_bytes=500,
                                                         min_volume_size_in_bytes=1000)

        self.mediator.client.cmd.vol_resize.assert_called_once_with(vol=volume_name, size_blocks=volume_size_in_blocks)

    def test_copy_to_existing_volume_from_snapshot_succeeds_without_resize(self):
        self._test_copy_to_existing_volume_from_snapshot(source_snapshot_capacity_in_bytes=1000,
                                                         min_volume_size_in_bytes=500)

        self.mediator.client.cmd.vol_resize.assert_not_called()

    def _test_copy_to_existing_volume_from_snapshot_error(self, client_method, xcli_exception,
                                                          expected_array_exception):
        client_method.side_effect = [xcli_exception]
        with self.assertRaises(expected_array_exception):
            self.mediator.copy_to_existing_volume("volume", "snapshot", 0, 0)

    def test_copy_to_existing_volume_from_snapshot_failed_illegal_id(self):
        self._test_copy_to_existing_volume_from_snapshot_error(self.mediator.client.cmd.vol_list,
                                                               xcli_errors.IllegalValueForArgumentError("", "", ""),
                                                               array_errors.InvalidArgumentError)

    def test_copy_to_existing_volume_from_snapshot_failed_volume_not_found(self):
        self._test_copy_to_existing_volume_from_snapshot_error(self.mediator.client.cmd.vol_copy,
                                                               xcli_errors.VolumeBadNameError("", "", ""),
                                                               array_errors.ObjectNotFoundError)

    def test_copy_to_existing_volume_from_snapshot_failed_snapshot_not_found(self):
        self._test_copy_to_existing_volume_from_snapshot_error(self.mediator.client.cmd.vol_copy,
                                                               xcli_errors.SourceVolumeBadNameError("", "", ""),
                                                               array_errors.ObjectNotFoundError)

    def test_copy_to_existing_volume_from_snapshot_failed_permission_denied(self):
        self._test_copy_to_existing_volume_from_snapshot_error(
            self.mediator.client.cmd.vol_copy,
            xcli_errors.OperationForbiddenForUserCategoryError("", "", ""),
            array_errors.PermissionDeniedError)

    def test_delete_volume_return_volume_not_found(self):
        self.mediator.client.cmd.vol_list.return_value = Mock(as_single_element=None)
        with self.assertRaises(array_errors.ObjectNotFoundError):
            self.mediator.delete_volume("volume-wwn")

    def _prepare_delete_volume_with_no_snapshots(self):
        self.mediator.client.cmd.snapshot_list.return_value = Mock(as_list=[])

    def test_delete_volume_raise_object_not_found(self):
        self._prepare_delete_volume_with_no_snapshots()
        self.mediator.client.cmd.vol_delete.side_effect = [xcli_errors.VolumeBadNameError("", "volume", "")]
        with self.assertRaises(array_errors.ObjectNotFoundError):
            self.mediator.delete_volume("volume-wwn")

    def test_delete_volume_raise_illegal_object_id(self):
        self.mediator.client.cmd.vol_list.side_effect = [xcli_errors.IllegalValueForArgumentError("", "volume-wwn", "")]
        with self.assertRaises(array_errors.InvalidArgumentError):
            self.mediator.delete_volume("volume-wwn")

    def test_delete_volume_fails_on_permissions(self):
        self._prepare_delete_volume_with_no_snapshots()
        self.mediator.client.cmd.vol_delete.side_effect = [
            xcli_errors.OperationForbiddenForUserCategoryError("", "volume", "")]
        with self.assertRaises(array_errors.PermissionDeniedError):
            self.mediator.delete_volume("volume-wwn")

    def test_delete_volume_with_snapshot(self):
        snapshot_name = "snapshot"
        snapshot_volume_name = "snapshot_volume"
        xcli_snapshot = self._get_single_snapshot_result_mock(snapshot_name, snapshot_volume_name)
        self.mediator.client.cmd.snapshot_list.return_value = Mock(as_list=[xcli_snapshot])
        with self.assertRaises(array_errors.ObjectIsStillInUseError):
            self.mediator.delete_volume("volume-wwn")

    def test_delete_volume_succeeds(self):
        self._prepare_delete_volume_with_no_snapshots()
        self.mediator.client.cmd.vol_delete = Mock()
        self.mediator.delete_volume("volume-wwn")

    def test_get_snapshot_return_correct_value(self):
        snapshot_name = "snapshot"
        snapshot_volume_name = "snapshot_volume"
        snapshot_volume_wwn = "123456789"
        xcli_snapshot = self._get_single_snapshot_result_mock(snapshot_name, snapshot_volume_name)
        self.mediator.client.cmd.vol_list.return_value = xcli_snapshot
        snapshot = self.mediator.get_snapshot(snapshot_volume_wwn, snapshot_name, None, False)
        self.assertEqual(snapshot_name, snapshot.name)
        self.assertEqual(snapshot_volume_wwn, snapshot.source_id)

    def test_get_snapshot_same_name_volume_exists_error(self):
        snapshot_name = "snapshot"
        snapshot_volume_name = ""
        snapshot_volume_wwn = "123456789"
        xcli_snapshot = self._get_single_snapshot_result_mock(snapshot_name, snapshot_volume_name)
        self.mediator.client.cmd.vol_list.return_value = xcli_snapshot
        with self.assertRaises(array_errors.ExpectedSnapshotButFoundVolumeError):
            self.mediator.get_snapshot(snapshot_volume_wwn, snapshot_name, None, False)

    def test_get_snapshot_raise_illegal_object_name(self):
        snapshot_name = "snapshot"
        snapshot_volume_wwn = "123456789"
        self.mediator.client.cmd.vol_list.side_effect = [xcli_errors.IllegalNameForObjectError("", snapshot_name, "")]
        with self.assertRaises(array_errors.InvalidArgumentError):
            self.mediator.get_snapshot(snapshot_volume_wwn, snapshot_name, None, False)

    def test_create_snapshot_succeeds(self):
        snapshot_name = "snapshot"
        snapshot_volume_wwn = "123456789"
        snapshot_volume_name = "snapshot_volume"
        size_in_blocks_string = "10"
        size_in_bytes = int(size_in_blocks_string) * XIVArrayMediator.BLOCK_SIZE_IN_BYTES
        xcli_snapshot = self._get_single_snapshot_result_mock(snapshot_name, snapshot_volume_name,
                                                              snapshot_capacity=size_in_blocks_string)
        self.mediator.client.cmd.snapshot_create.return_value = xcli_snapshot
        snapshot = self.mediator.create_snapshot(snapshot_volume_wwn, snapshot_name, space_efficiency=None, pool=None,
                                                 is_virt_snap_func=False)
        self.assertEqual(snapshot_name, snapshot.name)
        self.assertEqual(snapshot_volume_wwn, snapshot.source_id)
        self.assertEqual(size_in_bytes, snapshot.capacity_bytes)
        self.assertEqual(size_in_bytes, snapshot.capacity_bytes)

    def test_create_snapshot_raise_snapshot_source_pool_mismatch(self):
        snapshot_name = "snapshot"
        snapshot_volume_wwn = "123456789"
        xcli_volume = self._get_cli_volume()
        self.mediator.client.cmd.vol_list.return_value = Mock(as_single_element=xcli_volume)
        with self.assertRaises(array_errors.SnapshotSourcePoolMismatch):
            self.mediator.create_snapshot(snapshot_volume_wwn, snapshot_name, space_efficiency=None,
                                          pool="different_pool", is_virt_snap_func=False)

    def test_create_snapshot_raise_illegal_name_for_object(self):
        self._test_create_snapshot_error(xcli_errors.IllegalNameForObjectError, array_errors.InvalidArgumentError)

    def test_create_snapshot_raise_snapshot_exists_error(self):
        self._test_create_snapshot_error(xcli_errors.VolumeExistsError, array_errors.SnapshotAlreadyExists)

    def test_create_snapshot_raise_volume_does_not_exists_error(self):
        self._test_create_snapshot_error(xcli_errors.VolumeBadNameError, array_errors.ObjectNotFoundError)

    def test_create_snapshot_raise_permission_error(self):
        self._test_create_snapshot_error(xcli_errors.OperationForbiddenForUserCategoryError,
                                         array_errors.PermissionDeniedError)

    def test_create_snapshot_raise_illegal_object_id(self):
        self.mediator.client.cmd.vol_list.side_effect = [xcli_errors.IllegalValueForArgumentError("",
                                                                                                  "snapshot-wwn", "")]
        with self.assertRaises(array_errors.InvalidArgumentError):
            self.mediator.create_snapshot("volume_id", "snapshot", space_efficiency=None, pool="pool1",
                                          is_virt_snap_func=False)

    @patch.object(XIVArrayMediator, "_generate_snapshot_response")
    def test_create_snapshot_generate_snapshot_response_raise_exception(self, response):
        response.side_effect = Exception("err")
        with self.assertRaises(Exception):
            self.mediator.create_snapshot("volume_id", "snapshot", space_efficiency=None, pool="pool1",
                                          is_virt_snap_func=False)

    def _test_create_snapshot_error(self, xcli_exception, expected_exception):
        self.mediator.client.cmd.snapshot_create.side_effect = [xcli_exception("", "snapshot", "")]
        with self.assertRaises(expected_exception):
            self.mediator.create_snapshot("volume_id", "snapshot", space_efficiency=None, pool=None,
                                          is_virt_snap_func=False)

    def _get_single_snapshot_result_mock(self, snapshot_name, snapshot_volume_name, snapshot_capacity="17"):
        snapshot_wwn = "1235678"
        snapshot_volume_wwn = "123456789"
        mock_snapshot = utils.get_mock_xiv_snapshot(snapshot_capacity, snapshot_name, snapshot_wwn,
                                                    snapshot_volume_name, snapshot_volume_wwn)
        return Mock(as_single_element=mock_snapshot)

    def test_delete_snapshot_return_volume_not_found(self):
        self.mediator.client.cmd.vol_list.return_value = Mock(as_single_element=None)
        with self.assertRaises(array_errors.ObjectNotFoundError):
            self.mediator.delete_snapshot("snapshot-wwn", "internal_id")

    def test_delete_snapshot_raise_bad_name_error(self):
        self.mediator.client.cmd.snapshot_delete.side_effect = [xcli_errors.VolumeBadNameError("", "snapshot", "")]
        with self.assertRaises(array_errors.ObjectNotFoundError):
            self.mediator.delete_snapshot("snapshot-wwn", "internal_id")

    def test_delete_snapshot_raise_illegal_object_id(self):
        self.mediator.client.cmd.vol_list.side_effect = [xcli_errors.IllegalValueForArgumentError("",
                                                                                                  "snapshot-wwn", "")]
        with self.assertRaises(array_errors.InvalidArgumentError):
            self.mediator.delete_snapshot("snapshot-wwn", "internal_id")

    def test_delete_snapshot_fails_on_permissions(self):
        self.mediator.client.cmd.snapshot_delete.side_effect = [
            xcli_errors.OperationForbiddenForUserCategoryError("", "snapshot", "")]
        with self.assertRaises(array_errors.PermissionDeniedError):
            self.mediator.delete_snapshot("snapshot-wwn", "internal_id")

    def test_delete_snapshot_succeeds(self):
        self.mediator.client.cmd.snapshot_delete = Mock()
        self.mediator.delete_snapshot("snapshot-wwn", "internal_id")

    def test_get_object_by_id_return_correct_snapshot(self):
        snapshot_name = "snapshot"
        snapshot_volume_name = "snapshot_volume"
        snapshot_volume_wwn = "123456789"
        xcli_snapshot = self._get_single_snapshot_result_mock(snapshot_name, snapshot_volume_name)
        self.mediator.client.cmd.vol_list.return_value = xcli_snapshot
        snapshot = self.mediator.get_object_by_id("1235678", "snapshot")
        self.assertEqual(snapshot.name, snapshot_name)
        self.assertEqual(snapshot.source_id, snapshot_volume_wwn)

    def test_get_object_by_id_return_correct_volume(self):
        volume_name = "volume_name"
        volume_wwn = "wwn"
        volume = utils.get_mock_xiv_volume(10, volume_name, volume_wwn)
        self.mediator.client.cmd.vol_list.return_value = Mock(as_single_element=volume)
        volume = self.mediator.get_object_by_id(volume_wwn, "volume")
        self.assertEqual(volume.name, volume_name)

    def test_get_object_by_id_same_name_volume_exists_error(self):
        snapshot_name = "snapshot"
        snapshot_volume_name = None
        xcli_snapshot = self._get_single_snapshot_result_mock(snapshot_name, snapshot_volume_name)
        self.mediator.client.cmd.vol_list.return_value = xcli_snapshot
        with self.assertRaises(array_errors.ExpectedSnapshotButFoundVolumeError):
            self.mediator.get_object_by_id("1235678", "snapshot")

    def test_get_object_by_id_raise_illegal_object_id(self):
        snapshot_wwn = "snapshot-wwn"
        self.mediator.client.cmd.vol_list.side_effect = [
            xcli_errors.IllegalValueForArgumentError("", snapshot_wwn, "")]
        with self.assertRaises(array_errors.InvalidArgumentError):
            self.mediator.get_object_by_id(snapshot_wwn, "snapshot")

    def test_get_object_by_id_returns_none(self):
        snapshot_wwn = "snapshot-wwn"
        self.mediator.client.cmd.vol_list.return_value = Mock(as_single_element=None)
        returned_value = self.mediator.get_object_by_id(snapshot_wwn, "snapshot")
        self.assertEqual(returned_value, None)

    def test_property(self):
        self.assertEqual(XIVArrayMediator.port, 7778)

    def test_get_host_by_name_success(self):
        host = utils.get_mock_xiv_host("test_host_1", "iqn.test.1", "")
        self.mediator.client.cmd.host_list.return_value = Mock(as_single_element=host)
        host = self.mediator.get_host_by_name('test_host_1')
        self.assertEqual(host.name, "test_host_1")
        self.assertEqual(host.connectivity_types, ['iscsi'])
        self.assertEqual(host.initiators.nvme_nqns, [])
        self.assertEqual(host.initiators.fc_wwns, [])
        self.assertEqual(host.initiators.iscsi_iqns, ["iqn.test.1"])

    def test_get_host_by_name_raise_host_not_found(self):
        self.mediator.client.cmd.host_list.return_value = Mock(as_single_element=None)
        with self.assertRaises(array_errors.HostNotFoundError):
            self.mediator.get_host_by_name('test_host_1')

    def test_get_host_by_identifiers_returns_host_not_found(self):
        nqn = ""
        wwns = ['wwn1', 'wwn2']
        iqn = "iqn"
        host1 = utils.get_mock_xiv_host("host1", "iqn1", "")
        host2 = utils.get_mock_xiv_host("host2", "iqn1", "")
        host3 = utils.get_mock_xiv_host("host3", "iqn2", "")

        self.mediator.client.cmd.host_list.return_value = Mock(as_list=[host1, host2, host3])
        with self.assertRaises(array_errors.HostNotFoundError):
            self.mediator.get_host_by_host_identifiers(Initiators([nqn], wwns, [iqn]))

    def test_get_host_by_identifiers_returns_host_not_found_when_no_hosts_exist(self):
        nqn = ""
        iqn = "iqn"
        self.mediator.client.cmd.host_list.return_value = Mock(as_list=[])
        with self.assertRaises(array_errors.HostNotFoundError):
            self.mediator.get_host_by_host_identifiers(Initiators([nqn], [], [iqn]))

    def test_get_host_by_iscsi_identifiers_succeeds(self):
        nqn = ""
        wwns = []
        iqn = "iqn1"
        right_host = "host1"
        host1 = utils.get_mock_xiv_host(right_host, "iqn1,iqn4", "")
        host2 = utils.get_mock_xiv_host("host2", "iqn2", "")
        host3 = utils.get_mock_xiv_host("host3", "iqn2", "")
        host4 = utils.get_mock_xiv_host("host4", "iqn3", "")

        self.mediator.client.cmd.host_list.return_value = Mock(as_list=[host1, host2, host3, host4])
        host, connectivity_type = self.mediator.get_host_by_host_identifiers(Initiators([nqn], wwns, [iqn]))
        self.assertEqual(host, right_host)
        self.assertEqual(connectivity_type, [ISCSI_CONNECTIVITY_TYPE])

    def test_get_host_by_fc_identifiers_succeeds(self):
        nqn = ""
        wwns = ["wwn2", "wwn5"]
        iqn = "iqn5"
        right_host = "host2"
        host1 = utils.get_mock_xiv_host("host1", "iqn1", "wwn1")
        host2 = utils.get_mock_xiv_host(right_host, "iqn2", "wwn2")
        host3 = utils.get_mock_xiv_host("host3", "iqn2", "wwn3")
        host4 = utils.get_mock_xiv_host("host4", "iqn3", "wwn4")

        self.mediator.client.cmd.host_list.return_value = Mock(as_list=[host1, host2, host3, host4])
        host, connectivity_type = self.mediator.get_host_by_host_identifiers(Initiators([nqn], wwns, [iqn]))
        self.assertEqual(host, right_host)
        self.assertEqual(connectivity_type, [FC_CONNECTIVITY_TYPE])

    def test_get_host_by_iscsi_and_fc_identifiers_succeeds(self):
        nqn = ""
        wwns = ["wwn2", "wwn5"]
        iqn = "iqn2"
        right_host = "host2"
        host1 = utils.get_mock_xiv_host("host1", "iqn1", "wwn1")
        host2 = utils.get_mock_xiv_host(right_host, "iqn2", "wwn2")
        host3 = utils.get_mock_xiv_host("host3", "iqn3", "wwn3")
        host4 = utils.get_mock_xiv_host("host4", "iqn4", "wwn4")

        self.mediator.client.cmd.host_list.return_value = Mock(as_list=[host1, host2, host3, host4])
        host, connectivity_type = self.mediator.get_host_by_host_identifiers(Initiators([nqn], wwns, [iqn]))
        self.assertEqual(host, right_host)
        self.assertEqual(connectivity_type, [FC_CONNECTIVITY_TYPE, ISCSI_CONNECTIVITY_TYPE])

    def test_get_volume_mappings_empty_mapping_list(self):
        # host3 = utils.get_mock_xiv_mapping(2, "host1")

        self.mediator.client.cmd.vol_mapping_list.return_value = Mock(as_list=[])
        mappings = self.mediator.get_volume_mappings("volume")
        self.assertEqual(mappings, {})

    def test_get_volume_mappings_success(self):
        host1 = "host1"
        host2 = "host2"
        map1 = utils.get_mock_xiv_vol_mapping(2, host1)
        map2 = utils.get_mock_xiv_vol_mapping(3, host2)
        self.mediator.client.cmd.vol_mapping_list.return_value = Mock(as_list=[map1, map2])
        mappings = self.mediator.get_volume_mappings("volume")
        self.assertEqual(mappings, {host1: 2, host2: 3})

    def test_get_volume_mappings_on_volume_not_found(self):
        self.mediator.client.cmd.vol_list.return_value = Mock(as_single_element=None)

        with self.assertRaises(array_errors.ObjectNotFoundError):
            self.mediator.get_volume_mappings("volume")

    def test_get_volume_mappings_raise_illegal_object_id(self):
        self.mediator.client.cmd.vol_list.side_effect = [xcli_errors.IllegalValueForArgumentError("", "volume-wwn", "")]

        with self.assertRaises(array_errors.InvalidArgumentError):
            self.mediator.get_volume_mappings("volume-wwn")

    def test_get_next_available_lun_raises_host_bad_name(self):
        # mapping = get_mock_xiv_host_mapping(1)
        self.mediator.client.cmd.mapping_list.side_effect = [xcli_errors.HostBadNameError("", "host", "")]
        with self.assertRaises(array_errors.HostNotFoundError):
            self.mediator._get_next_available_lun("host")

    def test_get_next_available_lun_with_no_host_mappings(self):
        self.mediator.client.cmd.mapping_list.return_value = Mock(as_list=[])
        lun = self.mediator._get_next_available_lun("host")
        self.assertTrue(lun <= self.mediator.MAX_LUN_NUMBER)
        self.assertTrue(lun >= self.mediator.MIN_LUN_NUMBER)

    @patch.object(XIVArrayMediator, "MAX_LUN_NUMBER", 3)
    @patch.object(XIVArrayMediator, "MIN_LUN_NUMBER", 1)
    def test_get_next_available_lun_success(self):
        mapping1 = utils.get_mock_xiv_host_mapping("1")
        mapping2 = utils.get_mock_xiv_host_mapping("3")

        self.mediator.client.cmd.mapping_list.return_value = Mock(as_list=[mapping1, mapping2])
        lun = self.mediator._get_next_available_lun("host")
        self.assertEqual(lun, 2)

    @patch.object(XIVArrayMediator, "MAX_LUN_NUMBER", 3)
    @patch.object(XIVArrayMediator, "MIN_LUN_NUMBER", 1)
    def test_get_next_available_lun_no_available_lun(self):
        mapping1 = utils.get_mock_xiv_host_mapping("1")
        mapping2 = utils.get_mock_xiv_host_mapping("3")
        mapping3 = utils.get_mock_xiv_host_mapping("2")

        self.mediator.client.cmd.mapping_list.return_value = Mock(as_list=[mapping1, mapping2, mapping3])
        with self.assertRaises(array_errors.NoAvailableLunError):
            self.mediator._get_next_available_lun("host")

    def test_map_volume_volume_not_found(self):
        self.mediator.client.cmd.vol_list.return_value = Mock(as_single_element=None)
        with self.assertRaises(array_errors.ObjectNotFoundError):
            self.mediator.map_volume("volume-wwn", "host", "connectivity_type")

    def test_map_volume_raise_illegal_object_id(self):
        self.mediator.client.cmd.vol_list.side_effect = [xcli_errors.IllegalValueForArgumentError("", "volume-wwn", "")]
        with self.assertRaises(array_errors.InvalidArgumentError):
            self.mediator.map_volume("volume-wwn", "host", "connectivity_type")

    @patch.object(XIVArrayMediator, "MAX_LUN_NUMBER", 3)
    @patch.object(XIVArrayMediator, "MIN_LUN_NUMBER", 1)
    def test_map_volume_no_availabe_lun(self):
        mapping1 = utils.get_mock_xiv_host_mapping("1")
        mapping2 = utils.get_mock_xiv_host_mapping("3")
        mapping3 = utils.get_mock_xiv_host_mapping("2")

        self.mediator.client.cmd.mapping_list.return_value = Mock(as_list=[mapping1, mapping2, mapping3])
        with self.assertRaises(array_errors.NoAvailableLunError):
            self.mediator.map_volume("volume-wwn", "host", "connectivity_type")

    def map_volume_with_error(self, xcli_err, status, returned_err):
        self.mediator.client.cmd.map_vol.side_effect = [xcli_err("", status, "")]
        with patch.object(XIVArrayMediator, "_get_next_available_lun"):
            with self.assertRaises(returned_err):
                self.mediator.map_volume("volume-wwn", "host", "connectivity_type")

    def test_map_volume_operation_forbidden(self):
        self.map_volume_with_error(xcli_errors.OperationForbiddenForUserCategoryError, "",
                                   array_errors.PermissionDeniedError)

    def test_map_volume_volume_bad_name(self):
        self.map_volume_with_error(xcli_errors.VolumeBadNameError, "",
                                   array_errors.ObjectNotFoundError)

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
        lun = self.mediator.map_volume("volume-wwn", "host", "connectivity_type")
        self.assertEqual(lun, '5')

    def test_unmap_volume_no_volume_raise_object_not_found(self):
        self.mediator.client.cmd.vol_list.return_value = Mock(as_single_element=None)
        with self.assertRaises(array_errors.ObjectNotFoundError):
            self.mediator.unmap_volume("volume-wwn", "host")

    def test_unmap_volume_raise_illegal_object_id(self):
        self.mediator.client.cmd.vol_list.side_effect = [xcli_errors.IllegalValueForArgumentError("", "volume-wwn", "")]
        with self.assertRaises(array_errors.InvalidArgumentError):
            self.mediator.unmap_volume("volume-wwn", "host")

    def unmap_volume_with_error(self, xcli_err, status, returned_err):
        self.mediator.client.cmd.unmap_vol.side_effect = [xcli_err("", status, "")]
        with self.assertRaises(returned_err):
            self.mediator.unmap_volume("volume-wwn", "host")

    def test_unmap_volume_volume_not_found_error(self):
        self.unmap_volume_with_error(xcli_errors.VolumeBadNameError, "", array_errors.ObjectNotFoundError)

    def test_unmap_volume_host_not_found(self):
        self.unmap_volume_with_error(xcli_errors.HostBadNameError, "", array_errors.HostNotFoundError)

    def test_unmap_volume_operation_forbidden(self):
        self.unmap_volume_with_error(xcli_errors.OperationForbiddenForUserCategoryError, "",
                                     array_errors.PermissionDeniedError)

    def test_unmap_volume_command_runtime_mapping_not_defined(self):
        self.unmap_volume_with_error(xcli_errors.CommandFailedRuntimeError, "The requested mapping is not defined",
                                     array_errors.VolumeAlreadyUnmappedError)

    def test_unmap_volume_command_runtime_other_error(self):
        self.unmap_volume_with_error(xcli_errors.CommandFailedRuntimeError, "", array_errors.UnmappingError)

    def test_unmap_volume_success(self):
        self.mediator.client.cmd.unmap_vol.return_value = None
        self.mediator.unmap_volume("volume-wwn", "host")

    def test_get_iscsi_targets_by_iqn_fail(self):
        self.mediator.client.cmd.config_get.return_value = Mock(as_list=[])
        self.mediator.client.cmd.ipinterface_list.return_value = []

        with self.assertRaises(Exception):
            self.mediator.get_iscsi_targets_by_iqn('test_host')

    def test_get_iscsi_targets_by_iqn_success(self):
        config_param = utils.get_mock_xiv_config_param(name="iscsi_name", value="iqn1")
        self.mediator.client.cmd.config_get.return_value = Mock(as_list=[config_param])
        ip_interface = utils.get_mock_xiv_ip_interface("iSCSI", address="1.2.3.4")
        ip_interface6 = utils.get_mock_xiv_ip_interface("iSCSI", address6="::1")
        self.mediator.client.cmd.ipinterface_list.return_value = [ip_interface, ip_interface6]

        targets_by_iqn = self.mediator.get_iscsi_targets_by_iqn('test_host')

        self.assertEqual(targets_by_iqn, {"iqn1": ["1.2.3.4", "[::1]"]})

    def _prepare_mocks_for_expand_volume(self):
        volume = utils.get_mock_xiv_volume(size="1", name="volume_name", wwn="volume_id")
        self.mediator.client.cmd.vol_list.return_value = Mock(as_single_element=volume)
        return volume

    def test_expand_volume_succeed(self):
        volume = self._prepare_mocks_for_expand_volume()
        required_size_in_blocks = 3
        self.mediator.expand_volume(volume_id=volume.wwn, required_bytes=self.required_bytes)
        self.mediator.client.cmd.vol_resize.assert_called_once_with(vol=volume.name,
                                                                    size_blocks=required_size_in_blocks)

    def test_expand_vol_list_return_none(self):
        volume = self._prepare_mocks_for_expand_volume()
        self.mediator.client.cmd.vol_list.return_value = Mock(as_single_element=None)
        with self.assertRaises(expected_exception=array_errors.ObjectNotFoundError):
            self.mediator.expand_volume(volume_id=volume.wwn, required_bytes=self.required_bytes)

    def _expand_volume_vol_resize_errors(self, returned_error, expected_exception):
        volume = self._prepare_mocks_for_expand_volume()
        self.mediator.client.cmd.vol_resize.side_effect = [returned_error]
        with self.assertRaises(expected_exception=expected_exception):
            self.mediator.expand_volume(volume_id=volume.wwn, required_bytes=self.required_bytes)

    def test_expand_volume_illegal_object_id_error(self):
        volume = self._prepare_mocks_for_expand_volume()
        self.mediator.client.cmd.vol_list.side_effect = [xcli_errors.IllegalValueForArgumentError("", volume.wwn, "")]
        with self.assertRaises(array_errors.InvalidArgumentError):
            self.mediator.expand_volume(volume_id=volume.wwn, required_bytes=self.required_bytes)

    def test_expand_volume_not_found_error(self):
        self._expand_volume_vol_resize_errors(returned_error=xcli_errors.VolumeBadNameError("", "", ""),
                                              expected_exception=array_errors.ObjectNotFoundError)

    def test_expand_volume_not_enough_space_error(self):
        self._expand_volume_vol_resize_errors(
            returned_error=xcli_errors.CommandFailedRuntimeError("", "No space to allocate to the volume", ""),
            expected_exception=array_errors.NotEnoughSpaceInPool)
