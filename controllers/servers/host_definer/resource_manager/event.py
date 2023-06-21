import datetime
from kubernetes import client

from controllers.common.csi_logger import get_stdout_logger
from controllers.servers.host_definer import settings
import controllers.common.settings as common_settings

logger = get_stdout_logger()


class EventManager():
    def generate_k8s_event(self, host_definition_info, message, action, message_type):
        return client.CoreV1Event(metadata=client.V1ObjectMeta(
            generate_name='{}.'.format(host_definition_info.name),),
            reporting_component=common_settings.HOST_DEFINER,
            reporting_instance=common_settings.HOST_DEFINER, action=action,
            type=self._get_event_type(message_type),
            reason=message_type + action, message=str(message),
            event_time=datetime.datetime.utcnow().isoformat(timespec='microseconds') + 'Z',
            involved_object=client.V1ObjectReference(
            api_version=common_settings.CSI_IBM_API_VERSION,
            kind=common_settings.HOST_DEFINITION_KIND, name=host_definition_info.name,
            resource_version=host_definition_info.resource_version,
            uid=host_definition_info.uid,))

    def _get_event_type(self, message_type):
        if message_type != common_settings.SUCCESSFUL_MESSAGE_TYPE:
            return common_settings.WARNING_EVENT_TYPE
        return common_settings.NORMAL_EVENT_TYPE
