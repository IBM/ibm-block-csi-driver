from threading import Thread
from time import sleep
from kubernetes import dynamic

from controllers.common.csi_logger import get_stdout_logger
from controllers.servers.host_definer.watcher.watcher_helper import Watcher
from controllers.servers.host_definer.common import settings

logger = get_stdout_logger()


class HostDefinitionWatcher(Watcher):
    def __init__(self):
        super().__init__()
        self.host_definitions_in_use = []

    def watch_host_definitions_resources(self):
        for event in self.host_definitions_api.watch():
            if self._is_host_definition_in_pending_phase_and_needs_verify(event):
                self._verify_host_defined_after_pending_host_definition(
                    event)

    def _is_host_definition_in_pending_phase_and_needs_verify(
            self, host_definition_event):
        return self._is_host_definition_in_pending_phase(host_definition_event) and (
            self._is_host_definition_in_use(host_definition_event)) and (
                host_definition_event[settings.TYPE_KEY] != settings.DELETED_EVENT)

    def _is_host_definition_in_pending_phase(self, host_definition_event):
        phase = self.get_phase_of_host_definition(
            host_definition_event[settings.OBJECT_KEY])
        if phase:
            return settings.PENDING_PHASE in phase
        return False

    def _is_host_definition_in_use(self, host_definition_event):
        return host_definition_event[settings.OBJECT_KEY].metadata.name not in self.host_definitions_in_use

    def _verify_host_defined_after_pending_host_definition(
            self, host_definition_event):
        self.host_definitions_in_use.append(
            host_definition_event[settings.OBJECT_KEY].metadata.name)
        remove_host_thread = Thread(
            target=self._verify_host_defined_using_exponential_backoff,
            args=(host_definition_event,))
        remove_host_thread.start()

    def _verify_host_defined_using_exponential_backoff(
            self, host_definition_event):
        retries = 10
        backoff_in_seconds = 3
        delay_in_seconds = 3
        host_definition_name = host_definition_event[settings.OBJECT_KEY].metadata.name
        logger.info('Verifying host definition {}, using exponantial backoff'.format(
            host_definition_name))
        while retries > 1:
            host_definition = host_definition_event[settings.OBJECT_KEY]
            self._remove_host_definition_from_in_use_list_if_ready(
                host_definition_name)
            if host_definition_name not in self.host_definitions_in_use:
                return
            self._handle_pending_host_definition(host_definition)
            retries -= 1
            delay_in_seconds *= backoff_in_seconds
            sleep(delay_in_seconds)

        self._set_host_definition_phase_to_error(host_definition)
        if host_definition_name in self.host_definitions_in_use:
            self.host_definitions_in_use.remove(
                host_definition_name)

    def _remove_host_definition_from_in_use_list_if_ready(
            self, host_definition_name):
        try:
            if self.is_host_definition_ready(host_definition_name):
                logger.info('host definition {} is in Ready phase'.format(
                    host_definition_name))
                self.host_definitions_in_use.remove(
                    host_definition_name)
        except dynamic.exceptions.NotFoundError:
            self.host_definitions_in_use.remove(
                host_definition_name)
        except Exception as ex:
            logger.error(ex)

    def _handle_pending_host_definition(self, host_definition):
        host_request = self.get_host_request_from_host_definition(
            host_definition)
        if not host_request:
            return
        response = self._verify_pending_host_definition(host_definition, host_request)
        self._add_event_when_response_has_error_message(response, host_definition)

    def _verify_pending_host_definition(self, host_definition, host_request):
        host_definition_phase = self.get_phase_of_host_definition(
            host_definition)
        host_definition_name = host_definition.metadata.name
        node_name = host_definition.spec.hostDefinition.nodeId
        if host_definition_phase == settings.PENDING_CREATION_PHASE:
            response = self.verify_host_defined_on_storage_and_on_cluster(host_request)
        elif host_definition_phase == settings.PENDING_DELETION_PHASE and \
                (self.is_host_can_be_undefined(node_name)):
            response = self.undefine_host_and_host_definition(host_request, host_definition_name)
        return response

    def _add_event_when_response_has_error_message(self, response, host_definition):
        host_definition_name = host_definition.metadata.name
        if response.error_message:
            self.add_event_to_host_definition(
                host_definition, str(response.error_message))
            return
        self.host_definitions_in_use.remove(
            host_definition_name)

    def _set_host_definition_phase_to_error(self, host_definition):
        host_definition_name = host_definition.metadata.name
        logger.info('Set host definition: {} error phase'.format(
            host_definition_name))
        try:
            self.set_host_definition_status(
                host_definition_name, settings.ERROR_PHASE)
        except Exception as ex:
            logger.error(ex)
