from host_definer.common import utils, settings
from host_definer.common.types import VerifyHostResponse

logger = utils.get_stdout_logger()


class StorageHostManager:
    def __init__(self):
        pass

    def verify_host_defined(self, host_request):
        node_name = host_request.node_id.split(';')[0]
        logger.info('Verifying host: {} created on storage: {}'.format(
            node_name, host_request.system_info[settings.MANAGEMENT_ADDRESS_KEY]))
        logger.info(
            'storage username: {0}'.format(
                host_request.system_info[settings.USERNAME_KEY]))
        logger.info(
            'storage password: {0}'.format(
                host_request.system_info[settings.PASSWORD_KEY]))
        logger.info(
            'prefix: {0}'.format(
                host_request.prefix))
        host_response = VerifyHostResponse()
        return host_response

    def verify_host_undefined(self, host_request):
        node_name = host_request.node_id.split(';')[0]
        logger.info('Verifying host: {} removed from storage: {}'.format(
            node_name, host_request.system_info[settings.MANAGEMENT_ADDRESS_KEY]))
        logger.info(
            'storage username: {0}'.format(
                host_request.system_info[settings.USERNAME_KEY]))
        logger.info(
            'storage password: {0}'.format(
                host_request.system_info[settings.PASSWORD_KEY]))
        logger.info(
            'prefix: {0}'.format(
                host_request.prefix))
        host_response = VerifyHostResponse()
        return host_response
