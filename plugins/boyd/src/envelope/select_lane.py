"""Lane selection: E-M envelope candidate ordering and first-viable provider pick."""

from datetime import datetime, timezone

from fleet import (
    DEEP_COMPLEXITIES,
    DEEP_ORDER,
    DEFAULT_POOL,
    FAST_COMPLEXITIES,
    FAST_ORDER,
    REVIEW_ORDER,
    STRIPE_POOL,
    count_active_leases,
    normalize_lane,
    parse_iso_utc,
    resolve_cli,
    router_depth,
)

from .compute_em import compute_em_envelope, model_family

_CONSERVE_LEADERS = ["devin-pro", "devin-fast", "mcode"]


def _capped_sort_key(item):
    """Sort capped lanes by soonest reset clock, then shortest cycle, then config order."""
    idx, name, info = item
    reset_ts = info.get("next_reset_at") or info.get("cooldown_until")
    reset_dt = parse_iso_utc(reset_ts) if reset_ts else None
    has_clock = 0 if reset_dt is not None else 1
    dt_val = reset_dt if reset_dt is not None else datetime.max.replace(tzinfo=timezone.utc)
    cycle_raw = info.get("cycle_hours")
    cycle_h = float(cycle_raw) if cycle_raw is not None else 9999.0
    return (has_clock, dt_val, cycle_h, idx, name)


def _fast_order(providers, chain, em):
    """Fast lanes: free non-capped first, then corner + deferred capped; never credits."""
    ordered = []
    deferred = []
    for name in dict.fromkeys(list(FAST_ORDER) + chain):
        tier = (providers.get(name) or {}).get("tier")
        if tier == "credits":
            continue
        if tier == "capped" or name == "agy-opus":
            deferred.append(name)
        else:
            ordered.append(name)
    tail = list(dict.fromkeys(em["corner_lanes"] + deferred))
    tail_set = set(tail)
    return [n for n in ordered if n not in tail_set] + tail


def _normal_order(comp, providers, chain, pool, em, capped_active, stripe_index):
    """Raw (non-deduplicated) order for review, high-G, and default complexities."""
    if comp == "review":
        return [
            n for n in list(REVIEW_ORDER) + chain
            if (providers.get(n) or {}).get("tier") != "credits"
        ]
    if comp in DEEP_COMPLEXITIES or comp == "heavy":
        base = list(DEEP_ORDER) if comp in DEEP_COMPLEXITIES else list(DEFAULT_POOL)
        return em["corner_lanes"] + capped_active + base + chain
    if em["conserve_capped"]:
        return em["corner_lanes"] + _CONSERVE_LEADERS + capped_active + chain
    if stripe_index is not None and pool:
        offset = stripe_index % len(pool)
        base = pool[offset:] + pool[:offset]
    else:
        base = list(DEFAULT_POOL)
    return em["corner_lanes"] + capped_active + base + chain


def build_candidate_order(
    requested_lane,
    task_complexity,
    quota_data,
    stripe_index=None,
    avoid_families=None,
):
    """Return ordered list of candidate provider names using E-M envelope & family rotation."""
    providers = quota_data.get("providers") or {}
    chain = list(quota_data.get("default_fallback_chain") or DEFAULT_POOL)
    pool = list(quota_data.get("parallel_stripe_pool") or STRIPE_POOL)
    canon = normalize_lane(requested_lane or "auto", allow_auto=True)
    if canon != "auto":
        return [canon] + [p for p in chain if p != canon]
    em = compute_em_envelope(quota_data)
    comp = str(task_complexity or "").strip().lower()
    capped_active = []
    if quota_data.get("drain_capped_first", True):
        capped_items = [
            (idx, name, info)
            for idx, (name, info) in enumerate(providers.items())
            if isinstance(info, dict)
            and info.get("tier") == "capped"
            and info.get("status") == "active"
        ]
        capped_items.sort(key=_capped_sort_key)
        capped_active = [name for _, name, _ in capped_items]
    if comp in FAST_COMPLEXITIES:
        ordered = _fast_order(providers, chain, em)
    else:
        raw = _normal_order(comp, providers, chain, pool, em, capped_active, stripe_index)
        ordered = list(dict.fromkeys(raw))
    if avoid_families:
        avoid_set = set(avoid_families)
        ordered = [p for p in ordered if model_family(p) not in avoid_set] + [
            p for p in ordered if model_family(p) in avoid_set
        ]
    return ordered


def select_provider(
    requested_lane,
    task_complexity,
    quota_data,
    exclude=None,
    stripe_index=None,
    avoid_families=None,
    defer_at_capacity=True,
):
    """Pick first active + installed provider, deferring lanes at max_parallel capacity in auto mode."""
    excluded = set(exclude or ())
    providers = quota_data.get("providers") or {}
    auto = normalize_lane(requested_lane or "auto", allow_auto=True) == "auto"
    candidates = build_candidate_order(
        requested_lane, task_complexity, quota_data,
        stripe_index=stripe_index, avoid_families=avoid_families,
    )
    depth = router_depth()
    skipped = []
    at_capacity = []
    for name in candidates:
        info = providers.get(name)
        if name in excluded:
            skipped.append(f"{name}: already attempted")
        elif name == "agy-opus" and depth >= 1:
            skipped.append("agy-opus: blocked inside nested ROUTER_DEPTH>=1")
        elif not info:
            skipped.append(f"{name}: not configured")
        elif info.get("status") != "active":
            why = info.get("reason") or info.get("status")
            skipped.append(f"{name}: {info.get('status')} ({why})")
        elif not (cli_path := resolve_cli(info["cli"])):
            skipped.append(f"{name}: cli '{info['cli']}' not installed")
        else:
            max_p = int(info.get("max_parallel") or 0)
            if auto and defer_at_capacity and max_p > 0 and count_active_leases(name, max_p) >= max_p:
                at_capacity.append((name, info, cli_path))
                skipped.append(f"{name}: {max_p}/{max_p} parallel slots busy")
                continue
            return name, info, cli_path, ("; ".join(skipped) if skipped else None)
    if at_capacity:
        name, info, cli_path = at_capacity[0]
        return name, info, cli_path, ("; ".join(skipped) if skipped else None)
    return None, None, None, "; ".join(skipped) if skipped else "no configured providers"
