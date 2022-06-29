from controllers.servers.config import SECRET_ARRAY_PARAMETER
from controllers.common.csi_logger import get_stdout_logger
from controllers.servers.host_definer.common.types import DefineHostResponse

logger = get_stdout_logger()


class StorageHostManager:

    def define_host(self, host_request):
        node_name = host_request.node_id.split(';')[0]
        logger.info('Verifying host: {} created on storage: {}'.format(
            host_request.prefix+'.'+node_name, host_request.system_info[SECRET_ARRAY_PARAMETER]))
        host_response = DefineHostResponse()
        return host_response

    def undefine_host(self, host_request):
        node_name = host_request.node_id.split(';')[0]
        logger.info('Verifying host: {} removed from storage: {}'.format(
            node_name, host_request.system_info[SECRET_ARRAY_PARAMETER]))
        host_response = DefineHostResponse()
        return host_response
