from threading import Thread
from time import sleep
from kubernetes import dynamic

from host_definer.watcher.watcher_helper import WatcherHelper
from host_definer.common import utils, settings

host_definition_objects_in_use = []
logger = utils.get_stdout_logger()

class CsiHostDefinitionWatcher(WatcherHelper):
    def __init__(self):
        super().__init__()
    
    def watch_csi_host_definitions_resources(self):
        for event in self.csi_hostdefinitions_api.watch():
            if self._is_host_definition_in_pending_phase_and_needs_verify(event) or (
                self._is_host_definition_has_retry_true(event)):
                self._verify_host_on_storage_after_pending_host_definition(event)

    def _is_host_definition_in_pending_phase_and_needs_verify(self, csi_host_definition_event):
        return self._is_host_definition_in_pending_phase(csi_host_definition_event) and (
            self._is_host_definition_in_use(csi_host_definition_event)) and (
                csi_host_definition_event['type'] != settings.DELETED_EVENT)

    def _is_host_definition_has_retry_true(self, csi_host_definition_event):
        return csi_host_definition_event['object'].spec.hostDefinition.retryVerifying == True and (
                csi_host_definition_event['type'] != settings.DELETED_EVENT) and(
                    self._is_host_definition_in_use(csi_host_definition_event))

    def _is_host_definition_in_pending_phase(self, csi_host_definition_event):
        phase = self.get_phase_of_host_definition_object(csi_host_definition_event['object'])
        if phase:
            return settings.PENDING_PHASE in phase
        return False

    def _is_host_definition_in_use(self, csi_host_definition_event):
        return csi_host_definition_event['object'].metadata.name not in host_definition_objects_in_use
    
    def _verify_host_on_storage_after_pending_host_definition(self, csi_host_definition_event):
        host_definition_objects_in_use.append(
            csi_host_definition_event['object'].metadata.name)
        remove_host_thread = Thread(
            target=self._verify_host_on_storage_using_exponential_backoff,
            args=(csi_host_definition_event,))
        remove_host_thread.start()
        
    def _verify_host_on_storage_using_exponential_backoff(self, csi_host_definition_event):
        retries = 3
        backoff_in_seconds = 3
        delay_in_seconds = 3
        csi_host_definition_name = csi_host_definition_event['object'].metadata.name
        logger.info('Verifying host definition {}, using exponantial backoff'.format(
            csi_host_definition_name))
        while retries > 1:
            host_definition_object = csi_host_definition_event['object']
            self._remove_host_definition_from_in_use_list_if_ready(csi_host_definition_name)
            if csi_host_definition_name not in host_definition_objects_in_use:
                return
            try:
                return self._verify_host_state_on_storage(host_definition_object)
            except Exception as ex:
                self._add_event_to_host_definition_object(host_definition_object, str(ex))
                retries -= 1
                delay_in_seconds *= backoff_in_seconds
                sleep(delay_in_seconds)

        self._set_host_definition_phase_to_error(host_definition_object)
        host_definition_objects_in_use.remove(
            csi_host_definition_name)

    def _remove_host_definition_from_in_use_list_if_ready(self, csi_host_definition_name):
        try:
            if self.is_host_definition_ready(csi_host_definition_name):
                logger.info('host definition {} is in Ready phase'.format(
                    csi_host_definition_name))
                return host_definition_objects_in_use.remove(
                    csi_host_definition_name)
        except dynamic.exceptions.NotFoundError:
                return host_definition_objects_in_use.remove(
                    csi_host_definition_name)
        except Exception as ex:
            logger.error(ex)

    def _verify_host_state_on_storage(self, host_definition_object):
        host_definition_phase = self.get_phase_of_host_definition_object(host_definition_object)
        if host_definition_phase == settings.PENDING_CREATION_PHASE:
            self._verify_host_on_storage(host_definition_object)
        elif host_definition_phase == settings.PENDING_DELETION_PHASE:
            self._verify_host_not_on_storage(host_definition_object)
        host_definition_objects_in_use.remove(
            host_definition_object.metadata.name)
    
    def _verify_host_on_storage(self, host_definition_object):
        host_object = self._get_host_object_from_host_definition_object(host_definition_object)
        self.storage_host_manager.verify_host_on_storage(host_object)
        try:
            host_object.phase = settings.READY_PHASE
            self.verify_csi_host_definition_from_host_object(host_object)
        except Exception as ex:
            logger.error('Failed to verify that hostdefinition {} is ready, got error: {}'.format(
                host_definition_object.metadata.name, ex))

    def _verify_host_not_on_storage(self, host_definition_object):
        host_object = self._get_host_object_from_host_definition_object(host_definition_object)
        self.storage_host_manager.verify_host_removed_from_storage(host_object)
        try:
            self.delete_host_definition_object(host_definition_object.metadata.name)
        except Exception as ex:
            logger.error('Failed to verify that hostdefinition {} is removed, got error: {}'.format(
                host_definition_object.metadata.name, ex))

    def _get_host_object_from_host_definition_object(self, host_definition_object):
        secret_name = host_definition_object.spec.hostDefinition.secretName
        secret_namespace = host_definition_object.spec.hostDefinition.secretNamespace
        host_object = self.get_host_object_from_secret_name_and_namespace(secret_name, secret_namespace)
        host_object.host_name = host_definition_object.spec.hostDefinition.hostNameInStorage
        return host_object

    def _add_event_to_host_definition_object(self, host_definition_object, message):
        logger.info('Create host definition: {} error event: {}'.format(
            host_definition_object.metadata.name, message))
        event = self.get_event_for_object(host_definition_object, message)
        self.create_event(settings.DEFAULT_NAMESPACE, event)

    def _set_host_definition_phase_to_error(self, host_definition_object):
        host_definition_name = host_definition_object.metadata.name
        logger.info('Set host definition: {} error phase'.format(
            host_definition_name))
        try:
            self.set_host_definition_status(host_definition_name, settings.ERROR_PHASE)
        except Exception as ex:
            logger.error(ex)
