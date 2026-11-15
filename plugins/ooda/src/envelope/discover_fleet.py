"""Live CLI model catalog discovery, caching, and effective per-lane resolution."""

import json
import os
from pathlib import Path
import re
import subprocess
import time

from fleet import (
    PROVIDER_ORDER,
    atomic_write_json,
    load_quota,
    normalize_model_for_provider,
    now_iso,
    quota_file_path,
    resolve_cli,
    resolve_effort,
    run,
)

from .probe_configs import (
    is_gemini_model,
    probe_agy_settings,
    probe_devin_config,
    probe_grok_config,
    probe_minimax_config,
    probe_muse_config,
    probe_vibe_config,
)

DISCOVERY_TTL_SECS = 21600
_DEVIN_LINE_RE = re.compile(r"^  ([a-z0-9][a-z0-9._-]+)\s{2,}.+?\[([^\]]+)\]")
_GROK_LINE_RE = re.compile(r"^\s*[*-]\s+([A-Za-z0-9._-]+)")
_TIER_SUFFIX_RE = re.compile(r"-(low|medium|high|xhigh|max)$")


def discovery_cache_path():
    """Return path to the cached CLI model catalog (defaults beside quota.json)."""
    env = os.environ.get("OODA_DISCOVERY_FILE")
    if env:
        return Path(env).expanduser()
    return quota_file_path().with_name("discovery.json")


def _parse_cli_models(cli_name, stdout):
    """Parse non-Gemini model slugs from `agy|devin|grok models` output."""
    out = []
    for line in (stdout or "").splitlines():
        item = None
        if cli_name == "agy" and "\t" in line:
            slug, label = line.split("\t", 1)
            s = slug.strip()
            if s and not is_gemini_model(s) and not is_gemini_model(label):
                item = s
        elif cli_name == "devin":
            m = _DEVIN_LINE_RE.match(line)
            if m and not is_gemini_model(m.group(1)):
                item = f"{m.group(1)}[free]" if "free" in m.group(2).lower() else m.group(1)
        elif cli_name == "grok":
            m = _GROK_LINE_RE.match(line)
            if m and not is_gemini_model(m.group(1)):
                item = m.group(1)
        if item and item not in out:
            out.append(item)
    return out


def _probe_cli_models(cli_name, timeout=5):
    """Run `<cli> models` when binary exists on disk; return parsed non-Gemini list."""
    cli_path = resolve_cli(cli_name)
    if not cli_path or not Path(cli_path).is_file():
        return []
    try:
        p = run([cli_path, "models"], timeout=timeout)
    except (subprocess.TimeoutExpired, OSError):
        return []
    return _parse_cli_models(cli_name, p.stdout or "") if p.returncode == 0 else []


def load_discovery_cache(force_refresh=False, probe_agy=True):
    """Load or refresh cached `agy|devin|grok models` lists in discovery.json."""
    cpath = discovery_cache_path()
    old = {}
    if cpath.is_file():
        try:
            loaded = json.loads(cpath.read_text(encoding="utf-8"))
            if isinstance(loaded, dict):
                old = loaded
        except (OSError, json.JSONDecodeError):
            old = {}
    cached = old.get("cli_models") if isinstance(old.get("cli_models"), dict) else {}
    fresh = bool(cached) and (time.time() - float(old.get("ts") or 0)) < DISCOVERY_TTL_SECS
    if force_refresh:
        cli_models = {cli: _probe_cli_models(cli) for cli in ("agy", "devin", "grok")}
        atomic_write_json(
            cpath, {"version": 1, "updated_at": now_iso(), "ts": time.time(), "cli_models": cli_models}
        )
        return cli_models
    if probe_agy and not fresh and os.environ.get("OODA_NO_AUTO_PROBE") != "1":
        cli_models = dict(cached)
        cli_models["agy"] = _probe_cli_models("agy")
        atomic_write_json(
            cpath, {"version": 1, "updated_at": now_iso(), "ts": time.time(), "cli_models": cli_models}
        )
        return cli_models
    return cached


def _pick_agy_worker_model(agy_cfg, agy_models, effort=None):
    """Select a safe non-Gemini worker model for agy-opus from settings or `agy models`."""
    clean = [m.split("[", 1)[0] for m in (agy_models or []) if not is_gemini_model(m)]
    sm = agy_cfg.get("settings_model")
    if sm and not agy_cfg.get("gemini_blocked") and " " not in sm:
        em = _TIER_SUFFIX_RE.search(sm.lower())
        return None, sm, (em.group(1) if em else "default"), False
    tier = str(effort or "high").lower()
    if tier not in ("low", "medium", "high"):
        tier = "high"
    rules = (
        lambda m: m.startswith("claude-opus-") and m.endswith(f"-{tier}"),
        lambda m: m.startswith("claude-opus-") and m.endswith("-high"),
        lambda m: m.startswith("claude-opus-"),
        lambda m: m.startswith("claude-"),
        lambda _m: True,
    )
    for pred in rules:
        picked = next((m for m in clean if pred(m)), None)
        if picked:
            em = _TIER_SUFFIX_RE.search(picked.lower())
            return picked, picked, (em.group(1) if em else "default"), False
    return None, None, "default", bool(agy_cfg.get("gemini_blocked"))


def discover_fleet_capabilities(quota_data=None, home=None, force_refresh=False, probe_agy=True):
    """Discover real local models, effort mechanics, and Gemini guard state per lane."""
    qdata = load_quota() if quota_data is None else quota_data
    providers = qdata.get("providers") or {}
    agy_cfg, devin_cfg = probe_agy_settings(home), probe_devin_config(home)
    single_cfgs = {
        "muse": probe_muse_config(home), "mcode": probe_minimax_config(home),
        "grok": probe_grok_config(home), "mistral": probe_vibe_config(home),
    }
    use_cache = home is None or bool(os.environ.get("OODA_DISCOVERY_FILE"))
    cli_models = load_discovery_cache(force_refresh=force_refresh, probe_agy=probe_agy) if use_cache else {}
    caps = {}
    for lane in PROVIDER_ORDER:
        info = providers.get(lane) or {}
        pin_m, pin_e = info.get("model"), info.get("effort")
        if lane == "agy-opus":
            d_m, eff_m, eff_e, unsafe = _pick_agy_worker_model(agy_cfg, cli_models.get("agy"), pin_e)
            guard = f"settings={agy_cfg.get('settings_model')!r} blocked" if agy_cfg["gemini_blocked"] else ""
            caps[lane] = {
                "dispatch_model": pin_m or d_m, "effort_mode": agy_cfg["effort_mode"],
                "effective_model": pin_m or eff_m or ("BLOCKED(gemini)" if unsafe else "default"),
                "effective_effort": pin_e or eff_e, "models": list(cli_models.get("agy") or []),
                "alias_efforts": {}, "gemini_guard": guard, "agy_unsafe": unsafe and not pin_m,
            }
        elif lane in ("devin-fusion", "devin-pro", "devin-fast"):
            dm = devin_cfg["lane_models"].get(lane)
            all_m = list(dict.fromkeys(devin_cfg["models"] + list(cli_models.get("devin") or [])))
            caps[lane] = {
                "dispatch_model": pin_m or dm, "effort_mode": devin_cfg["effort_mode"],
                "effective_model": pin_m or dm or "default",
                "effective_effort": pin_e or devin_cfg["lane_efforts"].get(lane) or "default",
                "models": all_m, "alias_efforts": {}, "gemini_guard": "", "agy_unsafe": False,
            }
        else:
            cfg = single_cfgs[lane]
            extra = cli_models.get("grok") if lane == "grok" else []
            eff_m = pin_m or cfg.get("active_model") or "default"
            alias_effs = cfg.get("alias_efforts") or {}
            caps[lane] = {
                "dispatch_model": pin_m, "effort_mode": cfg["effort_mode"],
                "effective_model": eff_m,
                "effective_effort": pin_e or alias_effs.get(eff_m) or cfg.get("active_effort") or "default",
                "models": list(dict.fromkeys(list(cfg["models"]) + list(extra or []))),
                "alias_efforts": alias_effs, "gemini_guard": "", "agy_unsafe": False,
            }
    return caps


def effective_lane_spec(
    provider_name, info, explicit_model=None, explicit_effort=None, complexity=None, home=None
):
    """Resolve (argv_model, display_model, argv_effort, display_effort) with Gemini guard."""
    env_model = os.environ.get("AGENT_MODEL") or os.environ.get("OODA_MODEL") or None
    raw_model = explicit_model or env_model or (info or {}).get("model")
    norm_model = normalize_model_for_provider(provider_name, raw_model)
    norm_effort = resolve_effort(provider_name, info, explicit_effort, complexity)
    need_agy = provider_name == "agy-opus" and not norm_model
    cap = discover_fleet_capabilities(home=home, probe_agy=need_agy).get(provider_name) or {}
    argv_model = norm_model or cap.get("dispatch_model")
    if need_agy and norm_effort:
        tier = str(norm_effort).lower()
        match = next(
            (m for m in cap.get("models") or [] if m.startswith("claude-opus-") and m.endswith(f"-{tier}")),
            None,
        )
        if match:
            argv_model = match
    disp_model = argv_model or cap.get("effective_model") or "default"
    alias_eff = (cap.get("alias_efforts") or {}).get(disp_model)
    em = _TIER_SUFFIX_RE.search(str(disp_model).lower()) if disp_model else None
    devin_or_agy = provider_name in ("agy-opus", "devin-fusion", "devin-pro", "devin-fast")
    inferred = em.group(1) if (devin_or_agy and em) else None
    disp_effort = norm_effort or alias_eff or inferred or cap.get("effective_effort") or "default"
    return argv_model, disp_model, norm_effort, disp_effort
