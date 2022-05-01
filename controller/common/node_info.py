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

    def __init__(self, nvme_nqns, fc_wwns, iscsi_iqns):
        """
        Args:
            nvme_nqns: list of nqns
            fc_wwns : list of fc wwns
            iscsi_iqns : list of iqns
        """
        self._nvme_nqns = self._strip_and_filter(nvme_nqns)
        self._fc_wwns = self._strip_and_filter(fc_wwns)
        self._iscsi_iqns = self._strip_and_filter(iscsi_iqns)
        self._nvme_nqns_lowercase = self._lower_and_parse(self._nvme_nqns)
        self._fc_wwns_lowercase = self._lower_and_parse(self._fc_wwns)
        self._iscsi_iqns_lowercase = self._lower_and_parse(self._iscsi_iqns)

    def _strip_and_filter(self, ports):
        ports_strip = [port.strip() for port in ports if port]
        ports_filter = filter(None, ports_strip)
        return list(ports_filter)

    def _lower_and_parse(self, ports):
        return {port.lower() for port in ports if ports}

    def is_array_wwns_match(self, host_wwns):
        """
        Args:
           host_wwns : storage host wwns list

        Returns:
           Is current host wwns matches
        """
        host_wwns_lower = [wwn.lower() for wwn in host_wwns]
        return not self._fc_wwns_lowercase.isdisjoint(host_wwns_lower)

    def is_array_iscsi_iqns_match(self, host_iqns):
        """
        Args:
           host_iqns: storage host iqns list

        Returns:
           Is current host iqns matches
        """
        host_iqns_lower = [iqn.lower() for iqn in host_iqns]
        return not self._iscsi_iqns_lowercase.isdisjoint(host_iqns_lower)

    def is_array_nvme_nqn_match(self, host_nqns):
        """
        Args:
           host_nqns: storage host nqns list

        Returns:
           Is current host nqns matches
        """
        host_nqns_lower = [nqn.lower() for nqn in host_nqns]
        return not self._nvme_nqns_lowercase.isdisjoint(host_nqns_lower)

    def __contains__(self, other_initiators):
        return other_initiators.is_array_nvme_nqn_match(self._nvme_nqns) or \
               other_initiators.is_array_wwns_match(self._fc_wwns) or \
               other_initiators.is_array_iscsi_iqns_match(self._iscsi_iqns)

    def _get_iter(self):
        if self.nvme_nqn:
            yield array_config.NVME_OVER_FC_CONNECTIVITY_TYPE, self.nvme_nqn
        for fc_wwn in self.fc_wwns:
            yield array_config.FC_CONNECTIVITY_TYPE, fc_wwn
        if self.iscsi_iqn:
            yield array_config.ISCSI_CONNECTIVITY_TYPE, self.iscsi_iqn

    def __iter__(self):
        return self._get_iter()

    def __str__(self):
        return "nvme_nqn: {}, fc_wwns : {}, iscsi_iqn : {} ".format(self._nvme_nqns, self._fc_wwns, self._iscsi_iqns)
