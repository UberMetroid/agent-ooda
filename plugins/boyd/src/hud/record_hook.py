"""Antigravity lifecycle hook recording active skills per session for the boyd HUD."""

import json
import re
import sys
import time

from fleet import atomic_write_json, hud_dir_path, router_depth, safe_id

from .ansi_format import IGNORED_SKILLS

SKILL_PATH_RE = re.compile(r"/skills/([A-Za-z0-9._-]+)/SKILL\.md$")
ROUTER_CMD_RE = re.compile(r"(?:^|[\s/;|&\"'])(boyd|router|dispatch\.py)(?:\s|$|\"|')")
SKILL_FLAG_RE = re.compile(r"--skills?(?:=|\s+)([A-Za-z0-9.,:_-]+)")


def _append_skill(skills, raw_name):
    """Append a normalized, non-hidden skill name to skills if not already present."""
    name = str(raw_name or "").strip()
    if name.startswith("boyd:"):
        name = name[len("boyd:"):]
    if name == "agent-router":
        name = "router"
    if name and name not in IGNORED_SKILLS and name not in skills:
        skills.append(name)


def _apply_tool_call(skills, payload):
    """Update skills from a PostToolUse payload (SKILL.md reads or router --skill flags)."""
    tool = payload.get("toolCall") if isinstance(payload.get("toolCall"), dict) else {}
    tname = str(tool.get("name") or "")
    args = tool.get("args") if isinstance(tool.get("args"), dict) else {}
    if tname in ("read_file", "view_file"):
        path_str = str(args.get("Path") or args.get("AbsolutePath") or args.get("path") or "")
        m = SKILL_PATH_RE.search(path_str)
        if m:
            _append_skill(skills, m.group(1))
    elif tname == "run_command":
        cmd_str = str(args.get("CommandLine") or args.get("command") or "")
        if ROUTER_CMD_RE.search(cmd_str):
            skills = []
            for m in SKILL_FLAG_RE.finditer(cmd_str):
                for part in m.group(1).split(","):
                    _append_skill(skills, part)
    return skills


def _handle_event(payload):
    """Merge one hook event into the session skill record and current-session pointer."""
    conv_id = str(payload.get("conversationId") or payload.get("conversation_id") or "default")
    event = str(payload.get("hookEventName") or "")
    if event not in ("SessionStart", "PostToolUse"):
        return
    model_name = str(payload.get("modelName") or "")
    cwd = str(payload.get("cwd") or "")
    now_ts = round(time.time(), 2)

    base = hud_dir_path()
    sess_file = base / "sessions" / f"{safe_id(conv_id)}.json"
    existing = {}
    if sess_file.is_file():
        try:
            loaded = json.loads(sess_file.read_text(encoding="utf-8"))
            if isinstance(loaded, dict):
                existing = loaded
        except (OSError, json.JSONDecodeError):
            existing = {}
    raw_skills = existing.get("skills") if isinstance(existing.get("skills"), list) else []
    skills = []
    for item in raw_skills:
        _append_skill(skills, item)
    if event == "PostToolUse":
        skills = _apply_tool_call(skills, payload)

    record = {
        "conversation_id": conv_id,
        "skills": skills,
        "model_name": model_name or existing.get("model_name", ""),
        "cwd": cwd or existing.get("cwd", ""),
        "updated_at": now_ts,
    }
    atomic_write_json(sess_file, record, compact=True, private=True)
    pointer = {"conversation_id": conv_id, "updated_at": now_ts}
    atomic_write_json(base / "current_session.json", pointer, compact=True, private=True)


def _handle():
    """Read the hook payload from stdin and record it unless running as a nested worker."""
    if router_depth() >= 1:
        return
    raw_stdin = sys.stdin.read().strip()
    if not raw_stdin:
        return
    payload = json.loads(raw_stdin)
    if isinstance(payload, dict):
        _handle_event(payload)


def main():
    """Hook entrypoint: never fail the agent; always emit an empty JSON object."""
    try:
        _handle()
    except Exception:
        pass
    sys.stdout.write("{}\n")
    sys.stdout.flush()


if __name__ == "__main__":
    main()
