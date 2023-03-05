from controllers.common.csi_logger import get_stdout_logger
import controllers.common.settings as common_settings
from controllers.servers.host_definer import settings
from controllers.servers.host_definer.globals import NODES
from controllers.servers.host_definer.utils import utils
from controllers.servers.host_definer.utils import manifest_utils
from controllers.servers.host_definer.types import HostDefinitionInfo
import controllers.servers.host_definer.messages as messages
from controllers.servers.host_definer.k8s.api import K8SApi
from controllers.servers.host_definer.k8s.manager import K8SManager

logger = get_stdout_logger()


class HostDefinitionManager:
    def __init__(self):
        self.k8s_api = K8SApi()
        self.k8s_manager = K8SManager()

    def get_host_definition_info_from_secret_and_node_name(self, node_name, secret_info):
        host_definition_info = self.get_host_definition_info_from_secret(secret_info)
        host_definition_info = self.add_name_to_host_definition_info(node_name, host_definition_info)
        return host_definition_info

    def get_host_definition_info_from_secret(self, secret_info):
        host_definition_info = HostDefinitionInfo()
        host_definition_info.secret_name = secret_info.name
        host_definition_info.secret_namespace = secret_info.namespace
        return host_definition_info

    def add_name_to_host_definition_info(self, node_name, host_definition_info):
        host_definition_info.node_name = node_name
        host_definition_info.node_id = NODES[node_name].node_id
        host_definition_info.name = self._get_host_definition_name(node_name)
        return host_definition_info

    def _get_host_definition_name(self, node_name):
        return '{0}-{1}'.format(node_name, utils.get_random_string()).replace('_', '.')

    def update_host_definition_info(self, host_definition_info):
        host_definition_info_on_cluster = self.get_matching_host_definition_info(
            host_definition_info.node_name, host_definition_info.secret_name, host_definition_info.secret_namespace)
        if host_definition_info_on_cluster:
            host_definition_info.connectivity_type = host_definition_info_on_cluster.connectivity_type
            host_definition_info.node_id = host_definition_info_on_cluster.node_id
        return host_definition_info

    def create_host_definition_if_not_exist(self, host_definition_info, response):
        node_id = NODES[host_definition_info.node_name].node_id
        host_definition_manifest = manifest_utils.get_host_definition_manifest(host_definition_info, response, node_id)
        current_host_definition_info_on_cluster = self.get_matching_host_definition_info(
            host_definition_info.node_name, host_definition_info.secret_name, host_definition_info.secret_namespace)
        if current_host_definition_info_on_cluster:
            host_definition_manifest[settings.METADATA][
                common_settings.NAME_FIELD] = current_host_definition_info_on_cluster.name
            self.k8s_api.patch_host_definition(host_definition_manifest)
            return current_host_definition_info_on_cluster
        else:
            logger.info(messages.CREATING_NEW_HOST_DEFINITION.format(host_definition_info.name))
            return self.create_host_definition(host_definition_manifest)

    def create_host_definition(self, host_definition_manifest):
        k8s_host_definition = self.k8s_api.create_host_definition(host_definition_manifest)
        if k8s_host_definition:
            logger.info(messages.CREATED_HOST_DEFINITION.format(k8s_host_definition.metadata.name))
            self._add_finalizer(k8s_host_definition.metadata.name)
            return self.generate_host_definition_info(k8s_host_definition)
        return HostDefinitionInfo()

    def _add_finalizer(self, host_definition_name):
        logger.info(messages.ADD_FINALIZER_TO_HOST_DEFINITION.format(host_definition_name))
        self._update_finalizer(host_definition_name, [settings.CSI_IBM_FINALIZER, ])

    def set_status_to_host_definition_after_definition(self, message_from_storage, host_definition_info):
        if message_from_storage and host_definition_info:
            self.set_host_definition_status(host_definition_info.name,
                                            settings.PENDING_CREATION_PHASE)
            self.create_k8s_event_for_host_definition(
                host_definition_info, message_from_storage, settings.DEFINE_ACTION, settings.FAILED_MESSAGE_TYPE)
        elif host_definition_info:
            self.set_host_definition_status_to_ready(host_definition_info)

    def set_host_definition_status_to_ready(self, host_definition):
        self.set_host_definition_status(host_definition.name, settings.READY_PHASE)
        self.create_k8s_event_for_host_definition(
            host_definition, settings.SUCCESS_MESSAGE, settings.DEFINE_ACTION, settings.SUCCESSFUL_MESSAGE_TYPE)

    def handle_k8s_host_definition_after_undefine_action_if_exist(self, host_definition_info, response):
        current_host_definition_info_on_cluster = self.get_matching_host_definition_info(
            host_definition_info.node_name, host_definition_info.secret_name, host_definition_info.secret_namespace)
        if current_host_definition_info_on_cluster:
            self._handle_k8s_host_definition_after_undefine_action(
                response.error_message, current_host_definition_info_on_cluster)

    def _handle_k8s_host_definition_after_undefine_action(self, message_from_storage, host_definition_info):
        if message_from_storage and host_definition_info:
            self.set_host_definition_status(host_definition_info.name,
                                            settings.PENDING_DELETION_PHASE)
            self.create_k8s_event_for_host_definition(
                host_definition_info, message_from_storage,
                settings.UNDEFINE_ACTION, settings.FAILED_MESSAGE_TYPE)
        elif host_definition_info:
            self.delete_host_definition(host_definition_info.name)

    def create_k8s_event_for_host_definition(self, host_definition_info, message, action, message_type):
        logger.info(messages.CREATE_EVENT_FOR_HOST_DEFINITION.format(message, host_definition_info.name))
        k8s_event = self.k8s_manager.generate_k8s_event(host_definition_info, message, action, message_type)
        self.k8s_api.create_event(settings.DEFAULT_NAMESPACE, k8s_event)

    def delete_host_definition(self, host_definition_name):
        logger.info(messages.DELETE_HOST_DEFINITION.format(host_definition_name))
        remove_finalizer_status_code = self._remove_finalizer(host_definition_name)
        if remove_finalizer_status_code == 200:
            self.k8s_api.delete_host_definition(host_definition_name)
        else:
            logger.error(messages.FAILED_TO_DELETE_HOST_DEFINITION.format(
                host_definition_name, messages.FAILED_TO_REMOVE_FINALIZER))

    def _remove_finalizer(self, host_definition_name):
        logger.info(messages.REMOVE_FINALIZER_TO_HOST_DEFINITION.format(host_definition_name))
        return self._update_finalizer(host_definition_name, [])

    def _update_finalizer(self, host_definition_name, finalizers):
        finalizer_manifest = manifest_utils.get_finalizer_manifest(host_definition_name, finalizers)
        return self.k8s_api.patch_host_definition(finalizer_manifest)

    def is_host_definition_in_pending_phase(self, phase):
        return phase.startswith(settings.PENDING_PREFIX)

    def set_host_definition_phase_to_error(self, host_definition_info):
        logger.info(messages.SET_HOST_DEFINITION_PHASE_TO_ERROR.format(host_definition_info.name))
        self.set_host_definition_status(host_definition_info.name, settings.ERROR_PHASE)

    def set_host_definition_status(self, host_definition_name, host_definition_phase):
        logger.info(messages.SET_HOST_DEFINITION_STATUS.format(host_definition_name, host_definition_phase))
        status = manifest_utils.get_host_definition_status_manifest(host_definition_phase)
        self.k8s_api.patch_cluster_custom_object_status(
            common_settings.CSI_IBM_GROUP, common_settings.VERSION, common_settings.HOST_DEFINITION_PLURAL,
            host_definition_name, status)

    def is_host_definition_not_pending(self, host_definition_info):
        current_host_definition_info_on_cluster = self.get_matching_host_definition_info(
            host_definition_info.node_name, host_definition_info.secret_name, host_definition_info.secret_namespace)
        return not current_host_definition_info_on_cluster or \
            current_host_definition_info_on_cluster.phase == settings.READY_PHASE

    def get_matching_host_definition_info(self, node_name, secret_name, secret_namespace):
        k8s_host_definitions = self.k8s_api.list_host_definition().items
        for k8s_host_definition in k8s_host_definitions:
            host_definition_info = self.generate_host_definition_info(k8s_host_definition)
            if self._is_host_definition_matches(host_definition_info, node_name, secret_name, secret_namespace):
                return host_definition_info
        return None

    def generate_host_definition_info(self, k8s_host_definition):
        host_definition_info = HostDefinitionInfo()
        host_definition_info.name = k8s_host_definition.metadata.name
        host_definition_info.resource_version = utils.get_k8s_object_resource_version(k8s_host_definition)
        host_definition_info.uid = k8s_host_definition.metadata.uid
        host_definition_info.phase = self._get_host_definition_phase(k8s_host_definition)
        host_definition_info.secret_name = self._get_attr_from_host_definition(
            k8s_host_definition, settings.SECRET_NAME_FIELD)
        host_definition_info.secret_namespace = self._get_attr_from_host_definition(
            k8s_host_definition, settings.SECRET_NAMESPACE_FIELD)
        host_definition_info.node_name = self._get_attr_from_host_definition(
            k8s_host_definition, settings.NODE_NAME_FIELD)
        host_definition_info.node_id = self._get_attr_from_host_definition(
            k8s_host_definition, common_settings.HOST_DEFINITION_NODE_ID_FIELD)
        host_definition_info.connectivity_type = self._get_attr_from_host_definition(
            k8s_host_definition, settings.CONNECTIVITY_TYPE_FIELD)
        return host_definition_info

    def _get_host_definition_phase(self, k8s_host_definition):
        if k8s_host_definition.status:
            return k8s_host_definition.status.phase
        return ''

    def _get_attr_from_host_definition(self, k8s_host_definition, attribute):
        if hasattr(k8s_host_definition.spec.hostDefinition, attribute):
            return getattr(k8s_host_definition.spec.hostDefinition, attribute)
        return ''

    def _is_host_definition_matches(self, host_definition_info, node_name, secret_name, secret_namespace):
        return host_definition_info.node_name == node_name and \
            host_definition_info.secret_name == secret_name and \
            host_definition_info.secret_namespace == secret_namespace
