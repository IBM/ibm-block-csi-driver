from host_definer.common import utils, settings

logger = utils.get_stdout_logger()


class StorageHostManager:
    def __init__(self):
        pass

    def verify_host_on_storage(self, host_request):
        logger.info('Verifying host: {} created on storage: {}'.format(
            host_request.name, host_request.system_info[settings.MANAGEMENT_ADDRESS_KEY]))
        logger.info(
            'storage username: {0}'.format(
                host_request.system_info[settings.USERNAME_KEY]))
        logger.info(
            'storage password: {0}'.format(
                host_request.system_info[settings.PASSWORD_KEY]))

    def verify_host_removed_from_storage(self, host_request):
        logger.info('Verifying host: {} removed from storage: {}'.format(
            host_request.name, host_request.system_info[settings.MANAGEMENT_ADDRESS_KEY]))
        logger.info(
            'storage username: {0}'.format(
                host_request.system_info[settings.USERNAME_KEY]))
        logger.info(
            'storage password: {0}'.format(
                host_request.system_info[settings.PASSWORD_KEY]))
