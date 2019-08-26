import unittest
from munch import Munch
from mock import patch, Mock
from controller.array_action.array_mediator_svc import SVCArrayMediator
from controller.array_action.array_mediator_svc import \
    build_kwargs_from_capabilities
import controller.array_action.errors as array_errors
from pysvc.unified.response import CLIFailureError
from pysvc import errors as svc_errors
from controller.array_action.config import ISCSI_CONNECTIVITY_TYPE


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

    def test_get_vol_by_wwn_return_error(self):
        vol_ret = Mock(as_single_element=Munch({}))
        self.svc.client.svcinfo.lsvdisk.return_value = vol_ret
        with self.assertRaises(array_errors.VolumeNotFoundError):
            self.svc._get_vol_by_wwn("vol")

    def test_get_vol_by_wwn_return_success(self):
        vol_ret = Mock(as_single_element=Munch({'vdisk_UID': 'vol_id',
                                                'name': 'test_vol',
                                                'capacity': '1024',
                                                'mdisk_grp_name': 'pool_name'
                                                }))
        self.svc.client.svcinfo.lsvdisk.return_value = vol_ret
        ret = self.svc._get_vol_by_wwn("vol_id")
        self.assertEqual(ret, 'test_vol')

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
        self.assertEqual(SVCArrayMediator.max_vol_name_length, 64)
        self.assertEqual(SVCArrayMediator.max_connections, 2)
        self.assertEqual(SVCArrayMediator.max_lun_retries, 10)

    def test_get_host_by_identifiers_returns_host_not_found(self):
        host_munch_ret_1 = Munch({'id': 'host_id_1', 'name': 'test_host_1',
                                  'iscsi_name': 'iqn.test.1'})
        host_munch_ret_2 = Munch({'id': 'host_id_2', 'name': 'test_host_1',
                                  'iscsi_name': 'iqn.test.2'})
        host_munch_ret_3 = Munch({'id': 'host_id_3', 'name': 'test_host_3',
                                  'iscsi_name': 'iqn.test.3'})
        ret1 = [host_munch_ret_1, host_munch_ret_2]
        ret2 = Mock
        ret2.as_single_element = host_munch_ret_3

        self.svc.client.svcinfo.lshost.side_effect = [ret1, ret2, ret2]
        with self.assertRaises(array_errors.HostNotFoundError):
            self.svc.get_host_by_host_identifiers('Test')

    def test_get_host_by_identifier_return_host_not_found_when_no_hosts_exist(
            self):
        host_munch_ret_1 = Munch({})
        host_munch_ret_2 = Munch({})
        host_munch_ret_3 = Munch({})
        ret1 = [host_munch_ret_1, host_munch_ret_2]
        ret2 = Mock
        ret2.as_single_element = host_munch_ret_3

        self.svc.client.svcinfo.lshost.side_effect = [ret1, ret2, ret2]
        with self.assertRaises(array_errors.HostNotFoundError):
            self.svc.get_host_by_host_identifiers('Test')

    def test_get_host_by_identifiers_succeeds(self):
        host_munch_ret_1 = Munch({'id': 'host_id_1', 'name': 'test_host_1',
                                  'iscsi_name': 'iqn.test.1'})
        host_munch_ret_2 = Munch({'id': 'host_id_2', 'name': 'test_host_2',
                                  'iscsi_name': 'iqn.test.2'})
        ret1 = [host_munch_ret_1, host_munch_ret_2]
        ret2 = Mock
        ret2.as_single_element = host_munch_ret_2

        self.svc.client.svcinfo.lshost.side_effect = [ret1, ret2, ret2]

        host, connectivity_type = self.svc.get_host_by_host_identifiers(
            'iqn.test.2')
        self.assertEqual(host, 'test_host_2')
        self.assertEqual(connectivity_type, [ISCSI_CONNECTIVITY_TYPE])

    def test_get_volume_mappings_empty_mapping_list(self):
        self.svc.client.svcinfo.lsvdiskhostmap.return_value = []
        mappings = self.svc.get_volume_mappings("vol")
        self.assertEqual(mappings, {})

    def test_get_volume_mappings_on_volume_not_found(self):
        self.svc.client.svcinfo.lsvdiskhostmap.side_effect = [
            svc_errors.CommandExecutionError('Failed')]

        with self.assertRaises(array_errors.VolumeNotFoundError):
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
        with self.assertRaises(array_errors.VolumeNotFoundError):
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
        with self.assertRaises(array_errors.VolumeNotFoundError):
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
        lun = self.svc.unmap_volume("vol", "host")

    def test_get_array_iscsi_name_with_exception(self):
        self.svc.client.svcinfo.lsnode.side_effect = [Exception]
        with self.assertRaises(Exception):
            self.svc.get_array_iscsi_name()

    def test_get_array_iscsi_name_without_node(self):
        self.svc.client.svcinfo.lsnode.return_value = []
        iqns = self.svc.get_array_iscsi_name()
        self.assertEqual(iqns, [])

    def test_get_array_iscsi_name_with_no_online_node(self):
        node = Munch({'id': '1',
                      'name': 'node1',
                      'iscsi_name': 'iqn.1986-03.com.ibm:2145.v7k1.node1',
                      'status': 'offline'})
        self.svc.client.svcinfo.lsnode.return_value = [node]
        iqns = self.svc.get_array_iscsi_name()
        self.assertEqual(iqns, [])

    def test_get_array_iscsi_name_with_nore_nodes(self):
        node1 = Munch({'id': '1',
                       'name': 'node1',
                       'iscsi_name': 'iqn.1986-03.com.ibm:2145.v7k1.node1',
                       'status': 'online'})
        node2 = Munch({'id': '2',
                       'name': 'node2',
                       'iscsi_name': 'iqn.1986-03.com.ibm:2145.v7k1.node2',
                       'status': 'online'})
        self.svc.client.svcinfo.lsnode.return_value = [node1, node2]
        iqns = self.svc.get_array_iscsi_name()
        self.assertEqual(iqns,
                         ["iqn.1986-03.com.ibm:2145.v7k1.node1",
                          "iqn.1986-03.com.ibm:2145.v7k1.node2"])
