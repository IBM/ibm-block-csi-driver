import unittest
from munch import Munch
from mock import patch, NonCallableMagicMock
from controller.array_action.array_mediator_ds8k import DS8KArrayMediator
from controller.array_action.array_mediator_ds8k import shorten_volume_name
from controller.array_action.array_mediator_ds8k import IOPORT_STATUS_ONLINE
from pyds8k.exceptions import ClientError, ClientException, NotFound
from controller.common import settings
import controller.array_action.errors as array_errors
from controller.array_action import config
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
             "pool": "p0",
             }
        )

        self.array = DS8KArrayMediator("user", "password", self.endpoint)

    def test_shorten_volume_name(self):
        test_prefix = "test"
        test_name = "it is a very very long volume name"
        full_name = test_prefix + settings.NAME_PREFIX_SEPARATOR + test_name
        new_name = shorten_volume_name(full_name, test_prefix)

        # new name length should be 16
        self.assertEqual(len(new_name), 16)

        # the volume prefix should not be changed.
        self.assertTrue(new_name.startswith(test_prefix + settings.NAME_PREFIX_SEPARATOR))

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

    def test_validate_capabilities_passed(self):
        self.array.validate_supported_capabilities(
            {config.CAPABILITIES_SPACEEFFICIENCY: config.CAPABILITY_THIN}
        )
        # nothing is raised

    def test_validate_capabilities_failed(self):
        with self.assertRaises(array_errors.StorageClassCapabilityNotSupported):
            self.array.validate_supported_capabilities(
                {config.CAPABILITIES_SPACEEFFICIENCY: "fake"}
            )

    def test_get_volume_with_no_context(self):
        with self.assertRaises(array_errors.VolumeNotFoundError):
            self.array.get_volume("fake_name")

    def test_get_volume_with_pool_context(self):
        self.client_mock.get_volumes_by_pool.return_value = [
            self.volume_response,
        ]
        vol = self.array.get_volume(
            self.volume_response.name,
            volume_context={
                config.CONTEXT_POOL: self.volume_response.pool
            }
        )
        self.assertEqual(vol.volume_name, self.volume_response.name)

    def test_get_volume_with_long_name(self):
        volume_name = "it is a very long name, more than 16 characters"
        short_name = shorten_volume_name(volume_name, "")
        volume_res = self.volume_response
        volume_res.name = short_name
        self.client_mock.get_volumes_by_pool.return_value = [volume_res, ]
        vol = self.array.get_volume(
            volume_name,
            volume_context={
                config.CONTEXT_POOL: self.volume_response.pool
            }
        )
        self.assertEqual(vol.volume_name, short_name)

    def test_get_volume_with_pool_context_not_found(self):
        self.client_mock.get_volumes_by_pool.return_value = [
            self.volume_response,
        ]
        with self.assertRaises(array_errors.VolumeNotFoundError):
            self.array.get_volume(
                "fake_name",
                volume_context={
                    config.CONTEXT_POOL: self.volume_response.pool
                }
            )

    def test_create_volume_with_default_capabilities_succeeded(self):
        self._test_create_volume_with_capabilities_succeeded(False)

    def test_create_volume_with_thin_capabilities_succeeded(self):
        self._test_create_volume_with_capabilities_succeeded(True)

    def _test_create_volume_with_capabilities_succeeded(self, is_thin):
        self.client_mock.create_volume.return_value = self.volume_response
        self.client_mock.get_volume.return_value = self.volume_response
        name = self.volume_response.name
        size_in_bytes = self.volume_response.cap
        if is_thin:
            capabilities = {
                config.CAPABILITIES_SPACEEFFICIENCY: config.CAPABILITY_THIN
            }
            tp = 'ese'
        else:
            capabilities = {}
            tp = 'none'
        pool_id = self.volume_response.pool
        vol = self.array.create_volume(
            name, size_in_bytes, capabilities, pool_id,
        )
        self.client_mock.create_volume.assert_called_once_with(
            pool_id=pool_id,
            capacity_in_bytes=self.volume_response.cap,
            tp=tp,
            name='test_name',
        )
        self.assertEqual(vol.volume_name, self.volume_response.name)

    def test_create_volume_return_existing(self):
        self.client_mock.get_volumes_by_pool.return_value = [
            self.volume_response,
        ]
        pool_id = self.volume_response.pool
        vol = self.array.create_volume(
            self.volume_response.name, "1", {}, pool_id,
        )
        self.assertEqual(vol.volume_name, self.volume_response.name)

    def test_create_volume_with_long_name_succeeded(self):
        volume_name = "it is a very long name, more than 16 characters"
        short_name = shorten_volume_name(volume_name, "")
        volume_res = self.volume_response
        volume_res.name = short_name
        self.client_mock.create_volume.return_value = volume_res
        self.client_mock.get_volume.return_value = volume_res
        size_in_bytes = volume_res.cap
        capabilities = {}
        tp = 'none'
        pool_id = volume_res.pool
        vol = self.array.create_volume(
            volume_name, size_in_bytes, capabilities, pool_id,
        )
        self.client_mock.create_volume.assert_called_once_with(
            pool_id=pool_id,
            capacity_in_bytes=self.volume_response.cap,
            tp=tp,
            name=short_name,
        )
        self.assertEqual(vol.volume_name, short_name)

    def test_create_volume_failed_with_ClientException(self):
        self.client_mock.create_volume.side_effect = ClientException("500")
        with self.assertRaises(array_errors.VolumeCreationError):
            self.array.create_volume("fake_name", 1, {}, "fake_pool")

    def test_create_volume_failed_with_pool_not_found(self):
        self.client_mock.create_volume.side_effect = NotFound("404", message="BE7A0001")
        with self.assertRaises(array_errors.PoolDoesNotExist):
            self.array.create_volume("fake_name", 1, {}, "fake_pool")

    def test_delete_volume(self):
        scsi_id = "6005076306FFD3010000000000000001"
        self.array.delete_volume(scsi_id)
        self.client_mock.delete_volume.assert_called_once_with(volume_id=scsi_id[-4:])

    def test_delete_volume_failed_with_ClientException(self):
        self.client_mock.delete_volume.side_effect = ClientException("500")
        with self.assertRaises(array_errors.VolumeDeletionError):
            self.array.delete_volume("fake_name")

    def test_delete_volume_failed_with_NotFound(self):
        self.client_mock.delete_volume.side_effect = NotFound("404")
        with self.assertRaises(array_errors.VolumeNotFoundError):
            self.array.delete_volume("fake_name")

    def test_get_volume_mappings_failed_with_ClientException(self):
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
        self.assertDictEqual(self.array.get_volume_mappings(scsi_id), {host_name: lunid})

    def test_map_volume_host_not_found(self):
        self.client_mock.map_volume_to_host.side_effect = NotFound("404")
        with self.assertRaises(array_errors.HostNotFoundError):
            self.array.map_volume("fake_name", "fake_host")

    def test_map_volume_volume_not_found(self):
        self.client_mock.map_volume_to_host.side_effect = ClientException("500", "[BE586015]")
        with self.assertRaises(array_errors.VolumeNotFoundError):
            self.array.map_volume("fake_name", "fake_host")

    def test_map_volume_failed_with_ClientException(self):
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
        self.client_mock.get_host_mappings.side_effect = NotFound("404")
        with self.assertRaises(array_errors.HostNotFoundError):
            self.array.unmap_volume("fake_name", "fake_host")

    def test_unmap_volume_volume_not_found(self):
        self.client_mock.get_host_mappings.return_value = []
        with self.assertRaises(array_errors.VolumeNotFoundError):
            self.array.unmap_volume("fake_name", "fake_host")

    def test_unmap_volume_failed_with_ClientException(self):
        volume_id = "0001"
        lunid = "1"
        host_name = "test_host"
        scsi_id = "6005076306FFD301000000000000{}".format(volume_id)
        self.client_mock.get_host_mappings.return_value = [
            Munch({
                "volume": volume_id,
                "lunid": lunid
            })
        ]
        self.client_mock.unmap_volume_from_host.side_effect = ClientException("500")
        with self.assertRaises(array_errors.UnMappingError):
            self.array.unmap_volume(scsi_id, host_name)

    def test_unmap_volume(self):
        volume_id = "0001"
        lunid = "1"
        host_name = "test_host"
        scsi_id = "6005076306FFD301000000000000{}".format(volume_id)
        self.client_mock.get_host_mappings.return_value = [
            Munch({
                "volume": volume_id,
                "lunid": lunid
            })
        ]
        self.array.unmap_volume(scsi_id, host_name)
        self.client_mock.unmap_volume_from_host.assert_called_once_with(host_name=host_name, lunid=lunid)

    def test_get_array_fc_wwns_failed_with_ClientException(self):
        self.client_mock.get_fcports.side_effect = ClientException("500")
        with self.assertRaises(ClientException):
            self.array.get_array_fc_wwns()

    def test_get_array_fc_wwns_skip_offline_port(self):
        wwpn1 = "fake_wwpn"
        wwpn2 = "offine_wwpn"
        self.client_mock.get_fcports.return_value = [
            Munch({
                "wwpn": wwpn1,
                "state": IOPORT_STATUS_ONLINE,
            }),
            Munch({
                "wwpn": wwpn2,
                "state": "offline",
            }),
        ]
        self.assertListEqual(self.array.get_array_fc_wwns(), [wwpn1])

    def test_get_array_fc_wwns(self):
        wwpn = "fake_wwpn"
        self.client_mock.get_fcports.return_value = [
            Munch({
                "wwpn": wwpn,
                "state": IOPORT_STATUS_ONLINE,
            })
        ]
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
