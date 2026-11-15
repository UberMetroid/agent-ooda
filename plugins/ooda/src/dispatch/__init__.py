"""Dispatch stage: worker command building, routed fallback execution, and manifest fan-out."""

from .telemetry import (
    hud_review_hooks,
    hud_worker_finish,
    hud_worker_start,
    parse_agy_output,
    parse_vibe_export,
    short_model,
    visible_extra_skills,
)
from .build_cmd import build_worker_command, execute_once
from .run_fallback import resolve_task_timeout, run_task_with_fallback
from .fanout_batch import process_task, run_manifest_batch

__all__ = [
    "visible_extra_skills",
    "short_model",
    "hud_worker_start",
    "hud_worker_finish",
    "hud_review_hooks",
    "parse_vibe_export",
    "parse_agy_output",
    "build_worker_command",
    "execute_once",
    "resolve_task_timeout",
    "run_task_with_fallback",
    "process_task",
    "run_manifest_batch",
]
