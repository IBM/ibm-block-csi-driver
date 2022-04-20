import controller.controller_server.utils as utils
import controller.controller_server.config as config


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

    def __init__(self, nvme_nqn="", fc_wwns=None, iscsi_iqn=""):
        """
        Args:
            nvme_nqn: nqn
            fc_wwns : list of fc wwns
            iscsi_iqn : iqn
        """
        if fc_wwns is None:
            fc_wwns = []
        self.nvme_nqn = nvme_nqn
        self.fc_wwns = fc_wwns
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

    def __contains__(self, other_initiators):
        if other_initiators.is_array_wwns_match(self.fc_wwns) or \
                other_initiators.is_array_nvme_nqn_match(self.nvme_nqn) or \
                other_initiators.is_array_iscsi_iqns_match([self.iscsi_iqn]):
            return True
        return False

    def __str__(self):
        return "nvme_nqn: {}, fc_wwns : {}, iscsi_iqn : {} ".format(self.nvme_nqn, self.fc_wwns, self.iscsi_iqn)
