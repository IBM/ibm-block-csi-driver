from queue import Queue, Full, Empty
from threading import RLock
from controller.common.csi_logger import get_stdout_logger

logger = get_stdout_logger()


class ConnectionPool(object):
    """A simple pool to hold connections."""

    def __init__(self, endpoints, username, password, med_class, min_size, max_size):
        self.endpoints = endpoints
        self.username = username
        self.password = password
        self.med_class = med_class

        self.current_size = 0
        self.max_size = max_size
        self.channel = Queue(max_size)
        self.lock = RLock()

        for x in range(min_size):
            self.put(self.get())

    def __del__(self):
        # delete the free clients in queue, and wait for outside ones.
        while self.current_size:
            item = self.get()
            item.disconnect()
            with self.lock:
                self.current_size -= 1

    def create(self):
        try:
            logger.debug("Creating a new connection for endpoint {}".format(", ".join(self.endpoints)))
            return self.med_class(self.username, self.password, self.endpoints)
        except Exception:
            raise

    def get(self, block=True, timeout=None):
        """
        Return an item from the pool, when one is available.

        This may cause the calling thread to block. Check if a connection is
        active before returning it. For dead connections, create and return a new connection.

        If optional args *block* is true and *timeout* is ``None`` (the default),
        block if necessary until an item is available. If *timeout* is a positive number,
        it blocks at most *timeout* seconds and raises the :class:`Empty` exception
        if no item was available within that time. Otherwise (*block* is false), return
        an item if one is immediately available, else raise the :class:`Empty` exception
        (*timeout* is ignored in that case).
        """

        # if there is a free and active item in the channel, return it directly.
        while True:
            try:
                item = self.channel.get(block=False)
                if item.is_active():
                    return item
                else:
                    with self.lock:
                        self.current_size -= 1
                    try:
                        logger.debug("The connection for storage {} is inactive, close it".format(item.identifier))
                        item.disconnect()
                    except Exception as ex:
                        # failed to disconnect the mediator, delete the stale client.
                        logger.error(
                            "Failed to disconnect the connection for storage {} before use, "
                            "reason is {}".format(item.identifier, ex)
                        )
                        del item
            except Empty:
                break

        # If there is no free items, and current_size is not full, create a new item.
        if self.current_size < self.max_size:
            created = self.create()
            with self.lock:
                self.current_size += 1
            return created

        # If current_size is full, waiting for an available one.
        return self.channel.get(block, timeout)

    def put(self, item):
        """
        Put an item back into the pool, when done.  This may cause the putting thread to block.
        """
        if self.current_size > self.max_size:
            with self.lock:
                self.current_size -= 1
            discard = True
        else:
            try:
                self.channel.put(item, block=False)
                return
            except Full:
                discard = True

        if discard:
            try:
                item.disconnect()
            except Exception as ex:
                # failed to disconnect the mediator, delete the stale client.
                logger.error(
                    "Failed to disconnect the connection for storage {} after use, "
                    "reason is {}".format(item.identifier, ex)
                )
                del item
