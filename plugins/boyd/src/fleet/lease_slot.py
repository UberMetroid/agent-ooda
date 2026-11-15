"""Cross-process parallel concurrency leases backed by fcntl.flock slot files."""

import fcntl
import os
import time

from .runtime_paths import hud_dir_path, safe_id

_POLL_SECS = 0.15


def lease_dir():
    """Return (creating with 0o700 perms) the HUD leases directory."""
    base = hud_dir_path()
    ldir = base / "leases"
    for directory in (base, ldir):
        directory.mkdir(parents=True, exist_ok=True)
        try:
            os.chmod(directory, 0o700)
        except OSError:
            pass
    return ldir


def count_active_leases(provider_name, max_parallel):
    """Return number of currently locked parallel slots for provider_name."""
    if not max_parallel or int(max_parallel) <= 0:
        return 0
    ldir = lease_dir()
    busy = 0
    for slot_idx in range(int(max_parallel)):
        lock_path = ldir / f"{safe_id(provider_name)}-{slot_idx}.lock"
        if not lock_path.exists():
            continue
        try:
            with open(lock_path, "a+", encoding="utf-8") as fh:
                try:
                    fcntl.flock(fh.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)
                    fcntl.flock(fh.fileno(), fcntl.LOCK_UN)
                except BlockingIOError:
                    busy += 1
        except OSError:
            continue
    return busy


def acquire_parallel_lease(provider_name, max_parallel, blocking=False, timeout=None):
    """Acquire an flock lease slot in 0..max_parallel-1.

    Returns the open file handle on success, None if max_parallel is unset or all
    slots are busy (when blocking=False), or False if blocking timed out.
    """
    if not max_parallel or int(max_parallel) <= 0:
        return None
    ldir = lease_dir()
    slots = int(max_parallel)
    start = time.monotonic()
    while True:
        for slot_idx in range(slots):
            lock_path = ldir / f"{safe_id(provider_name)}-{slot_idx}.lock"
            fh = open(lock_path, "a+", encoding="utf-8")
            try:
                os.chmod(lock_path, 0o600)
            except OSError:
                pass
            try:
                fcntl.flock(fh.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)
                return fh
            except BlockingIOError:
                fh.close()
            except BaseException:
                fh.close()
                raise
        if not blocking:
            return None
        if timeout is not None and (time.monotonic() - start) >= float(timeout):
            return False
        time.sleep(_POLL_SECS)


def release_parallel_lease(lease_handle):
    """Release and close an acquired parallel flock lease handle."""
    if not lease_handle:
        return
    try:
        fcntl.flock(lease_handle.fileno(), fcntl.LOCK_UN)
    except (OSError, ValueError):
        pass
    try:
        lease_handle.close()
    except OSError:
        pass
