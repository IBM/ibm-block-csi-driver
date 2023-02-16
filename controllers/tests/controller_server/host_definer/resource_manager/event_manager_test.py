from controllers.tests.controller_server.host_definer.resource_manager.base_resource_manager import BaseResourceManager
from controllers.servers.host_definer.resource_manager.event import EventManager
import controllers.tests.controller_server.host_definer.utils.test_utils as test_utils
import controllers.tests.controller_server.host_definer.settings as test_settings


class TestEventManagerTest(BaseResourceManager):
    def setUp(self):
        self.event_manager = EventManager()
        self.fake_host_definition_info = test_utils.get_fake_host_definition_info()

    def test_generate_k8s_normal_event_success(self):
        self._test_generate_k8s_event_success(test_settings.SUCCESSFUL_MESSAGE_TYPE, test_settings.NORMAL_EVENT_TYPE)

    def test_generate_k8s_warning_event_success(self):
        self._test_generate_k8s_event_success('unsuccessful message type', test_settings.WARNING_EVENT_TYPE)

    def _test_generate_k8s_event_success(self, message_type, expected_event_type):
        result = self.event_manager.generate_k8s_event(
            self.fake_host_definition_info, test_settings.MESSAGE,
            test_settings.DEFINE_ACTION, message_type)
        self.assertEqual(result.metadata, test_utils.get_event_object_metadata())
        self.assertEqual(result.reporting_component, test_settings.HOST_DEFINER)
        self.assertEqual(result.reporting_instance, test_settings.HOST_DEFINER)
        self.assertEqual(result.action, test_settings.DEFINE_ACTION)
        self.assertEqual(result.type, expected_event_type)
        self.assertEqual(result.reason, message_type + test_settings.DEFINE_ACTION)
        self.assertEqual(result.message, test_settings.MESSAGE)
        self.assertEqual(result.involved_object, test_utils.get_object_reference())
