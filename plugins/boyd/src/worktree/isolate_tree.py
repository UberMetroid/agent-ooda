"""Per-task git worktree isolation, change detection, and scope enforcement."""

import os
import shutil
import sys

from fleet import run, slots_root_path

from .parse_manifest import is_ignored_worktree_artifact, normalize_rel_path

_GIT_TIMEOUT = 120


def ensure_slot(slot_key):
    """Create/refresh an isolated runtime slot dir for prompt/export scratch files."""
    slot = slots_root_path() / f"slot-{slot_key}"
    slot.mkdir(parents=True, exist_ok=True)
    os.chmod(slot, 0o700)
    return slot


def ensure_worktree(root, task_id):
    """Create .worktrees/<id> on branch ooda/<id>; returns (path, branch)."""
    wt_dir = root / ".worktrees"
    wt_dir.mkdir(exist_ok=True)
    path = wt_dir / task_id
    branch = f"ooda/{task_id}"
    if path.exists():
        run(["git", "worktree", "remove", "--force", str(path)], cwd=root, timeout=_GIT_TIMEOUT)
        shutil.rmtree(path, ignore_errors=True)
    exists = run(
        ["git", "rev-parse", "--verify", "--quiet", f"refs/heads/{branch}"],
        cwd=root,
        timeout=_GIT_TIMEOUT,
    ).returncode == 0
    if exists:
        argv = ["git", "worktree", "add", str(path), branch]
    else:
        argv = ["git", "worktree", "add", "-b", branch, str(path)]
    p = run(argv, cwd=root, timeout=_GIT_TIMEOUT)
    if p.returncode != 0:
        sys.stderr.write(f"error: worktree add failed for {task_id}:\n{p.stdout}\n")
        sys.exit(2)
    return path, branch


def changed_files_in(worktree):
    """Return sorted relative paths of modified, staged, or untracked files."""
    files = set()
    queries = (
        ["git", "diff", "--name-only", "HEAD", "--", "."],
        ["git", "ls-files", "--others", "--exclude-standard", "--", "."],
    )
    for argv in queries:
        p = run(argv, cwd=worktree, timeout=_GIT_TIMEOUT)
        if p.returncode != 0:
            continue
        for line in p.stdout.splitlines():
            if line.strip():
                rel = normalize_rel_path(line)
                if not is_ignored_worktree_artifact(rel):
                    files.add(rel)
    return sorted(files)


def find_scope_violations(changed_files, declared_files):
    """Return changed files outside the task's declared files list."""
    if not declared_files:
        return []
    allowed = [normalize_rel_path(f) for f in declared_files]
    return [
        cf
        for cf in changed_files
        if not any(cf == df or cf.startswith(df.rstrip("/") + "/") for df in allowed)
    ]


def diff_stat(worktree):
    """Return compact diff stat and untracked/modified short status."""
    p = run(["git", "diff", "--stat", "HEAD", "--", "."], cwd=worktree, timeout=_GIT_TIMEOUT)
    stat = p.stdout.strip() if p.returncode == 0 else f"<diff failed: {p.stdout.strip()}>"
    s = run(["git", "status", "--short", "--", "."], cwd=worktree, timeout=_GIT_TIMEOUT)
    if s.returncode == 0 and s.stdout.strip():
        stat += "\n-- untracked/modified --\n" + s.stdout.strip()
    return stat if stat.strip() else "(clean)"


def safe_worktree_files(files, worktree):
    """Resolve existing files inside worktree without escaping worktree root."""
    out = []
    wt_resolved = worktree.resolve()
    for f in files or []:
        cand = (worktree / f).resolve()
        try:
            cand.relative_to(wt_resolved)
        except ValueError:
            continue
        if (worktree / f).exists():
            out.append(str(worktree / f))
    return out
