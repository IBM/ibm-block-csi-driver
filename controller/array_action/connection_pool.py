from eventlet import pools
from controller.common.csi_logger import get_stdout_logger
from queue import Empty

logger = get_stdout_logger()


class ConnectionPool(pools.Pool):
    """A simple eventlet pool to hold connections."""

    def __init__(self, endpoints, username, password, med_class, min_size, max_size):
        self.endpoints = endpoints
        self.username = username
        self.password = password
        self.med_class = med_class

        super(ConnectionPool, self).__init__(min_size, max_size)

    def __del__(self):
        if not self.current_size:
            return
        # change the size of the pool to reduce the number
        # of elements on the pool via puts.
        self.resize(1)
        # release all but the last connection using
        # get and put to allow any get waiters to complete.
        while self.waiting() or self.current_size > 1:
            conn = self.get()
            self.put(conn)
        # Now free everything that is left
        while self.free_items:
            self.free_items.popleft().disconnect()
            self.current_size -= 1

    def create(self):  # pylint: disable=method-hidden
        try:
            return self.med_class(self.username, self.password, self.endpoints)
        except Exception:
            raise

    def get(self, block=True, timeout=None):
        """
        Return an item from the pool, when one is available.

        This may cause the calling greenthread to block. Check if a connection is
        active before returning it. For dead connections, create and return a new connection.

        If optional args *block* is true and *timeout* is ``None`` (the default),
        block if necessary until an item is available. If *timeout* is a positive number,
        it blocks at most *timeout* seconds and raises the :class:`Empty` exception
        if no item was available within that time. Otherwise (*block* is false), return
        an item if one is immediately available, else raise the :class:`Empty` exception
        (*timeout* is ignored in that case).
        """

        while self.free_items:
            mediator = self.free_items.popleft()
            if mediator.is_active():
                return mediator
            else:
                try:
                    mediator.disconnect()
                except Exception as ex:
                    # failed to disconnect the mediator, delete the stale client.
                    logger.error("Failed to disconnect the array mediator, reason is {}".format(ex))
                    del mediator
                self.current_size -= 1

        self.current_size += 1
        if self.current_size <= self.max_size:
            try:
                created = self.create()
            except Exception:
                self.current_size -= 1
                raise
            return created
        self.current_size -= 1  # did not create
        return self.channel.get(block, timeout)

    def put(self, mediator):
        # If we have more connections then we should just disconnect it
        if self.current_size > self.max_size:
            mediator.disconnect()
            self.current_size -= 1
            return
        super(ConnectionPool, self).put(mediator)

    def remove(self, mediator):
        """Close an mediator client and remove it from free_items."""
        mediator.disconnect()
        if mediator in self.free_items:
            self.free_items.remove(mediator)
            if self.current_size > 0:
                self.current_size -= 1
