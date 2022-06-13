from threading import Thread
from time import sleep
from kubernetes import dynamic

from watcher.watcher_helper import WatcherHelper
from common import utils, settings

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
        return csi_host_definition_event['object'].spec.hostDefinition.phase == settings.PENDING_PHASE

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
        retries = 10
        backoff_in_seconds = 3
        delay_in_seconds = 3
        csi_host_definition_name = csi_host_definition_event['object'].metadata.name
        logger.info('Verifying host definition {}, using exponantial backoff'.format(
            csi_host_definition_name))
        while retries > 1:
            host_definition_object = csi_host_definition_event['object']
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
            try:
                return self._verify_host_state_on_storage(csi_host_definition_event, host_definition_object)
            except Exception as ex:
                self._set_host_definition_message(host_definition_object, str(ex))
                retries -= 1
                delay_in_seconds *= backoff_in_seconds
                sleep(delay_in_seconds)

        self._set_host_definition_phase_to_error(host_definition_object)
        host_definition_objects_in_use.remove(
            csi_host_definition_name)

    def _verify_host_state_on_storage(self, csi_host_definition_event, host_definition_object):
        host_definition_action = host_definition_object.spec.hostDefinition.action
        if host_definition_action == settings.CREATE_ACTION:
            self._verify_host_on_storage(host_definition_object)
        elif host_definition_action == settings.DELETE_ACTION:
            self._verify_host_not_on_storage(host_definition_object)
        host_definition_objects_in_use.remove(
            csi_host_definition_event['object'].metadata.name)
    
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

    def _set_host_definition_message(self, host_definition_object, message):
        logger.info('Set host definition: {} error message: {}'.format(
            host_definition_object.metadata.name, message))
        host_definition_object.spec.hostDefinition.message = message
        self._patch_host_definition_object(host_definition_object)
        
    def _set_host_definition_phase_to_error(self, host_definition_object):
        logger.info('Set host definition: {} error phase'.format(
            host_definition_object.metadata.name))
        host_definition_object.spec.hostDefinition.phase = settings.ERROR_PHASE
        self._patch_host_definition_object(host_definition_object)

    def _patch_host_definition_object(self, host_definition_object):
        host_definition_manifest = self._get_host_definition_manifest(host_definition_object)
        try:
            self.patch_host_definition(host_definition_manifest)
        except Exception as ex:
            logger.error(ex)

    def _get_host_definition_manifest(self, host_definition_object):
        manifest = {
            'apiVersion': settings.CSI_IBM_BLOCK_API_VERSION,
            'kind': settings.HOSTDEFINITION_KIND,
            'metadata': {
                'name': host_definition_object.metadata.name,
            },
            'spec': {
                'hostDefinition': {
                    'storageServer': host_definition_object.spec.hostDefinition.storageServer,
                    'hostNameInStorage': host_definition_object.spec.hostDefinition.hostNameInStorage,
                    'secretName': host_definition_object.spec.hostDefinition.secretName,
                    'secretNamespace': host_definition_object.spec.hostDefinition.secretNamespace,
                    'phase': host_definition_object.spec.hostDefinition.phase,
                    'message': host_definition_object.spec.hostDefinition.message,
                },
            },
        }
        return manifest
