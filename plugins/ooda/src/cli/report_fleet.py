"""Fleet readiness (--status) and cycle telemetry (--usage) tabular reports."""

from datetime import datetime, timezone

from dispatch import short_model
from envelope import compute_em_envelope, select_provider
from fleet import (
    DEFAULT_POOL, DEVIN_FAST_MAX_PARALLEL, REVIEW_MODEL, STRIPE_POOL,
    count_active_leases, format_countdown, load_quota, probe_muse_usage,
    quota_file_path, resolve_cli,
)


def _status_row(name, info, now_utc):
    """Format one provider row for the --status readiness table."""
    cli = info.get("cli", "")
    bin_note = resolve_cli(cli) or "MISSING"
    effort = info.get("effort") or "default"
    model_disp = short_model(info.get("model") or "default")
    reset_cd = format_countdown(info.get("cooldown_until") or info.get("next_reset_at"), now_utc=now_utc)
    if info.get("status") != "active":
        why = info.get("reason") or "cooldown"
        if info.get("cooldown_until") and reset_cd:
            detail = f"{bin_note} | resets in {reset_cd} | {why}"
        elif info.get("manual_disable"):
            detail = f"{bin_note} | [manual] {why}"
        else:
            detail = f"{bin_note} | {why}"
    elif reset_cd:
        detail = f"{bin_note} | cycle resets in {reset_cd}"
    else:
        detail = bin_note
    max_p = info.get("max_parallel")
    if max_p and int(max_p) > 0:
        busy = count_active_leases(name, max_p)
        detail += f" | [max {int(max_p)} parallel, {busy}/{int(max_p)} busy]"
    return (
        f"{name:<13} {info.get('status', '?'):<9} {info.get('tier', '?'):<10} "
        f"{effort:<7} {cli:<13} {model_disp:<25} {detail}"
    )


def do_status():
    """Print provider readiness, effort standards, reset timers, and active routing table."""
    quota_data = load_quota()
    providers = quota_data.get("providers") or {}
    chain = quota_data.get("default_fallback_chain") or DEFAULT_POOL
    stripe_pool = quota_data.get("parallel_stripe_pool") or STRIPE_POOL
    drain_capped = quota_data.get("drain_capped_first", True)
    now_utc = datetime.now(timezone.utc)
    print(
        f"Quota state        : {quota_file_path()} (v{quota_data.get('version', '?')}, "
        f"updated: {quota_data.get('updated_at', 'unknown')})"
    )
    print(f"Drain capped first : {drain_capped} (soonest expiring cycle prioritized)")
    print(f"Parallel stripe    : {' -> '.join(stripe_pool)}")
    print(f"Fallback chain     : {' -> '.join(chain)}")
    print("")
    print(
        f"{'LANE':<13} {'STATUS':<9} {'TIER':<10} {'EFFORT':<7} {'CLI':<13} "
        f"{'MODEL':<25} {'RESET / BINARY'}"
    )
    print("-" * 116)
    for name, info in providers.items():
        print(_status_row(name, info, now_utc))
    print("")
    default_pick = select_provider("auto", None, quota_data)[0] or "NONE"
    fast_pick = select_provider("auto", "fast", quota_data)[0] or "NONE"
    deep_pick = select_provider("auto", "deep", quota_data)[0] or "NONE"
    review_bin = resolve_cli("mcode") or "MISSING"
    print(f"Effective default/heavy lane : {default_pick}")
    print(f"Effective fast/recon lane    : {fast_pick}")
    print(f"Effective deep/arch lane     : {deep_pick}")
    rev_lbl = REVIEW_MODEL if REVIEW_MODEL != "cli-default" else "cli default unless overridden"
    print(f"Auto-review mcode ({rev_lbl}): {review_bin}")
    return 0


def _usage_row(name, info, regimes, now_utc):
    """Format one provider row for the --usage cycle telemetry table."""
    cycle_h = info.get("cycle_hours")
    cycle_str = f"{cycle_h:g}h" if cycle_h is not None else "creds"
    runs = int(info.get("cycle_runs") or 0)
    toks = int(info.get("cycle_tokens") or 0)
    tok_prefix = "" if (bool(info.get("cycle_tokens_exact")) or toks == 0) else "~"
    tok_str = f"{tok_prefix}{toks:,}"
    if info.get("cycle_token_budget"):
        tok_str += f"/{int(info['cycle_token_budget']):,}"
    cost = float(info.get("cycle_cost_usd") or 0.0)
    cost_budget = info.get("cycle_cost_budget_usd")
    cost_str = f"${cost:.4f}" if cost > 0 else "-"
    if cost_budget:
        cost_str = f"${cost:.2f}/${float(cost_budget):.2f}"
    reset_cd = format_countdown(info.get("cooldown_until") or info.get("next_reset_at"), now_utc=now_utc)
    if not reset_cd:
        if info.get("tier") == "credits" and info.get("status") != "active":
            reset_cd = "top-up"
        elif info.get("manual_disable"):
            reset_cd = "manual"
        else:
            reset_cd = "idle"
    em_tag = regimes.get(name, "-")
    note = info.get("reason") or (
        "fresh" if runs == 0 and info.get("status") == "active" else short_model(info.get("model", ""))
    )
    return (
        f"{name:<13} {info.get('status', '?'):<9} {em_tag:<9} {cycle_str:<6} {runs:<5} "
        f"{tok_str:<14} {cost_str:<9} {reset_cd:<12} {note}"
    )


def do_usage():
    """Print detailed per-lane cycle usage, E-M regimes, budgets, and live muse probe."""
    muse_probe = probe_muse_usage()
    quota_data = load_quota()
    providers = quota_data.get("providers") or {}
    now_utc = datetime.now(timezone.utc)
    em = compute_em_envelope(quota_data, now_utc=now_utc)
    regimes = em["regimes"]
    print(f"Cycle Usage Telemetry ({quota_file_path()}, v{quota_data.get('version', '?')})")
    print("")
    print(
        f"{'LANE':<13} {'STATUS':<9} {'E-M':<9} {'CYCLE':<6} {'RUNS':<5} {'TOKENS':<14} "
        f"{'COST':<9} {'RESETS IN':<12} {'NOTE'}"
    )
    print("-" * 116)
    for name, info in providers.items():
        print(_usage_row(name, info, regimes, now_utc))
    print("")
    fast_info = providers.get("devin-fast") or {}
    fast_cap = int(fast_info.get("max_parallel") or DEVIN_FAST_MAX_PARALLEL)
    fast_busy = count_active_leases("devin-fast", fast_cap)
    if em["conserve_capped"]:
        em_summary = "Ps<0 CONSERVE (<=1 capped lane, next wake >45m; sparing capped reserves on routine work)"
    else:
        em_summary = "Ps>=0 SUSTAINED (capped capacity replenished or corner-dumping)"
    print(f"Fleet E-M envelope      : {em_summary} | devin-fast parallel slots: {fast_busy}/{fast_cap} busy")
    print(f"Live probe [muse usage] : {muse_probe['summary']}")
    print("Headless probes         : grok/devin/mcode omit remaining balance; agy-opus & vibe report exact per-run tokens")
    return 0
