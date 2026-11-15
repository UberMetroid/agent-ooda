"""E-M envelope: fleet regimes, lane selection, live discovery, and mismatch memory."""

from .compute_em import compute_em_envelope, model_family
from .discover_fleet import (
    discover_fleet_capabilities,
    discovery_cache_path,
    effective_lane_spec,
    load_discovery_cache,
)
from .mismatch_log import clear_mismatch, load_recent_mismatch, record_mismatch
from .probe_configs import (
    is_gemini_model,
    probe_agy_settings,
    probe_devin_config,
    probe_grok_config,
    probe_minimax_config,
    probe_muse_config,
    probe_vibe_config,
)
from .select_lane import build_candidate_order, select_provider

__all__ = [
    "model_family",
    "compute_em_envelope",
    "build_candidate_order",
    "select_provider",
    "load_recent_mismatch",
    "record_mismatch",
    "clear_mismatch",
    "is_gemini_model",
    "probe_agy_settings",
    "probe_devin_config",
    "probe_grok_config",
    "probe_minimax_config",
    "probe_muse_config",
    "probe_vibe_config",
    "discovery_cache_path",
    "load_discovery_cache",
    "discover_fleet_capabilities",
    "effective_lane_spec",
]
