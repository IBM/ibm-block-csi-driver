from threading import Thread
from time import sleep

from controllers.common.csi_logger import get_stdout_logger
from controllers.servers.host_definer.watcher.watcher_helper import Watcher
from controllers.servers.host_definer import settings

logger = get_stdout_logger()


class HostDefinitionWatcher(Watcher):

    def watch_host_definitions_resources(self):
        for event in self.host_definitions_api.watch():
            host_definition = self._get_host_definition_object(event[settings.OBJECT_KEY])
            if self._is_host_definition_in_pending_phase(host_definition.phase) and \
                    event[settings.TYPE_KEY] != settings.DELETED_EVENT:
                self._verify_host_defined_after_pending_host_definition(event)

    def _is_host_definition_in_pending_phase(self, phase):
        return settings.PENDING_PHASE in phase

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
        logger.info('Verifying host definition {}, using exponantial backoff'.format(host_definition_name))
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
        _, error_status = self._get_host_definition(host_definition_name)
        if error_status == 400 and self._get_host_definition_phase(host_definition) == settings.PENDING_DELETION_PHASE:
            return True
        if error_status == 200 and self._get_host_definition_phase(host_definition) == settings.PENDING_CREATION_PHASE:
            return True

    def _handle_pending_host_definition(self, host_definition):
        host_definition_obj = self._get_host_definition_object(host_definition)
        response = self._verify_pending_host_definition(host_definition_obj)
        self._add_event_when_response_has_error_message(response, host_definition)

    def _verify_pending_host_definition(self, host_definition):
        host_definition_phase = host_definition.phase
        if host_definition_phase == settings.PENDING_CREATION_PHASE:
            response = self.verify_host_defined_on_storage_and_on_cluster(host_definition)
        elif host_definition_phase == settings.PENDING_DELETION_PHASE and \
                (self.is_host_can_be_undefined(host_definition.node_name)):
            response = self.undefine_host_and_host_definition(host_definition)
        return response

    def _add_event_when_response_has_error_message(self, response, host_definition):
        if response.error_message:
            self.add_event_to_host_definition(host_definition, str(response.error_message))
            return

    def _set_host_definition_phase_to_error(self, host_definition):
        host_definition_name = host_definition.metadata.name
        logger.info('Set host definition: {} error phase'.format(host_definition_name))
        self.set_host_definition_status(host_definition_name, settings.ERROR_PHASE)
