"""Compose the single-line ooda statusLine HUD (OODA step, worker models, skills, fleet)."""

import json
import os
import sys
import time

from fleet import hud_dir_path

from .ansi_format import (
    BRAILLE_FRAMES, C_ACT, C_ACT_MODEL, C_FLEET_FULL, C_FLEET_LOW, C_FLEET_PARTIAL,
    C_IDLE_ICON, C_IDLE_TEXT, C_MUTED, C_OBSERVE, C_OBSERVE_MODEL, C_ORIENT, C_VERIFY,
    C_VERIFY_MODEL, ORIENT_FRAMES, colorize, short_model, truncate_ansi, visible_len,
)
from .probe_hud import collect_skills, load_active_workers, load_fleet_summary

IDLE_STATES = frozenset(
    {"idle", "ready", "waiting", "done", "complete", "completed", "stopped", "paused", "error", "none"}
)


def step_style(mode):
    """Map a worker mode to (step label, step color, model color)."""
    m = str(mode or "act")
    if m in ("observe", "recon"):
        return "observe", C_OBSERVE, C_OBSERVE_MODEL
    if m in ("act:verify", "review"):
        return "verify", C_VERIFY, C_VERIFY_MODEL
    return "act", C_ACT, C_ACT_MODEL


def _build_left_segment(workers, is_thinking, frame_idx, use_color, dot):
    """Render the leading OODA step segment for idle, orient, single, or parallel workers."""
    if len(workers) == 1:
        w = workers[0]
        spin = BRAILLE_FRAMES[frame_idx % len(BRAILLE_FRAMES)]
        step, step_col, mod_col = step_style(w.get("mode"))
        smodel = short_model(w.get("model") or "-")
        return f"{colorize(f'{spin} {step}', step_col, use_color)}{dot}{colorize(smodel, mod_col, use_color)}"
    if len(workers) >= 2:
        spin = BRAILLE_FRAMES[frame_idx % len(BRAILLE_FRAMES)]
        if all(str(w.get("mode") or "") in ("observe", "recon") for w in workers):
            step, step_col, mod_col = f"observe ×{len(workers)}", C_OBSERVE, C_OBSERVE_MODEL
        else:
            step, step_col, mod_col = f"act ×{len(workers)}", C_ACT, C_ACT_MODEL
        models_str = ", ".join(short_model(w.get("model") or "-") for w in workers[:3])
        if len(workers) > 3:
            models_str += ", …"
        return f"{colorize(f'{spin} {step}', step_col, use_color)}{dot}{colorize(models_str, mod_col, use_color)}"
    if is_thinking:
        spin = ORIENT_FRAMES[frame_idx % len(ORIENT_FRAMES)]
        return colorize(f"{spin} orient/decide", C_ORIENT, use_color)
    return f"{colorize('○', C_IDLE_ICON, use_color)} {colorize('ooda', C_IDLE_TEXT, use_color)}"


def _build_line(workers, skills, active_cnt, total_cnt, wake_cd, is_thinking, frame_idx,
                use_color, include_wake):
    """Assemble the full HUD line from step, skill, and fleet segments."""
    if total_cnt > 0 and active_cnt == total_cnt:
        fleet_color = C_FLEET_FULL
    elif active_cnt <= 1:
        fleet_color = C_FLEET_LOW
    else:
        fleet_color = C_FLEET_PARTIAL
    sep = colorize(" │ ", C_MUTED, use_color)
    dot = colorize(" · ", C_MUTED, use_color)
    if not skills:
        skill_seg = ""
    elif len(skills) == 1:
        skill_seg = f"{sep}{colorize(f'skill {skills[0]}', C_MUTED, use_color)}"
    else:
        skill_seg = f"{sep}{colorize('skills ' + ', '.join(skills), C_MUTED, use_color)}"
    fleet_seg = colorize(f"fleet {active_cnt}/{total_cnt}", fleet_color, use_color)
    if include_wake and wake_cd:
        fleet_seg += colorize(f" (next {wake_cd})", C_MUTED, use_color)
    left = _build_left_segment(workers, is_thinking, frame_idx, use_color, dot)
    return f"{left}{skill_seg}{sep}{fleet_seg}"


def _agent_thinking(data):
    """Return True when the agy payload reports an in-progress (non-idle) agent state."""
    agent_obj = data.get("agent") if isinstance(data.get("agent"), dict) else {}
    agent_state = str(
        agent_obj.get("state")
        or agent_obj.get("status")
        or agent_obj.get("phase")
        or data.get("agent_state")
        or data.get("state")
        or ""
    ).strip().lower()
    return bool(agent_state) and agent_state not in IDLE_STATES


def render_statusline(raw_input, use_color=True, now_ts=None):
    """Render the HUD line for the agy statusLine JSON payload in raw_input."""
    if os.environ.get("NO_COLOR"):
        use_color = False
    try:
        data = json.loads(raw_input) if raw_input.strip() else {}
        if not isinstance(data, dict):
            data = {}
    except json.JSONDecodeError:
        data = {}
    try:
        term_width = int(data.get("terminal_width") or data.get("terminalWidth") or 120)
    except (TypeError, ValueError):
        term_width = 120
    max_width = max(40, term_width - 2)

    hud_base = hud_dir_path()
    ts = time.time() if now_ts is None else float(now_ts)
    frame_idx = int(ts * 4)
    workers = load_active_workers(hud_base)
    skills = collect_skills(hud_base, data, workers)
    active_cnt, total_cnt, wake_cd = load_fleet_summary()[:3]
    ctx = (workers, skills, active_cnt, total_cnt, wake_cd, _agent_thinking(data), frame_idx, use_color)

    line = _build_line(*ctx, True)
    if visible_len(line) > max_width and wake_cd:
        line = _build_line(*ctx, False)
    if visible_len(line) > max_width and len(skills) > 1:
        compact = [f"{skills[0]}+{len(skills) - 1}"]
        line = _build_line(workers, compact, *ctx[2:], False)
    return truncate_ansi(line, max_width)


def main():
    """statusLine entrypoint: read agy JSON from stdin and print one HUD line."""
    try:
        raw_input = sys.stdin.read() if not sys.stdin.isatty() else ""
        sys.stdout.write(render_statusline(raw_input, use_color=True) + "\n")
    except Exception:
        sys.stdout.write("○ ooda\n")
    sys.stdout.flush()


if __name__ == "__main__":
    main()
