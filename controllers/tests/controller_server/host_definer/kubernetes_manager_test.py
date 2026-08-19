import unittest
from mock import Mock, MagicMock, patch
from munch import Munch
from kubernetes.client.rest import ApiException

from controllers.servers.host_definer.kubernetes_manager.manager import KubernetesManager
import controllers.tests.controller_server.host_definer.utils.test_utils as test_utils


class TestKubernetesManagerNewMethods(unittest.TestCase):
    """Test cases for new KubernetesManager methods"""

    def setUp(self):
        """Set up test fixtures"""
        test_utils.patch_kubernetes_manager_init()
        self.k8s_manager = KubernetesManager()
        
        # Mock the APIs
        self.k8s_manager.core_api = Mock()
        self.k8s_manager.storage_api = Mock()

    def tearDown(self):
        """Clean up after tests"""
        patch.stopall()

    def test_get_persistent_volume_by_volume_handle_found(self):
        """Test successful retrieval of PV by volume handle"""

        mock_pv1 = Munch()
        mock_pv1.metadata = Munch(name="pv1")
        mock_pv1.spec = Munch()
        mock_pv1.spec.csi = Munch(volume_handle="volume-handle-1")
        
        mock_pv2 = Munch()
        mock_pv2.metadata = Munch(name="pv2")
        mock_pv2.spec = Munch()
        mock_pv2.spec.csi = Munch(volume_handle="volume-handle-2")
        
        mock_pv_list = Mock(items=[mock_pv1, mock_pv2])
        self.k8s_manager.core_api.list_persistent_volume.return_value = mock_pv_list
        
        result = self.k8s_manager.get_persistent_volume_by_volume_handle("volume-handle-2")
        
        self.assertIsNotNone(result)
        self.assertEqual(result.metadata.name, "pv2")
        self.k8s_manager.core_api.list_persistent_volume.assert_called_once()

    def test_get_persistent_volume_by_volume_handle_not_found(self):
        """Test when PV with given volume handle doesn't exist"""

        mock_pv1 = Munch()
        mock_pv1.metadata = Munch(name="pv1")
        mock_pv1.spec = Munch()
        mock_pv1.spec.csi = Munch(volume_handle="volume-handle-1")

        mock_pv_list = Mock(items=[mock_pv1])
        self.k8s_manager.core_api.list_persistent_volume.return_value = mock_pv_list
        
        result = self.k8s_manager.get_persistent_volume_by_volume_handle("non-existent-handle")
        
        self.assertIsNone(result)

    def test_get_persistent_volume_by_volume_handle_no_csi_spec(self):
        """Test when PV doesn't have CSI spec"""

        mock_pv1 = Munch()
        mock_pv1.metadata = Munch(name="pv1")
        mock_pv1.spec = Munch()  # No csi attribute
        
        mock_pv_list = Mock(items=[mock_pv1])
        self.k8s_manager.core_api.list_persistent_volume.return_value = mock_pv_list
        
        result = self.k8s_manager.get_persistent_volume_by_volume_handle("any-handle")
        
        self.assertIsNone(result)

    def test_get_persistent_volume_by_volume_handle_no_volume_handle_attr(self):
        """Test when CSI spec doesn't have volume_handle attribute"""

        mock_pv1 = Munch()
        mock_pv1.metadata = Munch(name="pv1")
        mock_pv1.spec = Munch()
        mock_pv1.spec.csi = Munch()
        
        mock_pv_list = Mock(items=[mock_pv1])
        self.k8s_manager.core_api.list_persistent_volume.return_value = mock_pv_list
        
        result = self.k8s_manager.get_persistent_volume_by_volume_handle("any-handle")
        
        self.assertIsNone(result)

    def test_get_persistent_volume_by_volume_handle_empty_list(self):
        """Test when no PVs exist"""

        mock_pv_list = Mock(items=[])
        self.k8s_manager.core_api.list_persistent_volume.return_value = mock_pv_list
        
        result = self.k8s_manager.get_persistent_volume_by_volume_handle("any-handle")
        
        self.assertIsNone(result)

    def test_get_persistent_volume_by_volume_handle_api_exception(self):
        """Test when API call raises exception"""

        api_exception = ApiException(status=500, reason="Internal Server Error")
        api_exception.body = "Error listing PVs"
        self.k8s_manager.core_api.list_persistent_volume.side_effect = api_exception
        
        result = self.k8s_manager.get_persistent_volume_by_volume_handle("any-handle")
        
        self.assertIsNone(result)


    def test_get_storage_class_found(self):
        """Test successful retrieval of StorageClass"""

        mock_sc = Munch()
        mock_sc.metadata = Munch(name="test-sc")
        mock_sc.parameters = {"pool": "test-pool", "virt_snap_func": "true"}
        
        self.k8s_manager.storage_api.read_storage_class.return_value = mock_sc
        
        result = self.k8s_manager.get_storage_class("test-sc")
        
        self.assertIsNotNone(result)
        self.assertEqual(result.metadata.name, "test-sc")
        self.k8s_manager.storage_api.read_storage_class.assert_called_once_with(name="test-sc")

    def test_get_storage_class_not_found(self):
        """Test when StorageClass doesn't exist (404)"""

        api_exception = ApiException(status=404, reason="Not Found")
        api_exception.body = "StorageClass not found"
        self.k8s_manager.storage_api.read_storage_class.side_effect = api_exception
        
        result = self.k8s_manager.get_storage_class("non-existent-sc")
        
        self.assertIsNone(result)
        self.k8s_manager.storage_api.read_storage_class.assert_called_once_with(name="non-existent-sc")

    def test_get_storage_class_api_exception_non_404(self):
        """Test when API call raises non-404 exception"""

        api_exception = ApiException(status=500, reason="Internal Server Error")
        api_exception.body = "Server error"
        self.k8s_manager.storage_api.read_storage_class.side_effect = api_exception
        
        with self.assertRaises(ApiException) as context:
            self.k8s_manager.get_storage_class("test-sc")
        
        self.assertEqual(context.exception.status, 500)

    def test_get_storage_class_api_exception_403(self):
        """Test when API call raises 403 Forbidden exception"""

        api_exception = ApiException(status=403, reason="Forbidden")
        api_exception.body = "Access denied"
        self.k8s_manager.storage_api.read_storage_class.side_effect = api_exception
        
        with self.assertRaises(ApiException) as context:
            self.k8s_manager.get_storage_class("test-sc")
        
        self.assertEqual(context.exception.status, 403)

    def test_get_storage_class_with_parameters(self):
        """Test StorageClass with various parameters"""

        mock_sc = Munch()
        mock_sc.metadata = Munch(name="complex-sc")
        mock_sc.parameters = {
            "pool": "test-pool",
            "virt_snap_func": "false",
            "SpaceEfficiency": "thin",
            "volume_name_prefix": "test-prefix"
        }
        
        self.k8s_manager.storage_api.read_storage_class.return_value = mock_sc
        
        result = self.k8s_manager.get_storage_class("complex-sc")
        
        self.assertIsNotNone(result)
        self.assertEqual(len(result.parameters), 4)
        self.assertEqual(result.parameters["virt_snap_func"], "false")
