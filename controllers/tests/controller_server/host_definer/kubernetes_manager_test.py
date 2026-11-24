import json
import unittest
from mock import Mock, patch
from munch import Munch
from kubernetes.client import ApiClient
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
        self.k8s_manager.core_api.api_client = ApiClient()
        self.k8s_manager.storage_api = Mock()

    def tearDown(self):
        """Clean up after tests"""
        patch.stopall()

    @staticmethod
    def _make_pv_list_response(items, continue_token=None):
        """Helper to create a raw HTTP response mock for _preload_content=False"""
        metadata = {"continue": continue_token} if continue_token else {}
        payload = {
            "apiVersion": "v1",
            "kind": "PersistentVolumeList",
            "metadata": metadata,
            "items": items,
        }
        mock_response = Mock()
        mock_response.data = json.dumps(payload).encode("utf-8")
        return mock_response

    def test_get_persistent_volume_by_volume_handle_found(self):
        """Test successful retrieval of PV by volume handle"""

        pv1 = {
            "metadata": {"name": "pv1"},
            "spec": {"csi": {"driver": "test.csi", "volumeHandle": "volume-handle-1"}},
        }
        pv2 = {
            "metadata": {"name": "pv2"},
            "spec": {"csi": {"driver": "test.csi", "volumeHandle": "volume-handle-2"}},
        }

        mock_response = self._make_pv_list_response([pv1, pv2])
        self.k8s_manager.core_api.list_persistent_volume.return_value = mock_response

        result = self.k8s_manager.get_persistent_volume_by_volume_handle("volume-handle-2")

        self.assertIsNotNone(result)
        self.assertEqual(result.metadata.name, "pv2")
        self.assertEqual(result.spec.csi.volume_handle, "volume-handle-2")
        self.k8s_manager.core_api.list_persistent_volume.assert_called_once()
        mock_response.release_conn.assert_called_once()

    def test_get_persistent_volume_by_volume_handle_pagination(self):
        """Test finding a PV across multiple paginated responses"""

        pv1 = {
            "metadata": {"name": "pv1"},
            "spec": {"csi": {"driver": "test.csi", "volumeHandle": "volume-handle-1"}},
        }
        pv2 = {
            "metadata": {"name": "pv2"},
            "spec": {"csi": {"driver": "test.csi", "volumeHandle": "volume-handle-2"}},
        }

        page1 = self._make_pv_list_response([pv1], continue_token="token-page-2")
        page2 = self._make_pv_list_response([pv2], continue_token=None)
        self.k8s_manager.core_api.list_persistent_volume.side_effect = [page1, page2]

        result = self.k8s_manager.get_persistent_volume_by_volume_handle("volume-handle-2")

        self.assertIsNotNone(result)
        self.assertEqual(result.metadata.name, "pv2")
        self.assertEqual(self.k8s_manager.core_api.list_persistent_volume.call_count, 2)
        second_call_kwargs = self.k8s_manager.core_api.list_persistent_volume.call_args_list[1].kwargs
        self.assertEqual(second_call_kwargs["_continue"], "token-page-2")
        page1.release_conn.assert_called_once()
        page2.release_conn.assert_called_once()

    def test_get_persistent_volume_by_volume_handle_410_retry(self):
        """Test restarting pagination once when a continue token expires with 410 Gone"""

        pv1 = {
            "metadata": {"name": "pv1"},
            "spec": {"csi": {"driver": "test.csi", "volumeHandle": "volume-handle-1"}},
        }
        pv2 = {
            "metadata": {"name": "pv2"},
            "spec": {"csi": {"driver": "test.csi", "volumeHandle": "volume-handle-2"}},
        }

        page1_initial = self._make_pv_list_response([pv1], continue_token="expired-token")
        expired_exc = ApiException(status=410, reason="Gone")
        page1_retry = self._make_pv_list_response([pv1, pv2], continue_token=None)

        self.k8s_manager.core_api.list_persistent_volume.side_effect = [
            page1_initial,
            expired_exc,
            page1_retry,
        ]

        result = self.k8s_manager.get_persistent_volume_by_volume_handle("volume-handle-2")

        self.assertIsNotNone(result)
        self.assertEqual(result.metadata.name, "pv2")
        self.assertEqual(self.k8s_manager.core_api.list_persistent_volume.call_count, 3)

    def test_get_persistent_volume_by_volume_handle_not_found(self):
        """Test when PV with given volume handle doesn't exist"""

        pv1 = {
            "metadata": {"name": "pv1"},
            "spec": {"csi": {"driver": "test.csi", "volumeHandle": "volume-handle-1"}},
        }

        mock_response = self._make_pv_list_response([pv1])
        self.k8s_manager.core_api.list_persistent_volume.return_value = mock_response

        result = self.k8s_manager.get_persistent_volume_by_volume_handle("non-existent-handle")

        self.assertIsNone(result)
        mock_response.release_conn.assert_called_once()

    def test_get_persistent_volume_by_volume_handle_no_csi_spec(self):
        """Test when PV doesn't have CSI spec"""

        pv1 = {
            "metadata": {"name": "pv1"},
            "spec": {},  # No csi field
        }

        mock_response = self._make_pv_list_response([pv1])
        self.k8s_manager.core_api.list_persistent_volume.return_value = mock_response

        result = self.k8s_manager.get_persistent_volume_by_volume_handle("any-handle")

        self.assertIsNone(result)

    def test_get_persistent_volume_by_volume_handle_no_volume_handle_attr(self):
        """Test when CSI spec doesn't have volume_handle attribute"""

        pv1 = {
            "metadata": {"name": "pv1"},
            "spec": {"csi": {}},
        }

        mock_response = self._make_pv_list_response([pv1])
        self.k8s_manager.core_api.list_persistent_volume.return_value = mock_response

        result = self.k8s_manager.get_persistent_volume_by_volume_handle("any-handle")

        self.assertIsNone(result)

    def test_get_persistent_volume_by_volume_handle_empty_list(self):
        """Test when no PVs exist"""

        mock_response = self._make_pv_list_response([])
        self.k8s_manager.core_api.list_persistent_volume.return_value = mock_response

        result = self.k8s_manager.get_persistent_volume_by_volume_handle("any-handle")

        self.assertIsNone(result)
        mock_response.release_conn.assert_called_once()

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
