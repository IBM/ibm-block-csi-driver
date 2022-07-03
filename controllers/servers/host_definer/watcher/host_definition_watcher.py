from threading import Thread
from time import sleep

import controllers.servers.messages as messages
from controllers.common.csi_logger import get_stdout_logger
from controllers.servers.host_definer.watcher.watcher_helper import Watcher
from controllers.servers.host_definer import settings

logger = get_stdout_logger()


class HostDefinitionWatcher(Watcher):

    def watch_host_definitions_resources(self):
        for event in self.host_definitions_api.watch():
            event_object = event[settings.OBJECT_KEY]
            host_definition = self._get_host_definition_object(event_object)
            if self._is_host_definition_in_pending_phase(host_definition.phase) and \
                    event[settings.TYPE_KEY] != settings.DELETED_EVENT:
                self._verify_host_defined_after_pending_host_definition(event)

    def _is_host_definition_in_pending_phase(self, phase):
        return settings.PENDING_PREFIX in phase

    def _verify_host_defined_after_pending_host_definition(self, host_definition_event):
        host_definition = host_definition_event[settings.OBJECT_KEY]
        remove_host_thread = Thread(target=self._verify_host_defined_using_exponential_backoff,
                                    args=(host_definition,))
        remove_host_thread.start()

    def _verify_host_defined_using_exponential_backoff(self, host_definition):
        retries = 10
        backoff_in_seconds = 3
        delay_in_seconds = 3
        host_definition_name = host_definition.metadata.name
        logger.info(messages.VERIFY_HOST_DEFINITION_USING_EXPONANTIAL_BACKOFF.format(host_definition_name))
        while retries > 1:
            if self._is_host_definition_in_desired_state(host_definition):
                return
            self._handle_pending_host_definition(host_definition)
            retries -= 1
            delay_in_seconds *= backoff_in_seconds
            sleep(delay_in_seconds)

        self._set_host_definition_phase_to_error(host_definition)

    def _is_host_definition_in_desired_state(self, host_definition):
        host_definition_name = host_definition.metadata.name
        _, status_code = self._get_host_definition(host_definition_name)
        phase = self._get_host_definition_phase(host_definition)
        if status_code == 400 and phase == settings.PENDING_DELETION_PHASE:
            return True
        return status_code == 200 and phase == settings.PENDING_CREATION_PHASE

    def _handle_pending_host_definition(self, host_definition):
        host_definition_obj = self._get_host_definition_object(host_definition)
        response = self._verify_pending_host_definition(host_definition_obj)
        self._handle_error_message_for_pending_host_definition(
            host_definition, response.error_message, host_definition_obj.node_name)

    def _handle_error_message_for_pending_host_definition(self, host_definition, error_message, node_name):
        host_definition_name = host_definition.metadata.name
        phase = self._get_host_definition_phase(host_definition)
        if error_message:
            self._add_event_to_host_definition(host_definition, str(error_message))
        elif phase == settings.PENDING_CREATION_PHASE:
            self._set_host_definition_status(host_definition_name, settings.READY_PHASE)
        elif self._is_pending_for_deletion_need_to_be_handled(phase, node_name):
            self._delete_host_definition(host_definition_name)

    def _verify_pending_host_definition(self, host_definition):
        phase = host_definition.phase
        if phase == settings.PENDING_CREATION_PHASE:
            return self._define_host(host_definition)
        elif self._is_pending_for_deletion_need_to_be_handled(phase, host_definition.node_name):
            return self._undefine_host(host_definition)

    def _is_pending_for_deletion_need_to_be_handled(self, phase, node_name):
        return phase == settings.PENDING_DELETION_PHASE and self._is_host_can_be_undefined(node_name)

    def _set_host_definition_phase_to_error(self, host_definition):
        host_definition_name = host_definition.metadata.name
        logger.info(messages.SET_HOST_DEFINITION_PHASE_TO_ERROR.format(host_definition_name))
        self._set_host_definition_status(host_definition_name, settings.ERROR_PHASE)
