"""Gather git status, diff stat, and patch text (incl. untracked files) for M3.1 review."""

import os
from pathlib import Path
import subprocess

from fleet import run
from worktree import is_ignored_worktree_artifact, normalize_rel_path

MAX_DIFF_CHARS = 60000
GIT_TIMEOUT = 60
SNIFF_BYTES = 8192
MAX_UNTRACKED_BYTES = 65536


def _git(target_dir, *args):
    """Run a bounded argv-only git command; return (returncode, stdout) without raising."""
    try:
        proc = run(["git", *args], cwd=target_dir, timeout=GIT_TIMEOUT)
    except (OSError, subprocess.TimeoutExpired):
        return 1, ""
    # fleet.run merges stderr into stdout: drop output on failure so git errors never leak into diffs.
    return proc.returncode, (proc.stdout or "") if proc.returncode == 0 else ""


def _synthetic_file_diff(target_dir, rel):
    """Render one untracked file as a synthetic new-file unified diff (or a size note)."""
    path = Path(target_dir) / rel
    head = f"diff --git a/{rel} b/{rel}\n"
    if path.is_symlink():
        try:
            dest = os.readlink(path)
        except OSError:
            dest = "?"
        return head + f"new file mode 120000\n<untracked symlink -> {dest}>\n"
    head += "new file mode 100644\n"
    try:
        size = path.stat().st_size
        with open(path, "rb") as fh:
            raw = fh.read(MAX_UNTRACKED_BYTES + 1)
    except OSError as exc:
        return head + f"<untracked file unreadable: {exc.strerror or exc}>\n"
    if b"\0" in raw[:SNIFF_BYTES]:
        return head + f"Binary files /dev/null and b/{rel} differ ({size} bytes)\n"
    if len(raw) > MAX_UNTRACKED_BYTES:
        return head + f"<untracked text file too large to inline: {size} bytes; inspect in cwd>\n"
    try:
        text = raw.decode("utf-8")
    except UnicodeDecodeError:
        return head + f"<untracked non-UTF-8 file: {size} bytes; inspect in cwd>\n"
    if not text:
        return head
    lines = text.split("\n")
    if text.endswith("\n"):
        lines = lines[:-1]
    body = "".join(f"+{line}\n" for line in lines)
    no_eol = "" if text.endswith("\n") else "\\ No newline at end of file\n"
    hunk = f"--- /dev/null\n+++ b/{rel}\n@@ -0,0 +1,{len(lines)} @@\n"
    return head + hunk + body + no_eol


def _untracked_diff(target_dir, budget):
    """Return synthetic diffs for non-ignored untracked files, stopping once budget is spent."""
    code, out = _git(target_dir, "ls-files", "--others", "--exclude-standard", "-z", "--", ".")
    if code != 0:
        return ""
    chunks = []
    used = 0
    skipped = 0
    for rel in (r for r in out.split("\0") if r):
        if is_ignored_worktree_artifact(normalize_rel_path(rel)):
            continue
        if used >= budget:
            skipped += 1
            continue
        chunk = _synthetic_file_diff(target_dir, rel)
        chunks.append(chunk)
        used += len(chunk)
    if skipped:
        chunks.append(f"... <{skipped} more untracked file(s) omitted; inspect files in cwd> ...\n")
    return "".join(chunks)


def collect_diff_context(target_dir, include_last_commit_fallback=False):
    """Gather git status and diff text (tracked + untracked) for M3.1 code review."""
    status_out = _git(target_dir, "status", "--short", "--", ".")[1].strip()
    diff_stat_out = _git(target_dir, "diff", "--stat", "HEAD", "--", ".")[1].strip()
    diff_out = _git(target_dir, "diff", "HEAD", "--", ".")[1]
    source = "working-tree"
    if status_out:
        budget = MAX_DIFF_CHARS - len(diff_out)
        diff_out += _untracked_diff(target_dir, budget)

    if not status_out and not diff_out.strip() and include_last_commit_fallback:
        if _git(target_dir, "rev-parse", "--verify", "--quiet", "HEAD~1")[0] == 0:
            diff_stat_out = _git(target_dir, "diff", "--stat", "HEAD~1..HEAD")[1].strip()
            diff_out = _git(target_dir, "diff", "HEAD~1..HEAD")[1]
            source = "HEAD~1..HEAD"
        elif _git(target_dir, "rev-parse", "--verify", "--quiet", "HEAD")[0] == 0:
            diff_stat_out = _git(target_dir, "show", "--stat", "HEAD")[1].strip()
            diff_out = _git(target_dir, "show", "HEAD")[1]
            source = "HEAD"

    truncated = len(diff_out) > MAX_DIFF_CHARS
    clipped_diff = diff_out[:MAX_DIFF_CHARS] + (
        "\n... <diff truncated; inspect files in cwd> ...\n" if truncated else ""
    )
    return {
        "source": source,
        "status": status_out or "(clean)",
        "diff_stat": diff_stat_out or "(no tracked diff)",
        "diff": clipped_diff,
        "has_changes": bool(status_out or diff_out.strip()),
    }
