"""Worker telemetry: banner labels, live HUD markers, and CLI usage/output parsing."""

import json
import os
import re
import time

from fleet import CORE_HIDDEN_SKILLS, atomic_write_json, hud_dir_path, safe_id

_FUSION_RE = re.compile(
    r"^fusion-claude-([a-z]+)-(\d+)-(\d+)-(high|medium|low)(?:-sidekick-([a-z]+)-(\d+))?"
)
_EFFORT_ABBR = {"high": "hi", "medium": "med", "low": "lo"}


def visible_extra_skills(skills):
    """Return non-core extra domain skills for banner and HUD display."""
    return [s for s in (skills or []) if s not in CORE_HIDDEN_SKILLS]


def short_model(model_name):
    """Compact model label for log banners, e.g. 'fusion(opus-5.5-hi+swe2)' or 'MiniMax-M3.1-Flash'."""
    name = str(model_name or "").strip()
    if not name:
        return "default"
    m = _FUSION_RE.match(name.lower())
    if m:
        fam, major, minor, eff, side, side_ver = m.groups()
        label = f"{fam}-{major}.{minor}-{_EFFORT_ABBR.get(eff, eff)}"
        return f"fusion({label}+{side}{side_ver})" if side else f"fusion({label})"
    return name.rsplit("/", 1)[-1]


def hud_worker_start(mode, task_id, lane, cli_name, model, effort, skills, cwd):
    """Record an active worker marker under HUD_DIR/active/ and return its Path (best-effort)."""
    marker = hud_dir_path() / "active" / f"{os.getpid()}-{safe_id(task_id)}.json"
    payload = {
        "pid": os.getpid(),
        "task_id": str(task_id),
        "mode": mode,
        "lane": lane,
        "cli": cli_name,
        "model": model or "default",
        "effort": effort or "default",
        "skills": list(skills or []),
        "cwd": str(cwd),
        "started_at": round(time.time(), 2),
    }
    try:
        atomic_write_json(marker, payload, compact=True, private=True)
    except OSError:
        pass
    return marker


def hud_worker_finish(marker_path, mode, lane, model, effort, skills, exit_code, duration_s):
    """Remove the active worker marker and update HUD_DIR/last.json (best-effort)."""
    if marker_path is not None:
        try:
            marker_path.unlink()
        except OSError:
            pass
    payload = {
        "mode": mode,
        "lane": lane,
        "model": model or "default",
        "effort": effort or "default",
        "skills": list(skills or []),
        "exit_code": exit_code,
        "duration_s": round(float(duration_s), 1),
        "finished_at": round(time.time(), 2),
    }
    try:
        atomic_write_json(hud_dir_path() / "last.json", payload, compact=True, private=True)
    except OSError:
        pass


def hud_review_hooks():
    """Return (on_start, on_finish) HUD callables for verify.run_mcode_review."""
    return hud_worker_start, hud_worker_finish


def parse_vibe_export(export_dir):
    """Sum tokens/cost from a Mistral vibe export.json (incl. subagents), or None."""
    export_file = export_dir / "export.json"
    if not export_file.is_file():
        return None
    try:
        data = json.loads(export_file.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError):
        return None
    if not isinstance(data, dict):
        return None
    total_tokens = None
    usage = data.get("usage")
    if isinstance(usage, dict) and isinstance(usage.get("total_tokens"), int):
        total_tokens = usage["total_tokens"]
    cost_usd = data.get("cost_usd") if isinstance(data.get("cost_usd"), (int, float)) else None
    for sub in data.get("subagents") or []:
        if not isinstance(sub, dict):
            continue
        su = sub.get("usage")
        if isinstance(su, dict) and isinstance(su.get("total_tokens"), int):
            total_tokens = (total_tokens or 0) + su["total_tokens"]
        if isinstance(sub.get("cost_usd"), (int, float)):
            cost_usd = (cost_usd or 0.0) + float(sub["cost_usd"])
    if total_tokens is None and cost_usd is None:
        return None
    if cost_usd is not None:
        cost_usd = round(cost_usd, 6)
    return {"tokens": total_tokens, "cost_usd": cost_usd}


def parse_agy_output(raw_text, exit_code):
    """Parse `agy -p --output-format json` stdout into (clean_response, exit_code, export_info)."""
    text = raw_text or ""
    stripped = text.strip()
    data = None
    candidates = [stripped] if stripped else []
    candidates += [ln.strip() for ln in reversed(stripped.splitlines())]
    for cand in candidates:
        if not (cand.startswith("{") and cand.endswith("}")):
            continue
        try:
            parsed = json.loads(cand)
        except json.JSONDecodeError:
            continue
        if isinstance(parsed, dict):
            data = parsed
            break
    if data is None:
        return text, exit_code, None

    export_info = None
    usage = data.get("usage")
    if isinstance(usage, dict) and isinstance(usage.get("total_tokens"), int):
        export_info = {"tokens": usage["total_tokens"]}
        cost_val = data.get("cost_usd")
        if isinstance(cost_val, (int, float)):
            export_info["cost_usd"] = round(float(cost_val), 6)

    status = str(data.get("status") or "").upper()
    resp = data.get("response")
    if status and status != "SUCCESS":
        err_detail = data.get("error") or data.get("message") or resp or stripped
        return f"[{status}] {err_detail}", (exit_code if exit_code != 0 else 1), export_info
    clean = resp if isinstance(resp, str) else text
    return clean, exit_code, export_info
