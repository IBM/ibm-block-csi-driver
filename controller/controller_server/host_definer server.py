from controller.array_action.errors import BaseArrayActionException
from controller.array_action.storage_agent import detect_array_type, get_agent
from controller.common.csi_logger import get_stdout_logger
from controller.common.node_info import NodeIdInfo
from controller.controller_server.utils import get_array_connection_info_from_secrets, \
    generate_host_definer_create_volume_response

logger = get_stdout_logger()


class HostDefinerServicer:
    def verifyHostDefinitionOnStorage(self, request):
        host_name = request.host_name
        logger.debug("host name : {}".format(host_name))

        node_id_info = NodeIdInfo(request.node_id)
        initiators = node_id_info.initiators
        connectivity_type = request.connectivity_type

        try:
            array_connection_info = get_array_connection_info_from_secrets(request.secrets)

            array_type = detect_array_type(array_connection_info.array_addresses)
            with get_agent(array_connection_info, array_type).get_mediator() as array_mediator:

                host_name, connectivity_types = array_mediator.get_host_by_host_identifiers(initiators)
                if host_name:
                    logger.debug("host found : {}".format(host_name))
                else:
                    logger.debug("hos was not found. creating a new host with initiators: {0}".format(initiators))

                    host = array_mediator.create_host(host_name, initiators, connectivity_type)

                response = generate_host_definer_create_volume_response(host)
                return response
        except BaseArrayActionException as ex:
            return
