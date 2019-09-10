import threading

def set_current_thread_name(name):
    """
    Sets current thread name if ame not None or empty string
    
    Args:
        name : name to set
    """
    if name:
        current_thread = threading.current_thread()
        current_thread.setName(name)
