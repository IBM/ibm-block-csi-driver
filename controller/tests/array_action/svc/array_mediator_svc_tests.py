import unittest
from mock import patch, Mock, MagicMock
from controller.array_action.array_mediator_svc import SVCArrayMediator
from controller.array_action.errors import StorageClassCapabilityNotSupported, PoolDoesNotMatchCapabilities

class TestArrayMediatorSVC(unittest.TestCase):
    @patch("controller.array_action.array_mediator_svc.SVCArrayMediator._connect")
    def setUp(self, connect):
        self.endpoint = "endpoint"
        self.svc = SVCArrayMediator("user", "password", self.endpoint)
        self.svc.client = Mock()

    def test_validate_supported_capabilities_compression(self):
        capabilities = {'SpaceEfficiency': 'Compression'}
        self.svc.is_compression_enabled = MagicMock()
        self.svc.validate_supported_capabilities(capabilities)
        self.svc.is_compression_enabled.assert_called()

    def test_validate_supported_capabilities_dedup(self):
        capabilities = {'SpaceEfficiency': 'Dedup'}
        pool = 'Pool'
        self.svc.is_compression_enabled = MagicMock()
        self.svc.is_deduplication_supported = MagicMock()
        self.svc.validate_supported_capabilities(capabilities, pool)
        self.svc.is_compression_enabled.assert_called()
        self.svc.is_deduplication_supported.assert_called()

    def test_validate_supported_capabilities_Not_SpaceEfficiency(self):
        capabilities = {'SE': 'Thin'}
        with self.assertRaises(StorageClassCapabilityNotSupported):
            self.svc.validate_supported_capabilities(capabilities)

    def test_validate_supported_capabilities_unsupported_SE(self):
        capabilities = {'SpaceEfficiency': 'SE'}
        with self.assertRaises(StorageClassCapabilityNotSupported):
            self.svc.validate_supported_capabilities(capabilities)

    def test_validate_supported_capabilities_array_unsupport_compression(self):
        capabilities = {'SpaceEfficiency': 'Compression'}
        self.svc.is_compression_enabled = MagicMock()
        self.svc.is_compression_enabled.return_value = False
        with self.assertRaises(StorageClassCapabilityNotSupported):
            self.svc.validate_supported_capabilities(capabilities)

    def test_validate_supported_capabilities_dedup_array_unsupport_compression(self):
        capabilities = {'SpaceEfficiency': 'Dedup'}
        self.svc.is_compression_enabled = MagicMock()
        self.svc.is_deduplication_supported = MagicMock()
        self.svc.is_compression_enabled.return_value = False
        with self.assertRaises(StorageClassCapabilityNotSupported):
            self.svc.validate_supported_capabilities(capabilities)

    def test_validate_supported_capabilities_dedup_no_deup_pool(self):
        capabilities = {'SpaceEfficiency': 'Dedup'}
        pool = 'Pool'
        self.svc.is_compression_enabled = MagicMock()
        self.svc.is_deduplication_supported = MagicMock()
        self.svc.is_deduplication_supported.return_value = False
        with self.assertRaises(PoolDoesNotMatchCapabilities):
            self.svc.validate_supported_capabilities(capabilities, pool)
