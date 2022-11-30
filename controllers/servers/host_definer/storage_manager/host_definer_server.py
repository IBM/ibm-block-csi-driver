from controllers.array_action import settings as array_config
from controllers.array_action.errors import HostNotFoundError, HostAlreadyExists
from controllers.array_action.storage_agent import detect_array_type, get_agent
from controllers.common.csi_logger import get_stdout_logger
from controllers.common.node_info import NodeIdInfo
from controllers.servers.host_definer.types import DefineHostResponse
from controllers.servers.utils import join_object_prefix_with_name, get_initiators_connectivity_type
import controllers.servers.host_definer.settings as host_definer_settings
import controllers.common.settings as common_settings

logger = get_stdout_logger()


class HostDefinerServicer:
    def define_host(self, request):
        array_connection_info = request.array_connection_info
        node_id_info = NodeIdInfo(request.node_id_from_csi_node)
        initiators = node_id_info.initiators
        connectivity_type_from_user = get_initiators_connectivity_type(initiators, request.connectivity_type_from_user)
        host_name = join_object_prefix_with_name(prefix=request.prefix, name=node_id_info.node_name)
        logger.debug("host name : {}".format(host_name))
        try:
            array_type = detect_array_type(array_connection_info.array_addresses)
            with get_agent(array_connection_info, array_type).get_mediator() as array_mediator:
                try:
                    initiators_from_host_definition = self._get_initiators_from_node_id(
                        request.node_id_from_host_definition)
                    found_host_name = self._get_host_name(initiators_from_host_definition, array_mediator)
                    self._update_host_ports(request, found_host_name, array_mediator)
                    self._update_host_io_group(request, found_host_name, array_mediator)
                    host_name = found_host_name
                except HostNotFoundError:
                    logger.debug("host was not found. creating a new host with initiators: {0}".format(initiators))
                    try:
                        self._create_host(host_name, array_mediator, request)
                    except HostAlreadyExists:
                        host = array_mediator.get_host_by_name(host_name)
                        define_host_response = self._validate_host(host, initiators)
                        if define_host_response.error_message:
                            return define_host_response
                        host_name = host.name

                return self._generate_response(
                    array_mediator, host_name, connectivity_type_from_user, array_connection_info.array_addresses[0])
        except Exception as ex:
            logger.exception(ex)
            return DefineHostResponse(error_message=str(ex))

    def undefine_host(self, request):
        node_id_info = NodeIdInfo(request.node_id_from_csi_node)
        initiators = node_id_info.initiators
        try:
            array_connection_info = request.array_connection_info
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

    def _update_host_ports(self, request, host, array_mediator):
        initiators = self._get_initiators_from_node_id(request.node_id_from_csi_node)
        connectivity_type_from_user = get_initiators_connectivity_type(initiators, request.connectivity_type_from_user)
        connectivity_type_from_host = array_mediator.get_host_connectivity_type(host)
        if self._is_protocol_switched(connectivity_type_from_user, connectivity_type_from_host):
            array_mediator.delete_host(host)
            self._create_host(host, array_mediator, request)
        elif self._is_port_update_needed_when_same_protocol(request, connectivity_type_from_user,
                                                            connectivity_type_from_host):
            self._remove_host_ports(array_mediator, host, connectivity_type_from_host)
            array_mediator.add_ports_to_host(host, initiators, connectivity_type_from_user)

    def _get_initiators_from_node_id(self, node_id):
        node_id_info = NodeIdInfo(node_id)
        return node_id_info.initiators

    def _is_protocol_switched(self, connectivity_type_from_user, connectivity_type_from_host):
        return self._is_switching_from_nvme_to_scsi(connectivity_type_from_user, connectivity_type_from_host) or \
            self._is_switching_from_scsi_to_nvme(connectivity_type_from_user, connectivity_type_from_host)

    def _is_switching_from_nvme_to_scsi(self, connectivity_type_from_user, connectivity_type_from_host):
        return connectivity_type_from_host == array_config.NVME_OVER_FC_CONNECTIVITY_TYPE and \
            self._is_connectivity_type_scsi(connectivity_type_from_user)

    def _is_switching_from_scsi_to_nvme(self, connectivity_type_from_user, connectivity_type_from_host):
        return self._is_connectivity_type_scsi(connectivity_type_from_host) and \
            connectivity_type_from_user == array_config.NVME_OVER_FC_CONNECTIVITY_TYPE

    def _is_connectivity_type_scsi(self, connectivity_type):
        return connectivity_type in [array_config.FC_CONNECTIVITY_TYPE, array_config.ISCSI_CONNECTIVITY_TYPE]

    def _create_host(self, host, array_mediator, request):
        initiators = self._get_initiators_from_node_id(request.node_id_from_csi_node)
        connectivity_type = get_initiators_connectivity_type(initiators, request.connectivity_type_from_user)
        array_mediator.create_host(host, initiators, connectivity_type, request.io_group)
        array_mediator.add_ports_to_host(host, initiators, connectivity_type)

    def _is_port_update_needed_when_same_protocol(
            self, request, connectivity_type_from_user, connectivity_type_from_host):
        return connectivity_type_from_user != connectivity_type_from_host \
            or request.node_id_from_csi_node != request.node_id_from_host_definition

    def _remove_host_ports(self, array_mediator, host_name, connectivity_type):
        if connectivity_type:
            ports_to_remove = array_mediator.get_host_connectivity_ports(host_name, connectivity_type)
            array_mediator.remove_ports_from_host(host_name, ports_to_remove, connectivity_type)

    def _update_host_io_group(self, request, host, array_mediator):
        io_group_from_host = array_mediator.get_host_io_group(host)
        io_group_to_remove, io_group_to_add = self._get_io_group_to_remove_and_add(
            io_group_from_host, request.io_group)
        array_mediator.remove_io_group_from_host(host, io_group_to_remove)
        array_mediator.add_io_group_to_host(host, io_group_to_add)

    def _get_io_group_to_remove_and_add(self, io_group_from_host, ig_group_from_user):
        ig_group_from_user = self._split_io_group_from_user(ig_group_from_user)
        if not io_group_from_host:
            return '', common_settings.IO_GROUP_DELIMITER.join(ig_group_from_user)

        io_group_to_add, io_group_to_delete = self._get_io_group_to_remove_and_add_lists(
            io_group_from_host, ig_group_from_user)
        return common_settings.IO_GROUP_DELIMITER.join(io_group_to_delete), \
            common_settings.IO_GROUP_DELIMITER.join(io_group_to_add)

    def _split_io_group_from_user(self, ig_group_from_user):
        if not ig_group_from_user:
            return host_definer_settings.FULL_IO_GROUP.split(common_settings.IO_GROUP_DELIMITER)
        return ig_group_from_user.split(common_settings.IO_GROUP_DELIMITER)

    def _get_io_group_to_remove_and_add_lists(self, io_group_from_host, ig_group_from_user):
        io_group_to_add = []
        for io_group in ig_group_from_user:
            id_index = self._get_element_index_in_list(io_group, io_group_from_host.id)
            name_index = self._get_element_index_in_list(io_group, io_group_from_host.name)
            if id_index != -1 or name_index != -1:
                io_group_from_host.id.pop(id_index)
                io_group_from_host.name.pop(id_index)
            else:
                io_group_to_add.append(io_group)
        return io_group_to_add, io_group_from_host.id

    def _get_element_index_in_list(self, element, list_to_search_in):
        try:
            return list_to_search_in.index(element)
        except ValueError:
            return -1

    def _validate_host(self, host, initiators):
        if host.initiators not in initiators:
            error_message = "host ({}) found but with different initiators: {}".format(host, host.initiators)
            logger.exception(error_message)
            return DefineHostResponse(error_message=str(error_message))
        return DefineHostResponse()

    def _generate_response(self, array_mediator, host_name, connectivity_type, management_address):
        define_host_response = DefineHostResponse(connectivity_type=connectivity_type, node_name_on_storage=host_name,
                                                  management_address=management_address)
        ports = array_mediator.get_host_connectivity_ports(host_name, connectivity_type)
        define_host_response.ports = ports
        io_group_ids = array_mediator.get_host_io_group(host_name).id
        define_host_response.io_group = [int(io_group_id) for io_group_id in io_group_ids]
        return define_host_response
