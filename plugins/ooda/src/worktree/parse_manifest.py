"""Manifest loading, CLI list parsing, and workspace-relative path normalization."""

import json
import ntpath
import posixpath
from pathlib import Path
import sys

from fleet import ID_RE, normalize_lane, parse_lanes, parse_timeout

_IGNORED_CACHE_DIRS = (".pytest_cache", ".ruff_cache", ".mypy_cache")
_TASK_SHAPE_ERROR = (
    "error: each task needs {id, prompt} (+ optional files, skills, timeout, "
    "lane, complexity, model, effort)\n"
)


def normalize_rel_path(path_str):
    """Normalize a workspace-relative path; exit 2 on root, drive, NUL, or '..' escapes."""
    raw = str(path_str).strip()
    p = raw.replace("\\", "/")
    if "\x00" in raw or p.startswith("/") or ntpath.splitdrive(p)[0]:
        sys.stderr.write(f"error: path {path_str!r} escapes the workspace\n")
        sys.exit(2)
    while p.startswith("./"):
        p = p[2:]
    norm = posixpath.normpath(p) if p else "."
    if norm.startswith("/") or norm == ".." or norm.startswith("../") or ntpath.splitdrive(norm)[0]:
        sys.stderr.write(f"error: path {path_str!r} escapes the workspace\n")
        sys.exit(2)
    return "" if norm == "." else norm


def is_ignored_worktree_artifact(rel_path):
    """Return True for router scratch dirs and Python bytecode caches ignored in scope checks."""
    if not rel_path:
        return True
    if rel_path == ".worktrees" or rel_path.startswith(".worktrees/"):
        return True
    if "__pycache__/" in f"/{rel_path}/" or rel_path.endswith((".pyc", ".pyo")):
        return True
    return any(rel_path == d or rel_path.startswith(d + "/") for d in _IGNORED_CACHE_DIRS)


def parse_csv_arg(raw_values):
    """Flatten repeatable or comma-separated CLI list arguments."""
    out = []
    for item in raw_values or []:
        for part in str(item).split(","):
            stripped = part.strip()
            if stripped:
                out.append(stripped)
    return out


def parse_files_arg(raw_values):
    """Flatten repeatable or comma-separated --files CLI arguments."""
    return parse_csv_arg(raw_values)


def _validate_task(t, seen_ids, file_owners):
    """Validate one manifest task in place, tracking ids and file ownership."""
    if not isinstance(t, dict) or not t.get("id") or not t.get("prompt"):
        sys.stderr.write(_TASK_SHAPE_ERROR)
        sys.exit(2)
    tid = t["id"]
    if not isinstance(tid, str) or not ID_RE.match(tid):
        sys.stderr.write(f"error: bad task id {tid!r} (use [A-Za-z0-9._-], no slashes/paths)\n")
        sys.exit(2)
    if tid in seen_ids:
        sys.stderr.write(f"error: duplicate task id {tid!r}\n")
        sys.exit(2)
    seen_ids.add(tid)
    if t.get("lane") is not None:
        parse_lanes(t["lane"], allow_auto=True)
    if t.get("timeout") is not None:
        parse_timeout(t["timeout"])
    for key in ("skills", "exclude_lanes", "files"):
        if t.get(key) is not None and not isinstance(t[key], list):
            sys.stderr.write(f"error: task {tid} '{key}' must be a list\n")
            sys.exit(2)
    for item in t.get("exclude_lanes") or []:
        normalize_lane(item, allow_auto=False)
    for f in t.get("files") or []:
        norm = normalize_rel_path(f)
        if not norm:
            sys.stderr.write(f"error: task {tid} file path {f!r} must point to a file or subdir\n")
            sys.exit(2)
        for prev_path, prev_tid in file_owners.items():
            if prev_tid == tid:
                continue
            overlap = (
                norm == prev_path
                or norm == "."
                or prev_path == "."
                or norm.startswith(prev_path.rstrip("/") + "/")
                or prev_path.startswith(norm.rstrip("/") + "/")
            )
            if overlap:
                sys.stderr.write(
                    f"error: overlapping file {norm!r} "
                    f"(conflicts with {prev_path!r} in task {prev_tid!r})\n"
                )
                sys.exit(2)
        file_owners[norm] = tid


def load_manifest(path):
    """Load and validate manifest JSON ([], {tasks: []}, or {subtasks: []})."""
    try:
        data = json.loads(Path(path).read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as e:
        sys.stderr.write(f"error: cannot read manifest {path}: {e}\n")
        sys.exit(2)
    if isinstance(data, dict):
        tasks = data.get("tasks") if "tasks" in data else data.get("subtasks")
    else:
        tasks = data
    if not isinstance(tasks, list) or not tasks:
        sys.stderr.write(
            "error: manifest must be a non-empty JSON list (or {tasks/subtasks: [...]})\n"
        )
        sys.exit(2)
    seen_ids = set()
    file_owners = {}
    for t in tasks:
        _validate_task(t, seen_ids, file_owners)
    return tasks
