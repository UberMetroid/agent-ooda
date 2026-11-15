"""argparse construction for the boyd / router CLI (all legacy router flags preserved)."""

import argparse
import os
import sys

from fleet import DEFAULT_JOBS


def _add_execution_flags(ap):
    """Register worker routing, model, effort, timeout, and review-toggle flags."""
    ap.add_argument(
        "--lane", default="auto",
        help="worker lane (auto, agy-opus, devin-fusion, devin-pro, devin-fast, muse, mcode, grok, mistral)",
    )
    ap.add_argument("--exclude-lane", dest="exclude_lanes", action="append", default=[],
                    help="comma-separated lane(s) to exclude (e.g. after a failed OODA loop)")
    ap.add_argument("--no-pivot", action="store_true",
                    help="disable automatic cross-model mismatch pivot on crash/timeout/reject")
    ap.add_argument("--model", default=None, help="override worker model id (or set with --set-cycle)")
    ap.add_argument("--effort", default=None,
                    help="reasoning effort override (default: provider standard; auto-bumps muse/grok on deep)")
    ap.add_argument("--timeout", default=None,
                    help="per-task timeout override (default: fast-transient 3m observe, 5m fast, 8m act, 10m heavy, 15m deep)")
    ap.add_argument("--no-review", action="store_true",
                    help="skip automatic post-worker M3.1-Flash code review")


def _add_quota_flags(ap):
    """Register quota.json provider status and cycle configuration flags."""
    ap.add_argument("--enable", metavar="PROVIDER",
                    help="mark PROVIDER active in quota.json (e.g. agy-opus, devin-fusion, grok, mistral)")
    ap.add_argument("--disable", metavar="PROVIDER",
                    help="put PROVIDER into cooldown in quota.json")
    ap.add_argument("--auto-wake", action="store_true",
                    help="allow --disable to automatically wake at --hours or provider cycle reset")
    ap.add_argument("--reason", default=None, help="optional cooldown reason for --disable")
    ap.add_argument("--hours", type=float, default=None,
                    help="duration in hours for --disable or cycle window for --set-cycle")
    ap.add_argument("--set-cycle", metavar="PROVIDER",
                    help="configure PROVIDER cycle schedule/budgets/defaults or reset counters now")
    ap.add_argument("--token-budget", type=int, default=None,
                    help="optional per-cycle token budget for --set-cycle (0 clears)")
    ap.add_argument("--cost-budget", type=float, default=None,
                    help="optional per-cycle USD budget for --set-cycle (0 clears)")
    ap.add_argument("--max-parallel", type=int, default=None,
                    help="optional max concurrent worker cap for --set-cycle (0 clears)")
    ap.add_argument("--reset-now", action="store_true",
                    help="with --set-cycle, zero cycle counters and wake provider immediately")


def build_parser():
    """Construct the CLI argument parser (prog follows the invoked binary name)."""
    prog = os.path.basename((sys.argv[0] if sys.argv else "") or "boyd") or "boyd"
    ap = argparse.ArgumentParser(
        prog=prog,
        description="Multi-agent token router, single-shot OODA observe/act dispatcher, "
                    "parallel worktree runner, cycle tracker, and M3.1-Flash auto-reviewer.",
    )
    ap.add_argument("manifest", nargs="?", help="JSON manifest path ([], {tasks: []}, or {subtasks: []})")
    ap.add_argument("--recon", metavar="PROMPT", default=None,
                    help="run a read-only reconnaissance query in CWD and print findings")
    ap.add_argument("--task", metavar="PROMPT", default=None,
                    help="run a single coding task directly in CWD (plus M3.1 review unless --no-review)")
    ap.add_argument("--files", action="append", default=[],
                    help="comma-separated files in scope for --task or --recon (repeatable)")
    ap.add_argument("--skill", "--skills", dest="skills", action="append", default=[],
                    help="skill name(s) from ~/.agents/skills/<name>/SKILL.md to inject into worker prompt")
    ap.add_argument("--complexity", default=None,
                    help="task routing hint (heavy, deep, architecture, fast, tests, recon, review)")
    ap.add_argument("--jobs", "-j", "--slots", dest="jobs", type=int, default=DEFAULT_JOBS,
                    help="max parallel workers for manifest mode (default 4, max 8)")
    ap.add_argument("--no-stripe", action="store_true",
                    help="disable round-robin provider striping across parallel auto manifest tasks")
    _add_execution_flags(ap)
    ap.add_argument("--review", nargs="?", const=".", default=None, metavar="PATH",
                    help="run standalone read-only M3.1-Flash code review on PATH (default CWD) and exit")
    ap.add_argument("--status", action="store_true",
                    help="show agent quota state, local CLI readiness, reset timers, and routing order")
    ap.add_argument("--usage", action="store_true",
                    help="show per-lane cycle token/cost telemetry, reset countdowns, and live muse usage")
    _add_quota_flags(ap)
    ap.add_argument("--cleanup", action="store_true",
                    help="remove .worktrees and ooda/* branches, then exit")
    return ap
