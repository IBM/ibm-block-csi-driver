from host_definer.common import utils

logger = utils.get_stdout_logger()


class StorageHostManager:
    def __init__(self):
        pass

    def verify_host_on_storage(self, host_object):
        logger.info('Verifying host: {} created on storage: {}'.format(
            host_object.host_name, host_object.management_address))
        logger.info('storage server: {0}'.format(host_object.management_address))
        logger.info(
            'storage username: {0}'.format(
                host_object.storage_username))
        logger.info(
            'storage password: {0}'.format(
                host_object.storage_password))
        logger.info('host name: {0}'.format(host_object.host_name))

    def verify_host_removed_from_storage(self, host_object):
        logger.info('Verifying host: {} removed from storage: {}'.format(
            host_object.host_name, host_object.management_address))
        logger.info('storage server: {0}'.format(host_object.management_address))
        logger.info(
            'storage username: {0}'.format(
                host_object.storage_username))
        logger.info(
            'storage password: {0}'.format(
                host_object.storage_password))
        logger.info('host name: {0}'.format(host_object.host_name))
