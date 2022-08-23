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
            for watch_event in stream:
                watch_event = self._munch_watch_event(watch_event)
                host_definition_info = self._generate_host_definition_info(watch_event.object)
                if self._is_host_definition_in_pending_phase(host_definition_info.phase) and \
                        watch_event.type != settings.DELETED_EVENT:
                    self.define_host_definition_after_pending_state(host_definition_info)

    def _is_host_definition_in_pending_phase(self, phase):
        return phase.startswith(settings.PENDING_PREFIX)

    def define_host_definition_after_pending_state(self, host_definition_info):
        remove_host_thread = Thread(target=self.define_host_using_exponential_backoff,
                                    args=(host_definition_info, ))
        remove_host_thread.start()

    def define_host_using_exponential_backoff(self, host_definition_info):
        retries = 5
        backoff_in_seconds = 3
        delay_in_seconds = 3
        while retries > 0:
            logger.info(messages.VERIFY_HOST_DEFINITION_USING_EXPONENTIAL_BACKOFF.format(
                host_definition_info.name, retries))
            if self._is_host_definition_in_desired_state(host_definition_info) and retries != 5:
                logger.info(messages.HOST_DEFINITION_IN_DESIRED_STATE.format(host_definition_info.name))
                return
            self._handle_pending_host_definition(host_definition_info)
            retries -= 1
            delay_in_seconds *= backoff_in_seconds
            sleep(delay_in_seconds)

        self._set_host_definition_phase_to_error(host_definition_info)

    def _is_host_definition_in_desired_state(self, host_definition_info):
        current_host_definition_info_on_cluster, status_code = self._get_host_definition_info(
            host_definition_info.node_name, host_definition_info.secret_info)
        phase = host_definition_info.phase
        if not current_host_definition_info_on_cluster:
            return False
        if status_code == 404 and phase == settings.PENDING_DELETION_PHASE:
            return True
        return current_host_definition_info_on_cluster.phase == settings.READY_PHASE

    def _handle_pending_host_definition(self, host_definition_info):
        response = DefineHostResponse()
        phase = host_definition_info.phase
        if phase == settings.PENDING_CREATION_PHASE:
            response = self._define_host(host_definition_info)
        elif self._is_pending_for_deletion_need_to_be_handled(phase, host_definition_info.node_name):
            response = self._undefine_host(host_definition_info)
        self._handle_message_from_storage(
            host_definition_info, response.error_message)

    def _handle_message_from_storage(self, host_definition_info, error_message):
        phase = host_definition_info.phase
        if error_message:
            self._add_k8s_event_to_host_definition(host_definition_info, str(error_message))
        elif phase == settings.PENDING_CREATION_PHASE:
            self._set_host_definition_status(host_definition_info.name, settings.READY_PHASE)
        elif self._is_pending_for_deletion_need_to_be_handled(phase, host_definition_info.node_name):
            self._delete_host_definition(host_definition_info.name)

    def _is_pending_for_deletion_need_to_be_handled(self, phase, node_name):
        return phase == settings.PENDING_DELETION_PHASE and self._is_host_can_be_undefined(node_name)

    def _set_host_definition_phase_to_error(self, host_definition_info):
        logger.info(messages.SET_HOST_DEFINITION_PHASE_TO_ERROR.format(host_definition_info.name))
        self._set_host_definition_status(host_definition_info.name, settings.ERROR_PHASE)
