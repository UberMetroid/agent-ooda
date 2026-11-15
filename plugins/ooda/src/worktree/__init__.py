"""Worktree isolation: manifest parsing, skill loading, and per-task git worktrees."""

from .parse_manifest import (
    is_ignored_worktree_artifact,
    load_manifest,
    normalize_rel_path,
    parse_csv_arg,
    parse_files_arg,
)
from .load_skills import load_skill_blocks
from .isolate_tree import (
    changed_files_in,
    diff_stat,
    ensure_slot,
    ensure_worktree,
    find_scope_violations,
    safe_worktree_files,
)

__all__ = [
    "is_ignored_worktree_artifact",
    "load_manifest",
    "normalize_rel_path",
    "parse_csv_arg",
    "parse_files_arg",
    "load_skill_blocks",
    "changed_files_in",
    "diff_stat",
    "ensure_slot",
    "ensure_worktree",
    "find_scope_violations",
    "safe_worktree_files",
]
