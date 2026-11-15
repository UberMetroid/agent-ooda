"""Read-only non-secret local CLI configuration probes (vibe, muse, mcode, grok, devin, agy)."""

import json
import os
from pathlib import Path
import re

from fleet import GEMINI_MODEL_RE

_VIBE_ACTIVE_RE = re.compile(r'^\s*active_model\s*=\s*"([^"]+)"', re.MULTILINE)
_VIBE_INLINE_RE = re.compile(r"\{([^{}]+)\}")
_VIBE_ALIAS_RE = re.compile(r'\balias\s*=\s*"([^"]+)"')
_VIBE_THINK_RE = re.compile(r'\bthinking\s*=\s*"([^"]+)"')
_MM_DEFAULT_RE = re.compile(r"^\s*defaultModel\s*:\s*[\"']?([^\"'#\s]+)", re.MULTILINE)
_MM_EFFORT_RE = re.compile(
    r"^\s*defaultModelThinking\s*:\s*\n(?:\s*(?:#.*)?\n)*\s+effort\s*:\s*[\"']?([A-Za-z0-9_-]+)",
    re.MULTILINE,
)
_MM_MODEL_LINE_RE = re.compile(r"^\s*(?:-\s*)?((?:minimax/)?MiniMax-[A-Za-z0-9._-]+)\s*:?\s*$")
_GROK_DEF_RE = re.compile(r'^\s*default\s*=\s*"([^"]+)"', re.MULTILINE)
_GROK_EFF_RE = re.compile(r'^\s*default_reasoning_effort\s*=\s*"([^"]+)"', re.MULTILINE)
_DEVIN_EFF_RE = re.compile(r"-(low|medium|high|xhigh|max)$")


def is_gemini_model(model_name):
    """Return True when model_name refers to a Gemini or Orchestrator model."""
    return bool(model_name and GEMINI_MODEL_RE.search(str(model_name)))


def _read_safe(path, as_json=False):
    """Read UTF-8 text or parsed JSON dict from path cleanly (never raises on missing/bad files)."""
    try:
        text = Path(path).read_text(encoding="utf-8")
    except (OSError, UnicodeDecodeError):
        return {} if as_json else ""
    if not as_json:
        return text
    try:
        parsed = json.loads(text)
    except json.JSONDecodeError:
        return {}
    return parsed if isinstance(parsed, dict) else {}


def probe_vibe_config(home=None):
    """Read non-secret active_model and model/thinking table from Mistral Vibe config.toml."""
    base = Path(home) if home is not None else Path.home()
    candidates = [base / ".vibe" / "config.toml", base / ".config" / "vibe" / "config.toml"]
    if home is None and os.environ.get("VIBE_HOME"):
        candidates.insert(0, Path(os.environ["VIBE_HOME"]).expanduser() / "config.toml")
    text = ""
    for cand in candidates:
        text = _read_safe(cand)
        if text:
            break
    m_act = _VIBE_ACTIVE_RE.search(text)
    active_model = m_act.group(1).strip() if m_act else None
    alias_efforts, models = {}, []
    for block in _VIBE_INLINE_RE.findall(text):
        am = _VIBE_ALIAS_RE.search(block)
        if not am:
            continue
        alias = am.group(1).strip()
        tm = _VIBE_THINK_RE.search(block)
        think = tm.group(1).strip() if tm else None
        if think and alias not in alias_efforts:
            alias_efforts[alias] = think
        tag = f"{alias}[{think}]" if think else alias
        if tag not in models:
            models.append(tag)
    for sec in re.split(r"(?m)^\s*\[\[models\]\]\s*$", text)[1:]:
        chunk = sec.split("\n[", 1)[0]
        am = _VIBE_ALIAS_RE.search(chunk)
        if not am:
            continue
        alias = am.group(1).strip()
        tm = _VIBE_THINK_RE.search(chunk)
        think = tm.group(1).strip() if tm else None
        if think and alias not in alias_efforts:
            alias_efforts[alias] = think
        tag = f"{alias}[{think}]" if think else alias
        if tag not in models:
            models.append(tag)
    if active_model and not any(m == active_model or m.startswith(active_model + "[") for m in models):
        models.insert(0, active_model)
    return {
        "active_model": active_model,
        "active_effort": alias_efforts.get(active_model),
        "models": models,
        "alias_efforts": alias_efforts,
        "effort_mode": "via-model (no --effort flag)",
    }


def probe_muse_config(home=None):
    """Read non-secret model and per-model reasoning efforts from Muse settings.json."""
    base = Path(home) if home is not None else Path.home()
    cfg = _read_safe(base / ".config" / "muse" / "settings.json", as_json=True)
    model = cfg.get("model") if isinstance(cfg.get("model"), str) else None
    eff = cfg.get("reasoning_effort") if isinstance(cfg.get("reasoning_effort"), str) else None
    per_raw = cfg.get("model_reasoning_efforts")
    per_model = per_raw if isinstance(per_raw, dict) else {}
    alias_efforts = {str(k): str(v) for k, v in per_model.items() if isinstance(v, str)}
    active_effort = alias_efforts.get(model) or eff
    models = [f"{k}[{v}]" for k, v in alias_efforts.items()]
    if model and model not in alias_efforts:
        models.insert(0, model)
    return {
        "active_model": model,
        "active_effort": active_effort,
        "models": models,
        "alias_efforts": alias_efforts,
        "effort_mode": "--reasoning-effort (none..ultra)",
    }


def probe_minimax_config(home=None):
    """Read non-secret defaultModel, effort, and MiniMax models from mcode config.yaml."""
    base = Path(home) if home is not None else Path.home()
    mm_dir = (
        Path(os.environ["MINIMAX_DATA_DIR"]).expanduser()
        if home is None and os.environ.get("MINIMAX_DATA_DIR")
        else base / ".minimax"
    )
    text = _read_safe(mm_dir / "config.yaml")
    m_def = _MM_DEFAULT_RE.search(text)
    active_model = m_def.group(1).strip() if m_def else None
    m_eff = _MM_EFFORT_RE.search(text)
    active_effort = m_eff.group(1).strip().lower() if m_eff else None
    models = [active_model] if active_model else []
    for raw_line in text.splitlines():
        clean = raw_line.split("#", 1)[0]
        mm = _MM_MODEL_LINE_RE.match(clean)
        if not mm:
            continue
        mid = mm.group(1)
        full = mid if mid.startswith("minimax/") else f"minimax/{mid}"
        if full not in models:
            models.append(full)
    return {
        "active_model": active_model,
        "active_effort": active_effort,
        "models": models,
        "alias_efforts": {},
        "effort_mode": "--effort (low|medium|high|xhigh|max)",
    }


def probe_grok_config(home=None):
    """Read non-secret default model, effort, and cached models from ~/.grok."""
    base = Path(home) if home is not None else Path.home()
    toml_text = _read_safe(base / ".grok" / "config.toml")
    m_def = _GROK_DEF_RE.search(toml_text)
    active_model = m_def.group(1).strip() if m_def else None
    m_eff = _GROK_EFF_RE.search(toml_text)
    active_effort = m_eff.group(1).strip().lower() if m_eff else None
    cache = _read_safe(base / ".grok" / "models_cache.json", as_json=True)
    raw_models = cache.get("models") if isinstance(cache.get("models"), dict) else {}
    models = []
    for mid, meta in raw_models.items():
        info = meta.get("info") if isinstance(meta, dict) else {}
        if isinstance(info, dict) and info.get("hidden") is True:
            continue
        if mid not in models and not is_gemini_model(mid):
            models.append(str(mid))
    if active_model and active_model not in models and not is_gemini_model(active_model):
        models.insert(0, active_model)
    return {
        "active_model": active_model,
        "active_effort": active_effort,
        "models": models,
        "alias_efforts": {},
        "effort_mode": "--reasoning-effort (medium|high|xhigh)",
    }


def probe_devin_config(home=None):
    """Read per-pool family models (fusion, swe-2, swe-2-flash) from devin config.json."""
    base = Path(home) if home is not None else Path.home()
    cfg = _read_safe(base / ".config" / "devin" / "config.json", as_json=True)
    agent = cfg.get("agent") if isinstance(cfg.get("agent"), dict) else {}
    prefs_raw = agent.get("preferred_family_models")
    prefs = prefs_raw if isinstance(prefs_raw, dict) else {}
    lane_models = {
        "devin-fusion": prefs.get("fusion"),
        "devin-pro": prefs.get("swe-2") or agent.get("model"),
        "devin-fast": prefs.get("swe-2-flash") or agent.get("subagent_model") or cfg.get("model"),
    }
    lane_efforts = {}
    for lane, mod in lane_models.items():
        if isinstance(mod, str):
            em = _DEVIN_EFF_RE.search(mod.lower())
            if em:
                lane_efforts[lane] = em.group(1)
    models = []
    for val in list(lane_models.values()) + list(prefs.values()):
        if isinstance(val, str) and val and val not in models and not is_gemini_model(val):
            models.append(val)
    return {
        "lane_models": lane_models,
        "lane_efforts": lane_efforts,
        "models": models,
        "effort_mode": "via-model (no --effort flag)",
    }


def probe_agy_settings(home=None):
    """Read active agy model from ~/.gemini/antigravity-cli/settings.json and check Gemini guard."""
    base = Path(home) if home is not None else Path.home()
    cfg = _read_safe(base / ".gemini" / "antigravity-cli" / "settings.json", as_json=True)
    model = cfg.get("model") if isinstance(cfg.get("model"), str) else None
    return {
        "settings_model": model,
        "gemini_blocked": is_gemini_model(model),
        "effort_mode": "--effort (low..max) + model tier",
    }
