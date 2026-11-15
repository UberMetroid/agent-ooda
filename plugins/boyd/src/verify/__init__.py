"""Verify stage: diff gathering and independent mcode M3.1 review (no dispatch/hud imports)."""

from .gather_diff import collect_diff_context
from .audit_diff import build_review_prompt, parse_review_verdict, run_mcode_review

__all__ = [
    "collect_diff_context",
    "parse_review_verdict",
    "build_review_prompt",
    "run_mcode_review",
]
