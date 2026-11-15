"""E-M envelope: fleet regimes, lane selection, and cross-model mismatch memory."""

from .compute_em import compute_em_envelope, model_family
from .select_lane import build_candidate_order, select_provider
from .mismatch_log import clear_mismatch, load_recent_mismatch, record_mismatch

__all__ = [
    "model_family",
    "compute_em_envelope",
    "build_candidate_order",
    "select_provider",
    "load_recent_mismatch",
    "record_mismatch",
    "clear_mismatch",
]
