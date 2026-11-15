"""Environment-aware runtime paths and low-level persistence primitives.

All mutable state lives outside git: quota under ``~/.local/state/boyd`` and
HUD / lease / slot markers under ``/tmp``. Every location can be overridden
through environment variables at call time (never cached at import).
"""

import json
import os
from pathlib import Path
import re
import threading

_SAFE_ID_RE = re.compile(r"[^A-Za-z0-9._-]")


def plugin_root_path():
    """Return the boyd plugin root directory (``plugins/boyd``)."""
    return Path(__file__).resolve().parents[2]


def quota_file_path(quota_path=None):
    """Resolve quota.json: explicit override, AGENT_/OODA_QUOTA_FILE, else XDG-ish state."""
    if quota_path:
        return Path(quota_path)
    env_path = os.environ.get("AGENT_QUOTA_FILE") or os.environ.get("OODA_QUOTA_FILE")
    if env_path:
        return Path(env_path)
    return Path.home() / ".local" / "state" / "boyd" / "quota.json"


def hud_dir_path():
    """Return the live HUD marker directory (ROUTER_HUD_DIR or /tmp/router-hud)."""
    return Path(os.environ.get("ROUTER_HUD_DIR") or "/tmp/router-hud")


def slots_root_path():
    """Return the worktree slot root directory (ROUTER_SLOTS_DIR or /tmp/router-slots)."""
    return Path(os.environ.get("ROUTER_SLOTS_DIR") or "/tmp/router-slots")


def safe_id(val):
    """Return a filesystem-safe identifier, defaulting to 'default' when empty."""
    cleaned = _SAFE_ID_RE.sub("_", str(val or "").strip())
    return cleaned or "default"


def router_depth():
    """Return nested router dispatch depth from ROUTER_DEPTH (1 when unparsable)."""
    raw = (os.environ.get("ROUTER_DEPTH") or "0").strip() or "0"
    try:
        return max(0, int(raw))
    except ValueError:
        return 1


def atomic_write_json(path, payload, compact=False, private=False):
    """Atomically write payload as JSON via a sibling temp file and os.replace."""
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    if private:
        try:
            os.chmod(path.parent, 0o700)
        except OSError:
            pass
    if compact:
        text = json.dumps(payload, separators=(",", ":"))
    else:
        text = json.dumps(payload, indent=2) + "\n"
    tmp = path.with_name(f".{path.name}.tmp.{os.getpid()}.{threading.get_ident()}")
    fd = os.open(str(tmp), os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600 if private else 0o644)
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as fh:
            fh.write(text)
        if private:
            os.chmod(tmp, 0o600)
        os.replace(tmp, path)
    except BaseException:
        try:
            tmp.unlink()
        except OSError:
            pass
        raise
    return path
