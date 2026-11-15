"""Static lane catalog: schema version, pools, regimes, regexes, and provider specs."""

from datetime import datetime
import os
from pathlib import Path
import re
import threading

QUOTA_SCHEMA_VERSION = 7
CORNER_WINDOW_SECS = 5400
SOON_WAKE_SECS = 2700
DEVIN_FAST_MAX_PARALLEL = 4
MCODE_MAX_PARALLEL = 4
REVIEW_MODEL = (
    os.environ.get("AGENT_REVIEW_MODEL")
    or os.environ.get("OODA_REVIEW_MODEL")
    or "cli-default"
)
DEFAULT_AGY_OPUS_MODEL = "claude-opus-5-5-high"
DEFAULT_FUSION_MODEL = "fusion-claude-opus-5-5-high-sidekick-swe-2-medium"
DEFAULT_JOBS = 4
MAX_JOBS = 8
REGIME_TIMEOUTS = {
    "observe": 180,
    "fast": 300,
    "act": 480,
    "heavy": 600,
    "deep": 900,
    "verify": 240,
}
DEFAULT_TIMEOUT = REGIME_TIMEOUTS["act"]
REVIEW_TIMEOUT = REGIME_TIMEOUTS["verify"]
ID_RE = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._-]*$")
DURATION_RE = re.compile(r"^(\d+(?:\.\d+)?)\s*([smhd]?)$", re.IGNORECASE)
RESET_TS_RE = re.compile(
    r"(?:resets?\s+at|try\s+again\s+at|available\s+at|until)\s+"
    r"(\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:?\d{2}))",
    re.IGNORECASE,
)
VERDICT_RE = re.compile(
    r"^\s*(?:\*\*|__)?VERDICT(?:\*\*|__)?\s*:\s*(?:\*\*|__)?\s*(ACCEPT|REJECT)\b",
    re.IGNORECASE,
)
CORE_HIDDEN_SKILLS = frozenset(
    {
        "router",
        "agent-router",
        "ooda",
        "ooda:ooda",
        "ooda:router",
        "agy-customizations",
        "antigravity-guide",
        "antigravity_guide",
        "run-workflow",
        "swarm",
        "swarm_monitor",
    }
)

DEFAULT_POOL = [
    "agy-opus", "muse", "grok", "mistral", "devin-fusion", "devin-fast", "devin-pro", "mcode",
]
STRIPE_POOL = [
    "agy-opus", "muse", "grok", "mistral", "devin-fast", "devin-pro", "mcode", "devin-fusion",
]
FAST_ORDER = ["devin-fast", "mcode", "devin-pro", "muse"]
DEEP_ORDER = [
    "agy-opus", "muse", "grok", "mistral", "devin-fusion", "devin-pro", "devin-fast", "mcode",
]
REVIEW_ORDER = ["mcode", "devin-fast", "devin-pro", "grok", "mistral", "muse", "agy-opus"]
PROVIDER_ORDER = [
    "agy-opus",
    "devin-fusion",
    "devin-pro",
    "devin-fast",
    "muse",
    "mcode",
    "grok",
    "mistral",
]
MODEL_FAMILIES = {
    "agy-opus": "claude-opus",
    "devin-fusion": "claude-opus",
    "devin-pro": "swe-2",
    "devin-fast": "swe-2",
    "muse": "muse",
    "mcode": "minimax",
    "grok": "grok",
    "mistral": "glm",
}

FAST_COMPLEXITIES = frozenset(
    {"fast", "mechanical", "simple", "flash", "tests", "test", "docs", "doc", "recon"}
)
DEEP_COMPLEXITIES = frozenset(
    {"deep", "architecture", "arch", "reasoning", "audit", "ember"}
)

RECON_PREFIX = (
    "READ-ONLY RECONNAISSANCE: Do NOT modify, create, or delete any files. "
    "Answer concisely with exact file paths, line numbers, and findings:\n\n"
)

_HOME = Path.home()
_LOCAL_BIN = _HOME / ".local" / "bin"
CLI_FALLBACK_PATHS = {
    "agy": [_LOCAL_BIN / "agy"],
    "devin": [
        _LOCAL_BIN / "devin",
        _HOME / ".local" / "share" / "devin" / "cli" / "_versions" / "current" / "bin" / "devin",
    ],
    "mcode": [_HOME / ".minimax-code" / "bin" / "mcode", _LOCAL_BIN / "mcode"],
    "muse": [_LOCAL_BIN / "muse"],
    "grok": [_LOCAL_BIN / "grok", _HOME / ".grok" / "bin" / "grok"],
    "vibe": [
        _LOCAL_BIN / "vibe",
        _HOME / ".local" / "share" / "uv" / "tools" / "mistral-vibe" / "bin" / "vibe",
    ],
}

QUOTA_LOCK = threading.Lock()

# name -> (cli, model, effort, tier, cycle_hours, max_parallel, role)
PROVIDER_SPECS = {
    "agy-opus": (
        "agy", None, None, "capped", 5.0, None,
        "Antigravity agent pool (separate 5h quota pool; uses CLI default unless overridden)",
    ),
    "devin-fusion": (
        "devin", None, None, "credits", None, None,
        "Devin Fusion pool (paid credits; uses CLI default unless overridden)",
    ),
    "devin-pro": (
        "devin", None, None, "unlimited", 1.0, None,
        "Heavy multi-file implementation, complex bugs, refactors (Devin Pro pool)",
    ),
    "devin-fast": (
        "devin", None, None, "unlimited", 1.0, DEVIN_FAST_MAX_PARALLEL,
        "Fast mechanical sweeps, tests, boilerplate, read-only recon (max 4 parallel)",
    ),
    "muse": (
        "muse", None, None, "capped", 5.0, None,
        "Deep reasoning, architecture, second-opinion implementation (Muse pool)",
    ),
    "mcode": (
        "mcode", None, None, "high-cap", 5.0, MCODE_MAX_PARALLEL,
        "Primary code review + fast parallel coding & recon (5h cap, max 4 parallel)",
    ),
    "grok": (
        "grok", None, None, "capped", 5.0, None,
        "xAI Grok Build coding agent (Grok pool)",
    ),
    "mistral": (
        "vibe", None, None, "capped", 5.0, None,
        "Mistral Vibe coding agent (Mistral pool)",
    ),
}


def provider_entry(
    cli,
    model,
    effort,
    tier,
    role,
    cycle_hours,
    status="active",
    reason=None,
    max_parallel=None,
):
    """Return a fresh provider record with zeroed cycle counters."""
    return {
        "cli": cli,
        "model": model,
        "effort": effort,
        "tier": tier,
        "role": role,
        "status": status,
        "cooldown_until": None,
        "reason": reason,
        "manual_disable": False,
        "max_parallel": max_parallel,
        "cycle_hours": cycle_hours,
        "cycle_started_at": None,
        "next_reset_at": None,
        "cycle_runs": 0,
        "cycle_tokens": 0,
        "cycle_tokens_exact": False,
        "cycle_cost_usd": 0.0,
        "cycle_token_budget": None,
        "cycle_cost_budget_usd": None,
    }


def default_providers():
    """Return canonical provider configurations, all eight lanes active."""
    providers = {}
    for name in PROVIDER_ORDER:
        cli, model, effort, tier, cycle_hours, max_parallel, role = PROVIDER_SPECS[name]
        providers[name] = provider_entry(
            cli=cli,
            model=model,
            effort=effort,
            tier=tier,
            role=role,
            cycle_hours=cycle_hours,
            status="active",
            max_parallel=max_parallel,
        )
    return providers


def default_quota_data():
    """Return baseline quota.json structure when no state file exists on disk."""
    return {
        "version": QUOTA_SCHEMA_VERSION,
        "updated_at": datetime.now().astimezone().isoformat(timespec="seconds"),
        "drain_capped_first": True,
        "parallel_stripe_pool": list(STRIPE_POOL),
        "default_fallback_chain": list(DEFAULT_POOL),
        "providers": default_providers(),
    }
