"""Calibrated cross-model read-only diff review with off-family fallback.

Small working-tree diffs (<= NATIVE_REVIEW_MAX_BYTES) run mcode M3.1-Flash's native
``mcode exec review`` (15 steps); larger or committed diffs run a single-pass inline
patch review (``--mode lightweight --permission off --max-steps 4``). Timeouts,
non-zero exits, or UNKNOWN verdicts cascade across off-family backup reviewers;
[P0-3] findings and REJECT verdicts always fail closed.
"""

import re
import sys
import time

from envelope import model_family, select_provider
from fleet import (
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
)

NATIVE_REVIEW_MAX_BYTES = 8192
ACCEPT_LINE_RE = re.compile(
    r"^\s*(?:\*\*|__)?VERDICT(?:\*\*|__)?\s*:\s*(?:\*\*|__)?\s*ACCEPT\s*(?:\*\*|__)?\.?\s*$",
    re.IGNORECASE,
)
_PTAG = r"\[P[0-3](?:-[0-3])?\]"
MCODE_FINDING_RE = re.compile(_PTAG, re.IGNORECASE)
MCODE_CLEAN_TAG_RE = re.compile(
    rf"\b(?:none\s+of\s+(?:the\s+)?{_PTAG}\s+(?:(?:categories|levels|tags)\s+)?appl(?:y|ies)"
    rf"|(?:no|zero|without|0)\s+"
    rf"(?:(?:new|other|remaining|additional|qualifying|reportable|actionable)\s+)*"
    rf"(?:\*\*|__)?(?:{_PTAG}(?:\s*(?:,\s*(?:or|and)|[-/,]|or|and)\s*|\s+))+"
    rf"(?:\*\*|__)?(?:issues?|findings?|bugs?|defects?|problems?|regressions?|violations?))\b",
    re.IGNORECASE,
)
MCODE_CLEAN_RE = re.compile(
    r"^\s*no (?:(?:reportable|actionable|qualifying) )?issues(?: were)? found\.?\s*$",
    re.IGNORECASE,
)


def parse_review_verdict(text):
    """Fail-closed verdict: REJECT/[P0-3] findings win; ACCEPT requires final verdict or clean note."""
    lines = (text or "").splitlines()
    verdicts = [m.group(1).upper() for ln in lines if (m := VERDICT_RE.match(ln))]
    has_finding = any(
        MCODE_FINDING_RE.search(MCODE_CLEAN_TAG_RE.sub("", ln)) for ln in lines
    )
    if "REJECT" in verdicts or has_finding:
        return "REJECT"
    nonempty = [ln for ln in lines if ln.strip()]
    if not nonempty:
        return "UNKNOWN"
    if ACCEPT_LINE_RE.match(nonempty[-1]):
        return "ACCEPT"
    if len(nonempty) <= 3 and any(MCODE_CLEAN_RE.match(ln) for ln in nonempty):
        return "ACCEPT"
    return "UNKNOWN"


def build_review_prompt(target_dir, diff_ctx, scope_violations=None, declared_files=None):
    """Compose the read-only review prompt with scope, status, stat, and patch context."""
    scope_note = ", ".join(declared_files) if declared_files else "(unspecified)"
    viol_note = ", ".join(scope_violations) if scope_violations else "none"
    return (
        f"Read-only independent code review of {diff_ctx['source']} changes in {target_dir}. "
        "The complete git status, diff stat, and patch are provided inline below. "
        "Do NOT modify, create, or delete any files, and do NOT execute shell commands "
        "or crawl unrelated repository files.\n\n"
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


def build_native_review_note(target_dir, diff_ctx, scope_violations=None, declared_files=None):
    """Build explicit read-only contract + assembled patch (<= 8 KiB) for native review."""
    scope = ", ".join(declared_files) if declared_files else "(unspecified)"
    viol = ", ".join(scope_violations) if scope_violations else "none"
    return (
        f"Read-only independent code review of {diff_ctx['source']} changes in {target_dir}. "
        "The assembled patch below includes untracked files as synthetic hunks. You may read "
        "surrounding lines of touched files if needed, but do NOT modify, create, or delete "
        "any files; do NOT run state-mutating or arbitrary shell commands; and do NOT crawl "
        "unrelated repository files.\n\n"
        f"Declared files in scope: {scope}\n"
        f"Detected scope violations: {viol}\n"
        f"git status --short:\n{diff_ctx.get('status') or '(clean)'}\n\n"
        f"git diff --stat:\n{diff_ctx.get('diff_stat') or '(no tracked diff)'}\n\n"
        f"Patch:\n{diff_ctx.get('diff') or '(empty)'}\n\n"
        "Tag any findings with [P0], [P1], [P2], or [P3] concisely. "
        "Final line MUST be exactly: VERDICT: ACCEPT  or  VERDICT: REJECT - <reason>"
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
        native = provider_name == "mcode" and bool(ctx.get("native_eligible"))
        rmode = "native" if native else True
        call_prompt = ctx["scope_note"] if native else ctx["prompt"]
        out = ctx["runner"](provider_name, info, cli_path, model, effort, call_prompt, [],
                            target_dir, slot, task_id, timeout, read_only=True, review_mode=rmode)
        res = out[0] if isinstance(out, tuple) else out
        rev_exit = res.get("exit_code")
        return res, model, effort
    finally:
        release_parallel_lease(lease)
        if callable(ctx["on_finish"]):
            ctx["on_finish"](marker, "act:verify", provider_name, model, effort, [], rev_exit,
                             time.time() - start)


def run_mcode_review(
    target_dir, slot, diff_ctx, scope_violations=None, declared_files=None,
    timeout=REVIEW_TIMEOUT, log_stream=sys.stdout, on_start=None, on_finish=None,
    author_lane=None, exclude_lanes=None, runner=None,
):
    """Read-only cross-model review: calibrated mcode first, then off-family fallback reviewers."""
    if runner is None:
        from dispatch.build_cmd import execute_once as runner
    avoid = {model_family(author_lane)} if author_lane else set()
    tried = set(exclude_lanes or ())
    prompt = build_review_prompt(target_dir, diff_ctx, scope_violations, declared_files)
    scope_note = build_native_review_note(target_dir, diff_ctx, scope_violations, declared_files)
    native_eligible = (
        diff_ctx.get("source") == "working-tree"
        and len((diff_ctx.get("diff") or "").encode("utf-8")) <= NATIVE_REVIEW_MAX_BYTES
    )
    ctx = {"slot": slot, "target_dir": target_dir, "timeout": timeout, "log_stream": log_stream,
           "on_start": on_start, "on_finish": on_finish, "runner": runner, "prompt": prompt,
           "native_eligible": native_eligible, "scope_note": scope_note}
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
        verdict = parse_review_verdict(out)
        if code != 0 and verdict == "ACCEPT":
            verdict = "UNKNOWN"
        if verdict != "REJECT" and (res.get("timed_out") or code != 0 or verdict == "UNKNOWN"):
            nxt = select_provider("auto", "review", quota, exclude=tried | {provider_name},
                                  avoid_families=avoid)[0]
            if nxt and model_family(nxt) not in avoid:
                tried.add(provider_name)
                avoid.add(model_family(provider_name))
                continue
        if scope_violations:
            verdict = "REJECT"
        return {"ran": True, "lane": provider_name, "model": model,
                "verdict": verdict, "exit_code": code, "output": out}


run_review = run_mcode_review
