"""Page-rule enforcement and file discovery helpers for agy-skills."""

import ast
from pathlib import Path

SKIP_DIRS = frozenset({".git", ".worktrees", "__pycache__"})
MIN_PAGE_LINES = 16
MAX_PAGE_LINES = 256
SHIM_ASSIGN_TARGETS = frozenset({"__all__"})


def _is_skipped(rel_parts):
    return any(part in SKIP_DIRS for part in rel_parts)


def iter_repo_files(root):
    """Yield sorted repository file Paths, skipping transient and VCS dirs."""
    root = Path(root).resolve()
    files = []
    for path in root.rglob("*"):
        if not path.is_file():
            continue
        rel = path.relative_to(root)
        if _is_skipped(rel.parts):
            continue
        files.append(path)
    return sorted(files)


def iter_python_files(root):
    """Return sorted Paths for all .py modules and plugins/boyd/bin entrypoints."""
    root = Path(root).resolve()
    bin_dir = root / "plugins" / "boyd" / "bin"
    matched = []
    for path in iter_repo_files(root):
        if path.suffix == ".py" or path.parent == bin_dir:
            matched.append(path)
    return matched


def is_pure_init_shim(path, source):
    """Return True when path is an __init__.py containing only imports and __all__."""
    if path.name != "__init__.py":
        return False
    try:
        tree = ast.parse(source, filename=str(path))
    except SyntaxError:
        return False
    body = list(tree.body)
    if body and isinstance(body[0], ast.Expr) and isinstance(body[0].value, ast.Constant):
        if isinstance(body[0].value.value, str):
            body = body[1:]
    for node in body:
        if isinstance(node, (ast.Import, ast.ImportFrom)):
            continue
        if isinstance(node, ast.Assign) and all(
            isinstance(t, ast.Name) and t.id in SHIM_ASSIGN_TARGETS for t in node.targets
        ):
            continue
        return False
    return True


def check_line_cap(root):
    """Verify every Python page and bin entrypoint obeys the 16..256 line rule."""
    root = Path(root).resolve()
    violations = []
    for path in iter_python_files(root):
        rel = path.relative_to(root).as_posix()
        source = path.read_text(encoding="utf-8")
        line_count = len(source.splitlines())
        if line_count > MAX_PAGE_LINES:
            violations.append(f"{rel}: {line_count} lines exceeds cap {MAX_PAGE_LINES}")
        elif line_count < MIN_PAGE_LINES and not is_pure_init_shim(path, source):
            violations.append(
                f"{rel}: {line_count} lines below floor {MIN_PAGE_LINES} (not a pure __init__.py shim)"
            )
    return violations
