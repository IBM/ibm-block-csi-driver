import unittest

from mock import patch, NonCallableMagicMock, Mock
from munch import Munch
from pyds8k.exceptions import ClientError, ClientException, InternalServerError, NotFound

import controllers.array_action.errors as array_errors
from controllers.array_action import config
from controllers.array_action.array_action_types import Volume, Snapshot
from controllers.array_action.array_mediator_ds8k import DS8KArrayMediator, FLASHCOPY_PERSISTENT_OPTION, \
    FLASHCOPY_PERMIT_SPACE_EFFICIENT_TARGET_OPTION
from controllers.array_action.array_mediator_ds8k import LOGIN_PORT_WWPN, LOGIN_PORT_STATE, \
    LOGIN_PORT_STATE_ONLINE
from controllers.common.node_info import Initiators
from controllers.tests.common.test_settings import VOLUME_NAME, POOL, USER, PASSWORD, VOLUME_UID, SNAPSHOT_NAME, \
    SNAPSHOT_VOLUME_UID, INTERNAL_SNAPSHOT_ID, VOLUME_OBJECT_TYPE, SNAPSHOT_OBJECT_TYPE


class TestArrayMediatorDS8K(unittest.TestCase):

    def setUp(self):
        self.endpoint = ["1.2.3.4"]
        self.client_mock = NonCallableMagicMock()
        patcher = patch('controllers.array_action.array_mediator_ds8k.RESTClient')
        self.connect_mock = patcher.start()
        self.addCleanup(patcher.stop)
        self.connect_mock.return_value = self.client_mock

        self.client_mock.get_system.return_value = Munch(
            {"id": "dsk array id",
             "name": "mtc032h",
             "state": "online",
             "release": "7.4",
             "bundle": "87.51.47.0",
             "MTM": "2421-961",
             "sn": "75DHZ81",
             "wwnn": "5005076306FFD2F0",
             "cap": "440659",
             "capalloc": "304361",
             "capavail": "136810",
             "capraw": "73282879488"
             }
        )

        self.volume_response = Munch(
            {"cap": "1073741824",
             "id": "0001",
             "name": VOLUME_NAME,
             "pool": POOL,
             "tp": "ese",
             "flashcopy": ""
             }
        )

        self.snapshot_response = Munch(
            {"cap": "1073741824",
             "id": "0002",
             "name": SNAPSHOT_NAME,
             "pool": POOL,
             "flashcopy": ""
             }
        )

        self.flashcopy_response = Munch(
            {"sourcevolume": "0001",
             "targetvolume": "0002",
             "id": "0001:0002",
             "state": "valid",
             "backgroundcopy": "enabled",
             "representation": {}
             }
        )

        self.array = DS8KArrayMediator(USER, PASSWORD, self.endpoint)
        self.array.volume_cache = Mock()

    def test_connect_with_incorrect_credentials(self):
        self.client_mock.get_system.side_effect = \
            ClientError("400", "BE7A002D")
        with self.assertRaises(array_errors.CredentialsError):
            DS8KArrayMediator(USER, PASSWORD, self.endpoint)

    def test_connect_to_unsupported_system(self):
        self.client_mock.get_system.return_value = \
            Munch({"bundle": "87.50.34.0"})
        with self.assertRaises(array_errors.UnsupportedStorageVersionError):
            DS8KArrayMediator(USER, PASSWORD, self.endpoint)

    def test_connect_with_error(self):
        self.client_mock.get_system.side_effect = \
            ClientError("400", "other_error")
        with self.assertRaises(ClientError) as ex:
            DS8KArrayMediator(USER, PASSWORD, self.endpoint)
        self.assertEqual("other_error", ex.exception.message)

    def test_validate_space_efficiency_thin_success(self):
        self.array.validate_supported_space_efficiency(
            config.SPACE_EFFICIENCY_THIN
        )
        # nothing is raised

    def test_validate_space_efficiency_none_success(self):
        self.array.validate_supported_space_efficiency(
            config.SPACE_EFFICIENCY_NONE
        )

    def test_validate_space_efficiency_fail(self):
        with self.assertRaises(array_errors.SpaceEfficiencyNotSupported):
            self.array.validate_supported_space_efficiency("fake")

    def test_get_volume_with_no_pool(self):
        with self.assertRaises(array_errors.PoolParameterIsMissing):
            self.array.get_volume(VOLUME_NAME, None, False)

    def _test_get_volume(self, with_cache=False):
        if with_cache:
            self.array.volume_cache.get.return_value = self.volume_response.id
            self.client_mock.get_volume.return_value = self.volume_response
        else:
            self.array.volume_cache.get.return_value = None
            self.client_mock.get_volumes_by_pool.return_value = [self.volume_response]
        volume = self.array.get_volume(self.volume_response.name, pool=self.volume_response.pool,
                                       is_virt_snap_func=False)

        self.assertEqual(self.volume_response.name, volume.name)
        self.array.volume_cache.add_or_delete.assert_called_once_with(self.volume_response.name,
                                                                      self.volume_response.id)

    def test_get_volume_with_empty_cache(self, ):
        self._test_get_volume()
        self.client_mock.get_volumes_by_pool.assert_called_once_with(self.volume_response.pool)

    def test_get_volume_with_volume_in_cache(self):
        self._test_get_volume(with_cache=True)
        self.client_mock.get_volume.assert_called_once_with(self.volume_response.id)
        self.client_mock.get_volumes_by_pool.assert_not_called()

    def test_get_volume_with_pool_context(self):
        self.client_mock.get_volumes_by_pool.return_value = [
            self.volume_response,
        ]
        volume = self.array.get_volume(self.volume_response.name, pool=self.volume_response.pool,
                                       is_virt_snap_func=False)
        self.assertEqual(self.volume_response.name, volume.name)
        self.client_mock.get_volumes_by_pool.assert_called_once_with(self.volume_response.pool)

    def test_get_volume_with_pool_context_not_found(self):
        self.client_mock.get_volumes_by_pool.return_value = [
            self.volume_response,
        ]
        with self.assertRaises(array_errors.ObjectNotFoundError):
            self.array.get_volume("fake_name", pool=self.volume_response.pool, is_virt_snap_func=False)

    def test_create_volume_with_default_space_efficiency_success(self):
        self._test_create_volume_success(space_efficiency='none')

    def test_create_volume_with_thin_space_efficiency_success(self):
        self._test_create_volume_success(space_efficiency='thin')

    def test_create_volume_with_empty_space_efficiency_success(self):
        self._test_create_volume_success(space_efficiency='')

    def test_create_volume_with_empty_cache(self):
        self._test_create_volume_success()
        self.array.volume_cache.add.assert_called_once_with(self.volume_response.name, self.volume_response.id)

    def _test_create_volume_success(self, space_efficiency=''):
        self.client_mock.create_volume.return_value = self.volume_response
        self.client_mock.get_volume.return_value = self.volume_response
        name = self.volume_response.name
        size_in_bytes = self.volume_response.cap
        pool_id = self.volume_response.pool
        volume = self.array.create_volume(
            name, size_in_bytes, space_efficiency, pool_id, None, None, None, None, False)
        if space_efficiency == 'thin':
            space_efficiency = 'ese'
        else:
            space_efficiency = 'none'
        self.client_mock.create_volume.assert_called_once_with(
            pool_id=pool_id,
            capacity_in_bytes=self.volume_response.cap,
            thin_provisioning=space_efficiency,
            name=VOLUME_NAME,
        )
        self.assertEqual(self.volume_response.name, volume.name)

    def test_create_volume_fail_with_client_exception(self):
        self.client_mock.create_volume.side_effect = ClientException("500")
        with self.assertRaises(array_errors.VolumeCreationError):
            self.array.create_volume(VOLUME_NAME, 1, 'thin', POOL, None, None, None, None, False)

    def test_create_volume_fail_with_pool_not_found(self):
        self.client_mock.create_volume.side_effect = NotFound("404", message="BE7A0001")
        with self.assertRaises(array_errors.PoolDoesNotExist):
            self.array.create_volume(VOLUME_NAME, 1, 'thin', POOL, None, None, None, None, False)

    def test_create_volume_fail_with_incorrect_id(self):
        self.client_mock.create_volume.side_effect = InternalServerError("500", message="BE7A0005")
        with self.assertRaises(array_errors.PoolDoesNotExist):
            self.array.create_volume(VOLUME_NAME, 1, 'thin', POOL, None, None, None, None, False)

    def test_create_volume_fail_with_no_space_in_pool(self):
        self.client_mock.create_volume.side_effect = InternalServerError("500", message="BE534459")
        with self.assertRaises(array_errors.NotEnoughSpaceInPool):
            self.array.create_volume(VOLUME_NAME, 1, 'thin', POOL, None, None, None, None, False)

    def test_delete_volume(self):
        scsi_id = "volume_scsi_id_{}".format(self.volume_response.id)
        self.array.delete_volume(scsi_id)
        self.client_mock.delete_volume.assert_called_once_with(volume_id=self.volume_response.id)

    def test_delete_volume_with_remove_from_cache(self):
        self.client_mock.get_volume.return_value = self.volume_response
        scsi_id = "volume_scsi_id_{}".format(self.volume_response.id)
        self.array.delete_volume(scsi_id)
        self.array.volume_cache.remove.assert_called_once_with(self.volume_response.name)

    def test_delete_volume_fail_with_client_exception(self):
        self.client_mock.delete_volume.side_effect = ClientException("500")
        with self.assertRaises(array_errors.VolumeDeletionError):
            self.array.delete_volume("fake_id")

    def test_delete_volume_fail_with_not_found(self):
        self.client_mock.delete_volume.side_effect = NotFound("404")
        with self.assertRaises(array_errors.ObjectNotFoundError):
            self.array.delete_volume("fake_id")

    def test_delete_volume_failed_with_illegal_object_id(self):
        self.client_mock.get_volume.side_effect = InternalServerError("500", message="BE7A0005")
        with self.assertRaises(array_errors.InvalidArgumentError):
            self.array.delete_volume("fake_id")

    def test_delete_volume_with_flashcopies_as_source_and_target_fail(self):
        self.client_mock.get_volume.return_value = self.volume_response
        self.client_mock.get_flashcopies_by_volume.return_value = [
            Munch({"sourcevolume": "0001",
                   "targetvolume": "0002",
                   "id": "0001:0002",
                   "state": "valid",
                   "backgroundcopy": "disabled",
                   "representation": {}
                   }),
            Munch({"sourcevolume": "0003",
                   "targetvolume": "0001",
                   "id": "0003:0001",
                   "state": "valid",
                   "backgroundcopy": "enabled",
                   "representation": {}
                   })]
        self.client_mock.get_flashcopies.return_value = Munch({"out_of_sync_tracks": "0"})
        self.client_mock.delete_volume.side_effect = ClientException("500")
        with self.assertRaises(array_errors.ObjectIsStillInUseError):
            self.array.delete_volume("0001")

    def _prepare_mocks_for_volume(self):
        self.client_mock.get_flashcopies_by_volume.return_value = [self.flashcopy_response]
        self.client_mock.get_flashcopies.return_value = Munch({"out_of_sync_tracks": "0",
                                                               "targetvolume": "0002",
                                                               "representation": {}})
        volume = self.volume_response
        self.client_mock.get_volume.return_value = volume
        return volume

    def test_delete_volume_with_snapshot_as_source(self):
        self._prepare_mocks_for_volume()
        flashcopy = self.flashcopy_response
        flashcopy.backgroundcopy = "disabled"
        self.client_mock.get_flashcopies_by_volume.return_value = [flashcopy]
        with self.assertRaises(array_errors.ObjectIsStillInUseError):
            self.array.delete_volume("0001")

    def test_delete_volume_with_flashcopy_still_copying(self):
        self._prepare_mocks_for_volume()
        self.client_mock.get_flashcopies.return_value.out_of_sync_tracks = "55"
        with self.assertRaises(array_errors.ObjectIsStillInUseError):
            self.array.delete_volume("0001")
        self.client_mock.delete_flashcopy.assert_not_called()

    def test_delete_volume_with_flashcopy_as_source_deleted(self):
        self._prepare_mocks_for_volume()
        self.array.delete_volume("0001")
        self.client_mock.delete_flashcopy.assert_called_once()

    def test_delete_volume_with_flashcopy_as_target_success(self):
        self._prepare_mocks_for_volume()
        self.array.delete_volume("0001")
        self.client_mock.delete_flashcopy.assert_called_once_with("0001:0002")
        self.client_mock.delete_volume.assert_called_once_with(volume_id="0001")

    def test_get_volume_mappings_fail_with_client_exception(self):
        self.client_mock.get_hosts.side_effect = ClientException("500")
        with self.assertRaises(ClientException):
            self.array.get_volume_mappings("fake_name")

    def test_get_volume_mappings_found_nothing(self):
        volume_id = "0001"
        scsi_id = "6005076306FFD301000000000000{}".format(volume_id)
        self.client_mock.get_hosts.return_value = [
            Munch({
                "mappings_briefs": [{
                    "volume_id": "0000",
                    "lunid": "1",
                }]
            })
        ]
        self.assertDictEqual(self.array.get_volume_mappings(scsi_id), {})

    def test_get_volume_mappings(self):
        volume_id = "0001"
        lunid = "1"
        host_name = "test_host"
        scsi_id = "6005076306FFD301000000000000{}".format(volume_id)
        self.client_mock.get_hosts.return_value = [
            Munch({
                "mappings_briefs": [{
                    "volume_id": volume_id,
                    "lunid": lunid,
                }],
                "name": host_name,
            })
        ]
        self.assertDictEqual(self.array.get_volume_mappings(scsi_id), {host_name: int(lunid)})

    def test_map_volume_host_not_found(self):
        self.client_mock.map_volume_to_host.side_effect = NotFound("404")
        with self.assertRaises(array_errors.HostNotFoundError):
            self.array.map_volume("fake_name", "fake_host", "fake_connectivity_type")

    def test_map_volume_volume_not_found(self):
        self.client_mock.map_volume_to_host.side_effect = ClientException("500", "[BE586015]")
        with self.assertRaises(array_errors.ObjectNotFoundError):
            self.array.map_volume("fake_name", "fake_host", "fake_connectivity_type")

    def test_map_volume_no_available_lun(self):
        self.client_mock.map_volume_to_host.side_effect = InternalServerError("500", "[BE74121B]")
        with self.assertRaises(array_errors.NoAvailableLunError):
            self.array.map_volume("fake_name", "fake_host", "fake_connectivity_type")

    def test_map_volume_fail_with_client_exception(self):
        self.client_mock.map_volume_to_host.side_effect = ClientException("500")
        with self.assertRaises(array_errors.MappingError):
            self.array.map_volume("fake_name", "fake_host", "fake_connectivity_type")

    def test_map_volume(self):
        scsi_id = "6005076306FFD3010000000000000001"
        host_name = "test_name"
        connectivity_type = "fake_connectivity_type"
        self.client_mock.map_volume_to_host.return_value = Munch({"lunid": "01"})
        lun = self.array.map_volume(scsi_id, host_name, connectivity_type)
        self.assertEqual(1, lun)
        self.client_mock.map_volume_to_host.assert_called_once_with(host_name, scsi_id[-4:])

    def test_unmap_volume_host_not_found(self):
        self.client_mock.get_host_mappings.side_effect = NotFound("404", message='BE7A0016')
        with self.assertRaises(array_errors.HostNotFoundError):
            self.array.unmap_volume("fake_name", "fake_host")

    def test_unmap_volume_already_unmapped(self):
        self.client_mock.get_host_mappings.side_effect = NotFound("404", message='BE7A001F')
        with self.assertRaises(array_errors.VolumeAlreadyUnmappedError):
            self.array.unmap_volume("fake_name", "fake_host")

    def test_unmap_volume_volume_not_found(self):
        self.client_mock.get_host_mappings.return_value = []
        with self.assertRaises(array_errors.ObjectNotFoundError):
            self.array.unmap_volume("fake_name", "fake_host")

    def test_unmap_volume_fail_with_client_exception(self):
        volume_id = "0001"
        lunid = "1"
        host_name = "test_host"
        scsi_id = "6005076306FFD301000000000000{}".format(volume_id)
        self.client_mock.get_host_mappings.return_value = [
            Munch({
                "volume": volume_id,
                "id": lunid
            })
        ]
        self.client_mock.unmap_volume_from_host.side_effect = ClientException("500")
        with self.assertRaises(array_errors.UnmappingError):
            self.array.unmap_volume(scsi_id, host_name)

    def test_unmap_volume(self):
        volume_id = "0001"
        lunid = "1"
        host_name = "test_host"
        scsi_id = "6005076306FFD301000000000000{}".format(volume_id)
        self.client_mock.get_host_mappings.return_value = [
            Munch({
                "volume": volume_id,
                "id": lunid
            })
        ]
        self.array.unmap_volume(scsi_id, host_name)
        self.client_mock.unmap_volume_from_host.assert_called_once_with(host_name=host_name,
                                                                        lunid=lunid)

    def test_get_array_fc_wwns_fail_with_client_exception(self):
        self.client_mock.get_host.side_effect = ClientException("500")
        with self.assertRaises(ClientException):
            self.array.get_array_fc_wwns(None)

    def test_get_array_fc_wwns_skip_offline_port(self):
        wwpn1 = "fake_wwpn"
        wwpn2 = "offine_wwpn"
        self.client_mock.get_host.return_value = Munch(
            {"login_ports": [
                {
                    LOGIN_PORT_WWPN: wwpn1,
                    LOGIN_PORT_STATE: LOGIN_PORT_STATE_ONLINE,
                },
                {
                    LOGIN_PORT_WWPN: wwpn2,
                    LOGIN_PORT_STATE: "offline",
                }
            ]})
        self.assertListEqual(self.array.get_array_fc_wwns(None), [wwpn1])

    def test_get_array_fc_wwns(self):
        wwpn = "fake_wwpn"
        self.client_mock.get_host.return_value = Munch(
            {"login_ports": [
                {
                    LOGIN_PORT_WWPN: wwpn,
                    LOGIN_PORT_STATE: LOGIN_PORT_STATE_ONLINE
                }
            ]})
        self.assertListEqual(self.array.get_array_fc_wwns(None), [wwpn])

    def test_get_host_by_name_success(self):
        self.client_mock.get_host.return_value = Munch(
            {"name": "test_host_1", "host_ports_briefs": [{"wwpn": "wwpn1"}, {"wwpn": "wwpn2"}]})
        host = self.array.get_host_by_name('test_host_1')
        self.assertEqual("test_host_1", host.name)
        self.assertEqual(['fc'], host.connectivity_types)
        self.assertEqual([], host.initiators.nvme_nqns)
        self.assertEqual(['wwpn1', 'wwpn2'], host.initiators.fc_wwns)
        self.assertEqual([], host.initiators.iscsi_iqns)

    def test_get_host_by_name_raise_host_not_found(self):
        self.client_mock.get_host.side_effect = NotFound("404", message='BE7A0001')
        with self.assertRaises(array_errors.HostNotFoundError):
            self.array.get_host_by_name('test_host_1')

    def test_get_host_by_identifiers(self):
        host_name = "test_host"
        wwpn1 = "wwpn1"
        wwpn2 = "wwpn2"
        self.client_mock.get_hosts.return_value = [
            Munch({
                "name": host_name,
                "host_ports_briefs": [{"wwpn": wwpn1}, {"wwpn": wwpn2}]
            })
        ]
        host, connectivity_type = self.array.get_host_by_host_identifiers(
            Initiators([], [wwpn1, wwpn2], [])
        )
        self.assertEqual(host_name, host)
        self.assertEqual([config.FC_CONNECTIVITY_TYPE], connectivity_type)

    def test_get_host_by_identifiers_partial_match(self):
        host_name = "test_host"
        wwpn1 = "wwpn1"
        wwpn2 = "wwpn2"
        self.client_mock.get_hosts.return_value = [
            Munch({
                "name": host_name,
                "host_ports_briefs": [{"wwpn": wwpn1}, {"wwpn": wwpn2}]
            })
        ]
        host, connectivity_type = self.array.get_host_by_host_identifiers(
            Initiators([], [wwpn1, "another_wwpn"], [])
        )
        self.assertEqual(host, host_name)
        self.assertEqual([config.FC_CONNECTIVITY_TYPE], connectivity_type)

    def test_get_host_by_identifiers_not_found(self):
        host_name = "test_host"
        wwpn1 = "wwpn1"
        wwpn2 = "wwpn2"
        self.client_mock.get_hosts.return_value = [
            Munch({
                "name": host_name,
                "host_ports_briefs": [{"wwpn": wwpn1}, {"wwpn": wwpn2}]
            })
        ]
        with self.assertRaises(array_errors.HostNotFoundError):
            self.array.get_host_by_host_identifiers(
                Initiators([], ["new_wwpn", "another_wwpn"], [])
            )

    def test_get_snapshot_not_exist_return_none(self):
        self.client_mock.get_snapshot.side_effect = [ClientError("400", "BE7A002D")]
        snapshot = self.array.get_snapshot(VOLUME_UID, "fake_name", pool=self.volume_response.pool,
                                           is_virt_snap_func=False)
        self.assertIsNone(snapshot)

    def test_get_snapshot_get_flashcopy_not_exist_raise_error(self):
        self._prepare_mocks_for_snapshot()
        self.client_mock.get_flashcopies_by_volume.return_value = []

        with self.assertRaises(array_errors.ExpectedSnapshotButFoundVolumeError):
            self.array.get_snapshot(VOLUME_UID, SNAPSHOT_NAME, pool=self.volume_response.pool, is_virt_snap_func=False)

    def test_get_snapshot_failed_with_incorrect_id(self):
        self.client_mock.get_volume.side_effect = InternalServerError("500", message="BE7A0005")
        with self.assertRaises(array_errors.InvalidArgumentError):
            self.array.get_snapshot(VOLUME_UID, SNAPSHOT_NAME, pool=None, is_virt_snap_func=False)

    def _test_get_snapshot_success(self, with_cache=False):
        if with_cache:
            self.array.volume_cache.get.return_value = self.snapshot_response.id
        else:
            self.array.volume_cache.get.return_value = None
        target_volume = self._prepare_mocks_for_snapshot()
        volume = self.array.get_snapshot(VOLUME_UID, SNAPSHOT_NAME, pool=self.volume_response.pool,
                                         is_virt_snap_func=False)
        self.assertEqual(volume.name, target_volume.name)
        self.array.volume_cache.add_or_delete.assert_called_once_with(target_volume.name,
                                                                      target_volume.id)

    def test_get_snapshot_with_empty_cache(self):
        self._test_get_snapshot_success()
        self.client_mock.get_volumes_by_pool.assert_called_once_with(self.snapshot_response.pool)

    def test_get_snapshot_with_volume_in_cache(self):
        self._test_get_snapshot_success(with_cache=True)
        self.client_mock.get_volume.assert_called_once_with(self.snapshot_response.id)
        self.client_mock.get_volumes_by_pool.assert_not_called()

    def test_get_snapshot_no_pool_success(self):
        target_volume = self._prepare_mocks_for_snapshot()
        volume = self.array.get_snapshot(VOLUME_UID, SNAPSHOT_NAME, pool=None, is_virt_snap_func=False)
        self.assertEqual(volume.name, target_volume.name)

    def _prepare_mocks_for_create_snapshot(self, thin_provisioning="none"):
        self.client_mock.create_volume = Mock()
        self.client_mock.get_volume.side_effect = [
            Munch(
                {"cap": "1073741824",
                 "id": "0001",
                 "name": "source_volume",
                 "pool": POOL,
                 "tp": thin_provisioning,
                 }
            ),
            Mock(),
            self.snapshot_response
        ]
        self.client_mock.get_flashcopies.return_value = self.flashcopy_response

    def test_create_snapshot_create_volume_error(self):
        self.client_mock.create_volume.side_effect = ClientException("500")

        with self.assertRaises(array_errors.VolumeCreationError):
            self.array.create_snapshot(VOLUME_UID, SNAPSHOT_NAME, space_efficiency=None,
                                       pool=self.volume_response.pool, is_virt_snap_func=False)

    def test_create_snapshot_create_fcrel_error(self):
        self.client_mock.create_volume = Mock()
        self.client_mock.get_volume = Mock()
        self.client_mock.create_flashcopy.side_effect = ClientException("500")

        with self.assertRaises(Exception):
            self.array.create_snapshot(VOLUME_UID, SNAPSHOT_NAME, space_efficiency=None,
                                       pool=self.volume_response.pool, is_virt_snap_func=False)

    def test_create_snapshot_get_volume_not_found(self):
        self.client_mock.create_volume = Mock()
        self.client_mock.get_volume.side_effect = NotFound("404")

        with self.assertRaises(array_errors.ObjectNotFoundError):
            self.array.create_snapshot(VOLUME_UID, SNAPSHOT_NAME, space_efficiency=None,
                                       pool=self.volume_response.pool, is_virt_snap_func=False)

    def test_create_snapshot_create_flashcopy_volume_not_found(self):
        self._prepare_mocks_for_create_snapshot()
        self.client_mock.create_flashcopy.side_effect = ClientException("500", message="00000013")

        with self.assertRaises(array_errors.ObjectNotFoundError):
            self.array.create_snapshot(VOLUME_UID, SNAPSHOT_NAME, space_efficiency=None,
                                       pool=self.volume_response.pool, is_virt_snap_func=False)

    def test_create_snapshot_already_exist(self):
        self._prepare_mocks_for_create_snapshot()
        self.client_mock.create_flashcopy.side_effect = ClientException("500", message="000000AE")

        with self.assertRaises(array_errors.SnapshotAlreadyExists):
            self.array.create_snapshot(VOLUME_UID, SNAPSHOT_NAME, space_efficiency=None,
                                       pool=self.volume_response.pool, is_virt_snap_func=False)

    def test_create_snapshot_failed_with_incorrect_id(self):
        self.client_mock.get_volume.side_effect = InternalServerError("500", message="BE7A0005")
        with self.assertRaises(array_errors.InvalidArgumentError):
            self.array.create_snapshot(VOLUME_UID, SNAPSHOT_NAME, space_efficiency=None, pool=None,
                                       is_virt_snap_func=False)

    def test_create_snapshot_success(self):
        self._prepare_mocks_for_create_snapshot()
        snapshot_response = self.snapshot_response
        snapshot = self.array.create_snapshot(VOLUME_UID, SNAPSHOT_NAME, space_efficiency=None, pool=None,
                                              is_virt_snap_func=False)

        self.assertEqual(snapshot.name, snapshot_response.name)
        self.assertEqual(snapshot.id, self.array._generate_volume_scsi_identifier(snapshot_response.id))
        self.client_mock.create_volume.assert_called_once_with(name=SNAPSHOT_NAME, capacity_in_bytes=1073741824,
                                                               pool_id=POOL, thin_provisioning='none')

    def test_create_snapshot_with_empty_cache(self):
        self._prepare_mocks_for_create_snapshot()

        self.array.create_snapshot(VOLUME_UID, SNAPSHOT_NAME, space_efficiency=None, pool=None,
                                   is_virt_snap_func=False)

        self.array.volume_cache.add.assert_called_once_with(self.snapshot_response.name, self.snapshot_response.id)

    def test_create_snapshot_with_different_pool_success(self):
        self._prepare_mocks_for_create_snapshot()

        self.array.create_snapshot(VOLUME_UID, SNAPSHOT_NAME, space_efficiency=None, pool="different_pool",
                                   is_virt_snap_func=False)

        self.client_mock.create_volume.assert_called_once_with(name=SNAPSHOT_NAME, capacity_in_bytes=1073741824,
                                                               pool_id='different_pool', thin_provisioning='none')

    def _test_create_snapshot_with_space_efficiency_success(self, source_volume_space_efficiency,
                                                            space_efficiency_called, space_efficiency_parameter=None):
        self._prepare_mocks_for_create_snapshot(thin_provisioning=source_volume_space_efficiency)

        if space_efficiency_parameter is None:
            self.array.create_snapshot(VOLUME_UID, SNAPSHOT_NAME, space_efficiency=None, pool=None,
                                       is_virt_snap_func=False)
        else:
            self.array.create_snapshot(VOLUME_UID, SNAPSHOT_NAME, space_efficiency=space_efficiency_parameter,
                                       pool=None, is_virt_snap_func=False)

        self.client_mock.create_volume.assert_called_with(name=SNAPSHOT_NAME, capacity_in_bytes=1073741824,
                                                          pool_id=POOL,
                                                          thin_provisioning=space_efficiency_called)

    def test_create_snapshot_with_specified_source_volume_space_efficiency_success(self):
        self._test_create_snapshot_with_space_efficiency_success(source_volume_space_efficiency="none",
                                                                 space_efficiency_called="none")

    def test_create_snapshot_with_different_request_parameter_space_efficiency_success(self):
        self._test_create_snapshot_with_space_efficiency_success(source_volume_space_efficiency="none",
                                                                 space_efficiency_called="ese",
                                                                 space_efficiency_parameter="thin")

    def test_create_snapshot_with_different_request_parameter_empty_space_efficiency_success(self):
        self._test_create_snapshot_with_space_efficiency_success(source_volume_space_efficiency="ese",
                                                                 space_efficiency_called="ese",
                                                                 space_efficiency_parameter="")

    def test_create_snapshot_not_valid(self):
        self._prepare_mocks_for_create_snapshot()
        flashcopy_response = Munch(
            {"sourcevolume": {"id": "0001"},
             "targetvolume": {"id": "0002"},
             "id": "0001:0002",
             "state": "invalid"
             })
        self.client_mock.get_flashcopies.return_value = flashcopy_response
        with self.assertRaises(ValueError) as ar_context:
            self.array.create_snapshot(VOLUME_UID, SNAPSHOT_NAME, space_efficiency=None,
                                       pool=self.volume_response.pool, is_virt_snap_func=False)
        self.assertIn("invalid", str(ar_context.exception))

    def _prepare_mocks_for_snapshot(self):
        flashcopy_as_target = self.flashcopy_response
        snapshot = self.snapshot_response
        flashcopy_as_target.backgroundcopy = "disabled"
        self.client_mock.get_volume.return_value = snapshot
        self.client_mock.get_volumes_by_pool.return_value = [snapshot]
        self.client_mock.get_flashcopies_by_volume.return_value = [flashcopy_as_target]
        return snapshot

    def test_delete_snapshot(self):
        self._prepare_mocks_for_snapshot()
        self.array.delete_snapshot(SNAPSHOT_VOLUME_UID, INTERNAL_SNAPSHOT_ID)
        self.client_mock.delete_volume.assert_called_once()
        self.client_mock.delete_flashcopy.assert_called_once_with(self.flashcopy_response.id)

    def test_delete_snapshot_with_remove_from_cache(self):
        self._prepare_mocks_for_snapshot()
        self.array.delete_snapshot(self.snapshot_response.id, INTERNAL_SNAPSHOT_ID)

        self.array.volume_cache.remove.assert_called_once_with(self.snapshot_response.name)

    def test_delete_snapshot_flashcopy_fail_with_client_exception(self):
        self._prepare_mocks_for_snapshot()
        self.client_mock.delete_flashcopy.side_effect = ClientException("500")
        self.client_mock.get_volume.return_value = self.snapshot_response
        with self.assertRaises(ClientException):
            self.array.delete_snapshot(SNAPSHOT_VOLUME_UID, INTERNAL_SNAPSHOT_ID)

    def test_delete_snapshot_fail_with_client_exception(self):
        self._prepare_mocks_for_snapshot()
        self.client_mock.delete_volume.side_effect = ClientException("500")
        with self.assertRaises(array_errors.VolumeDeletionError):
            self.array.delete_snapshot(SNAPSHOT_VOLUME_UID, INTERNAL_SNAPSHOT_ID)

    def test_delete_snapshot_fail_with_not_found(self):
        self.client_mock.get_volume.side_effect = NotFound("404")
        with self.assertRaises(array_errors.ObjectNotFoundError):
            self.array.delete_snapshot(SNAPSHOT_VOLUME_UID, INTERNAL_SNAPSHOT_ID)
        self.client_mock.get_volume.side_effect = [self.snapshot_response]
        self.client_mock.get_flashcopies_by_volume.return_value = [self.flashcopy_response]
        with self.assertRaises(array_errors.ObjectNotFoundError):
            self.array.delete_snapshot(SNAPSHOT_VOLUME_UID, INTERNAL_SNAPSHOT_ID)

    def test_delete_snapshot_failed_with_illegal_object_id(self):
        self.client_mock.get_volume.side_effect = InternalServerError("500", message="BE7A0005")
        with self.assertRaises(array_errors.InvalidArgumentError):
            self.array.delete_snapshot(SNAPSHOT_VOLUME_UID, INTERNAL_SNAPSHOT_ID)

    def _prepare_mocks_for_copy_to_existing_volume(self):
        volume = self.volume_response
        self.client_mock.get_volume.return_value = volume
        self.client_mock.get_flashcopies_by_volume.side_effect = \
            [[], [self.flashcopy_response], [self.flashcopy_response]]
        self.client_mock.get_flashcopies.return_value = self.flashcopy_response
        self.client_mock.create_flashcopy.return_value = self.flashcopy_response
        return volume

    def test_copy_to_existing_volume_success(self):
        volume = self._prepare_mocks_for_copy_to_existing_volume()
        self.array.copy_to_existing_volume(volume.id, "0002", 3, 2)
        self.client_mock.extend_volume.assert_called_once_with(volume_id=volume.id,
                                                               new_size_in_bytes=3)
        self.client_mock.create_flashcopy.assert_called_once_with(
            source_volume_id="0002",
            target_volume_id=volume.id,
            options=[FLASHCOPY_PERSISTENT_OPTION,
                     FLASHCOPY_PERMIT_SPACE_EFFICIENT_TARGET_OPTION
                     ])

    def _test_copy_to_existing_volume_raise_errors(self, client_method, client_error, expected_error):
        self._prepare_mocks_for_copy_to_existing_volume()
        client_method.side_effect = client_error
        with self.assertRaises(expected_error):
            self.array.copy_to_existing_volume(VOLUME_UID, "source_id", 3, 2)

    def test_copy_to_existing_volume_raise_not_found(self):
        self._test_copy_to_existing_volume_raise_errors(client_method=self.client_mock.extend_volume,
                                                        client_error=NotFound("404"),
                                                        expected_error=array_errors.ObjectNotFoundError)

    def test_copy_to_existing_volume_raise_illegal_object_id(self):
        self._test_copy_to_existing_volume_raise_errors(client_method=self.client_mock.get_volume,
                                                        client_error=InternalServerError("500", "BE7A0005"),
                                                        expected_error=array_errors.InvalidArgumentError)

    def test_get_object_by_id_snapshot(self):
        snapshot = self._prepare_mocks_for_snapshot()
        return_value = self.array.get_object_by_id(snapshot.id, SNAPSHOT_OBJECT_TYPE)
        self.assertEqual(type(return_value), Snapshot)
        self.assertEqual(return_value.id, self.array._generate_volume_scsi_identifier(snapshot.id))

    def test_get_object_by_id_volume_with_source(self):
        self.flashcopy_response.targetvolume = "0001"
        volume = self._prepare_mocks_for_volume()
        return_value = self.array.get_object_by_id(volume.id, VOLUME_OBJECT_TYPE)
        self.assertEqual(type(return_value), Volume)
        self.assertEqual(return_value.id, self.array._generate_volume_scsi_identifier(volume.id))
        self.assertEqual(return_value.source_id, self.array._generate_volume_scsi_identifier(volume.id))

    def test_get_object_by_id_return_none(self):
        self.client_mock.get_volume.side_effect = NotFound("404")
        return_value = self.array.get_object_by_id("", VOLUME_OBJECT_TYPE)
        self.assertEqual(return_value, None)

    def test_get_object_by_id_errors(self):
        self._prepare_mocks_for_snapshot()
        self.client_mock.get_flashcopies_by_volume.return_value = []
        with self.assertRaises(array_errors.ExpectedSnapshotButFoundVolumeError):
            self.array.get_object_by_id("", SNAPSHOT_OBJECT_TYPE)
        self.flashcopy_response.backgroundcopy = 'enabled'
        self.client_mock.get_flashcopies_by_volume.return_value = [self.flashcopy_response]
        with self.assertRaises(array_errors.ExpectedSnapshotButFoundVolumeError):
            self.array.get_object_by_id("", SNAPSHOT_OBJECT_TYPE)

    def test_get_object_by_id_get_volume_raise_error(self):
        self.client_mock.get_volume.side_effect = ClientException("500", "other error")
        with self.assertRaises(ClientException):
            self.array.get_object_by_id("", VOLUME_OBJECT_TYPE)

    def test_expand_volume_success(self):
        volume = self._prepare_mocks_for_volume()
        self.array.expand_volume(volume_id=volume.id, required_bytes=10)
        self.client_mock.extend_volume.assert_called_once_with(volume_id=volume.id, new_size_in_bytes=10)

    def test_expand_volume_raise_in_use(self):
        volume = self._prepare_mocks_for_volume()
        self.client_mock.get_flashcopies.return_value.out_of_sync_tracks = "55"
        with self.assertRaises(array_errors.ObjectIsStillInUseError):
            self.array.expand_volume(volume_id=volume.id, required_bytes=10)

    def test_expand_volume_raise_illegal(self):
        volume = self._prepare_mocks_for_volume()
        self.client_mock.get_volume.side_effect = [InternalServerError("500", "BE7A0005")]
        with self.assertRaises(array_errors.InvalidArgumentError):
            self.array.expand_volume(volume_id=volume.id, required_bytes=10)

    def test_expand_volume_get_volume_not_found_error(self):
        self.client_mock.get_volume.side_effect = [NotFound("404", message="BE7A0001")]
        with self.assertRaises(array_errors.ObjectNotFoundError):
            self.array.expand_volume(volume_id=VOLUME_UID, required_bytes=10)

    def test_expand_volume_extend_volume_not_found_error(self):
        self.client_mock.get_volume.side_effect = [self.volume_response, self.volume_response]
        self.client_mock.extend_volume.side_effect = [NotFound("404", message="BE7A0001")]
        with self.assertRaises(array_errors.ObjectNotFoundError):
            self.array.expand_volume(volume_id=VOLUME_UID, required_bytes=10)

    def test_expand_volume_extend_not_enough_space_error(self):
        self.client_mock.extend_volume.side_effect = [ClientException("500", message="BE531465")]
        with self.assertRaises(array_errors.NotEnoughSpaceInPool):
            self.array.expand_volume(volume_id=VOLUME_UID, required_bytes=10)

    def test_expand_volume_extend_raise_error(self):
        self.client_mock.extend_volume.side_effect = [ClientException("500", message="other error")]
        with self.assertRaises(ClientException):
            self.array.expand_volume(volume_id=VOLUME_UID, required_bytes=10)
