from controller.array_action import config as array_config
from controller.controller_server import config
from controller.controller_server import utils


class NodeIdInfo:
    def __init__(self, node_id):
        """
        Args:
            node_id: <node_name>,<iqn>,<wwns>
        """
        node_name, nvme_nqn, fc_wwns_str, iscsi_iqn = utils.get_node_id_info(node_id)
        fc_wwns = fc_wwns_str.split(config.PARAMETERS_FC_WWN_DELIMITER)
        self.node_name = node_name
        self.initiators = Initiators([nvme_nqn], fc_wwns, [iscsi_iqn])


class Initiators:
    """
    Object containing node initiators (e.g. iqn, fc_wwns)
    """

    def __init__(self, nvme_nqns=None, fc_wwns=None, iscsi_iqns=None):
        """
        Args:
            nvme_nqns: list of nqns
            fc_wwns : list of fc wwns
            iscsi_iqns : list of iqns
        """
        if not nvme_nqns:
            nvme_nqns = []
        if not fc_wwns:
            fc_wwns = []
        if not iscsi_iqns:
            iscsi_iqns = []
        self.nvme_nqns = self._filter_empty_parts(nvme_nqns)
        self.fc_wwns = self._filter_empty_parts(fc_wwns)
        self.iscsi_iqns = self._filter_empty_parts(iscsi_iqns)

    def _filter_empty_parts(self, ports):
        ports_strip = [port.strip() for port in ports]
        ports_filter = filter(None, ports_strip)
        return list(ports_filter)

    def _get_iter(self):
        for nvme_nqn in self.nvme_nqns:
            yield array_config.NVME_OVER_FC_CONNECTIVITY_TYPE, nvme_nqn
        for fc_wwn in self.fc_wwns:
            yield array_config.FC_CONNECTIVITY_TYPE, fc_wwn
        for iscsi_iqn in self.nvme_nqns:
            yield array_config.ISCSI_CONNECTIVITY_TYPE, iscsi_iqn

    def __iter__(self):
        return self._get_iter()

    def _lower(self, ports):
        return {port.lower() for port in ports if ports}

    def _is_match(self, ports, other_ports):
        ports_lower = self._lower(ports)
        other_ports_lower = self._lower(other_ports)
        return not ports_lower.isdisjoint(other_ports_lower)

    def is_array_wwns_match(self, host_wwns):
        return self._is_match(self.fc_wwns, host_wwns)

    def is_array_iscsi_iqns_match(self, host_iqns):
        return self._is_match(self.iscsi_iqns, host_iqns)

    def is_array_nvme_nqn_match(self, host_nqns):
        return self._is_match(self.nvme_nqns, host_nqns)

    def __contains__(self, other_initiators):
        return other_initiators.is_array_nvme_nqn_match(self.nvme_nqns) or \
               other_initiators.is_array_wwns_match(self.fc_wwns) or \
               other_initiators.is_array_iscsi_iqns_match(self.iscsi_iqns)

    def __str__(self):
        return "nvme_nqns : {}, fc_wwns : {}, iscsi_iqns : {} ".format(self.nvme_nqns, self.fc_wwns, self.iscsi_iqns)
