from unittest.mock import MagicMock

from controllers.servers.host_definer.resource_manager.daemon_set import DaemonSetManager
import controllers.tests.controller_server.host_definer.utils.test_utils as test_utils
import controllers.tests.controller_server.host_definer.settings as test_settings
from controllers.tests.controller_server.host_definer.resource_manager.base_resource_manager import BaseResourceManager


class TestEventManagerTest(BaseResourceManager):
    def setUp(self):
        super().setUp()
        self.daemon_set_manager = DaemonSetManager()
        self.daemon_set_manager.k8s_api = MagicMock()
        self.fake_updated_daemon_set_items = test_utils.get_fake_k8s_daemon_set_items(1, 1)
        self.fake_not_updated_daemon_set_items = test_utils.get_fake_k8s_daemon_set_items(0, 1)
        self.fake_updated_daemon_set = test_utils.get_fake_k8s_daemon_set(1, 1)

    def test_get_updated_daemon_set_so_it_will_return_the_daemon_set_name(self):
        daemon_set_name = self.fake_updated_daemon_set.metadata.name
        self._test_wait_for_updated_daemon_set_called_once(self.fake_updated_daemon_set_items, daemon_set_name)

    def test_get_none_when_fail_to_get_csi_daemon_set(self):
        self._test_wait_for_updated_daemon_set_called_once(None, None)

    def _test_wait_for_updated_daemon_set_called_once(self, daemon_set, expected_result):
        self.daemon_set_manager.k8s_api.list_daemon_set_for_all_namespaces.return_value = daemon_set
        result = self.daemon_set_manager.wait_until_all_daemon_set_pods_are_up_to_date()
        self.assertEqual(result, expected_result)
        self.daemon_set_manager.k8s_api.list_daemon_set_for_all_namespaces.assert_called_once_with(
            test_settings.DRIVER_PRODUCT_LABEL)

    def test_get_not_updated_daemon_and_wait_until_it_will_be_updated(self):
        daemon_set_name = self.fake_updated_daemon_set.metadata.name
        self._test_wait_for_updated_daemon_set_called_twice(
            [self.fake_not_updated_daemon_set_items, self.fake_updated_daemon_set_items], daemon_set_name)

    def test_get_none_when_fail_to_get_csi_daemon_set_in_the_second_time(self):
        self._test_wait_for_updated_daemon_set_called_twice([self.fake_not_updated_daemon_set_items, None], None)

    def _test_wait_for_updated_daemon_set_called_twice(self, daemon_sets, expected_result):
        self.daemon_set_manager.k8s_api.list_daemon_set_for_all_namespaces.side_effect = daemon_sets
        result = self.daemon_set_manager.wait_until_all_daemon_set_pods_are_up_to_date()
        self.assertEqual(result, expected_result)
        self.assertEqual(self.daemon_set_manager.k8s_api.list_daemon_set_for_all_namespaces.call_count, 2)
