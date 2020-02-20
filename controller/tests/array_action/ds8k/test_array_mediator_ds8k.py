import unittest
from mock import patch, NonCallableMagicMock
from controller.array_action.array_mediator_ds8k import DS8KArrayMediator
from controller.array_action.array_mediator_ds8k import shorten_volume_name
from controller.array_action.array_mediator_ds8k import SYSTEM_CODE_LEVEL, \
    VOLUME_LOGICAL_CAP, VOLUME_ID, VOLUME_NAME, VOLUME_POOL_ID
from pyds8k.exceptions import ClientError, ClientException, NotFound
import controller.array_action.errors as array_errors
from controller.array_action import config


class TestArrayMediatorDS8K(unittest.TestCase):

    def setUp(self):
        self.endpoint = ["1.2.3.4"]
        self.client_mock = NonCallableMagicMock()
        patcher = patch('controller.array_action.array_mediator_ds8k.RESTClient')
        self.connect_mock = patcher.start()
        self.addCleanup(patcher.stop)
        self.connect_mock.return_value = self.client_mock

        self.client_mock.get_system.return_value = [
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
        ]

        self.volume_response = {
            VOLUME_LOGICAL_CAP: "1073741824",
            VOLUME_ID: "0001",
            VOLUME_NAME: "test_name",
            VOLUME_POOL_ID: "p0",
        }

        self.array = DS8KArrayMediator("user", "password", self.endpoint)

    def test_connect_with_incorrect_credentials(self):
        self.client_mock.get_system.side_effect = \
            ClientError("400", "BE7A002D")
        with self.assertRaises(array_errors.CredentialsError):
            DS8KArrayMediator("user", "password", self.endpoint)

    def test_connect_to_unsupported_system(self):
        self.client_mock.get_system.return_value = \
            [{SYSTEM_CODE_LEVEL: "87.50.34.0"}]
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
        self.client_mock.list_extentpool_volumes.return_value = [
            self.volume_response,
        ]
        vol = self.array.get_volume(
            self.volume_response[VOLUME_NAME],
            volume_context={
                config.CONTEXT_POOL: self.volume_response[VOLUME_POOL_ID]
            }
        )
        self.assertEqual(vol.volume_name, self.volume_response[VOLUME_NAME])

    def test_get_volume_with_long_name(self):
        volume_name = "it is a very long name, more than 16 characters"
        short_name = shorten_volume_name(volume_name)
        volume_res = self.volume_response
        volume_res[VOLUME_NAME] = short_name
        self.client_mock.list_extentpool_volumes.return_value = [volume_res, ]
        vol = self.array.get_volume(
            volume_name,
            volume_context={
                config.CONTEXT_POOL: self.volume_response[VOLUME_POOL_ID]
            }
        )
        self.assertEqual(vol.volume_name, short_name)

    def test_get_volume_with_pool_context_not_found(self):
        self.client_mock.list_extentpool_volumes.return_value = [
            self.volume_response,
        ]
        with self.assertRaises(array_errors.VolumeNotFoundError):
            self.array.get_volume(
                "fake_name",
                volume_context={
                    config.CONTEXT_POOL: self.volume_response[VOLUME_POOL_ID]
                }
            )

    def test_create_volume_with_default_capabilities_succeeded(self):
        self._test_create_volume_with_capabilities_succeeded(False)

    def test_create_volume_with_thin_capabilities_succeeded(self):
        self._test_create_volume_with_capabilities_succeeded(True)

    def _test_create_volume_with_capabilities_succeeded(self, is_thin):
        self.client_mock.create_volume.return_value = [
            self.volume_response,
        ]
        name = self.volume_response[VOLUME_NAME]
        size_in_bytes = self.volume_response[VOLUME_LOGICAL_CAP]
        if is_thin:
            capabilities = {
                config.CAPABILITIES_SPACEEFFICIENCY: config.CAPABILITY_THIN
            }
            tp = 'ese'
        else:
            capabilities = {}
            tp = 'none'
        pool_id = self.volume_response[VOLUME_POOL_ID]
        vol = self.array.create_volume(
            name, size_in_bytes, capabilities, pool_id,
        )
        self.client_mock.create_volume.assert_called_once_with(
            pool_id=pool_id,
            capacity_in_bytes='1073741824',
            tp=tp,
            name='test_name',
        )
        self.assertEqual(vol.volume_name, self.volume_response[VOLUME_NAME])

    def test_create_volume_with_long_name_succeeded(self):
        volume_name = "it is a very long name, more than 16 characters"
        short_name = shorten_volume_name(volume_name)
        volume_res = self.volume_response
        volume_res[VOLUME_NAME] = short_name
        self.client_mock.create_volume.return_value = [volume_res, ]
        size_in_bytes = volume_res[VOLUME_LOGICAL_CAP]
        capabilities = {}
        tp = 'none'
        pool_id = volume_res[VOLUME_POOL_ID]
        vol = self.array.create_volume(
            volume_name, size_in_bytes, capabilities, pool_id,
        )
        self.client_mock.create_volume.assert_called_once_with(
            pool_id=pool_id,
            capacity_in_bytes='1073741824',
            tp=tp,
            name=short_name,
        )
        self.assertEqual(vol.volume_name, short_name)

    def test_create_volume_failed_with_ClientException(self):
        self.client_mock.create_volume.side_effect = ClientException("500")
        with self.assertRaises(array_errors.VolumeCreationError):
            self.array.create_volume("fake_name", 1, {}, "fake_pool")

    def test_create_volume_failed_with_error_status(self):
        self.client_mock.create_volume.return_value = [
            {"status": "failed"},
        ]
        with self.assertRaises(array_errors.VolumeCreationError):
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
