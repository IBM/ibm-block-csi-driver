import unittest

from mock import patch, NonCallableMagicMock
from munch import Munch
from pyds8k.exceptions import ClientError, ClientException, InternalServerError, NotFound

import controller.array_action.errors as array_errors
from controller.array_action import config
from controller.array_action.array_action_types import Volume, Snapshot
from controller.array_action.array_mediator_ds8k import DS8KArrayMediator, FLASHCOPY_PERSISTENT_OPTION, \
    FLASHCOPY_PERMIT_SPACE_EFFICIENT_TARGET_OPTION
from controller.array_action.array_mediator_ds8k import LOGIN_PORT_WWPN, LOGIN_PORT_STATE, \
    LOGIN_PORT_STATE_ONLINE
from controller.common.node_info import Initiators


class TestArrayMediatorDS8K(unittest.TestCase):

    def setUp(self):
        self.endpoint = ["1.2.3.4"]
        self.client_mock = NonCallableMagicMock()
        patcher = patch('controller.array_action.array_mediator_ds8k.RESTClient')
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
             "name": "test_name",
             "pool": "fake_pool",
             "tp": "ese",
             "flashcopy": ""
             }
        )

        self.snapshot_response = Munch(
            {"cap": "1073741824",
             "id": "0002",
             "name": "test_name",
             "pool": "fake_pool",
             "flashcopy": ""
             }
        )

        self.snapshot_response = Munch(
            {"cap": "1073741824",
             "id": "0002",
             "name": "test_name",
             "pool": "fake_pool",
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

        self.array = DS8KArrayMediator("user", "password", self.endpoint)

    def test_connect_with_incorrect_credentials(self):
        self.client_mock.get_system.side_effect = \
            ClientError("400", "BE7A002D")
        with self.assertRaises(array_errors.CredentialsError):
            DS8KArrayMediator("user", "password", self.endpoint)

    def test_connect_to_unsupported_system(self):
        self.client_mock.get_system.return_value = \
            Munch({"bundle": "87.50.34.0"})
        with self.assertRaises(array_errors.UnsupportedStorageVersionError):
            DS8KArrayMediator("user", "password", self.endpoint)

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
            self.array.validate_supported_space_efficiency(
                "fake"
            )

    def test_get_volume_with_no_pool(self):
        with self.assertRaises(array_errors.PoolParameterIsMissing):
            self.array.get_volume("fake_name")

    def test_get_volume_with_pool_context(self):
        self.client_mock.get_volumes_by_pool.return_value = [
            self.volume_response,
        ]
        volume = self.array.get_volume(
            self.volume_response.name,
            pool=self.volume_response.pool
        )
        self.assertEqual(volume.name, self.volume_response.name)

    def test_get_volume_with_pool_context_not_found(self):
        self.client_mock.get_volumes_by_pool.return_value = [
            self.volume_response,
        ]
        with self.assertRaises(array_errors.ObjectNotFoundError):
            self.array.get_volume(
                "fake_name",
                pool=self.volume_response.pool
            )

    def test_create_volume_with_default_space_efficiency_success(self):
        self._test_create_volume_with_space_efficiency_success('none')

    def test_create_volume_with_thin_space_efficiency_success(self):
        self._test_create_volume_with_space_efficiency_success('thin')

    def test_create_volume_with_empty_space_efficiency_success(self):
        self._test_create_volume_with_space_efficiency_success('')

    def _test_create_volume_with_space_efficiency_success(self, space_efficiency):
        self.client_mock.create_volume.return_value = self.volume_response
        self.client_mock.get_volume.return_value = self.volume_response
        name = self.volume_response.name
        size_in_bytes = self.volume_response.cap
        pool_id = self.volume_response.pool
        volume = self.array.create_volume(
            name, size_in_bytes, space_efficiency, pool_id,
        )
        if space_efficiency == 'thin':
            space_efficiency = 'ese'
        else:
            space_efficiency = 'none'
        self.client_mock.create_volume.assert_called_once_with(
            pool_id=pool_id,
            capacity_in_bytes=self.volume_response.cap,
            tp=space_efficiency,
            name='test_name',
        )
        self.assertEqual(volume.name, self.volume_response.name)

    def test_create_volume_raise_already_exists(self):
        self.client_mock.get_volumes_by_pool.return_value = [
            self.volume_response,
        ]
        pool_id = self.volume_response.pool
        with self.assertRaises(array_errors.VolumeAlreadyExists):
            self.array.create_volume(self.volume_response.name, "1", 'thin', pool_id)

    def test_create_volume_fail_with_ClientException(self):
        self.client_mock.create_volume.side_effect = ClientException("500")
        with self.assertRaises(array_errors.VolumeCreationError):
            self.array.create_volume("fake_name", 1, 'thin', "fake_pool")

    def test_create_volume_fail_with_pool_not_found(self):
        self.client_mock.create_volume.side_effect = NotFound("404", message="BE7A0001")
        with self.assertRaises(array_errors.PoolDoesNotExist):
            self.array.create_volume("fake_name", 1, 'thin', "fake_pool")

    def test_create_volume_fail_with_incorrect_id(self):
        self.client_mock.get_volumes_by_pool.side_effect = InternalServerError("500", message="BE7A0005")
        with self.assertRaises(array_errors.PoolDoesNotExist):
            self.array.create_volume("fake_name", 1, 'thin', "fake_pool")

    def test_create_volume_fail_with_no_space_in_pool(self):
        self.client_mock.get_volumes_by_pool.side_effect = ClientException("500", message="BE534459")
        with self.assertRaises(array_errors.NotEnoughSpaceInPool):
            self.array.create_volume("fake_name", 1, 'thin', "fake_pool")

    def test_delete_volume(self):
        scsi_id = "6005076306FFD3010000000000000001"
        self.array.delete_volume(scsi_id)
        self.client_mock.delete_volume.assert_called_once_with(volume_id=scsi_id[-4:])

    def test_delete_volume_fail_with_ClientException(self):
        self.client_mock.delete_volume.side_effect = ClientException("500")
        with self.assertRaises(array_errors.VolumeDeletionError):
            self.array.delete_volume("fake_id")

    def test_delete_volume_fail_with_NotFound(self):
        self.client_mock.delete_volume.side_effect = NotFound("404")
        with self.assertRaises(array_errors.ObjectNotFoundError):
            self.array.delete_volume("fake_id")

    def test_delete_volume_failed_with_illegal_object_id(self):
        self.client_mock.get_volume.side_effect = InternalServerError("500", message="BE7A0005")
        with self.assertRaises(array_errors.IllegalObjectID):
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

    def test_get_volume_mappings_fail_with_ClientException(self):
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
            self.array.map_volume("fake_name", "fake_host")

    def test_map_volume_volume_not_found(self):
        self.client_mock.map_volume_to_host.side_effect = ClientException("500", "[BE586015]")
        with self.assertRaises(array_errors.ObjectNotFoundError):
            self.array.map_volume("fake_name", "fake_host")

    def test_map_volume_fail_with_ClientException(self):
        self.client_mock.map_volume_to_host.side_effect = ClientException("500")
        with self.assertRaises(array_errors.MappingError):
            self.array.map_volume("fake_name", "fake_host")

    def test_map_volume(self):
        scsi_id = "6005076306FFD3010000000000000001"
        host_name = "test_name"
        self.client_mock.map_volume_to_host.return_value = Munch({"lunid": "01"})
        lun = self.array.map_volume(scsi_id, host_name)
        self.assertEqual(lun, 1)
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

    def test_unmap_volume_fail_with_ClientException(self):
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

    def test_get_array_fc_wwns_fail_with_ClientException(self):
        self.client_mock.get_host.side_effect = ClientException("500")
        with self.assertRaises(ClientException):
            self.array.get_array_fc_wwns()

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
        self.assertListEqual(self.array.get_array_fc_wwns(), [wwpn1])

    def test_get_array_fc_wwns(self):
        wwpn = "fake_wwpn"
        self.client_mock.get_host.return_value = Munch(
            {"login_ports": [
                {
                    LOGIN_PORT_WWPN: wwpn,
                    LOGIN_PORT_STATE: LOGIN_PORT_STATE_ONLINE
                }
            ]})
        self.assertListEqual(self.array.get_array_fc_wwns(), [wwpn])

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
            Initiators('', [wwpn1, wwpn2])
        )
        self.assertEqual(host, host_name)
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
            Initiators('', [wwpn1, "another_wwpn"])
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
                Initiators('', ["new_wwpn", "another_wwpn"])
            )

    def test_get_snapshot_not_exist_return_none(self):
        self.client_mock.get_snapshot.side_effect = [ClientError("400", "BE7A002D")]
        snapshot = self.array.get_snapshot("volume_id", "fake_name", pool=self.volume_response.pool)
        self.assertIsNone(snapshot)

    def test_get_snapshot_get_flashcopy_not_exist_raise_error(self):
        self._prepare_mocks_for_snapshot()
        self.client_mock.get_flashcopies_by_volume.return_value = []

        with self.assertRaises(array_errors.ExpectedSnapshotButFoundVolumeError):
            self.array.get_snapshot("volume_id", "test_name", pool=self.volume_response.pool)

    def test_get_snapshot_failed_with_incorrect_id(self):
        self.client_mock.get_volume.side_effect = InternalServerError("500", message="BE7A0005")
        with self.assertRaises(array_errors.IllegalObjectID):
            self.array.get_snapshot("volume_id", "test_name", pool=None)

    def test_get_snapshot_success(self):
        target_volume = self._prepare_mocks_for_snapshot()
        volume = self.array.get_snapshot("volume_id", "test_name", pool=self.volume_response.pool)
        self.assertEqual(volume.name, target_volume.name)

    def test_get_snapshot_no_pool_success(self):
        target_volume = self._prepare_mocks_for_snapshot()
        volume = self.array.get_snapshot("volume_id", "test_name", pool=None)
        self.assertEqual(volume.name, target_volume.name)

    def _prepare_mocks_for_create_snapshot(self):
        self.client_mock.get_volumes_by_pool.return_value = [self.volume_response]
        volume = Munch(
            {"cap": "1073741824",
             "id": "0001",
             "name": "target_volume",
             "pool": "fake_pool",
             "tp": "ese",
             "flashcopy": ""
             }
        )
        return volume

    def test_create_snapshot_create_volume_error(self):
        self._prepare_mocks_for_create_snapshot()
        self.client_mock.create_volume.side_effect = ClientException("500")

        with self.assertRaises(array_errors.VolumeCreationError):
            self.array.create_snapshot("volume_id", "target_volume", pool=self.volume_response.pool)

    def test_create_snapshot_create_fcrel_error(self):
        volume = self._prepare_mocks_for_create_snapshot()
        self.client_mock.create_volume.return_value = volume
        self.client_mock.get_volume.return_value = volume
        self.client_mock.create_flashcopy.side_effect = ClientException("500")

        with self.assertRaises(Exception):
            self.array.create_snapshot("volume_id", "target_volume", pool=self.volume_response.pool)

    def test_create_snapshot_get_volume_not_found(self):
        volume = self._prepare_mocks_for_create_snapshot()
        self.client_mock.create_volume.return_value = volume
        self.client_mock.get_volume.side_effect = NotFound("404")

        with self.assertRaises(array_errors.ObjectNotFoundError):
            self.array.create_snapshot("volume_id", "target_volume", pool=self.volume_response.pool)

    def test_create_snapshot_create_flashcopy_volume_not_found(self):
        volume = self._prepare_mocks_for_create_snapshot()
        self.client_mock.create_volume.return_value = volume
        self.client_mock.get_volume.return_value = volume
        self.client_mock.create_flashcopy.side_effect = ClientException("500", message="00000013")

        with self.assertRaises(array_errors.ObjectNotFoundError):
            self.array.create_snapshot("volume_id", "target_volume", pool=self.volume_response.pool)

    def test_create_snapshot_already_exist(self):
        volume = self._prepare_mocks_for_create_snapshot()
        self.client_mock.create_volume.return_value = volume
        self.client_mock.get_volume.return_value = volume
        self.client_mock.create_flashcopy.side_effect = ClientException("500",
                                                                        message="000000AE")
        with self.assertRaises(array_errors.SnapshotAlreadyExists):
            self.array.create_snapshot("volume_id", "target_volume", pool=self.volume_response.pool)

    def test_create_snapshot_failed_with_incorrect_id(self):
        self.client_mock.get_volume.side_effect = InternalServerError("500", message="BE7A0005")
        with self.assertRaises(array_errors.IllegalObjectID):
            self.array.create_snapshot("volume_id", "test_name", pool=None)

    def test_create_snapshot_success(self):
        volume = self._prepare_mocks_for_create_snapshot()
        self.client_mock.create_volume.return_value = volume
        self.client_mock.get_volume.return_value = volume
        self.client_mock.create_flashcopy.return_value = self.flashcopy_response
        self.client_mock.get_flashcopies.return_value = self.flashcopy_response
        snapshot = self.array.create_snapshot("volume_id", "target_volume", pool=self.volume_response.pool)

        self.assertEqual(snapshot.name, volume.name)
        self.assertEqual(snapshot.id, self.array._generate_volume_scsi_identifier(volume.id))

    def test_create_snapshot_not_valid(self):
        volume = self._prepare_mocks_for_create_snapshot()
        self.client_mock.create_volume.return_value = volume
        self.client_mock.get_volume.return_value = volume
        flashcopy_response = Munch(
            {"sourcevolume": {"id": "0001"},
             "targetvolume": {"id": "0002"},
             "id": "0001:0002",
             "state": "invalid"
             })
        self.client_mock.create_flashcopy.return_value = flashcopy_response
        with self.assertRaises(ValueError):
            self.array.create_snapshot("volume_id", "target_volume", pool=self.volume_response.pool)

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
        self.array.delete_snapshot("test_id")
        self.client_mock.delete_volume.assert_called_once()
        self.client_mock.delete_flashcopy.assert_called_once_with(self.flashcopy_response.id)

    def test_delete_snapshot_flashcopy_fail_with_ClientException(self):
        self._prepare_mocks_for_snapshot()
        self.client_mock.delete_flashcopy.side_effect = ClientException("500")
        self.client_mock.get_volume.return_value = self.snapshot_response
        with self.assertRaises(ClientException):
            self.array.delete_snapshot("fake_name")

    def test_delete_snapshot_fail_with_ClientException(self):
        self._prepare_mocks_for_snapshot()
        self.client_mock.delete_volume.side_effect = ClientException("500")
        with self.assertRaises(array_errors.VolumeDeletionError):
            self.array.delete_snapshot("fake_id")

    def test_delete_snapshot_fail_with_NotFound(self):
        self.client_mock.get_volume.side_effect = NotFound("404")
        with self.assertRaises(array_errors.ObjectNotFoundError):
            self.array.delete_snapshot("fake_id")
        self.client_mock.get_volume.side_effect = [self.snapshot_response]
        self.client_mock.get_flashcopies_by_volume.return_value = [self.flashcopy_response]
        with self.assertRaises(array_errors.ObjectNotFoundError):
            self.array.delete_snapshot("fake_id")

    def test_delete_snapshot_failed_with_illegal_object_id(self):
        self.client_mock.get_volume.side_effect = InternalServerError("500", message="BE7A0005")
        with self.assertRaises(array_errors.IllegalObjectID):
            self.array.delete_snapshot("fake_id")

    def _prepare_mocks_for_copy_to_existing_volume(self):
        volume = self.volume_response
        self.client_mock.get_volumes_by_pool.side_effect = [[volume], [Munch(
            {"cap": "1073741824",
             "id": "0002",
             "name": "source_name",
             "pool": "fake_pool",
             "tp": "ese",
             "flashcopy": []
             }
        )]]
        self.client_mock.get_volume.return_value = volume
        self.client_mock.get_flashcopies_by_volume.side_effect = \
            [[], [self.flashcopy_response], [self.flashcopy_response]]
        self.client_mock.get_flashcopies.return_value = self.flashcopy_response
        self.client_mock.create_flashcopy.return_value = self.flashcopy_response
        return volume

    def test_copy_to_existing_volume_success(self):
        volume = self._prepare_mocks_for_copy_to_existing_volume()
        self.array.copy_to_existing_volume_from_source("test_name", "source_name", 3, 2, "fake_pool")
        self.client_mock.extend_volume.assert_called_once_with(volume_id=volume.id,
                                                               new_size_in_bytes=3)
        self.client_mock.create_flashcopy.assert_called_once_with(
            source_volume_id="0002",
            target_volume_id=volume.id,
            options=[FLASHCOPY_PERSISTENT_OPTION,
                     FLASHCOPY_PERMIT_SPACE_EFFICIENT_TARGET_OPTION
                     ])

    def test_copy_to_existing_volume_raise_not_found(self):
        self._prepare_mocks_for_copy_to_existing_volume()
        self.client_mock.extend_volume.side_effect = NotFound("404")
        with self.assertRaises(array_errors.ObjectNotFoundError):
            self.array.copy_to_existing_volume_from_source("test_name", "source_name", 3, 2, "fake_pool")

    def test_get_object_by_id_snapshot(self):
        snapshot = self._prepare_mocks_for_snapshot()
        return_value = self.array.get_object_by_id(snapshot.id, "snapshot")
        self.assertEqual(type(return_value), Snapshot)
        self.assertEqual(return_value.id, self.array._generate_volume_scsi_identifier(snapshot.id))

    def test_get_object_by_id_volume_with_source(self):
        self.flashcopy_response.targetvolume = "0001"
        volume = self._prepare_mocks_for_volume()
        return_value = self.array.get_object_by_id(volume.id, "volume")
        self.assertEqual(type(return_value), Volume)
        self.assertEqual(return_value.id, self.array._generate_volume_scsi_identifier(volume.id))
        self.assertEqual(return_value.copy_source_id, self.array._generate_volume_scsi_identifier(volume.id))

    def test_get_object_by_id_return_none(self):
        self.client_mock.get_volume.side_effect = NotFound("404")
        return_value = self.array.get_object_by_id("", "volume")
        self.assertEqual(return_value, None)

    def test_get_object_by_id_errors(self):
        self._prepare_mocks_for_snapshot()
        self.client_mock.get_flashcopies_by_volume.return_value = []
        with self.assertRaises(array_errors.ExpectedSnapshotButFoundVolumeError):
            self.array.get_object_by_id("", "snapshot")
        self.flashcopy_response.backgroundcopy = 'enabled'
        self.client_mock.get_flashcopies_by_volume.return_value = [self.flashcopy_response]
        with self.assertRaises(array_errors.ExpectedSnapshotButFoundVolumeError):
            self.array.get_object_by_id("", "snapshot")

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
        with self.assertRaises(array_errors.IllegalObjectID):
            self.array.expand_volume(volume_id=volume.id, required_bytes=10)

    def test_expand_volume_get_volume_not_found_error(self):
        self.client_mock.get_volume.side_effect = [NotFound("404", message="BE7A0001")]
        with self.assertRaises(array_errors.ObjectNotFoundError):
            self.array.expand_volume(volume_id="test_id", required_bytes=10)

    def test_expand_volume_extend_volume_not_found_error(self):
        self.client_mock.get_volume.side_effect = [self.volume_response, self.volume_response]
        self.client_mock.extend_volume.side_effect = [NotFound("404", message="BE7A0001")]
        with self.assertRaises(array_errors.ObjectNotFoundError):
            self.array.expand_volume(volume_id="test_id", required_bytes=10)

    def test_expand_volume_extend_not_enough_space_error(self):
        self.client_mock.extend_volume.side_effect = [ClientException("500", message="BE531465")]
        with self.assertRaises(array_errors.NotEnoughSpaceInPool):
            self.array.expand_volume(volume_id="test_id", required_bytes=10)
