"""Wall-clock formatting, argv-only subprocess runner, repo discovery, and CLI lookup."""

from datetime import datetime, timezone
import os
from pathlib import Path
import subprocess
import sys

from .lane_catalog import CLI_FALLBACK_PATHS

_SHIM_MARKER = ".superconductor"


def now_iso():
    """Return local timezone-aware ISO-8601 timestamp string."""
    return datetime.now().astimezone().isoformat(timespec="seconds")


def format_local_iso(dt_obj):
    """Format aware datetime in local timezone with second precision."""
    return dt_obj.astimezone().isoformat(timespec="seconds")


def parse_iso_utc(ts):
    """Parse an ISO-8601 timestamp into an aware UTC datetime, or None."""
    if not ts or not isinstance(ts, str):
        return None
    s = ts.strip()
    if s.endswith("Z"):
        s = s[:-1] + "+00:00"
    try:
        dt = datetime.fromisoformat(s)
    except ValueError:
        return None
    if dt.tzinfo is None:
        dt = dt.replace(tzinfo=datetime.now().astimezone().tzinfo)
    return dt.astimezone(timezone.utc)


def format_countdown(target_ts, now_utc=None):
    """Render concise human-readable duration until target_ts (or 'due now')."""
    target_dt = parse_iso_utc(target_ts)
    if target_dt is None:
        return None
    now_dt = now_utc or datetime.now(timezone.utc)
    secs = int((target_dt - now_dt).total_seconds())
    if secs <= 0:
        return "due now"
    hours, rem = divmod(secs, 3600)
    mins = rem // 60
    if hours >= 24:
        days, rem_h = divmod(hours, 24)
        return f"{days}d {rem_h}h"
    if hours > 0:
        return f"{hours}h {mins:02d}m"
    if mins > 0:
        return f"{mins}m"
    return f"{secs}s"


def run(cmd, cwd=None, env=None, timeout=None):
    """Run argv list cmd and return CompletedProcess (stderr merged) without raising."""
    return subprocess.run(
        list(cmd),
        cwd=cwd,
        env=env,
        timeout=timeout,
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        text=True,
    )


def try_repo_root(start_dir=None):
    """Return enclosing git repository root Path, or None if not inside a git repo."""
    try:
        p = run(["git", "rev-parse", "--show-toplevel"], cwd=start_dir, timeout=15)
    except (OSError, subprocess.TimeoutExpired):
        return None
    if p.returncode != 0:
        return None
    return Path(p.stdout.strip())


def repo_root(start_dir=None):
    """Resolve the enclosing git repository root, or exit 2."""
    root = try_repo_root(start_dir)
    if root is None:
        sys.stderr.write("error: not inside a git repository\n")
        sys.exit(2)
    return root


def resolve_cli(cli_name):
    """Find an executable for cli_name on PATH or fallbacks, skipping .superconductor shims."""
    candidates = []
    for d in os.environ.get("PATH", "").split(os.pathsep):
        if not d:
            continue
        d_path = Path(d).expanduser()
        if _SHIM_MARKER in d_path.parts:
            continue
        candidates.append(d_path / cli_name)
    candidates.extend(Path(c) for c in CLI_FALLBACK_PATHS.get(cli_name, []))
    for cand in candidates:
        if not (cand.is_file() and os.access(cand, os.X_OK)):
            continue
        raw_parts = cand.expanduser().parts
        resolved_parts = cand.expanduser().resolve(strict=False).parts
        if _SHIM_MARKER in raw_parts or _SHIM_MARKER in resolved_parts:
            continue
        return str(cand)
    return None
