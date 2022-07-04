from controllers.common.csi_logger import get_stdout_logger
from controllers.servers.host_definer.types import DefineHostResponse

logger = get_stdout_logger()


class HostDefinerServicer:

    def define_host(self, request):
        return DefineHostResponse()

    def undefine_host(self, request):
        return DefineHostResponse()
