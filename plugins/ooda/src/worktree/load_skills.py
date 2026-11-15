"""Resolve and load SKILL.md guidance blocks across workspace, plugin, and user roots."""

from pathlib import Path
import re
import sys

from fleet import CORE_HIDDEN_SKILLS, ID_RE, plugin_root_path, try_repo_root

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


def _parse_skill_meta(skill_file):
    """Extract (name, 1-line description) from SKILL.md YAML frontmatter."""
    fallback = skill_file.parent.name
    try:
        lines = skill_file.read_text(encoding="utf-8").splitlines()
    except (OSError, UnicodeDecodeError):
        return fallback, ""
    if not lines or lines[0].strip() != "---":
        return fallback, ""
    end = next((i for i in range(1, len(lines)) if lines[i].strip() == "---"), None)
    fm = lines[1:end] if end is not None else []
    name, desc_parts, in_desc = fallback, [], False
    for line in fm:
        if in_desc and (not line.strip() or line.startswith((" ", "\t"))):
            if line.strip():
                desc_parts.append(line.strip())
            continue
        in_desc = False
        m_name = re.match(r"^\s*name\s*:\s*(.+)$", line)
        if m_name:
            name = m_name.group(1).strip().strip("\"'") or fallback
            continue
        m_desc = re.match(r"^\s*description\s*:\s*(.*)$", line)
        if m_desc:
            val = m_desc.group(1).strip()
            if val in (">-", ">", "|-", "|"):
                in_desc = True
            elif val:
                desc_parts.append(val.strip("\"'"))
    desc = " ".join(desc_parts)
    return name, (desc[:105] + "..." if len(desc) > 108 else desc)


def discover_skills(cwd=None):
    """Return discovered skills sorted with domain skills before built-in core skills."""
    roots = _skill_roots(cwd)
    seen, found = set(), []
    for root in roots:
        if not root.is_dir():
            continue
        for p in sorted(root.iterdir()):
            skill_md = p / "SKILL.md"
            if not p.is_dir() or not skill_md.is_file() or p.name in seen:
                continue
            seen.add(p.name)
            name, desc = _parse_skill_meta(skill_md)
            found.append({"name": name, "description": desc, "core": name in CORE_HIDDEN_SKILLS})
    found.sort(key=lambda item: (item["core"], item["name"]))
    return found


def load_skill_blocks(skill_names, cwd=None):
    """Load requested SKILL.md contents as delimited guidance blocks; exit 2 on bad names."""
    if not skill_names:
        return ""
    roots = _skill_roots(cwd)
    blocks = []
    for raw_name in skill_names:
        name = str(raw_name).strip()
        if name.startswith("ooda:"):
            name = name[len("ooda:"):]
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
