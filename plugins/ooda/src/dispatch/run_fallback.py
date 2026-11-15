"""Routed task execution with flock leases, quota/auth fallback, and family-pivot retries."""

import sys

from envelope import effective_lane_spec, model_family, select_provider
from fleet import (
    DEEP_COMPLEXITIES,
    FAST_COMPLEXITIES,
    REGIME_TIMEOUTS,
    acquire_parallel_lease,
    is_auth_error,
    is_quota_error,
    load_quota,
    mark_provider_cooldown,
    now_iso,
    parse_lanes,
    parse_timeout,
    record_usage,
    release_parallel_lease,
)
from worktree import load_skill_blocks

from .build_cmd import execute_once
from .telemetry import hud_worker_finish, hud_worker_start, short_model, visible_extra_skills


def resolve_task_timeout(cli_timeout_arg, task_timeout_val, complexity, read_only=False):
    """Select fast-transient timeout by OODA regime unless explicitly overridden."""
    if task_timeout_val is not None:
        return parse_timeout(task_timeout_val)
    if cli_timeout_arg is not None:
        return parse_timeout(cli_timeout_arg)
    if read_only:
        return REGIME_TIMEOUTS["observe"]
    comp = str(complexity or "").strip().lower()
    if comp in DEEP_COMPLEXITIES:
        return REGIME_TIMEOUTS["deep"]
    if comp in FAST_COMPLEXITIES:
        return REGIME_TIMEOUTS["fast"]
    if comp == "heavy":
        return REGIME_TIMEOUTS["heavy"]
    return REGIME_TIMEOUTS["act"]


def _result(st, lane, cli, model, effort, exit_code, timed_out, output, duration_s):
    """Build the stable run_task_with_fallback result dict (key order preserved)."""
    return {
        "requested_lane": st["requested_lane"], "lane": lane, "cli": cli,
        "model": model, "effort": effort,
        "fallback_from": "; ".join(dict.fromkeys(st["notes"])) if st["notes"] else None,
        "attempts": st["attempts"], "exit_code": exit_code, "timed_out": timed_out,
        "output": output, "duration_s": duration_s,
    }


def _init_state(task, worktree, slot, default_lane, default_timeout, **opts):
    """Assemble the mutable per-task routing state (prompt, timeout, exclusions, notes)."""
    files = task.get("files") or []
    skills = task.get("skills") or []
    prompt = task["prompt"]
    if files:
        prompt = f"Files in scope: {', '.join(files)}\n\n{prompt}"
    if skills:
        prompt = load_skill_blocks(skills) + prompt
    read_only = bool(task.get("read_only", False))
    complexity = task.get("complexity")
    requested_lane = task.get("lane") or default_lane or "auto"
    req_lanes = parse_lanes(requested_lane, allow_auto=True)
    canon_req = "auto" if req_lanes == ["auto"] else ",".join(req_lanes)
    return dict(
        opts, task=task, worktree=worktree, slot=slot, files=files, skills=skills,
        prompt=prompt, read_only=read_only, complexity=complexity,
        timeout=resolve_task_timeout(default_timeout, task.get("timeout"), complexity, read_only=read_only),
        requested_lane=requested_lane, canon_req=canon_req,
        tried=set(opts.get("exclude_lanes") or ()), avoid=set(opts.get("avoid_families") or ()),
        crash_pivoted=False, attempts=[], notes=[], skipped=None,
    )


def _acquire_task_lease(st, provider_name, info, quota_data):
    """Return (lease, outcome): outcome None=run, 'retry'=reselect, dict=final timeout result."""
    max_p = info.get("max_parallel")
    if not max_p or int(max_p) <= 0:
        return None, None
    max_p = int(max_p)
    lease = None
    if st["canon_req"] == "auto":
        lease = acquire_parallel_lease(provider_name, max_p, blocking=False)
        if lease is None:
            alt, _, _, _ = select_provider(
                st["requested_lane"], st["complexity"], quota_data,
                exclude=st["tried"] | {provider_name},
                stripe_index=st["stripe_index"], avoid_families=st["avoid"],
            )
            if alt is not None:
                st["tried"].add(provider_name)
                st["notes"].append(f"{provider_name}: {max_p}/{max_p} parallel slots busy")
                return None, "retry"
            st["log_stream"].write(
                f"[{st['task']['id']}] waiting for free {provider_name} slot ({max_p}/{max_p} busy)\n"
            )
            st["log_stream"].flush()
    if lease is None:
        lease = acquire_parallel_lease(provider_name, max_p, blocking=True, timeout=st["timeout"])
    if lease is False:
        msg = f"<PARALLEL SLOT TIMEOUT after {st['timeout']}s waiting on {provider_name} ({max_p}/{max_p} busy)>"
        return None, _result(st, provider_name, info["cli"], None, None, -1, True, msg, float(st["timeout"]))
    return lease, None


def _run_provider_attempt(st, provider_name, info, cli_path, lease):
    """Run one leased attempt with banner + HUD markers; record usage; return (res, model, effort)."""
    task, log = st["task"], st["log_stream"]
    argv_model, model, argv_effort, effort = effective_lane_spec(
        provider_name, info, st["model_override"] or task.get("model"),
        st["effort_override"] or task.get("effort"), st["complexity"],
    )
    extra = visible_extra_skills(st["skills"])
    parallel_run = bool(st["is_parallel"] or st["stripe_index"] is not None)
    mode_tag = "observe" if st["read_only"] else ("act:parallel" if parallel_run else "act")
    stage = "OODA:OBSERVE" if st["read_only"] else (
        f"OODA:ACT parallel:{task['id']}" if parallel_run else "OODA:ACT")
    skill_suffix = f" | skill={','.join(extra)}" if extra else ""
    marker, res, res_exit, dur = None, None, -1, 0.0
    try:
        if st["skipped"] and not st["attempts"]:
            log.write(f"[{task['id']}] fallback: requested {st['requested_lane']!r} -> "
                      f"using {provider_name!r} ({st['skipped']})\n")
        log.write(f"▸ [{stage}] agent={provider_name} ({info['cli']}:{short_model(model)}) "
                  f"| effort={effort or 'default'}{skill_suffix}\n")
        log.flush()
        marker = hud_worker_start(mode_tag, task["id"], provider_name, info["cli"], model, effort,
                                  extra, st["worktree"])
        res, dur = execute_once(provider_name, info, cli_path, argv_model, argv_effort, st["prompt"],
                                st["files"], st["worktree"], st["slot"], task["id"], st["timeout"],
                                read_only=st["read_only"])
        res_exit = res["exit_code"]
    finally:
        release_parallel_lease(lease)
        hud_worker_finish(marker, mode_tag, provider_name, model, effort, extra, res_exit, dur)
    record_usage(provider_name, st["prompt"], res["output"], export_info=res.get("export_info"))
    st["attempts"].append({"lane": provider_name, "model": model, "effort": effort,
                           "exit_code": res["exit_code"], "timed_out": res["timed_out"],
                           "duration_s": round(dur, 1)})
    return res, model, effort


def _handle_worker_outcome(st, provider_name, res):
    """Apply quota/auth cooldown fallback or one-shot family pivot; return True to retry."""
    tid, log = st["task"]["id"], st["log_stream"]
    out, code = res["output"], res["exit_code"]
    if is_quota_error(out, code):
        mark_provider_cooldown(provider_name, f"Auto-detected quota/credit error on {now_iso()}",
                               is_quota=True, output_text=out)
        note, msg = "quota/credit limit detected", f"quota limit hit on {provider_name!r}; switching to fallback lane"
    elif is_auth_error(out, code):
        mark_provider_cooldown(provider_name, f"Authentication required on {now_iso()}", is_quota=False)
        note, msg = "authentication required", f"auth error on {provider_name!r}; switching to fallback lane"
    elif code != 0 and st["canon_req"] == "auto" and not st["no_pivot"] and not st["crash_pivoted"]:
        family = model_family(provider_name)
        probe, _, _, _ = select_provider(
            st["requested_lane"], st["complexity"], load_quota(),
            exclude=st["tried"] | {provider_name},
            stripe_index=st["stripe_index"], avoid_families=st["avoid"] | {family},
        )
        if probe is None:
            return False
        st["crash_pivoted"] = True
        st["avoid"].add(family)
        note = f"exit={code} mismatch pivot"
        msg = f"mismatch/timeout on {provider_name!r}; pivoting to fresh model family"
    else:
        return False
    st["tried"].add(provider_name)
    st["notes"].append(f"{provider_name}: {note}")
    log.write(f"[{tid}] {msg}\n")
    log.flush()
    return True


def run_task_with_fallback(task, worktree, slot, default_lane, model_override, effort_override,
                           default_timeout, stripe_index=None, is_parallel=False, exclude_lanes=None,
                           avoid_families=None, no_pivot=False, log_stream=sys.stdout):
    """Run a task on its routed provider, handling leases, E-M fallback, and mismatch pivots."""
    st = _init_state(task, worktree, slot, default_lane, default_timeout,
                     model_override=model_override, effort_override=effort_override,
                     stripe_index=stripe_index, is_parallel=is_parallel, exclude_lanes=exclude_lanes,
                     avoid_families=avoid_families, no_pivot=no_pivot, log_stream=log_stream)
    while True:
        quota_data = load_quota()
        provider_name, info, cli_path, skipped = select_provider(
            st["requested_lane"], st["complexity"], quota_data, exclude=st["tried"],
            stripe_index=stripe_index, avoid_families=st["avoid"],
        )
        st["skipped"] = skipped
        # Record the initial routing skip once; retries already carry per-lane notes.
        if skipped and not st["attempts"] and not st["notes"]:
            st["notes"].append(skipped)
        if not provider_name:
            err = f"error: no available provider for task {task['id']} ({skipped})"
            return _result(st, None, None, None, None, 2, False, err, 0.0)
        lease, outcome = _acquire_task_lease(st, provider_name, info, quota_data)
        if outcome == "retry":
            continue
        if outcome is not None:
            return outcome
        res, model, effort = _run_provider_attempt(st, provider_name, info, cli_path, lease)
        if _handle_worker_outcome(st, provider_name, res):
            continue
        total = sum(a["duration_s"] for a in st["attempts"])
        return _result(st, provider_name, info["cli"], model, effort, res["exit_code"],
                       res["timed_out"], res["output"], round(total, 1))
