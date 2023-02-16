import datetime
from kubernetes import client

from controllers.common.csi_logger import get_stdout_logger
from controllers.servers.host_definer import settings

logger = get_stdout_logger()


class EventManager():
    def generate_k8s_event(self, host_definition_info, message, action, message_type):
        return client.CoreV1Event(
            metadata=client.V1ObjectMeta(generate_name='{}.'.format(host_definition_info.name), ),
            reporting_component=settings.HOST_DEFINER, reporting_instance=settings.HOST_DEFINER, action=action,
            type=self._get_event_type(message_type), reason=message_type + action, message=str(message),
            event_time=datetime.datetime.utcnow().isoformat(timespec='microseconds') + 'Z',
            involved_object=client.V1ObjectReference(
                api_version=settings.CSI_IBM_API_VERSION, kind=settings.HOST_DEFINITION_KIND,
                name=host_definition_info.name, resource_version=host_definition_info.resource_version,
                uid=host_definition_info.uid, ))

    def _get_event_type(self, message_type):
        if message_type != settings.SUCCESSFUL_MESSAGE_TYPE:
            return settings.WARNING_EVENT_TYPE
        return settings.NORMAL_EVENT_TYPE
