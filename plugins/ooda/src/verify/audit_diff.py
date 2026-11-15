"""Cross-model read-only diff review: lightweight mcode M3.1-Flash first, family-rotated fallback."""

import os
import subprocess
import sys
import time

from envelope import model_family, select_provider
from fleet import (
    PROVIDER_ORDER,
    REVIEW_MODEL,
    REVIEW_TIMEOUT,
    VERDICT_RE,
    acquire_parallel_lease,
    is_auth_error,
    is_quota_error,
    load_quota,
    mark_provider_cooldown,
    now_iso,
    record_usage,
    release_parallel_lease,
    router_depth,
    run,
)

OUTPUT_TAIL = 20000
LIGHTWEIGHT_ARGS = ["--mode", "lightweight", "--max-steps", "8"]


def parse_review_verdict(text):
    """Extract ACCEPT/REJECT verdict from the end of reviewer output."""
    for line in reversed((text or "").splitlines()):
        m = VERDICT_RE.match(line)
        if m:
            return m.group(1).upper()
    return "UNKNOWN"


def build_review_prompt(target_dir, diff_ctx, scope_violations=None, declared_files=None):
    """Compose the read-only review prompt with scope, status, stat, and patch context."""
    scope_note = ", ".join(declared_files) if declared_files else "(unspecified)"
    viol_note = ", ".join(scope_violations) if scope_violations else "none"
    return (
        f"Read-only independent code review of {diff_ctx['source']} changes in {target_dir}. "
        "Do NOT modify, create, or delete any files.\n\n"
        f"Declared files in scope: {scope_note}\n"
        f"Detected scope violations: {viol_note}\n"
        f"git status --short:\n{diff_ctx['status']}\n\n"
        f"git diff --stat:\n{diff_ctx['diff_stat']}\n\n"
        f"Patch:\n{diff_ctx['diff']}\n\n"
        "Report concisely:\n"
        "1. Logic bugs or broken invariants in the diff\n"
        "2. Scope or unintended file modifications\n"
        "3. Missing error handling or fail-open paths\n"
        "4. Final line MUST be exactly: VERDICT: ACCEPT  or  VERDICT: REJECT - <reason>"
    )


def _skipped(reason):
    """Return a not-run review record when no reviewer lane is available."""
    return {"ran": False, "lane": None, "model": REVIEW_MODEL, "verdict": "SKIPPED",
            "exit_code": None, "reason": reason, "output": ""}


def _acquire_review_lease(provider_name, info, quota, tried, avoid, timeout, log_stream):
    """Return (lease, retry): lease handle (or None when uncapped) to run, retry=True to reselect."""
    try:
        max_p = int(info.get("max_parallel") or 0)
    except (TypeError, ValueError):
        max_p = 0
    if max_p <= 0:
        return None, False
    lease = acquire_parallel_lease(provider_name, max_p, blocking=False)
    if lease is not None:
        return lease, False
    alt = select_provider("auto", "review", quota, exclude=tried | {provider_name},
                          avoid_families=avoid)[0]
    if alt is not None:
        tried.add(provider_name)
        return None, True
    if log_stream is not None:
        log_stream.write(f"[verify] waiting for free {provider_name} slot ({max_p}/{max_p} busy)\n")
        log_stream.flush()
    lease = acquire_parallel_lease(provider_name, max_p, blocking=True, timeout=timeout)
    if lease is False:
        tried.add(provider_name)
        return None, True
    return lease, False


def _direct_mcode(cli_path, model, prompt, target_dir, timeout):
    """Runner-less fallback: lightweight read-only mcode exec returning an execute_once-shaped dict."""
    env = dict(os.environ)
    env["ROUTER_DEPTH"] = str(router_depth() + 1)
    cmd = [cli_path, "exec", "--cwd", str(target_dir), "--model", model, "--effort", "high",
           "--permission", "smart", "--prompt-mode", "coding", "--output-format", "text",
           ] + LIGHTWEIGHT_ARGS + [prompt]
    try:
        p = run(cmd, cwd=target_dir, env=env, timeout=timeout)
    except subprocess.TimeoutExpired as e:
        raw = getattr(e, "stdout", None)
        out = raw.decode(errors="replace") if isinstance(raw, bytes) else (raw or "")
        return {"exit_code": -1, "output": (out + f"\n<REVIEW TIMEOUT after {timeout}s>")[-OUTPUT_TAIL:],
                "timed_out": True, "export_info": None}
    return {"exit_code": p.returncode, "output": (p.stdout or "")[-OUTPUT_TAIL:],
            "timed_out": False, "export_info": None}


def _invoke_reviewer(ctx, provider_name, info, cli_path, lease):
    """Run one review attempt with banner + HUD hooks; always releases the lease."""
    model = REVIEW_MODEL if provider_name == "mcode" else info["model"]
    effort = info.get("effort") or "high"
    slot, target_dir, timeout = ctx["slot"], ctx["target_dir"], ctx["timeout"]
    task_id = f"review-{slot.name}"
    log_stream = ctx["log_stream"]
    if log_stream is not None:
        short = str(model).rsplit("/", 1)[-1]
        log_stream.write(f"▸ [OODA:ACT verify] agent={provider_name} ({info['cli']}:{short}) "
                         f"| effort={effort}\n")
        log_stream.flush()
    marker, rev_exit, start = None, -1, time.time()
    try:
        if callable(ctx["on_start"]):
            marker = ctx["on_start"]("act:verify", task_id, provider_name, info["cli"], model,
                                     effort, [], target_dir)
        runner = ctx["runner"]
        if callable(runner):
            out = runner(provider_name, info, cli_path, model, effort, ctx["prompt"], [], target_dir,
                         slot, task_id, timeout, read_only=True, review_mode=True)
            res = out[0] if isinstance(out, tuple) else out
        else:
            res = _direct_mcode(cli_path, model, ctx["prompt"], target_dir, timeout)
        rev_exit = res.get("exit_code")
        return res, model, effort
    finally:
        release_parallel_lease(lease)
        if callable(ctx["on_finish"]):
            ctx["on_finish"](marker, "act:verify", provider_name, model, effort, [], rev_exit,
                             time.time() - start)


def run_mcode_review(
    target_dir,
    slot,
    diff_ctx,
    scope_violations=None,
    declared_files=None,
    timeout=REVIEW_TIMEOUT,
    log_stream=sys.stdout,
    on_start=None,
    on_finish=None,
    author_lane=None,
    exclude_lanes=None,
    runner=None,
):
    """Read-only cross-model review: mcode lightweight first, then fallback reviewers off-family."""
    avoid = {model_family(author_lane)} if author_lane else set()
    tried = set(exclude_lanes or ())
    if not callable(runner):
        tried |= {name for name in PROVIDER_ORDER if name != "mcode"}
    prompt = build_review_prompt(target_dir, diff_ctx, scope_violations, declared_files)
    ctx = {"slot": slot, "target_dir": target_dir, "timeout": timeout, "log_stream": log_stream,
           "on_start": on_start, "on_finish": on_finish, "runner": runner, "prompt": prompt}
    while True:
        quota = load_quota()
        provider_name, info, cli_path, skipped = select_provider(
            "auto", "review", quota, exclude=tried, avoid_families=avoid)
        if provider_name is None:
            return _skipped(skipped or "no review provider available")
        lease, retry = _acquire_review_lease(provider_name, info, quota, tried, avoid, timeout,
                                             log_stream)
        if retry:
            continue
        res, model, effort = _invoke_reviewer(ctx, provider_name, info, cli_path, lease)
        out, code = res.get("output") or "", res.get("exit_code")
        record_usage(provider_name, prompt, out, export_info=res.get("export_info"))
        quota_hit = is_quota_error(out, code)
        if quota_hit or is_auth_error(out, code):
            kind = "quota/rate-limit" if quota_hit else "auth"
            mark_provider_cooldown(provider_name, f"Auto-detected {kind} error on {now_iso()}",
                                   is_quota=quota_hit, output_text=out)
            tried.add(provider_name)
            continue
        if res.get("timed_out") and len(tried) == 0:
            tried.add(provider_name)
            avoid.add(model_family(provider_name))
            continue
        return {"ran": True, "lane": provider_name, "model": model,
                "verdict": parse_review_verdict(out), "exit_code": code, "output": out}


run_review = run_mcode_review
