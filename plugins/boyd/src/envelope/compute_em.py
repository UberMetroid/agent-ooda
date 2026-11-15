"""E-M fleet energy envelope: CORNER / CRUISE / CONSERVE / RESERVE / REGEN regimes."""

from datetime import datetime, timezone

from fleet import CORNER_WINDOW_SECS, MODEL_FAMILIES, SOON_WAKE_SECS, parse_iso_utc


def model_family(provider_name):
    """Map provider lane to underlying model family for cross-model mismatch pivoting."""
    return MODEL_FAMILIES.get(provider_name, provider_name)


def _scan_providers(providers, now_dt):
    """Return (active_capped, sorted corner_lanes, soonest_regen_s) for the fleet."""
    active_capped = []
    corner_items = []
    soonest_regen_s = None
    for idx, (name, info) in enumerate(providers.items()):
        if not isinstance(info, dict):
            continue
        status = info.get("status")
        if info.get("tier") == "capped" and status == "active":
            active_capped.append(name)
            reset_ts = info.get("next_reset_at") or info.get("cooldown_until")
            reset_dt = parse_iso_utc(reset_ts) if reset_ts else None
            if reset_dt is not None:
                rem_s = (reset_dt - now_dt).total_seconds()
                if 0 < rem_s <= CORNER_WINDOW_SECS:
                    corner_items.append((rem_s, idx, name))
        elif status != "active" and not info.get("manual_disable", False):
            cd_ts = info.get("cooldown_until") or info.get("next_reset_at")
            cd_dt = parse_iso_utc(cd_ts) if cd_ts else None
            if cd_dt is not None:
                rem_s = (cd_dt - now_dt).total_seconds()
                if rem_s > 0 and (soonest_regen_s is None or rem_s < soonest_regen_s):
                    soonest_regen_s = rem_s
    corner_items.sort()
    return active_capped, [name for _, _, name in corner_items], soonest_regen_s


def _assign_regimes(providers, corner_set, conserve_capped):
    """Classify every configured provider into its E-M regime."""
    regimes = {}
    for name, info in providers.items():
        if not isinstance(info, dict):
            continue
        if info.get("status") != "active":
            regimes[name] = "REGEN"
        elif info.get("tier") == "credits":
            regimes[name] = "RESERVE"
        elif name in corner_set:
            regimes[name] = "CORNER"
        elif info.get("tier") == "capped" and conserve_capped:
            regimes[name] = "CONSERVE"
        else:
            regimes[name] = "CRUISE"
    return regimes


def compute_em_envelope(quota_data, now_utc=None):
    """Evaluate E-M fleet energy envelope: CORNER / CRUISE / CONSERVE / RESERVE / REGEN."""
    now_dt = now_utc or datetime.now(timezone.utc)
    providers = quota_data.get("providers") or {}
    active_capped, corner_lanes, soonest_regen_s = _scan_providers(providers, now_dt)
    conserve_capped = bool(
        len(active_capped) <= 1
        and len(corner_lanes) == 0
        and (soonest_regen_s is None or soonest_regen_s > SOON_WAKE_SECS)
    )
    return {
        "now_utc": now_dt,
        "active_capped": active_capped,
        "corner_lanes": corner_lanes,
        "soonest_regen_s": soonest_regen_s,
        "conserve_capped": conserve_capped,
        "regimes": _assign_regimes(providers, set(corner_lanes), conserve_capped),
    }
