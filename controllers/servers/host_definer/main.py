import atexit
import os
import signal
import tracemalloc

from controllers.common.csi_logger import get_stdout_logger
from controllers.servers.host_definer.host_definer_manager import HostDefinerManager

logger = get_stdout_logger()

_DEBUG_MEMORY = os.getenv('HD_DEBUG_MEMORY', '').lower() == 'true'
_SNAPSHOT_TOP_N = int(os.getenv('HD_DEBUG_MEMORY_TOP_N', '30'))

# Keep a module-level reference so signal handlers can reach it.
_tracemalloc_started = False


def _dump_memory_snapshot(label='on-exit'):
    """Log a tracemalloc top-N snapshot to stdout.

    Safe to call from a signal handler (uses the logger, which is thread-safe
    for stream handlers) and from atexit.
    """
    if not _tracemalloc_started:
        return
    snapshot = tracemalloc.take_snapshot()
    stats = snapshot.statistics('lineno')

    lines = [
        '=== HD_DEBUG_MEMORY snapshot ({}) — top {} allocations by size ==='.format(
            label, _SNAPSHOT_TOP_N),
    ]
    total_bytes = sum(s.size for s in stats)
    lines.append('  Total tracked: {:.1f} MiB'.format(total_bytes / 1024 / 1024))
    lines.append('')
    for rank, stat in enumerate(stats[:_SNAPSHOT_TOP_N], start=1):
        lines.append('  #{:>3}  {:>10.1f} KiB  count={:<6}  {}'.format(
            rank,
            stat.size / 1024,
            stat.count,
            stat.traceback.format()[0] if stat.traceback else '(no traceback)',
        ))
    lines.append('=== end snapshot ===')
    logger.warning('\n'.join(lines))


def _on_signal(signum, _frame):
    sig_name = signal.Signals(signum).name
    logger.warning('HD_DEBUG_MEMORY: received signal %s — dumping memory snapshot', sig_name)
    _dump_memory_snapshot(label='signal-{}'.format(sig_name))


def _setup_debug_memory():
    global _tracemalloc_started

    nframes = int(os.getenv('HD_DEBUG_MEMORY_NFRAMES', '5'))
    tracemalloc.start(nframes)
    _tracemalloc_started = True
    logger.warning(
        'HD_DEBUG_MEMORY enabled: tracemalloc started with %d frames, top-%d on exit. '
        'Send SIGUSR1 or SIGUSR2 to dump a snapshot at any time.',
        nframes, _SNAPSHOT_TOP_N,
    )

    # Dump on clean shutdown (KeyboardInterrupt, normal exit, etc.)
    atexit.register(_dump_memory_snapshot, 'atexit')

    # Dump on SIGUSR1 / SIGUSR2 — send from outside:
    #   kubectl exec <pod> -- kill -USR1 1
    for sig in (signal.SIGUSR1, signal.SIGUSR2):
        try:
            signal.signal(sig, _on_signal)
        except OSError:
            pass  # Not all platforms support every signal.

    # SIGTERM is what Kubernetes sends before OOM-kill; dump before we go down.
    original_sigterm = signal.getsignal(signal.SIGTERM)

    def _on_sigterm(signum, frame):
        _dump_memory_snapshot(label='SIGTERM')
        if callable(original_sigterm):
            original_sigterm(signum, frame)

    signal.signal(signal.SIGTERM, _on_sigterm)


def main():
    if _DEBUG_MEMORY:
        _setup_debug_memory()

    host_definition_manager = HostDefinerManager()
    host_definition_manager.start_host_definition()


if __name__ == '__main__':
    main()
