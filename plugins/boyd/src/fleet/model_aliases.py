"""Lane / model alias maps, effort maps, error classifiers, and normalizers."""

from datetime import datetime, timezone
import os
import sys

from .lane_catalog import (
    DEEP_COMPLEXITIES,
    DEFAULT_AGY_OPUS_MODEL,
    DEFAULT_FUSION_MODEL,
    DURATION_RE,
    PROVIDER_ORDER,
    RESET_TS_RE,
    REVIEW_MODEL,
)
from .locate_cli import format_local_iso, parse_iso_utc

QUOTA_ERROR_PATTERNS = (
    "429", "rate limit", "rate_limit", "quota exceeded", "insufficient_quota",
    "usage limit", "too many requests", "credit balance", "insufficient credits",
    "out of credits", "credits exhausted", "limit reached", "resource_exhausted",
    "out of quota", "model quota", "quota exhausted", "limit exceeded",
)

AUTH_ERROR_PATTERNS = (
    "authentication required", "not logged in", "run 'agent login'",
    "invalid api key", "please log in", "auth login",
)

LANE_ALIASES = {
    "auto": "auto", "default": "auto",
    "agy-opus": "agy-opus", "apy-opus": "agy-opus", "agy": "agy-opus",
    "agy-claude": "agy-opus", "claude": "agy-opus", "opus": "agy-opus",
    "devin-fusion": "devin-fusion", "fusion": "devin-fusion",
    "devin": "devin-pro", "devin-pro": "devin-pro", "swe-2-pro": "devin-pro",
    "swe-2-max": "devin-pro", "pro": "devin-pro",
    "devin-fast": "devin-fast", "swe-2-fast": "devin-fast", "swe-2-flash": "devin-fast",
    "swe-2-flash-max": "devin-fast", "fast": "devin-fast", "flash": "devin-fast",
    "mcode": "mcode", "mcode-review": "mcode", "minimax": "mcode", "m3.1": "mcode",
    "m3": "mcode", "mcode-code": "mcode",
    "muse": "muse", "ember": "muse", "muse-ember": "muse", "muse-wonder": "muse",
    "muse-spark": "muse",
    "grok": "grok",
    "mistral": "mistral", "vibe": "mistral",
}

AGY_MODEL_ALIASES = {
    "opus": DEFAULT_AGY_OPUS_MODEL,
    "opus-high": DEFAULT_AGY_OPUS_MODEL,
    "claude-opus": DEFAULT_AGY_OPUS_MODEL,
    "opus-medium": "claude-opus-5-5-medium",
    "opus-low": "claude-opus-5-5-low",
    "sonnet": "claude-sonnet-5-5-high",
    "sonnet-high": "claude-sonnet-5-5-high",
    "claude-sonnet": "claude-sonnet-5-5-high",
    "sonnet-medium": "claude-sonnet-5-5-medium",
    "sonnet-low": "claude-sonnet-5-5-low",
}

MCODE_MODEL_ALIASES = {
    "minimax/m3.1-flash": "minimax/MiniMax-M3.1-Flash",
    "m3.1-flash": "minimax/MiniMax-M3.1-Flash",
    "m3.1": "minimax/MiniMax-M3.1-Flash",
    "flash": "minimax/MiniMax-M3.1-Flash",
    "minimax/m3": "minimax/MiniMax-M3",
    "m3": "minimax/MiniMax-M3",
}

DEVIN_MODEL_ALIASES = {
    "fusion": DEFAULT_FUSION_MODEL,
    "fusion-opus": DEFAULT_FUSION_MODEL,
    "fusion-sonnet": "fusion-claude-sonnet-5-5-high-sidekick-swe-2-medium",
}

MISTRAL_MODEL_ALIASES = {
    "glm": "glm-5-3",
    "glm-5.3": "glm-5-3",
    "glm5.3": "glm-5-3",
    "zai-glm-5-3": "glm-5-3",
    "ml4": "mistral-large-4-0-3",
    "mistral-large-4": "mistral-large-4-0-3",
    "mistral-large-4-0": "mistral-large-4-0-3",
}

GROK_EFFORT_MAP = {
    "none": "medium", "minimal": "medium", "low": "medium", "medium": "medium",
    "high": "high", "xhigh": "xhigh", "max": "xhigh", "ultra": "xhigh",
}

MCODE_EFFORT_MAP = {
    "default": "default", "none": "low", "minimal": "low", "low": "low",
    "medium": "medium", "high": "high", "xhigh": "xhigh", "max": "max", "ultra": "max",
}

MUSE_VALID_EFFORTS = frozenset(
    {"none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra"}
)

_DURATION_MULT = {"": 1, "s": 1, "m": 60, "h": 3600, "d": 86400}


def normalize_lane(raw_lane, allow_auto=True):
    """Map user-facing lane or model aliases to canonical provider names (exit 2 if unknown)."""
    key = (raw_lane or "auto").strip().lower()
    canon = LANE_ALIASES.get(key)
    if canon is None or (not allow_auto and canon == "auto"):
        valid = list(PROVIDER_ORDER)
        if allow_auto:
            valid.insert(0, "auto")
        sys.stderr.write(f"error: unknown provider/lane {raw_lane!r} (valid: {', '.join(valid)})\n")
        sys.exit(2)
    return canon


def normalize_model_for_provider(provider_name, model):
    """Normalize shorthand model aliases for any provider (mcode defaults to REVIEW_MODEL)."""
    if provider_name == "mcode" and not model:
        return REVIEW_MODEL
    if not model:
        return model
    key = model.strip().lower()
    table = {
        "agy-opus": AGY_MODEL_ALIASES,
        "mcode": MCODE_MODEL_ALIASES,
        "devin-fusion": DEVIN_MODEL_ALIASES,
        "mistral": MISTRAL_MODEL_ALIASES,
    }.get(provider_name)
    return table.get(key, model) if table else model


def resolve_effort(provider_name, info, explicit_effort, task_complexity):
    """Resolve effective reasoning effort: explicit, AGENT_/OODA_EFFORT env, then defaults."""
    env_effort = os.environ.get("AGENT_EFFORT") or os.environ.get("OODA_EFFORT") or None
    chosen = explicit_effort or env_effort
    if not chosen:
        comp = str(task_complexity or "").strip().lower()
        if provider_name in ("muse", "grok") and comp in DEEP_COMPLEXITIES:
            chosen = "xhigh"
        else:
            chosen = (info or {}).get("effort") or "high"
    return str(chosen).strip().lower()


def parse_timeout(value):
    """Parse seconds (int/float) or duration strings like '10m', '45s', '1h' (exit 2 if bad)."""
    if isinstance(value, bool):
        value = None
    if isinstance(value, (int, float)):
        secs = float(value)
    elif isinstance(value, str):
        m = DURATION_RE.match(value.strip())
        if not m:
            sys.stderr.write(f"error: invalid timeout {value!r} (use seconds or e.g. 10m, 45s)\n")
            sys.exit(2)
        secs = float(m.group(1)) * _DURATION_MULT[m.group(2).lower()]
    else:
        sys.stderr.write(f"error: invalid timeout {value!r}\n")
        sys.exit(2)
    if secs <= 0:
        sys.stderr.write(f"error: timeout must be > 0 (got {value!r})\n")
        sys.exit(2)
    return int(secs) if secs.is_integer() else secs


def is_quota_error(output_text, exit_code):
    """Detect quota, credit exhaustion, or rate-limit failures in non-zero worker output."""
    if exit_code == 0 or not output_text:
        return False
    lower = output_text.lower()
    return any(pat in lower for pat in QUOTA_ERROR_PATTERNS)


def is_auth_error(output_text, exit_code):
    """Detect missing/expired CLI authentication in non-zero worker output."""
    if exit_code == 0 or not output_text:
        return False
    lower = output_text.lower()
    return any(pat in lower for pat in AUTH_ERROR_PATTERNS)


def extract_reset_timestamp(output_text):
    """Extract a future ISO reset timestamp from CLI quota errors, formatted locally."""
    if not output_text:
        return None
    now_utc = datetime.now(timezone.utc)
    for m in RESET_TS_RE.finditer(output_text):
        dt = parse_iso_utc(m.group(1))
        if dt is not None and dt > now_utc:
            return format_local_iso(dt)
    return None
