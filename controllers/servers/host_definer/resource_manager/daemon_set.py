import time

import controllers.common.settings as common_settings
from controllers.common.csi_logger import get_stdout_logger
import controllers.servers.host_definer.messages as messages
from controllers.servers.host_definer import settings
from controllers.servers.host_definer.k8s.api import K8SApi

logger = get_stdout_logger()


class DaemonSetManager():
    def __init__(self):
        self.k8s_api = K8SApi()

    def wait_until_all_daemon_set_pods_are_up_to_date(self):
        csi_daemon_set = self._get_csi_daemon_set()
        if not csi_daemon_set:
            return None
        status = csi_daemon_set.status
        while status.updated_number_scheduled != status.desired_number_scheduled:
            logger.info(messages.UPDATED_CSI_NODE_VS_DESIRED.format(
                status.updated_number_scheduled, status.desired_number_scheduled))
            if status.desired_number_scheduled == 0:
                return None
            csi_daemon_set = self._get_csi_daemon_set()
            if not csi_daemon_set:
                return None
            status = csi_daemon_set.status
            time.sleep(0.5)
        return csi_daemon_set.metadata.name

    def _get_csi_daemon_set(self):
        daemon_sets = self.k8s_api.list_daemon_set_for_all_namespaces(common_settings.DRIVER_PRODUCT_LABEL)
        if daemon_sets and daemon_sets.items:
            return daemon_sets.items[0]
        return None
