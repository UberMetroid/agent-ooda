"""Resolve and load SKILL.md guidance blocks across workspace, plugin, and user roots."""

from pathlib import Path
import sys

from fleet import ID_RE, plugin_root_path, try_repo_root

_SKILL_ALIASES = {"agent-router": "router"}


def _skill_roots(cwd=None):
    """Return candidate skill directories in priority order (deduplicated)."""
    base = Path(cwd) if cwd else Path.cwd()
    workspace = try_repo_root(str(base)) or base
    home = Path.home()
    roots = [
        workspace / ".agents" / "skills",
        plugin_root_path() / "skills",
        home / ".agents" / "skills",
    ]
    plugins_dir = home / ".gemini" / "config" / "plugins"
    if plugins_dir.is_dir():
        roots.extend(sorted(p / "skills" for p in plugins_dir.iterdir() if p.is_dir()))
    out = []
    seen = set()
    for root in roots:
        key = str(root.resolve()) if root.exists() else str(root)
        if key not in seen:
            seen.add(key)
            out.append(root)
    return out


def _available_skills(roots):
    """Return sorted unique skill names that ship a SKILL.md under any root."""
    names = set()
    for root in roots:
        if not root.is_dir():
            continue
        for p in root.iterdir():
            if p.is_dir() and (p / "SKILL.md").is_file():
                names.add(p.name)
    return sorted(names)


def load_skill_blocks(skill_names, cwd=None):
    """Load requested SKILL.md contents as delimited guidance blocks; exit 2 on bad names."""
    if not skill_names:
        return ""
    roots = _skill_roots(cwd)
    blocks = []
    for raw_name in skill_names:
        name = str(raw_name).strip()
        if name.startswith("boyd:"):
            name = name[len("boyd:"):]
        name = _SKILL_ALIASES.get(name, name)
        if not ID_RE.match(name):
            sys.stderr.write(f"error: invalid skill name {raw_name!r}\n")
            sys.exit(2)
        skill_file = next(
            (r / name / "SKILL.md" for r in roots if (r / name / "SKILL.md").is_file()),
            None,
        )
        if skill_file is None:
            available = ", ".join(_available_skills(roots))
            sys.stderr.write(f"error: unknown skill {raw_name!r} (available: {available})\n")
            sys.exit(2)
        body = skill_file.read_text(encoding="utf-8").strip()
        blocks.append(f"=== Skill Guidance: {name} ===\n{body}\n=== End Skill: {name} ===")
    return "\n\n".join(blocks) + "\n\n"
