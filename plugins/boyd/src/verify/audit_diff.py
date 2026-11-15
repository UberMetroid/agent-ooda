"""Read-only independent code review of a worktree diff via mcode MiniMax-M3.1-Flash."""

import os
import subprocess
import sys
import time

from fleet import (
    REVIEW_MODEL,
    REVIEW_TIMEOUT,
    VERDICT_RE,
    is_quota_error,
    mark_provider_cooldown,
    now_iso,
    record_usage,
    resolve_cli,
    router_depth,
    run,
)


def parse_review_verdict(text):
    """Extract ACCEPT/REJECT verdict from the end of mcode review output."""
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
):
    """Run read-only review via mcode; optional on_start/on_finish HUD hooks bracket the run."""
    model = REVIEW_MODEL
    mcode = resolve_cli("mcode")
    if not mcode:
        return {"ran": False, "model": model, "verdict": "SKIPPED", "exit_code": None,
                "reason": "mcode binary not installed", "output": ""}
    prompt = build_review_prompt(target_dir, diff_ctx, scope_violations, declared_files)
    env = dict(os.environ)
    env["ROUTER_DEPTH"] = str(router_depth() + 1)
    cmd = [mcode, "exec", "--cwd", str(target_dir), "--model", model, "--effort", "high",
           "--permission", "smart", "--prompt-mode", "coding", "--output-format", "text", prompt]
    if log_stream is not None:
        short = str(model).rsplit("/", 1)[-1]
        log_stream.write(f"▸ [OODA:ACT verify] agent=mcode (mcode:{short}) | effort=high\n")
        log_stream.flush()
    marker = None
    if callable(on_start):
        marker = on_start("act:verify", f"review-{slot.name}", "mcode", "mcode", model, "high", [], target_dir)
    rev_start = time.time()
    rev_exit = -1
    try:
        p = run(cmd, cwd=target_dir, env=env, timeout=timeout)
        rev_exit = p.returncode
        out = (p.stdout or "")[-20000:]
        record_usage("mcode", prompt, out)
        if is_quota_error(out, p.returncode):
            mark_provider_cooldown("mcode", f"Auto-detected quota/rate-limit error on {now_iso()}",
                                   is_quota=True, output_text=out)
        return {"ran": True, "model": model, "verdict": parse_review_verdict(out),
                "exit_code": p.returncode, "output": out}
    except subprocess.TimeoutExpired as e:
        raw = getattr(e, "stdout", None)
        out = raw.decode(errors="replace") if isinstance(raw, bytes) else (raw or "")
        record_usage("mcode", prompt, out)
        return {"ran": True, "model": model, "verdict": "UNKNOWN", "exit_code": -1,
                "output": (out + f"\n<REVIEW TIMEOUT after {timeout}s>")[-20000:]}
    finally:
        if callable(on_finish):
            on_finish(marker, "act:verify", "mcode", model, "high", [], rev_exit, time.time() - rev_start)
