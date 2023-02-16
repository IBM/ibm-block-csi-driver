from controllers.common.csi_logger import get_stdout_logger
from controllers.servers.host_definer import settings

logger = get_stdout_logger()


class StorageClassManager:
    def is_storage_class_has_csi_as_a_provisioner(self, storage_class_info):
        return storage_class_info.provisioner == settings.CSI_PROVISIONER_NAME
