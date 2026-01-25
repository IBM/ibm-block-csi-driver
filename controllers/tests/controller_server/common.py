from mock.mock import patch, Mock


def mock_get_agent(contex, server_path):
    contex.storage_agent.get_mediator.return_value.__enter__.return_value = contex.mediator
    get_agent_path = '.'.join((server_path, 'get_agent'))
    get_agent_patcher = patch(get_agent_path, return_value=contex.storage_agent)
    contex.get_agent = get_agent_patcher.start()
    contex.addCleanup(get_agent_patcher.stop)


def mock_array_type(contex, server_path):
    detect_array_type_path = '.'.join((server_path, 'detect_array_type'))
    detect_array_type_patcher = patch(detect_array_type_path)
    contex.detect_array_type = detect_array_type_patcher.start()
    contex.detect_array_type.return_value = "a9k"
    contex.addCleanup(detect_array_type_patcher.stop)


def mock_utils_method(contex, utils_method_name, return_value=None):
    utils_method_path = '.'.join(('controllers.servers.utils', utils_method_name))
    utils_method_patcher = patch(utils_method_path)
    utils_method = utils_method_patcher.start()
    if return_value:
        utils_method.return_value = return_value
    contex.addCleanup(utils_method_patcher.stop)


def mock_mediator():
    mediator = Mock()
    mediator.maximal_volume_size_in_bytes = 10
    mediator.minimal_volume_size_in_bytes = 2
    mediator.default_object_prefix = None
    mediator.max_object_name_length = 63
    mediator.max_object_prefix_length = 20
    return mediator
