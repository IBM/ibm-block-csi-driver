import controller.controller_server.utils as utils
import controller.controller_server.config as config

class NodeIdInfo:
    def __init__(self, node_id):
        """
        Args:
            node_id: <node_name>,<iqn>,<wwns>
        """
        node_name, iscsi_iqn, fc_wwns_str = utils.get_node_id_info(node_id)
        fc_wwns = fc_wwns_str.split(config.PARAMETERS_FC_WWN_DELIMITER)
        self.node_name = node_name
        self.initiators = Initiators(iscsi_iqn.strip(), fc_wwns)


class Initiators:
    """
    Object containing node initiators (e.g. iqn, fc_wwns)
    """
    def __init__(self, iscsi_iqn, fc_wwns):
        """
        Args:
            iscsi_iqn : iqn
            fc_wwns : list of fc wwns
        """
        self.iscsi_iqn = iscsi_iqn
        self.fc_wwns = fc_wwns
        self._fc_wwns_lowercase_set = set([wwn.lower() for wwn in fc_wwns])
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
        return not self._iscsi_iqn_lowercase in host_iqns_lower

    def __str__(self):
        return "iscsi_iqn: " + self.iscsi_iqn + ", fc_wwns: " + ",".join(self.fc_wwns)
