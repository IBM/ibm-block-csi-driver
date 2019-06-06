from mock import Mock


def get_mock_xiv_volume(size, name, wwn):
    vol = Mock()
    vol.capacity = size
    vol.wwn = wwn
    vol.name = name
    vol.pool_name = "vol-name"
    return vol

def get_mock_xiv_host(name, ports):
    host = Mock()
    host.iscsi_ports = ports
    host.name = name
    return host


def get_mock_xiv_vol_mapping(lun, host):
    mapping = Mock()
    mapping.lun = lun
    mapping.host = host
    return mapping

def get_mock_xiv_host_mapping(lun):
    mapping = Mock()
    mapping.lun = lun
    return mapping