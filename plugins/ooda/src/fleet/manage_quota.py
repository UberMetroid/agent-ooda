"""Locked quota mutations: status toggles, cooldowns, usage accounting, cycle config."""

from datetime import datetime, timedelta, timezone
import subprocess
import sys

from .lane_catalog import QUOTA_LOCK
from .locate_cli import format_local_iso, now_iso, parse_iso_utc, resolve_cli, run
from .model_aliases import extract_reset_timestamp, normalize_lane, normalize_model_for_provider
from .quota_state import load_quota, read_and_prep_unlocked, save_quota_unlocked
from .runtime_paths import quota_file_path


def _compute_quota_wake(entry, reason_prefix, output_text, now_local):
    """Set cooldown_until/next_reset_at/reason for a quota hit using best wake source."""
    now_utc = now_local.astimezone(timezone.utc)
    server_wake = extract_reset_timestamp(output_text)
    next_reset = entry.get("next_reset_at")
    next_dt = parse_iso_utc(next_reset)
    if server_wake is not None:
        entry["cooldown_until"] = server_wake
        entry["next_reset_at"] = server_wake
        entry["reason"] = f"{reason_prefix} (resets {server_wake})"
    elif next_dt is not None and next_dt > now_utc:
        entry["cooldown_until"] = next_reset
        entry["reason"] = f"{reason_prefix} (auto-wakes at cycle reset)"
    elif entry.get("cycle_hours") is not None:
        hrs = float(entry["cycle_hours"])
        wake_iso = format_local_iso(now_local + timedelta(hours=hrs))
        entry["cooldown_until"] = wake_iso
        entry["next_reset_at"] = wake_iso
        if not entry.get("cycle_started_at"):
            entry["cycle_started_at"] = format_local_iso(now_local)
        entry["reason"] = f"{reason_prefix} (auto-wakes in {hrs:g}h)"
    else:
        entry["cooldown_until"] = None
        entry["reason"] = "Credits exhausted — waiting for top-up"
    return entry


def set_provider_status(
    provider_name, status, reason=None, hours=None, auto_wake=False, quota_path=None
):
    """Update a provider's active/cooldown status in quota.json; return (canon, entry)."""
    path = quota_file_path(quota_path)
    canon = normalize_lane(provider_name, allow_auto=False)
    load_quota(path)
    with QUOTA_LOCK:
        data = read_and_prep_unlocked(path)
        providers = data.setdefault("providers", {})
        if canon not in providers:
            sys.stderr.write(f"error: provider {canon!r} not defined in {path}\n")
            sys.exit(2)
        entry = providers[canon]
        entry["status"] = status
        now_local = datetime.now().astimezone()
        if status == "active":
            entry.update(cooldown_until=None, reason=None, manual_disable=False)
        else:
            entry["manual_disable"] = not auto_wake
            entry["reason"] = reason or "Manually set to cooldown"
            if hours is not None:
                entry["cooldown_until"] = format_local_iso(now_local + timedelta(hours=float(hours)))
            elif auto_wake and entry.get("cycle_hours") is not None:
                until_iso = entry.get("next_reset_at")
                until_dt = parse_iso_utc(until_iso)
                if until_dt is None or until_dt <= datetime.now(timezone.utc):
                    hrs = float(entry["cycle_hours"])
                    until_iso = format_local_iso(now_local + timedelta(hours=hrs))
                    entry["next_reset_at"] = until_iso
                    if not entry.get("cycle_started_at"):
                        entry["cycle_started_at"] = now_iso()
                entry["cooldown_until"] = until_iso
            else:
                entry["cooldown_until"] = None
        save_quota_unlocked(data, path)
        return canon, entry


def mark_provider_cooldown(
    provider_name, reason_prefix, is_quota=True, output_text=None, quota_path=None
):
    """Put a provider into cooldown using server reset timestamps or cycle schedule."""
    path = quota_file_path(quota_path)
    canon = normalize_lane(provider_name, allow_auto=False)
    with QUOTA_LOCK:
        data = read_and_prep_unlocked(path)
        entry = data["providers"][canon]
        now_local = datetime.now().astimezone()
        entry["status"] = "cooldown"
        entry["manual_disable"] = False
        if is_quota:
            _compute_quota_wake(entry, reason_prefix, output_text, now_local)
        else:
            entry["cooldown_until"] = format_local_iso(now_local + timedelta(hours=1))
            entry["reason"] = f"{reason_prefix} (run login or --enable)"
        save_quota_unlocked(data, path)
        return dict(entry)


def record_usage(provider_name, prompt, output, export_info=None, quota_path=None):
    """Record cycle runs, tokens, and USD cost; auto-cooldown if a budget is reached."""
    path = quota_file_path(quota_path)
    canon = normalize_lane(provider_name, allow_auto=False)
    export_info = export_info or {}
    with QUOTA_LOCK:
        data = read_and_prep_unlocked(path)
        entry = (data.get("providers") or {}).get(canon)
        if entry is None:
            return None
        now_local = datetime.now().astimezone()
        if entry.get("cycle_hours") is not None and not entry.get("cycle_started_at"):
            hrs = float(entry["cycle_hours"])
            entry["cycle_started_at"] = format_local_iso(now_local)
            entry["next_reset_at"] = format_local_iso(now_local + timedelta(hours=hrs))
        runs = int(entry.get("cycle_runs") or 0) + 1
        entry["cycle_runs"] = runs
        exact_tokens = export_info.get("tokens")
        if exact_tokens is not None:
            entry["cycle_tokens"] = int(entry.get("cycle_tokens") or 0) + int(exact_tokens)
            entry["cycle_tokens_exact"] = runs == 1 or bool(entry.get("cycle_tokens_exact", False))
        else:
            est = max(1, (len(prompt or "") + len(output or "")) // 4)
            entry["cycle_tokens"] = int(entry.get("cycle_tokens") or 0) + est
            entry["cycle_tokens_exact"] = False
        cost_val = export_info.get("cost_usd")
        if cost_val is not None:
            spent = float(entry.get("cycle_cost_usd") or 0.0) + float(cost_val)
            entry["cycle_cost_usd"] = round(spent, 6)
        tok_budget = entry.get("cycle_token_budget")
        cost_budget = entry.get("cycle_cost_budget_usd")
        budget_hit = None
        if tok_budget is not None and entry["cycle_tokens"] >= int(tok_budget):
            budget_hit = f"Cycle token budget ({tok_budget}) reached"
        elif cost_budget is not None and float(entry.get("cycle_cost_usd") or 0.0) >= float(cost_budget):
            budget_hit = f"Cycle cost budget (${float(cost_budget):.2f}) reached"
        if budget_hit and entry.get("status") == "active":
            entry.update(status="cooldown", manual_disable=False, reason=budget_hit)
            entry["cooldown_until"] = entry.get("next_reset_at")
        save_quota_unlocked(data, path)
        return dict(entry)


def configure_cycle(
    provider_name, hours=None, token_budget=None, cost_budget=None, reset_now=False,
    effort=None, model=None, max_parallel=None, quota_path=None,
):
    """Update cycle length, budgets, concurrency cap, effort/model defaults, or reset now."""
    path = quota_file_path(quota_path)
    canon = normalize_lane(provider_name, allow_auto=False)
    with QUOTA_LOCK:
        data = read_and_prep_unlocked(path)
        providers = data.setdefault("providers", {})
        if canon not in providers:
            sys.stderr.write(f"error: provider {canon!r} not defined in {path}\n")
            sys.exit(2)
        entry = providers[canon]
        if hours is not None and hours <= 0:
            entry.update(cycle_hours=None, next_reset_at=None)
        elif hours is not None:
            entry["cycle_hours"] = float(hours)
            started = parse_iso_utc(entry.get("cycle_started_at"))
            if started is not None:
                wake_iso = format_local_iso(started + timedelta(hours=float(hours)))
                entry["next_reset_at"] = wake_iso
                if entry.get("status") == "cooldown" and not entry.get("manual_disable", False):
                    entry["cooldown_until"] = wake_iso
        if token_budget is not None:
            entry["cycle_token_budget"] = None if token_budget <= 0 else int(token_budget)
        if cost_budget is not None:
            entry["cycle_cost_budget_usd"] = None if cost_budget <= 0 else float(cost_budget)
        if max_parallel is not None:
            entry["max_parallel"] = None if max_parallel <= 0 else int(max_parallel)
        if effort is not None:
            entry["effort"] = effort.strip().lower()
        if model is not None:
            entry["model"] = normalize_model_for_provider(canon, model)
        if reset_now:
            entry.update(
                cycle_runs=0, cycle_tokens=0, cycle_tokens_exact=False, cycle_cost_usd=0.0,
                cycle_started_at=None, next_reset_at=None, status="active",
                cooldown_until=None, reason=None, manual_disable=False,
            )
        save_quota_unlocked(data, path)
        return canon, dict(entry)


def probe_muse_usage(timeout=4, quota_path=None):
    """Query `muse usage` headlessly; server telemetry omissions never override cooldown."""
    muse_bin = resolve_cli("muse")
    if not muse_bin:
        return {"installed": False, "fresh": False, "summary": "muse CLI not installed", "raw": ""}
    try:
        p = run([muse_bin, "usage"], timeout=timeout)
    except subprocess.TimeoutExpired:
        return {"installed": True, "fresh": False, "summary": f"timeout after {timeout}s", "raw": ""}
    except OSError as exc:
        return {"installed": True, "fresh": False, "summary": f"exec failed: {exc}", "raw": ""}
    raw = (p.stdout or "").strip()
    lower = raw.lower()
    if "server_sent_no_usage" in lower or "no usage data" in lower:
        summary = "telemetry omitted by server (server_sent_no_usage); using quota.json reset timer"
        return {"installed": True, "fresh": False, "summary": summary, "raw": raw}
    lines = [ln.strip() for ln in raw.splitlines() if ln.strip()]
    summary = lines[0] if lines else f"exit={p.returncode}"
    return {"installed": True, "fresh": False, "summary": summary, "raw": raw}
