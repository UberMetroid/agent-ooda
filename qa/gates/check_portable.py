"""Portability, secret scan, stdlib-only import, and shell hygiene checks."""

import ast
from pathlib import Path
import re
import sys

from .check_pages import iter_python_files, iter_repo_files

HOME_PATH_RE = re.compile(r"/" + r"(?:home|Users)/[A-Za-z0-9._-]+")
SHELL_INLINE_RE = re.compile(r"\b(?:ba)?" + r"sh\s+" + r"-c\b")
SECRET_PATTERNS = (
    ("GitHub classic token", re.compile(r"gh" + r"p_[A-Za-z0-9]{20,}")),
    ("GitHub fine-grained PAT", re.compile(r"github" + r"_pat_[A-Za-z0-9_]{20,}")),
    ("API secret prefix", re.compile(r"\bs" + r"k-[A-Za-z0-9_-]{20,}")),
    ("Bearer token", re.compile(r"\bBea" + r"rer\s+[A-Za-z0-9._-]{16,}")),
    ("Private key block", re.compile(r"-----BEGIN " + r"(?:RSA |EC |OPENSSH )?PRIVATE KEY-----")),
)
LOCAL_PACKAGES = frozenset(
    {"fleet", "envelope", "worktree", "dispatch", "verify", "hud", "cli", "qa"}
)
STDLIB_MODULES = frozenset(getattr(sys, "stdlib_module_names", ()))


def _scan_text_file(rel, text):
    violations = []
    for idx, line in enumerate(text.splitlines(), start=1):
        if HOME_PATH_RE.search(line):
            violations.append(f"{rel}:{idx}: author-machine home path detected")
        for label, pat in SECRET_PATTERNS:
            if pat.search(line):
                violations.append(f"{rel}:{idx}: possible secret ({label})")
    return violations


def _check_imports(tree, rel):
    if not STDLIB_MODULES:
        return []
    violations = []
    for node in ast.walk(tree):
        if isinstance(node, ast.Import):
            for alias in node.names:
                root_pkg = alias.name.split(".", 1)[0]
                if root_pkg not in STDLIB_MODULES and root_pkg not in LOCAL_PACKAGES:
                    violations.append(f"{rel}:{node.lineno}: non-stdlib import '{alias.name}'")
        elif isinstance(node, ast.ImportFrom) and node.level == 0 and node.module:
            root_pkg = node.module.split(".", 1)[0]
            if root_pkg not in STDLIB_MODULES and root_pkg not in LOCAL_PACKAGES:
                violations.append(f"{rel}:{node.lineno}: non-stdlib import '{node.module}'")
    return violations


def _check_shell_hygiene(tree, rel):
    violations = []
    shell_bins = frozenset({"sh", "bash", "/bin/sh", "/bin/bash"})
    for node in ast.walk(tree):
        if isinstance(node, ast.Call):
            for kw in node.keywords:
                if kw.arg == "shell" and isinstance(kw.value, ast.Constant) and kw.value.value is True:
                    violations.append(f"{rel}:{node.lineno}: forbidden shell=True call")
        elif isinstance(node, ast.Constant) and isinstance(node.value, str):
            if SHELL_INLINE_RE.search(node.value):
                violations.append(f"{rel}:{node.lineno}: forbidden inline shell command string")
        elif isinstance(node, (ast.List, ast.Tuple)) and len(node.elts) >= 2:
            e0, e1 = node.elts[0], node.elts[1]
            if isinstance(e0, ast.Constant) and isinstance(e1, ast.Constant):
                if e0.value in shell_bins and e1.value == "-c":
                    violations.append(f"{rel}:{node.lineno}: forbidden shell -c argv invocation")
    return violations


def check_portable(root):
    """Enforce zero author paths, zero secrets, stdlib-only imports, and argv hygiene."""
    root = Path(root).resolve()
    violations = []
    for path in iter_repo_files(root):
        rel = path.relative_to(root).as_posix()
        try:
            text = path.read_text(encoding="utf-8")
        except UnicodeDecodeError:
            continue
        violations.extend(_scan_text_file(rel, text))

    for path in iter_python_files(root):
        rel = path.relative_to(root).as_posix()
        source = path.read_text(encoding="utf-8")
        try:
            tree = ast.parse(source, filename=str(path))
        except SyntaxError as exc:
            violations.append(f"{rel}:{exc.lineno}: SyntaxError: {exc.msg}")
            continue
        violations.extend(_check_imports(tree, rel))
        violations.extend(_check_shell_hygiene(tree, rel))
    return violations
