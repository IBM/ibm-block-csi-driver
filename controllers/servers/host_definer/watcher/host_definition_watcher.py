from threading import Thread
from time import sleep

import controllers.servers.host_definer.messages as messages
from controllers.common.csi_logger import get_stdout_logger
from controllers.servers.host_definer.watcher.watcher_helper import Watcher
from controllers.servers.host_definer.types import DefineHostResponse
from controllers.servers.host_definer import settings

logger = get_stdout_logger()


class HostDefinitionWatcher(Watcher):

    def watch_host_definitions_resources(self):
        while True:
            resource_version = self.host_definitions_api.get().metadata.resourceVersion
            stream = self.host_definitions_api.watch(resource_version=resource_version, timeout=5)
            for event in stream:
                event_object = event[settings.OBJECT_KEY]
                host_definition = self._get_host_definition_object(event_object)
                if self._is_host_definition_in_pending_phase(host_definition.phase) and \
                        event[settings.TYPE_KEY] != settings.DELETED_EVENT:
                    self._verify_host_defined_after_pending_host_definition(host_definition)

    def _is_host_definition_in_pending_phase(self, phase):
        return phase.startswith(settings.PENDING_PREFIX)

    def _verify_host_defined_after_pending_host_definition(self, host_definition):
        remove_host_thread = Thread(target=self._verify_host_defined_using_exponential_backoff,
                                    args=(host_definition, ))
        remove_host_thread.start()

    def _verify_host_defined_using_exponential_backoff(self, host_definition):
        retries = 5
        backoff_in_seconds = 3
        delay_in_seconds = 3
        logger.info(messages.VERIFY_HOST_DEFINITION_USING_EXPONENTIAL_BACKOFF.format(host_definition.name))
        while retries > 1:
            if self._is_host_definition_in_desired_state(host_definition) and retries != 5:
                return
            self._handle_pending_host_definition(host_definition)
            retries -= 1
            delay_in_seconds *= backoff_in_seconds
            sleep(delay_in_seconds)

        self._set_host_definition_phase_to_error(host_definition)

    def _is_host_definition_in_desired_state(self, host_definition):
        host_definition_instance, status_code = self._get_host_definition(
            host_definition.node_name, host_definition.secret)
        phase = host_definition.phase
        if not host_definition_instance:
            return False
        if status_code == 404 and phase == settings.PENDING_DELETION_PHASE:
            return True
        return host_definition_instance.phase == settings.READY_PHASE

    def _handle_pending_host_definition(self, host_definition):
        response = DefineHostResponse()
        phase = host_definition.phase
        if phase == settings.PENDING_CREATION_PHASE:
            response = self._define_host(host_definition)
        elif self._is_pending_for_deletion_need_to_be_handled(phase, host_definition.node_name):
            response = self._undefine_host(host_definition)
        self._handle_error_message_for_pending_host_definition(
            host_definition, response.error_message)

    def _handle_error_message_for_pending_host_definition(self, host_definition, error_message):
        phase = host_definition.phase
        if error_message:
            self._add_event_to_host_definition(host_definition, str(error_message))
        elif phase == settings.PENDING_CREATION_PHASE:
            self._set_host_definition_status(host_definition.name, settings.READY_PHASE)
        elif self._is_pending_for_deletion_need_to_be_handled(phase, host_definition.node_name):
            self._delete_host_definition(host_definition.name)

    def _is_pending_for_deletion_need_to_be_handled(self, phase, node_name):
        return phase == settings.PENDING_DELETION_PHASE and self._is_host_can_be_undefined(node_name)

    def _set_host_definition_phase_to_error(self, host_definition):
        logger.info(messages.SET_HOST_DEFINITION_PHASE_TO_ERROR.format(host_definition.name))
        self._set_host_definition_status(host_definition.name, settings.ERROR_PHASE)
