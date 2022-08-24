from controllers.array_action.errors import HostNotFoundError, HostAlreadyExists
from controllers.array_action.storage_agent import detect_array_type, get_agent
from controllers.common.csi_logger import get_stdout_logger
from controllers.common.node_info import NodeIdInfo
from controllers.servers.host_definer.types import DefineHostResponse
from controllers.servers.utils import join_object_prefix_with_name, get_array_connection_info_from_secrets

logger = get_stdout_logger()


class HostDefinerServicer:
    def define_host(self, request):
        node_id_info = NodeIdInfo(request.node_id)
        initiators = node_id_info.initiators

        host_name = join_object_prefix_with_name(prefix=request.prefix, name=node_id_info.node_name)
        logger.debug("host name : {}".format(host_name))

        try:
            array_connection_info = get_array_connection_info_from_secrets(request.system_info)

            array_type = detect_array_type(array_connection_info.array_addresses)
            with get_agent(array_connection_info, array_type).get_mediator() as array_mediator:

                try:
                    self._get_host_name(initiators, array_mediator)
                except HostNotFoundError:
                    logger.debug("host was not found. creating a new host with initiators: {0}".format(initiators))
                    try:
                        array_mediator.create_host(host_name, initiators, request.connectivity_type)
                    except HostAlreadyExists:
                        host = array_mediator.get_host_by_name(host_name)
                        return self._validate_host(host, initiators)

                return DefineHostResponse()
        except Exception as ex:
            logger.exception(ex)
            return DefineHostResponse(error_message=str(ex))

    def undefine_host(self, request):
        node_id_info = NodeIdInfo(request.node_id)
        initiators = node_id_info.initiators
        try:
            array_connection_info = get_array_connection_info_from_secrets(request.system_info)
            array_type = detect_array_type(array_connection_info.array_addresses)
            with get_agent(array_connection_info, array_type).get_mediator() as array_mediator:

                try:
                    found_host_name = self._get_host_name(initiators, array_mediator)
                    array_mediator.delete_host(found_host_name)
                except HostNotFoundError:
                    logger.debug("host was not found")

                return DefineHostResponse()
        except Exception as ex:
            logger.exception(ex)
            return DefineHostResponse(error_message=str(ex))

    def _get_host_name(self, initiators, array_mediator):
        found_host_name, _ = array_mediator.get_host_by_host_identifiers(initiators)
        logger.debug("host found : {}".format(found_host_name))
        return found_host_name

    def _validate_host(self, host, initiators):
        if host.initiators not in initiators:
            error_message = "host () found but with different initiators: {}".format(host, host.initiators)
            logger.exception(error_message)
            return DefineHostResponse(error_message=str(error_message))
        return DefineHostResponse()
