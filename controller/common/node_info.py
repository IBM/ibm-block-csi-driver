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
        self.initiators = Initiators(nvme_nqn.strip(), fc_wwns, iscsi_iqn.strip())


class Initiators:
    """
    Object containing node initiators (e.g. iqn, fc_wwns)
    """

    def __init__(self, nvme_nqn, fc_wwns, iscsi_iqn):
        """
        Args:
            nvme_nqn: nqn
            fc_wwns : list of fc wwns
            iscsi_iqn : iqn
        """
        self.nvme_nqn = nvme_nqn
        self.fc_wwns = list(filter(None, fc_wwns))
        self.iscsi_iqn = iscsi_iqn
        self._nvme_nqn_lowercase = nvme_nqn.lower()
        self._fc_wwns_lowercase_set = set(wwn.lower() for wwn in fc_wwns)
        self._iscsi_iqn_lowercase = iscsi_iqn.lower()

    def is_array_wwns_match(self, host_wwns):
        """
        Args:
           host_wwns : storage host wwns list

        Returns:
           Is current host wwns matches
        """
        host_wwns_lower = [wwn.lower() for wwn in host_wwns]
        return not self._fc_wwns_lowercase_set.isdisjoint(host_wwns_lower)

    def is_array_iscsi_iqns_match(self, host_iqns):
        """
        Args:
           host_iqns: storage host iqns list

        Returns:
           Is current host iqns matches
        """
        host_iqns_lower = [iqn.lower() for iqn in host_iqns]
        return self._iscsi_iqn_lowercase in host_iqns_lower

    def is_array_nvme_nqn_match(self, host_nqns):
        """
        Args:
           host_nqns: storage host nqns list

        Returns:
           Is current host nqns matches
        """
        host_nqns_lower = [nqn.lower() for nqn in host_nqns]
        return self._nvme_nqn_lowercase in host_nqns_lower

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
        return "nvme_nqn: {}, fc_wwns : {}, iscsi_iqn : {} ".format(self.nvme_nqn, self.fc_wwns, self.iscsi_iqn)

    def __eq__(self, another):
        return self.nvme_nqn == another.nvme_nqn and \
               self.fc_wwns == another.fc_wwns and \
               self.iscsi_iqn == another.iscsi_iqn

    def __hash__(self):
        fcs_str = ""
        for fc in self.fc_wwns:
            fcs_str.join(fc)
        return hash(self.nvme_nqn + fcs_str + self.iscsi_iqn)
