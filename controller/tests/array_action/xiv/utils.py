from mock import Mock


def get_mock_xiv_volume(size, name, wwn):
    vol = Mock()
    vol.capacity = size
    vol.wwn = wwn
    vol.name = name
    vol.pool_name = "vol-name"
    return vol
