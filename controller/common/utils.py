import threading

def set_current_thread_name(name):
    if name:
        current_thread = threading.current_thread()
        current_thread.setName(name)
