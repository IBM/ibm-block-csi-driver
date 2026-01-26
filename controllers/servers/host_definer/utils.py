import controllers.servers.host_definer.settings as host_definer_settings
import controllers.common.settings as common_settings


def generate_io_group_from_labels(labels):
    io_group = ''
    for io_group_index in range(host_definer_settings.POSSIBLE_NUMBER_OF_IO_GROUP):
        label_content = labels.get(common_settings.IO_GROUP_LABEL_PREFIX + str(io_group_index))
        if label_content == host_definer_settings.TRUE_STRING:
            if io_group:
                io_group += common_settings.IO_GROUP_DELIMITER
            io_group += str(io_group_index)
    return io_group
