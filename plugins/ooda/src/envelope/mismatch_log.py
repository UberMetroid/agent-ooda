"""Cross-model mismatch memory: pivot model families after failed/rejected OODA loops."""

import json
from pathlib import Path
import threading
import time

from fleet import atomic_write_json, hud_dir_path, try_repo_root
from worktree.parse_manifest import normalize_rel_path

from .compute_em import model_family

MISMATCH_TTL_SECS = 1800
_LOCK = threading.Lock()


def mismatch_key(cwd, files):
    """Return a stable '<repo-root>::<sorted files|*>' key for this repo/scope."""
    base = Path(cwd or ".").resolve()
    root = try_repo_root(base) or base
    norm_files = sorted(normalize_rel_path(f) for f in (files or []) if str(f).strip())
    return f"{root}::" + (",".join(norm_files) if norm_files else "*")


def _read_mismatch_map():
    """Read the mismatch map from the HUD dir, returning {} on any problem."""
    mpath = hud_dir_path() / "mismatch.json"
    if not mpath.is_file():
        return {}
    try:
        data = json.loads(mpath.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError):
        return {}
    return data if isinstance(data, dict) else {}


def _recorded_at(entry):
    """Return the entry's recorded_at epoch seconds, or None when malformed."""
    try:
        return float(entry.get("recorded_at") or 0.0)
    except (TypeError, ValueError, AttributeError):
        return None


def load_recent_mismatch(cwd, files):
    """Return recent (<30m) failed/rejected lane record for this repo/scope, or None."""
    entry = _read_mismatch_map().get(mismatch_key(cwd, files))
    if not isinstance(entry, dict):
        return None
    recorded_at = _recorded_at(entry)
    if recorded_at is None or (time.time() - recorded_at) > MISMATCH_TTL_SECS:
        return None
    if not entry.get("family"):
        return None
    return entry


def record_mismatch(cwd, files, lane, reason):
    """Record a failed/rejected loop lane so the next OODA loop pivots model families."""
    if not lane:
        return
    key = mismatch_key(cwd, files)
    with _LOCK:
        now_ts = time.time()
        cleaned = {}
        for k, v in _read_mismatch_map().items():
            stamp = _recorded_at(v) if isinstance(v, dict) else None
            if stamp is not None and (now_ts - stamp) <= MISMATCH_TTL_SECS:
                cleaned[k] = v
        cleaned[key] = {
            "lane": lane,
            "family": model_family(lane),
            "reason": reason,
            "recorded_at": round(now_ts, 2),
        }
        atomic_write_json(hud_dir_path() / "mismatch.json", cleaned, compact=True, private=True)


def clear_mismatch(cwd, files):
    """Clear stored mismatch entry after a verified green loop."""
    key = mismatch_key(cwd, files)
    with _LOCK:
        mpath = hud_dir_path() / "mismatch.json"
        if not mpath.is_file():
            return
        mapping = _read_mismatch_map()
        if key in mapping:
            mapping.pop(key, None)
            atomic_write_json(mpath, mapping, compact=True, private=True)
