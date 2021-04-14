from mock import Mock


def get_mock_xiv_volume(size, name, wwn):
    volume = Mock()
    volume.capacity = size
    volume.wwn = wwn
    volume.name = name
    volume.pool_name = "volume-name"
    volume.master_name = ""
    return volume


def get_mock_xiv_snapshot(capacity, name, wwn, volume_name):
    snapshot = Mock()
    snapshot.capacity = capacity
    snapshot.name = name
    snapshot.wwn = wwn
    snapshot.master_name = volume_name
    snapshot.pool_name = "pool_name"
    return snapshot


def get_mock_xiv_host(name, iscsi_ports, fc_ports):
    host = Mock()
    host.iscsi_ports = iscsi_ports
    host.fc_ports = fc_ports
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


def get_mock_xiv_ip_interface(type_, address=None, address6=None):
    ip_interface = Mock()
    ip_interface.type = type_
    ip_interface.address = address
    ip_interface.address6 = address6
    return ip_interface


def get_mock_xiv_config_param(name, value):
    config_param = Mock()
    config_param.name = name
    config_param.value = value
    return config_param
