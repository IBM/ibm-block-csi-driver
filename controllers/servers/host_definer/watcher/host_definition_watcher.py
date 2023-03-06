from threading import Thread
from time import sleep

from controllers.servers.host_definer.utils import utils
import controllers.servers.host_definer.messages as messages
from controllers.common.csi_logger import get_stdout_logger
from controllers.servers.host_definer.watcher.watcher_helper import Watcher
from controllers.servers.host_definer.types import DefineHostResponse
from controllers.servers.host_definer import settings

logger = get_stdout_logger()


class HostDefinitionWatcher(Watcher):

    def watch_host_definitions_resources(self):
        self._watch_host_definition_with_timeout('')
        while utils.loop_forever():
            resource_version = utils.get_k8s_object_resource_version(self.k8s_api.list_host_definition())
            self._watch_host_definition_with_timeout(resource_version)

    def _watch_host_definition_with_timeout(self, resource_version, timeout=5):
        stream = self.k8s_api.get_host_definition_stream(resource_version, timeout)
        for watch_event in stream:
            watch_event = utils.munch(watch_event)
            host_definition_info = self.host_definition_manager.generate_host_definition_info(watch_event.object)
            if self.host_definition_manager.is_host_definition_in_pending_phase(host_definition_info.phase) and \
                    not utils.is_watch_object_type_is_delete(watch_event.type):
                self._define_host_definition_after_pending_state(host_definition_info)

    def _define_host_definition_after_pending_state(self, host_definition_info):
        logger.info(messages.FOUND_HOST_DEFINITION_IN_PENDING_STATE.format(host_definition_info.name))
        remove_host_thread = Thread(target=self._define_host_using_exponential_backoff,
                                    args=(host_definition_info, ))
        remove_host_thread.start()

    def _define_host_using_exponential_backoff(self, host_definition_info):
        retries = settings.HOST_DEFINITION_PENDING_RETRIES
        backoff_in_seconds = settings.HOST_DEFINITION_PENDING_EXPONENTIAL_BACKOFF_IN_SECONDS
        delay_in_seconds = settings.HOST_DEFINITION_PENDING_DELAY_IN_SECONDS
        while retries > 0:
            logger.info(messages.VERIFY_HOST_DEFINITION_USING_EXPONENTIAL_BACKOFF.format(
                host_definition_info.name, retries))
            if self.host_definition_manager.is_host_definition_not_pending(host_definition_info) and \
                    retries != settings.HOST_DEFINITION_PENDING_RETRIES:
                logger.info(messages.HOST_DEFINITION_IS_NOT_PENDING.format(host_definition_info.name))
                return
            self._handle_pending_host_definition(host_definition_info)
            retries -= 1
            delay_in_seconds *= backoff_in_seconds
            sleep(delay_in_seconds)

        self.host_definition_manager.set_host_definition_phase_to_error(host_definition_info)

    def _handle_pending_host_definition(self, host_definition_info):
        response = DefineHostResponse()
        phase = host_definition_info.phase
        action = self._get_action(phase)
        if phase == settings.PENDING_CREATION_PHASE:
            response = self.definition_manager.define_host_after_pending(host_definition_info)
        elif self._is_pending_for_deletion_need_to_be_handled(phase, host_definition_info.node_name):
            response = self.definition_manager.undefine_host_after_pending(host_definition_info)
        self._handle_message_from_storage(
            host_definition_info, response.error_message, action)

    def _get_action(self, phase):
        if phase == settings.PENDING_CREATION_PHASE:
            return settings.DEFINE_ACTION
        return settings.UNDEFINE_ACTION

    def _handle_message_from_storage(self, host_definition_info, error_message, action):
        phase = host_definition_info.phase
        if error_message:
            self.host_definition_manager.create_k8s_event_for_host_definition(host_definition_info, str(error_message),
                                                                              action, settings.FAILED_MESSAGE_TYPE)
        elif phase == settings.PENDING_CREATION_PHASE:
            self.host_definition_manager.set_host_definition_status_to_ready(host_definition_info)
        elif self._is_pending_for_deletion_need_to_be_handled(phase, host_definition_info.node_name):
            self.host_definition_manager.delete_host_definition(host_definition_info.name)
            self._remove_manage_node_label(host_definition_info.node_name)

    def _is_pending_for_deletion_need_to_be_handled(self, phase, node_name):
        return phase == settings.PENDING_DELETION_PHASE and self._is_node_can_be_undefined(node_name)
