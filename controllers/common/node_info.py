from dataclasses import dataclass, field

from controllers.array_action import config as array_config
from controllers.common import utils
import controllers.servers.config as config


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


@dataclass
class Initiators:
    """
    Object containing node initiators (e.g. iqn, fc_wwns)
    """
    nvme_nqns: list = field(default_factory=list)
    fc_wwns: list = field(default_factory=list)
    iscsi_iqns: list = field(default_factory=list)

    def __post_init__(self):
        self.nvme_nqns = self._filter_empty_parts(self.nvme_nqns)
        self.fc_wwns = self._filter_empty_parts(self.fc_wwns)
        self.iscsi_iqns = self._filter_empty_parts(self.iscsi_iqns)

    def _filter_empty_parts(self, ports):
        ports = [port.strip() for port in ports]
        ports = filter(None, ports)
        return list(ports)

    def _get_iter(self):
        for connectivity_type, initiators in ((array_config.NVME_OVER_FC_CONNECTIVITY_TYPE, self.nvme_nqns),
                                              (array_config.FC_CONNECTIVITY_TYPE, self.fc_wwns),
                                              (array_config.ISCSI_CONNECTIVITY_TYPE, self.iscsi_iqns)):
            for initiator in initiators:
                yield connectivity_type, initiator

    def __iter__(self):
        return self._get_iter()

    def get_by_connectivity_type(self, connectivity_type):
        return {
            array_config.NVME_OVER_FC_CONNECTIVITY_TYPE: self.nvme_nqns,
            array_config.FC_CONNECTIVITY_TYPE: self.fc_wwns,
            array_config.ISCSI_CONNECTIVITY_TYPE: self.iscsi_iqns
        }[connectivity_type]

    def _lower(self, ports):
        return {port.lower() for port in ports if ports}

    def _is_match(self, ports, other_ports):
        other_ports = self._filter_empty_parts(other_ports)
        other_ports = self._lower(other_ports)
        ports = self._lower(ports)
        return not ports.isdisjoint(other_ports)

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
