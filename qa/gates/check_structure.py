"""Directory density, banned drawer vocabulary, and AST shape checks."""

import ast
from collections import defaultdict
from pathlib import Path

from .check_pages import SKIP_DIRS, iter_python_files

BANNED_NAMES = frozenset(
    {
        "util.py",
        "utils.py",
        "helper.py",
        "helpers.py",
        "common.py",
        "misc.py",
        "shared.py",
        "base.py",
        "core.py",
    }
)
MAX_FILES_PER_DIR = 8
MAX_DEFS_PER_FILE = 8
MAX_LINES_PER_DEF = 64


def check_density(root):
    """Verify every directory contains at most 8 .py files."""
    root = Path(root).resolve()
    counts = defaultdict(list)
    for path in iter_python_files(root):
        if path.suffix != ".py":
            continue
        rel_dir = path.parent.relative_to(root)
        if any(part in SKIP_DIRS for part in rel_dir.parts):
            continue
        counts[rel_dir.as_posix() or "."].append(path.name)
    violations = []
    for directory, names in sorted(counts.items()):
        if len(names) > MAX_FILES_PER_DIR:
            violations.append(
                f"{directory}: {len(names)} .py files exceeds limit {MAX_FILES_PER_DIR} ({', '.join(names)})"
            )
    return violations


def check_vocab(root):
    """Verify zero .py files use banned drawer filenames."""
    root = Path(root).resolve()
    violations = []
    for path in iter_python_files(root):
        if path.name in BANNED_NAMES:
            rel = path.relative_to(root).as_posix()
            violations.append(f"{rel}: banned drawer filename '{path.name}'")
    return violations


def _inspect_file_shape(path, rel):
    source = path.read_text(encoding="utf-8")
    try:
        tree = ast.parse(source, filename=str(path))
    except SyntaxError as exc:
        return [f"{rel}: SyntaxError at line {exc.lineno}: {exc.msg}"]
    defs = [
        node
        for node in ast.walk(tree)
        if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef))
    ]
    violations = []
    if len(defs) > MAX_DEFS_PER_FILE:
        violations.append(
            f"{rel}: {len(defs)} function defs exceeds limit {MAX_DEFS_PER_FILE}"
        )
    for fn in defs:
        span = (fn.end_lineno or fn.lineno) - fn.lineno + 1
        if span > MAX_LINES_PER_DEF:
            violations.append(
                f"{rel}:{fn.lineno}: function '{fn.name}' is {span} lines (max {MAX_LINES_PER_DEF})"
            )
    return violations


def check_shape(root):
    """Verify every Python page and bin script respects <=8 defs and <=64 lines/def."""
    root = Path(root).resolve()
    violations = []
    for path in iter_python_files(root):
        rel = path.relative_to(root).as_posix()
        violations.extend(_inspect_file_shape(path, rel))
    return violations
