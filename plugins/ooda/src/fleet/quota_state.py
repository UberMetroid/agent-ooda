"""quota.json schema migration, cycle expiry, atomic persistence, and locked loading."""

from datetime import datetime, timedelta, timezone
import json
import sys

from .lane_catalog import (
    DEFAULT_POOL,
    DEVIN_FAST_MAX_PARALLEL,
    MCODE_MAX_PARALLEL,
    PROVIDER_ORDER,
    QUOTA_LOCK,
    QUOTA_SCHEMA_VERSION,
    STRIPE_POOL,
    default_providers,
    default_quota_data,
)
from .locate_cli import format_local_iso, now_iso, parse_iso_utc
from .runtime_paths import atomic_write_json, plugin_root_path, quota_file_path

_CYCLE_RESET = {
    "cycle_runs": 0,
    "cycle_tokens": 0,
    "cycle_tokens_exact": False,
    "cycle_cost_usd": 0.0,
    "cycle_started_at": None,
    "next_reset_at": None,
}
_WAKE = {"status": "active", "cooldown_until": None, "reason": None}


def _migrate_pool(data, key, default_list, old_version):
    """Drop legacy 'cursor' and reset stale/empty pools to the current default list."""
    changed = False
    pool = data.get(key) or []
    if "cursor" in pool:
        pool = [p for p in pool if p != "cursor"]
        data[key] = pool
        changed = True
    if old_version < QUOTA_SCHEMA_VERSION or not pool:
        data[key] = list(default_list)
        changed = True
    return changed


def _migrate_provider(name, info, tmpl, old_version, legacy_updated):
    """Backfill missing keys, apply legacy upgrades, and stamp stranded cooldown wakes."""
    changed = False
    for key, default_val in tmpl.items():
        if key not in info:
            info[key] = default_val
            changed = True
    if old_version < QUOTA_SCHEMA_VERSION:
        upgrades = (
            (name == "mcode" and info.get("tier") == "unlimited", {"tier": "high-cap"}),
            (name == "muse" and info.get("tier") == "unlimited", {"tier": "capped"}),
            (name == "devin-fast" and info.get("max_parallel") is None,
             {"max_parallel": DEVIN_FAST_MAX_PARALLEL}),
            (name == "mcode" and info.get("max_parallel") is None,
             {"max_parallel": MCODE_MAX_PARALLEL, "role": tmpl["role"]}),
            (info.get("effort") is None and tmpl.get("effort") is not None,
             {"effort": tmpl.get("effort")}),
            (info.get("cycle_hours") is None and tmpl.get("cycle_hours") is not None,
             {"cycle_hours": tmpl.get("cycle_hours")}),
        )
        for applies, patch in upgrades:
            if applies:
                info.update(patch)
                changed = True
    if (
        info.get("status") == "cooldown"
        and info.get("cooldown_until") is None
        and info.get("cycle_hours") is not None
        and not info.get("manual_disable", False)
    ):
        base_dt = legacy_updated or datetime.now(timezone.utc)
        wake_iso = format_local_iso(base_dt + timedelta(hours=float(info["cycle_hours"])))
        info["cooldown_until"] = wake_iso
        if not info.get("next_reset_at"):
            info["next_reset_at"] = wake_iso
        if not info.get("cycle_started_at"):
            info["cycle_started_at"] = format_local_iso(base_dt)
        changed = True
    return changed


def migrate_quota_v3(data):
    """Upgrade legacy quota.json in-place to the current schema; return changed flag."""
    changed = False
    try:
        old_version = int(data.get("version") or 1)
    except (TypeError, ValueError):
        old_version = 1
    providers = data.setdefault("providers", {})
    if "cursor" in providers:
        providers.pop("cursor", None)
        changed = True
    if old_version < QUOTA_SCHEMA_VERSION:
        data["version"] = QUOTA_SCHEMA_VERSION
        changed = True
    if _migrate_pool(data, "default_fallback_chain", DEFAULT_POOL, old_version):
        changed = True
    if _migrate_pool(data, "parallel_stripe_pool", STRIPE_POOL, old_version):
        changed = True
    legacy_updated = parse_iso_utc(data.get("updated_at"))
    for name, tmpl in default_providers().items():
        if name not in providers:
            providers[name] = dict(tmpl)
            changed = True
        elif _migrate_provider(name, providers[name], tmpl, old_version, legacy_updated):
            changed = True
    ordered = {name: providers[name] for name in PROVIDER_ORDER if name in providers}
    for name, info in providers.items():
        ordered.setdefault(name, info)
    if list(providers.keys()) != list(ordered.keys()):
        data["providers"] = ordered
        changed = True
    return changed


def expire_and_reset_cycles(data):
    """Auto-reset lapsed cycle windows and wake expired non-manual cooldowns."""
    changed = False
    now_utc = datetime.now(timezone.utc)
    for info in (data.get("providers") or {}).values():
        reset_dt = parse_iso_utc(info.get("next_reset_at"))
        if reset_dt is not None and reset_dt <= now_utc:
            info.update(_CYCLE_RESET)
            if info.get("status") == "cooldown" and not info.get("manual_disable", False):
                info.update(_WAKE)
            changed = True
        if info.get("status") == "cooldown" and info.get("cooldown_until"):
            until = parse_iso_utc(info["cooldown_until"])
            if until is not None and until <= now_utc:
                info.update(_WAKE)
                info["manual_disable"] = False
                changed = True
    return changed


def save_quota_unlocked(data, quota_path=None):
    """Atomically write quota.json; caller must hold QUOTA_LOCK."""
    data["updated_at"] = now_iso()
    return atomic_write_json(quota_file_path(quota_path), data)


def read_and_prep_unlocked(path):
    """Read (or seed) quota data, then migrate and expire cycles; caller holds QUOTA_LOCK."""
    path = quota_file_path(path)
    if path.is_file():
        try:
            data = json.loads(path.read_text(encoding="utf-8"))
        except (OSError, json.JSONDecodeError) as exc:
            sys.stderr.write(f"error: cannot read quota file {path}: {exc}\n")
            sys.exit(2)
        if not isinstance(data, dict):
            sys.stderr.write(f"error: quota file {path} must contain a JSON object\n")
            sys.exit(2)
    else:
        seed = plugin_root_path() / "config" / "quota.default.json"
        data = None
        if seed.is_file():
            try:
                data = json.loads(seed.read_text(encoding="utf-8"))
            except (OSError, json.JSONDecodeError):
                data = None
        if not isinstance(data, dict):
            data = default_quota_data()
    migrate_quota_v3(data)
    expire_and_reset_cycles(data)
    return data


import contextlib
import fcntl
import os


@contextlib.contextmanager
def quota_lock(quota_path=None):
    """Acquire thread lock + cross-process flock on quota.lock around quota.json I/O."""
    path = quota_file_path(quota_path)
    lock_path = path.with_suffix(".lock")
    lock_path.parent.mkdir(parents=True, exist_ok=True)
    with QUOTA_LOCK:
        fd = os.open(str(lock_path), os.O_CREAT | os.O_RDWR, 0o600)
        with os.fdopen(fd, "a+", encoding="utf-8") as lock_file:
            fcntl.flock(lock_file.fileno(), fcntl.LOCK_EX)
            try:
                yield path
            finally:
                fcntl.flock(lock_file.fileno(), fcntl.LOCK_UN)


def load_quota(quota_path=None):
    """Load quota.json (seeding if absent), migrate, expire lapsed cycles, and persist."""
    path = quota_file_path(quota_path)
    with quota_lock(path):
        if not path.is_file():
            data = read_and_prep_unlocked(path)
            save_quota_unlocked(data, path)
            return data
        try:
            data = json.loads(path.read_text(encoding="utf-8"))
        except (OSError, json.JSONDecodeError) as exc:
            sys.stderr.write(f"error: cannot read quota file {path}: {exc}\n")
            sys.exit(2)
        changed = migrate_quota_v3(data)
        if expire_and_reset_cycles(data):
            changed = True
        if changed:
            save_quota_unlocked(data, path)
        return data
