import threading

from controller.controller_server.errors import VolumeAlreadyProcessingError


def set_current_thread_name(name):
    """
    Sets current thread name if ame not None or empty string

    Args:
        name : name to set
    """
    current_thread = threading.current_thread()
    key_lock = threading.Lock()
    key_lock.acquire()
    for thread in threading.enumerate():
        if thread is not current_thread:
            if name == thread.getName():
                raise VolumeAlreadyProcessingError(name)
    if name:
        current_thread.setName(name)
    key_lock.release()


def string_to_array(str_val, separator):
    """
    Args
        str_val : string value
        separator : string separator
    Return
        List as splitted string by separator after stripping whitespaces from each element
    """
    if not str_val:
        return []
    str_val = str_val.strip()
    res = str_val.split(separator)
    return [res_val.strip() for res_val in res]
