from controllers.common.csi_logger import get_stdout_logger
import controllers.common.settings as common_settings
from controllers.servers.host_definer import settings
from controllers.servers.host_definer.utils import utils
from controllers.servers.host_definer.utils import manifest_utils
from controllers.servers.host_definer.types import HostDefinitionInfo
import controllers.servers.host_definer.messages as messages
from controllers.servers.host_definer.k8s.api import K8SApi

logger = get_stdout_logger()


class HostDefinitionManager:
    def __init__(self):
        self.k8s_api = K8SApi()

    def get_host_definition_info_from_secret(self, secret_info):
        host_definition_info = HostDefinitionInfo()
        host_definition_info.secret_name = secret_info.name
        host_definition_info.secret_namespace = secret_info.namespace
        return host_definition_info

    def get_matching_host_definition_info(self, node_name, secret_name, secret_namespace):
        k8s_host_definitions = self.k8s_api.list_host_definition().items
        for k8s_host_definition in k8s_host_definitions:
            host_definition_info = self.generate_host_definition_info(k8s_host_definition)
            if self._is_host_definition_matches(host_definition_info, node_name, secret_name, secret_namespace):
                return host_definition_info
        return None

    def _is_host_definition_matches(self, host_definition_info, node_name, secret_name, secret_namespace):
        return host_definition_info.node_name == node_name and \
            host_definition_info.secret_name == secret_name and \
            host_definition_info.secret_namespace == secret_namespace

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

    def set_host_definition_status(self, host_definition_name, host_definition_phase):
        logger.info(messages.SET_HOST_DEFINITION_STATUS.format(host_definition_name, host_definition_phase))
        status = manifest_utils.get_host_definition_status_manifest(host_definition_phase)
        self.k8s_api.patch_cluster_custom_object_status(
            common_settings.CSI_IBM_GROUP, common_settings.VERSION, common_settings.HOST_DEFINITION_PLURAL,
            host_definition_name, status)

    def get_host_definition_info_from_secret_and_node_name(self, node_name, secret_info):
        host_definition_info = self.get_host_definition_info_from_secret(secret_info)
        host_definition_info = self.add_name_to_host_definition_info(node_name, host_definition_info)
        return host_definition_info

    def add_name_to_host_definition_info(self, node_name, host_definition_info):
        host_definition_info.node_name = node_name
        host_definition_info.node_id = NODES[node_name].node_id
        host_definition_info.name = self._get_host_definition_name(node_name)
        return host_definition_info

    def _get_host_definition_name(self, node_name):
        return '{0}-{1}'.format(node_name, utils.get_random_string()).replace('_', '.')
