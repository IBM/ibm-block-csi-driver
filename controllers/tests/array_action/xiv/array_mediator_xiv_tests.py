import unittest

from mock import patch, Mock, call
from munch import Munch
from pyxcli import errors as xcli_errors

import controllers.array_action.errors as array_errors
import controllers.tests.array_action.test_settings as array_settings
import controllers.tests.array_action.xiv.test_settings as xiv_settings
import controllers.tests.common.test_settings as common_settings
from controllers.array_action.array_mediator_xiv import XIVArrayMediator
from controllers.common.node_info import Initiators
from controllers.tests.array_action.xiv import utils


class TestArrayMediatorXIV(unittest.TestCase):

    def setUp(self):
        self.endpoint = [common_settings.SECRET_MANAGEMENT_ADDRESS_VALUE]
        with patch("controllers.array_action.array_mediator_xiv.XIVArrayMediator._connect"):
            self.mediator = XIVArrayMediator(common_settings.SECRET_USERNAME_VALUE,
                                             common_settings.SECRET_PASSWORD_VALUE, self.endpoint)
        self.mediator.client = Mock()
        self.required_bytes = 2000

    def test_get_volume_raise_correct_errors(self):
        error_msg = array_settings.DUMMY_ERROR_MESSAGE
        self.mediator.client.cmd.vol_list.side_effect = [Exception(array_settings.DUMMY_ERROR_MESSAGE)]
        with self.assertRaises(Exception) as ex:
            self.mediator.get_volume(common_settings.VOLUME_NAME, None, False)

        self.assertIn(error_msg, str(ex.exception))

    def test_get_volume_return_correct_value(self):
        xcli_volume = utils.get_mock_xiv_volume(10, common_settings.VOLUME_NAME, common_settings.VOLUME_UID)
        self.mediator.client.cmd.vol_list.return_value = Mock(as_single_element=xcli_volume)
        volume = self.mediator.get_volume(common_settings.VOLUME_NAME, None, False)

        self.assertEqual(xcli_volume.capacity * array_settings.DUMMY_SMALL_CAPACITY_INT, volume.capacity_bytes)
        self.assertEqual(xcli_volume.capacity * array_settings.DUMMY_SMALL_CAPACITY_INT, volume.capacity_bytes)

    def test_get_volume_raise_illegal_object_name(self):
        self.mediator.client.cmd.vol_list.side_effect = [xcli_errors.IllegalNameForObjectError("", "", "")]
        with self.assertRaises(array_errors.InvalidArgumentError):
            self.mediator.get_volume(common_settings.VOLUME_NAME, None, False)

    def test_get_volume_returns_nothing(self):
        self.mediator.client.cmd.vol_list.return_value = Mock(as_single_element=None)
        with self.assertRaises(array_errors.ObjectNotFoundError):
            self.mediator.get_volume(common_settings.VOLUME_NAME, None, False)

    @patch("controllers.array_action.array_mediator_xiv.XCLIClient")
    def test_connect_errors(self, client):
        client.connect_multiendpoint_ssl.return_value = Mock()
        client.connect_multiendpoint_ssl.side_effect = [xcli_errors.CredentialsError("", "", "")]
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
    def _get_cli_volume(name=common_settings.VOLUME_NAME, wwn=common_settings.VOLUME_UID):
        return Munch({
            xiv_settings.VOLUME_UID_ATTR_KEY: wwn,
            array_settings.VOLUME_NAME_ATTR_KEY: name,
            array_settings.VOLUME_ID_ATTR_KEY: common_settings.INTERNAL_VOLUME_ID,
            xiv_settings.VOLUME_POOL_NAME_ATTR_KEY: common_settings.DUMMY_POOL1,
            xiv_settings.VOLUME_CAPACITY_ATTR_KEY: array_settings.DUMMY_SMALL_CAPACITY_STR,
            xiv_settings.VOLUME_COPY_MASTER_WWN_ATTR_KEY: wwn})

    def _test_create_volume_with_space_efficiency_success(self, space_efficiency):
        self.mediator.client.cmd.vol_create = Mock()
        self.mediator.client.cmd.vol_create.return_value = Mock(as_single_element=self._get_cli_volume())
        volume = self.mediator.create_volume(common_settings.VOLUME_NAME, array_settings.DUMMY_SMALL_CAPACITY_INT,
                                             space_efficiency,
                                             common_settings.DUMMY_POOL1, None,
                                             None, None, None,
                                             False)
        self.mediator.client.cmd.vol_create.assert_called_once_with(vol=common_settings.VOLUME_NAME, size_blocks=1,
                                                                    pool=common_settings.DUMMY_POOL1)
        self.assertEqual(common_settings.VOLUME_NAME, volume.name)
        self.assertEqual(common_settings.INTERNAL_VOLUME_ID, volume.internal_id)

    def test_create_volume_success(self):
        self._test_create_volume_with_space_efficiency_success(None)

    def test_create_volume_with_empty_space_efficiency_success(self):
        self._test_create_volume_with_space_efficiency_success("")

    def test_create_volume_with_not_available_wwn(self):
        self.mediator.client.cmd.vol_create = Mock()
        self.mediator.client.cmd.vol_create.return_value = Mock(
            as_single_element=self._get_cli_volume(wwn="Not Available"))
        volume = self.mediator.create_volume(common_settings.VOLUME_NAME, array_settings.DUMMY_SMALL_CAPACITY_INT, None,
                                             common_settings.DUMMY_POOL1, None, None, None,
                                             None, False)

        self.assertIsNone(volume.source_id)

    def test_create_volume_raise_illegal_name_for_object(self):
        self.mediator.client.cmd.vol_create.side_effect = [
            xcli_errors.IllegalNameForObjectError("", common_settings.VOLUME_NAME, "")]
        with self.assertRaises(array_errors.InvalidArgumentError):
            self.mediator.create_volume(common_settings.VOLUME_NAME, 10, None, common_settings.DUMMY_POOL1, None, None,
                                        None, None, False)

    def test_create_volume_raise_volume_exists_error(self):
        self.mediator.client.cmd.vol_create.side_effect = [
            xcli_errors.VolumeExistsError("", common_settings.VOLUME_NAME, "")]
        with self.assertRaises(array_errors.VolumeAlreadyExists):
            self.mediator.create_volume(common_settings.VOLUME_NAME, 10, None, common_settings.DUMMY_POOL1, None, None,
                                        None, None, False)

    def test_create_volume_raise_pool_does_not_exists_error(self):
        self.mediator.client.cmd.vol_create.side_effect = [xcli_errors.PoolDoesNotExistError("", "", "")]
        with self.assertRaises(array_errors.PoolDoesNotExist):
            self.mediator.create_volume(common_settings.VOLUME_NAME, 10, None, common_settings.DUMMY_POOL1, None, None,
                                        None, None, False)

    def test_create_volume_raise_no_space_error(self):
        self.mediator.client.cmd.vol_create.side_effect = [
            xcli_errors.CommandFailedRuntimeError("", "No space to allocate to the volume", "")]
        with self.assertRaises(array_errors.NotEnoughSpaceInPool):
            self.mediator.create_volume(common_settings.VOLUME_NAME, 10, None, common_settings.DUMMY_POOL1, None, None,
                                        None, None, False)

    def test_create_volume_raise_runtime_error(self):
        self.mediator.client.cmd.vol_create.side_effect = [
            xcli_errors.CommandFailedRuntimeError("", array_settings.DUMMY_ERROR_MESSAGE, "")]
        with self.assertRaises(xcli_errors.CommandFailedRuntimeError):
            self.mediator.create_volume(common_settings.VOLUME_NAME, 10, None, common_settings.DUMMY_POOL1, None, None,
                                        None, None, False)

    @patch.object(XIVArrayMediator, "_generate_volume_response")
    def test_create_volume__generate_volume_response_raise_exception(self, response):
        response.side_effect = Exception(array_settings.DUMMY_ERROR_MESSAGE)
        with self.assertRaises(Exception):
            self.mediator.create_volume(common_settings.VOLUME_NAME, 10, None, common_settings.DUMMY_POOL1, None, None,
                                        None, None, False)

    def _test_copy_to_existing_volume_from_snapshot(self, source_snapshot_capacity_in_bytes,
                                                    min_volume_size_in_bytes):
        self.mediator.client.cmd.vol_format = Mock()
        self.mediator.client.cmd.vol_copy = Mock()
        self.mediator.client.cmd.vol_resize = Mock()
        target_volume = self._get_cli_volume(name=common_settings.VOLUME_NAME)
        source_volume = self._get_cli_volume(name=common_settings.SNAPSHOT_NAME)
        self.mediator.client.cmd.vol_list.side_effect = [Mock(as_single_element=target_volume),
                                                         Mock(as_single_element=source_volume)]
        self.mediator.copy_to_existing_volume(common_settings.VOLUME_UID, common_settings.SOURCE_ID,
                                              source_snapshot_capacity_in_bytes, min_volume_size_in_bytes)
        calls = [call(wwn=common_settings.VOLUME_UID), call(wwn=common_settings.SOURCE_ID)]
        self.mediator.client.cmd.vol_list.assert_has_calls(calls, any_order=False)
        self.mediator.client.cmd.vol_format.assert_called_once_with(vol=common_settings.VOLUME_NAME)
        self.mediator.client.cmd.vol_copy.assert_called_once_with(vol_src=common_settings.SNAPSHOT_NAME,
                                                                  vol_trg=common_settings.VOLUME_NAME)

    def test_copy_to_existing_volume_from_snapshot_succeeds_with_resize(self):
        volume_size_in_blocks = 1
        self._test_copy_to_existing_volume_from_snapshot(source_snapshot_capacity_in_bytes=500,
                                                         min_volume_size_in_bytes=1000)

        self.mediator.client.cmd.vol_resize.assert_called_once_with(vol=common_settings.VOLUME_NAME,
                                                                    size_blocks=volume_size_in_blocks)

    def test_copy_to_existing_volume_from_snapshot_succeeds_without_resize(self):
        self._test_copy_to_existing_volume_from_snapshot(source_snapshot_capacity_in_bytes=1000,
                                                         min_volume_size_in_bytes=500)

        self.mediator.client.cmd.vol_resize.assert_not_called()

    def _test_copy_to_existing_volume_from_snapshot_error(self, client_method, xcli_exception,
                                                          expected_array_exception):
        client_method.side_effect = [xcli_exception]
        with self.assertRaises(expected_array_exception):
            self.mediator.copy_to_existing_volume(common_settings.VOLUME_UID, common_settings.SNAPSHOT_VOLUME_UID, 0, 0)

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
            self.mediator.delete_volume(common_settings.VOLUME_UID)

    def _prepare_delete_volume_with_no_snapshots(self):
        self.mediator.client.cmd.snapshot_list.return_value = Mock(as_list=[])

    def test_delete_volume_raise_object_not_found(self):
        self._prepare_delete_volume_with_no_snapshots()
        self.mediator.client.cmd.vol_delete.side_effect = [
            xcli_errors.VolumeBadNameError("", common_settings.VOLUME_NAME, "")]
        with self.assertRaises(array_errors.ObjectNotFoundError):
            self.mediator.delete_volume(common_settings.VOLUME_UID)

    def test_delete_volume_raise_illegal_object_id(self):
        self.mediator.client.cmd.vol_list.side_effect = [xcli_errors.IllegalValueForArgumentError("", "", "")]
        with self.assertRaises(array_errors.InvalidArgumentError):
            self.mediator.delete_volume(common_settings.VOLUME_UID)

    def test_delete_volume_fails_on_permissions(self):
        self._prepare_delete_volume_with_no_snapshots()
        self.mediator.client.cmd.vol_delete.side_effect = [
            xcli_errors.OperationForbiddenForUserCategoryError("", common_settings.VOLUME_NAME, "")]
        with self.assertRaises(array_errors.PermissionDeniedError):
            self.mediator.delete_volume(common_settings.VOLUME_UID)

    def test_delete_volume_with_snapshot(self):
        xcli_snapshot = self._get_single_snapshot_result_mock(common_settings.SNAPSHOT_NAME,
                                                              common_settings.VOLUME_NAME)
        self.mediator.client.cmd.snapshot_list.return_value = Mock(as_list=[xcli_snapshot])
        with self.assertRaises(array_errors.ObjectIsStillInUseError):
            self.mediator.delete_volume(common_settings.VOLUME_UID)

    def test_delete_volume_succeeds(self):
        self._prepare_delete_volume_with_no_snapshots()
        self.mediator.client.cmd.vol_delete = Mock()
        self.mediator.delete_volume(common_settings.VOLUME_UID)

    def test_get_snapshot_return_correct_value(self):
        xcli_snapshot = self._get_single_snapshot_result_mock(common_settings.SNAPSHOT_NAME,
                                                              common_settings.VOLUME_NAME)
        self.mediator.client.cmd.vol_list.return_value = xcli_snapshot
        snapshot = self.mediator.get_snapshot(common_settings.VOLUME_UID, common_settings.SNAPSHOT_NAME, None, False)
        self.assertEqual(common_settings.SNAPSHOT_NAME, snapshot.name)
        self.assertEqual(common_settings.VOLUME_UID, snapshot.source_id)

    def test_get_snapshot_same_name_volume_exists_error(self):
        xcli_snapshot = self._get_single_snapshot_result_mock(common_settings.SNAPSHOT_NAME, "")
        self.mediator.client.cmd.vol_list.return_value = xcli_snapshot
        with self.assertRaises(array_errors.ExpectedSnapshotButFoundVolumeError):
            self.mediator.get_snapshot(common_settings.VOLUME_UID, common_settings.SNAPSHOT_NAME, None, False)

    def test_get_snapshot_raise_illegal_object_name(self):
        self.mediator.client.cmd.vol_list.side_effect = \
            [xcli_errors.IllegalNameForObjectError("", common_settings.SNAPSHOT_NAME, "")]
        with self.assertRaises(array_errors.InvalidArgumentError):
            self.mediator.get_snapshot(common_settings.VOLUME_UID, common_settings.SNAPSHOT_NAME, None, False)

    def test_create_snapshot_succeeds(self):
        size_in_blocks_string = "10"
        size_in_bytes = int(size_in_blocks_string) * XIVArrayMediator.BLOCK_SIZE_IN_BYTES
        xcli_snapshot = self._get_single_snapshot_result_mock(common_settings.SNAPSHOT_NAME,
                                                              common_settings.VOLUME_NAME,
                                                              snapshot_capacity=size_in_blocks_string)
        self.mediator.client.cmd.snapshot_create.return_value = xcli_snapshot
        snapshot = self.mediator.create_snapshot(common_settings.VOLUME_UID, common_settings.SNAPSHOT_NAME,
                                                 space_efficiency=None, pool=None, is_virt_snap_func=False)
        self.assertEqual(common_settings.SNAPSHOT_NAME, snapshot.name)
        self.assertEqual(common_settings.VOLUME_UID, snapshot.source_id)
        self.assertEqual(size_in_bytes, snapshot.capacity_bytes)
        self.assertEqual(size_in_bytes, snapshot.capacity_bytes)

    def test_create_snapshot_raise_snapshot_source_pool_mismatch(self):
        xcli_volume = self._get_cli_volume()
        self.mediator.client.cmd.vol_list.return_value = Mock(as_single_element=xcli_volume)
        with self.assertRaises(array_errors.SnapshotSourcePoolMismatch):
            self.mediator.create_snapshot(common_settings.VOLUME_UID, common_settings.SNAPSHOT_NAME,
                                          space_efficiency=None, pool=common_settings.DUMMY_POOL2,
                                          is_virt_snap_func=False)

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
        self.mediator.client.cmd.vol_list.side_effect = [xcli_errors.IllegalValueForArgumentError("", "", "")]
        with self.assertRaises(array_errors.InvalidArgumentError):
            self.mediator.create_snapshot(common_settings.VOLUME_UID, common_settings.SNAPSHOT_NAME,
                                          space_efficiency=None, pool=common_settings.DUMMY_POOL1,
                                          is_virt_snap_func=False)

    @patch.object(XIVArrayMediator, "_generate_snapshot_response")
    def test_create_snapshot_generate_snapshot_response_raise_exception(self, response):
        response.side_effect = Exception(array_settings.DUMMY_ERROR_MESSAGE)
        with self.assertRaises(Exception):
            self.mediator.create_snapshot(common_settings.VOLUME_UID, common_settings.SNAPSHOT_NAME,
                                          space_efficiency=None, pool=common_settings.DUMMY_POOL1,
                                          is_virt_snap_func=False)

    def _test_create_snapshot_error(self, xcli_exception, expected_exception):
        self.mediator.client.cmd.snapshot_create.side_effect = [xcli_exception("", common_settings.SNAPSHOT_NAME, "")]
        with self.assertRaises(expected_exception):
            self.mediator.create_snapshot(common_settings.VOLUME_UID, common_settings.SNAPSHOT_NAME,
                                          space_efficiency=None, pool=None,
                                          is_virt_snap_func=False)

    def _get_single_snapshot_result_mock(self, snapshot_name, snapshot_volume_name, snapshot_capacity="17"):
        snapshot_wwn = common_settings.SNAPSHOT_VOLUME_UID
        snapshot_volume_wwn = common_settings.VOLUME_UID
        mock_snapshot = utils.get_mock_xiv_snapshot(snapshot_capacity, snapshot_name, snapshot_wwn,
                                                    snapshot_volume_name, snapshot_volume_wwn)
        return Mock(as_single_element=mock_snapshot)

    def test_delete_snapshot_return_volume_not_found(self):
        self.mediator.client.cmd.vol_list.return_value = Mock(as_single_element=None)
        with self.assertRaises(array_errors.ObjectNotFoundError):
            self.mediator.delete_snapshot(common_settings.SNAPSHOT_VOLUME_UID, common_settings.INTERNAL_SNAPSHOT_ID)

    def test_delete_snapshot_raise_bad_name_error(self):
        self.mediator.client.cmd.snapshot_delete.side_effect = [xcli_errors.VolumeBadNameError("", "", "")]
        with self.assertRaises(array_errors.ObjectNotFoundError):
            self.mediator.delete_snapshot(common_settings.SNAPSHOT_VOLUME_UID, common_settings.INTERNAL_SNAPSHOT_ID)

    def test_delete_snapshot_raise_illegal_object_id(self):
        self.mediator.client.cmd.vol_list.side_effect = [xcli_errors.IllegalValueForArgumentError("", "", "")]
        with self.assertRaises(array_errors.InvalidArgumentError):
            self.mediator.delete_snapshot(common_settings.SNAPSHOT_VOLUME_UID, common_settings.INTERNAL_SNAPSHOT_ID)

    def test_delete_snapshot_fails_on_permissions(self):
        self.mediator.client.cmd.snapshot_delete.side_effect = [
            xcli_errors.OperationForbiddenForUserCategoryError("", common_settings.SNAPSHOT_NAME, "")]
        with self.assertRaises(array_errors.PermissionDeniedError):
            self.mediator.delete_snapshot(common_settings.SNAPSHOT_VOLUME_UID, common_settings.INTERNAL_SNAPSHOT_ID)

    def test_delete_snapshot_succeeds(self):
        self.mediator.client.cmd.snapshot_delete = Mock()
        self.mediator.delete_snapshot(common_settings.SNAPSHOT_VOLUME_UID, common_settings.INTERNAL_SNAPSHOT_ID)

    def test_get_object_by_id_return_correct_snapshot(self):
        xcli_snapshot = self._get_single_snapshot_result_mock(common_settings.SNAPSHOT_NAME,
                                                              common_settings.VOLUME_NAME)
        self.mediator.client.cmd.vol_list.return_value = xcli_snapshot
        snapshot = self.mediator.get_object_by_id(common_settings.SNAPSHOT_VOLUME_UID,
                                                  common_settings.SNAPSHOT_OBJECT_TYPE)
        self.assertEqual(snapshot.name, common_settings.SNAPSHOT_NAME)
        self.assertEqual(snapshot.source_id, common_settings.VOLUME_UID)

    def test_get_object_by_id_return_correct_volume(self):
        volume = utils.get_mock_xiv_volume(10, common_settings.VOLUME_NAME, common_settings.VOLUME_UID)
        self.mediator.client.cmd.vol_list.return_value = Mock(as_single_element=volume)
        volume = self.mediator.get_object_by_id(common_settings.VOLUME_UID, common_settings.VOLUME_OBJECT_TYPE)
        self.assertEqual(volume.name, common_settings.VOLUME_NAME)

    def test_get_object_by_id_same_name_volume_exists_error(self):
        xcli_snapshot = self._get_single_snapshot_result_mock(common_settings.SNAPSHOT_NAME, None)
        self.mediator.client.cmd.vol_list.return_value = xcli_snapshot
        with self.assertRaises(array_errors.ExpectedSnapshotButFoundVolumeError):
            self.mediator.get_object_by_id(common_settings.SNAPSHOT_VOLUME_UID, common_settings.SNAPSHOT_OBJECT_TYPE)

    def test_get_object_by_id_raise_illegal_object_id(self):
        snapshot_wwn = common_settings.SNAPSHOT_VOLUME_UID
        self.mediator.client.cmd.vol_list.side_effect = [
            xcli_errors.IllegalValueForArgumentError("", snapshot_wwn, "")]
        with self.assertRaises(array_errors.InvalidArgumentError):
            self.mediator.get_object_by_id(snapshot_wwn, common_settings.SNAPSHOT_OBJECT_TYPE)

    def test_get_object_by_id_returns_none(self):
        snapshot_wwn = common_settings.SNAPSHOT_VOLUME_UID
        self.mediator.client.cmd.vol_list.return_value = Mock(as_single_element=None)
        returned_value = self.mediator.get_object_by_id(snapshot_wwn, common_settings.SNAPSHOT_OBJECT_TYPE)
        self.assertEqual(returned_value, None)

    def test_property(self):
        self.assertEqual(XIVArrayMediator.port, 7778)

    def test_get_host_by_name_success(self):
        host = utils.get_mock_xiv_host(array_settings.DUMMY_HOST_NAME1, array_settings.DUMMY_NODE1_IQN, "")
        self.mediator.client.cmd.host_list.return_value = Mock(as_single_element=host)
        host = self.mediator.get_host_by_name(array_settings.DUMMY_HOST_NAME1)
        self.assertEqual(host.name, array_settings.DUMMY_HOST_NAME1)
        self.assertEqual(host.connectivity_types, [array_settings.ISCSI_CONNECTIVITY_TYPE])
        self.assertEqual(host.initiators.nvme_nqns, [])
        self.assertEqual(host.initiators.fc_wwns, [])
        self.assertEqual(host.initiators.iscsi_iqns, [array_settings.DUMMY_NODE1_IQN])

    def test_get_host_by_name_raise_host_not_found(self):
        self.mediator.client.cmd.host_list.return_value = Mock(as_single_element=None)
        with self.assertRaises(array_errors.HostNotFoundError):
            self.mediator.get_host_by_name(array_settings.DUMMY_HOST_NAME1)

    def test_get_host_by_identifiers_returns_host_not_found(self):
        nqn = ""
        wwns = [array_settings.DUMMY_FC_WWN1, array_settings.DUMMY_FC_WWN2]
        iqn = array_settings.DUMMY_NODE5_IQN
        host1 = utils.get_mock_xiv_host(array_settings.DUMMY_HOST_ID1, array_settings.DUMMY_NODE1_IQN, "")
        host2 = utils.get_mock_xiv_host(array_settings.DUMMY_HOST_ID2, array_settings.DUMMY_NODE1_IQN, "")
        host3 = utils.get_mock_xiv_host(array_settings.DUMMY_HOST_ID3, array_settings.DUMMY_NODE2_IQN, "")

        self.mediator.client.cmd.host_list.return_value = Mock(as_list=[host1, host2, host3])
        with self.assertRaises(array_errors.HostNotFoundError):
            self.mediator.get_host_by_host_identifiers(Initiators([nqn], wwns, [iqn]))

    def test_get_host_by_identifiers_returns_host_not_found_when_no_hosts_exist(self):
        nqn = ""
        iqn = array_settings.DUMMY_NODE5_IQN
        self.mediator.client.cmd.host_list.return_value = Mock(as_list=[])
        with self.assertRaises(array_errors.HostNotFoundError):
            self.mediator.get_host_by_host_identifiers(Initiators([nqn], [], [iqn]))

    def test_get_host_by_iscsi_identifiers_succeeds(self):
        nqn = ""
        wwns = []
        iqn = array_settings.DUMMY_NODE1_IQN
        right_host = array_settings.DUMMY_HOST_ID1
        host1 = utils.get_mock_xiv_host(right_host, ",".join([array_settings.DUMMY_NODE1_IQN,
                                                              array_settings.DUMMY_NODE4_IQN]), "")
        host2 = utils.get_mock_xiv_host(array_settings.DUMMY_HOST_ID2, array_settings.DUMMY_NODE2_IQN, "")
        host3 = utils.get_mock_xiv_host(array_settings.DUMMY_HOST_ID3, array_settings.DUMMY_NODE2_IQN, "")
        host4 = utils.get_mock_xiv_host(array_settings.DUMMY_HOST_ID4, array_settings.DUMMY_NODE3_IQN, "")

        self.mediator.client.cmd.host_list.return_value = Mock(as_list=[host1, host2, host3, host4])
        host, connectivity_type = self.mediator.get_host_by_host_identifiers(Initiators([nqn], wwns, [iqn]))
        self.assertEqual(host, right_host)
        self.assertEqual(connectivity_type, [array_settings.ISCSI_CONNECTIVITY_TYPE])

    def test_get_host_by_fc_identifiers_succeeds(self):
        nqn = ""
        wwns = [array_settings.DUMMY_FC_WWN2, array_settings.DUMMY_FC_WWN5]
        iqn = array_settings.DUMMY_NODE5_IQN
        right_host = array_settings.DUMMY_HOST_ID2
        host1 = utils.get_mock_xiv_host(array_settings.DUMMY_HOST_ID1, array_settings.DUMMY_NODE1_IQN,
                                        array_settings.DUMMY_FC_WWN1)
        host2 = utils.get_mock_xiv_host(right_host, array_settings.DUMMY_NODE2_IQN, array_settings.DUMMY_FC_WWN2)
        host3 = utils.get_mock_xiv_host(array_settings.DUMMY_HOST_ID3, array_settings.DUMMY_NODE2_IQN,
                                        array_settings.DUMMY_FC_WWN3)
        host4 = utils.get_mock_xiv_host(array_settings.DUMMY_HOST_ID4, array_settings.DUMMY_NODE3_IQN,
                                        array_settings.DUMMY_FC_WWN4)

        self.mediator.client.cmd.host_list.return_value = Mock(as_list=[host1, host2, host3, host4])
        host, connectivity_type = self.mediator.get_host_by_host_identifiers(Initiators([nqn], wwns, [iqn]))
        self.assertEqual(host, right_host)
        self.assertEqual(connectivity_type, [array_settings.FC_CONNECTIVITY_TYPE])

    def test_get_host_by_iscsi_and_fc_identifiers_succeeds(self):
        nqn = ""
        wwns = [array_settings.DUMMY_FC_WWN2, array_settings.DUMMY_FC_WWN5]
        iqn = array_settings.DUMMY_NODE2_IQN
        right_host = array_settings.DUMMY_HOST_ID2
        host1 = utils.get_mock_xiv_host(array_settings.DUMMY_HOST_ID1, array_settings.DUMMY_NODE1_IQN,
                                        array_settings.DUMMY_FC_WWN1)
        host2 = utils.get_mock_xiv_host(right_host, array_settings.DUMMY_NODE2_IQN, array_settings.DUMMY_FC_WWN2)
        host3 = utils.get_mock_xiv_host(array_settings.DUMMY_HOST_ID3, array_settings.DUMMY_NODE3_IQN,
                                        array_settings.DUMMY_FC_WWN3)
        host4 = utils.get_mock_xiv_host(array_settings.DUMMY_HOST_ID4, array_settings.DUMMY_NODE4_IQN,
                                        array_settings.DUMMY_FC_WWN4)

        self.mediator.client.cmd.host_list.return_value = Mock(as_list=[host1, host2, host3, host4])
        host, connectivity_type = self.mediator.get_host_by_host_identifiers(Initiators([nqn], wwns, [iqn]))
        self.assertEqual(host, right_host)
        self.assertEqual(connectivity_type, [array_settings.FC_CONNECTIVITY_TYPE,
                                             array_settings.ISCSI_CONNECTIVITY_TYPE])

    def test_get_volume_mappings_empty_mapping_list(self):
        # host3 = utils.get_mock_xiv_mapping(2, DUMMY_HOST_ID1)

        self.mediator.client.cmd.vol_mapping_list.return_value = Mock(as_list=[])
        mappings = self.mediator.get_volume_mappings(common_settings.VOLUME_UID)
        self.assertEqual(mappings, {})

    def test_get_volume_mappings_success(self):
        map1 = utils.get_mock_xiv_vol_mapping(2, array_settings.DUMMY_HOST_ID1)
        map2 = utils.get_mock_xiv_vol_mapping(3, array_settings.DUMMY_HOST_ID2)
        self.mediator.client.cmd.vol_mapping_list.return_value = Mock(as_list=[map1, map2])
        mappings = self.mediator.get_volume_mappings(common_settings.VOLUME_UID)
        self.assertEqual(mappings, {array_settings.DUMMY_HOST_ID1: 2, array_settings.DUMMY_HOST_ID2: 3})

    def test_get_volume_mappings_on_volume_not_found(self):
        self.mediator.client.cmd.vol_list.return_value = Mock(as_single_element=None)

        with self.assertRaises(array_errors.ObjectNotFoundError):
            self.mediator.get_volume_mappings(common_settings.VOLUME_UID)

    def test_get_volume_mappings_raise_illegal_object_id(self):
        self.mediator.client.cmd.vol_list.side_effect = [xcli_errors.IllegalValueForArgumentError("", "", "")]

        with self.assertRaises(array_errors.InvalidArgumentError):
            self.mediator.get_volume_mappings(common_settings.VOLUME_UID)

    def test_get_next_available_lun_raises_host_bad_name(self):
        # mapping = get_mock_xiv_host_mapping(1)
        self.mediator.client.cmd.mapping_list.side_effect = [
            xcli_errors.HostBadNameError("", common_settings.HOST_NAME, "")]
        with self.assertRaises(array_errors.HostNotFoundError):
            self.mediator._get_next_available_lun(common_settings.HOST_NAME)

    def test_get_next_available_lun_with_no_host_mappings(self):
        self.mediator.client.cmd.mapping_list.return_value = Mock(as_list=[])
        lun = self.mediator._get_next_available_lun(common_settings.HOST_NAME)
        self.assertTrue(lun <= self.mediator.MAX_LUN_NUMBER)
        self.assertTrue(lun >= self.mediator.MIN_LUN_NUMBER)

    @patch.object(XIVArrayMediator, "MAX_LUN_NUMBER", 3)
    @patch.object(XIVArrayMediator, "MIN_LUN_NUMBER", 1)
    def test_get_next_available_lun_success(self):
        mapping1 = utils.get_mock_xiv_host_mapping("1")
        mapping2 = utils.get_mock_xiv_host_mapping("3")

        self.mediator.client.cmd.mapping_list.return_value = Mock(as_list=[mapping1, mapping2])
        lun = self.mediator._get_next_available_lun(common_settings.HOST_NAME)
        self.assertEqual(lun, 2)

    @patch.object(XIVArrayMediator, "MAX_LUN_NUMBER", 3)
    @patch.object(XIVArrayMediator, "MIN_LUN_NUMBER", 1)
    def test_get_next_available_lun_no_available_lun(self):
        mapping1 = utils.get_mock_xiv_host_mapping("1")
        mapping2 = utils.get_mock_xiv_host_mapping("3")
        mapping3 = utils.get_mock_xiv_host_mapping("2")

        self.mediator.client.cmd.mapping_list.return_value = Mock(as_list=[mapping1, mapping2, mapping3])
        with self.assertRaises(array_errors.NoAvailableLunError):
            self.mediator._get_next_available_lun(common_settings.HOST_NAME)

    def test_map_volume_volume_not_found(self):
        self.mediator.client.cmd.vol_list.return_value = Mock(as_single_element=None)
        with self.assertRaises(array_errors.ObjectNotFoundError):
            self.mediator.map_volume(common_settings.VOLUME_UID, common_settings.HOST_NAME,
                                     array_settings.DUMMY_CONNECTIVITY_TYPE)

    def test_map_volume_raise_illegal_object_id(self):
        self.mediator.client.cmd.vol_list.side_effect = [xcli_errors.IllegalValueForArgumentError("", "", "")]
        with self.assertRaises(array_errors.InvalidArgumentError):
            self.mediator.map_volume(common_settings.VOLUME_UID, common_settings.HOST_NAME,
                                     array_settings.DUMMY_CONNECTIVITY_TYPE)

    @patch.object(XIVArrayMediator, "MAX_LUN_NUMBER", 3)
    @patch.object(XIVArrayMediator, "MIN_LUN_NUMBER", 1)
    def test_map_volume_no_availabe_lun(self):
        mapping1 = utils.get_mock_xiv_host_mapping("1")
        mapping2 = utils.get_mock_xiv_host_mapping("3")
        mapping3 = utils.get_mock_xiv_host_mapping("2")

        self.mediator.client.cmd.mapping_list.return_value = Mock(as_list=[mapping1, mapping2, mapping3])
        with self.assertRaises(array_errors.NoAvailableLunError):
            self.mediator.map_volume(common_settings.VOLUME_UID, common_settings.HOST_NAME,
                                     array_settings.DUMMY_CONNECTIVITY_TYPE)

    def map_volume_with_error(self, xcli_err, status, returned_err):
        self.mediator.client.cmd.map_vol.side_effect = [xcli_err("", status, "")]
        with patch.object(XIVArrayMediator, "_get_next_available_lun"):
            with self.assertRaises(returned_err):
                self.mediator.map_volume(common_settings.VOLUME_UID, common_settings.HOST_NAME,
                                         array_settings.DUMMY_CONNECTIVITY_TYPE)

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
        next_lun.return_value = array_settings.DUMMY_LUN_ID_INT
        self.mediator.client.cmd.map_vol.return_value = None
        lun = self.mediator.map_volume(common_settings.VOLUME_UID, common_settings.HOST_NAME,
                                       array_settings.DUMMY_CONNECTIVITY_TYPE)
        self.assertEqual(lun, array_settings.DUMMY_LUN_ID)

    def test_unmap_volume_no_volume_raise_object_not_found(self):
        self.mediator.client.cmd.vol_list.return_value = Mock(as_single_element=None)
        with self.assertRaises(array_errors.ObjectNotFoundError):
            self.mediator.unmap_volume(common_settings.VOLUME_UID, common_settings.HOST_NAME)

    def test_unmap_volume_raise_illegal_object_id(self):
        self.mediator.client.cmd.vol_list.side_effect = [xcli_errors.IllegalValueForArgumentError("", "", "")]
        with self.assertRaises(array_errors.InvalidArgumentError):
            self.mediator.unmap_volume(common_settings.VOLUME_UID, common_settings.HOST_NAME)

    def unmap_volume_with_error(self, xcli_err, status, returned_err):
        self.mediator.client.cmd.unmap_vol.side_effect = [xcli_err("", status, "")]
        with self.assertRaises(returned_err):
            self.mediator.unmap_volume(common_settings.VOLUME_UID, common_settings.HOST_NAME)

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
        self.mediator.unmap_volume(common_settings.VOLUME_UID, common_settings.HOST_NAME)

    def test_get_iscsi_targets_by_iqn_fail(self):
        self.mediator.client.cmd.config_get.return_value = Mock(as_list=[])
        self.mediator.client.cmd.ipinterface_list.return_value = []

        with self.assertRaises(Exception):
            self.mediator.get_iscsi_targets_by_iqn(common_settings.HOST_NAME)

    def test_get_iscsi_targets_by_iqn_success(self):
        config_param = utils.get_mock_xiv_config_param(name="iscsi_name", value=array_settings.DUMMY_NODE1_IQN)
        self.mediator.client.cmd.config_get.return_value = Mock(as_list=[config_param])
        ip_interface = utils.get_mock_xiv_ip_interface("iSCSI", address=array_settings.DUMMY_IP_ADDRESS1)
        ip_interface6 = utils.get_mock_xiv_ip_interface("iSCSI", address6=array_settings.DUMMY_IP_ADDRESS_6_1)
        self.mediator.client.cmd.ipinterface_list.return_value = [ip_interface, ip_interface6]

        targets_by_iqn = self.mediator.get_iscsi_targets_by_iqn(common_settings.HOST_NAME)

        self.assertEqual(targets_by_iqn,
                         {array_settings.DUMMY_NODE1_IQN: [array_settings.DUMMY_IP_ADDRESS1, "[{}]".format(
                             array_settings.DUMMY_IP_ADDRESS_6_1)]})

    def _prepare_mocks_for_expand_volume(self):
        volume = utils.get_mock_xiv_volume(size="1", name=common_settings.VOLUME_NAME, wwn=common_settings.VOLUME_UID)
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
