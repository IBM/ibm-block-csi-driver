
class classproperty(object):

    def __init__(self, function):
        self._function = function

    def __get__(self, instance, owner):
        return self._function(owner)

def is_wwns_match(node_wwns_set, array_host_wwns):
    """
    This function will find the host name by iscsi iqn or fc wwns.

    Args:
       node_wwns_set : lowercase storage host wwns set
       array_host_wwns : storage host wwns list

    Returns:
       Is current host matches
    """
    array_host_wwns = [wwn.lower() for wwn in array_host_wwns] if array_host_wwns else []
    return not node_wwns_set.isdisjoint(array_host_wwns)

def get_all_ports(iscsi_iqn, wwns):
    res = []
    if iscsi_iqn:
        res.append(iscsi_iqn)
    if wwns:
        res = res + wwns
    return res
