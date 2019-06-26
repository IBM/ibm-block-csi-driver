from controller.array_action.array_mediator_interface import  ArrayMediator
from controller.common.csi_logger import get_stdout_logger
from controller.array_action.errors import StorageClassCapabilityNotSupported,PoolDoesNotMatchCapabilities

array_connections_dict = {}
logger = get_stdout_logger()

class SVCArrayMediator(ArrayMediator):
    max_connections = 10  # TODO : need to implement all the interface methods\properties 
    port = 22
    array_type = "svc"
    pass

    def validate_supported_capabilities(self, capabilities, pool=None):
        """
        :param capabilities: supportted capabilities is one of [{"SpaceEfficiency": "Thin"},\
                                                                {"SpaceEfficiency": "Thick"},\
                                                                {"SpaceEfficiency": "Compression"},\
                                                                {"SpaceEfficiency": "Dedup"}]
        :param pool: pool name
        :return:
        """
        logger.info("validate_supported_capabilities for capabilities : {0}".format(capabilities))
        if (len(capabilities) > 0 and not capabilities.get('SpaceEfficiency')) or \
                        capabilities.get('SpaceEfficiency') not in ['Thin', 'Thick', 'Compression', 'Dedup']:
            logger.error("capabilities is not supported {0}".format(capabilities))
            raise StorageClassCapabilityNotSupported(capabilities)

        if capabilities.get('SpaceEfficiency') in ['Compression', 'Dedup']:
            if not self.is_compression_enabled():
                raise StorageClassCapabilityNotSupported(capabilities)

        if capabilities.get('SpaceEfficiency') == 'Dedup':
            if not self.is_deduplication_supported(pool):
                e = "it is not a dedup pool"
                raise PoolDoesNotMatchCapabilities(pool, capabilities, e)

        logger.info("Finished validate_supported_capabilities")

    def is_compression_enabled(self):
        for iogrp in self.client.svcinfo.lsiogrp():
            if self.client.svcinfo.lsiogrp(object_id=iogrp.id).as_single_element.\
                    get('compression_supported', '') == 'yes':
                return True
        return False

    def is_deduplication_supported(self, pool_name):
        cap = self.client.svcinfo.lsguicapabilities().as_single_element
        pool = self.client.svcinfo.lsmdiskgrp(bytes=True, object_id=pool_name).as_single_element
        return pool.get("data_reduction", '') == 'yes' and cap.get('data_reduction', '') == 'yes' \
               and self.is_compression_enabled()
