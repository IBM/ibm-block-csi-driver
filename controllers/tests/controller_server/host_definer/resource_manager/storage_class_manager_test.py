from copy import deepcopy

from controllers.tests.controller_server.host_definer.resource_manager.base_resource_manager import BaseResourceManager
from controllers.servers.host_definer.resource_manager.storage_class import StorageClassManager
import controllers.tests.controller_server.host_definer.utils.test_utils as test_utils


class TestStorageClassManager(BaseResourceManager):
    def setUp(self):
        self.storage_class_manager = StorageClassManager()
        self.fake_storage_class_info = test_utils.get_fake_storage_class_info()

    def test_return_true_when_storage_class_has_csi_as_a_provisioner(self):
        self._test_is_storage_class_has_csi_as_a_provisioner(self.fake_storage_class_info, True)

    def test_return_false_when_storage_class_does_not_have_csi_as_a_provisioner(self):
        storage_class_info = deepcopy(self.fake_storage_class_info)
        storage_class_info.provisioner = 'some_provisioner'
        self._test_is_storage_class_has_csi_as_a_provisioner(storage_class_info, False)

    def _test_is_storage_class_has_csi_as_a_provisioner(self, storage_class_info, expected_result):
        result = self.storage_class_manager.is_storage_class_has_csi_as_a_provisioner(storage_class_info)
        self.assertEqual(result, expected_result)
