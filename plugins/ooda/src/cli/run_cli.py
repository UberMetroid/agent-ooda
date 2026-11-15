"""Top-level ooda / router CLI dispatch: quota flags, recursion guard, modes, manifests."""

import sys

from dispatch import run_manifest_batch
from envelope import select_provider
from fleet import (
    MAX_JOBS, STRIPE_POOL, configure_cycle, load_quota, parse_lanes, parse_timeout,
    quota_file_path, repo_root, router_depth, set_provider_status,
)
from worktree import load_manifest, parse_csv_arg

from .build_args import build_parser
from .report_fleet import do_status, do_usage
from .run_actions import do_cleanup, do_recon, do_single_task, do_standalone_review


def _handle_quota_flags(args):
    """Apply --enable / --disable / --set-cycle; return exit code, or None if none given."""
    if args.enable:
        canon, _ = set_provider_status(args.enable, "active")
        print(f"enabled {canon} in {quota_file_path()}")
        return 0
    if args.disable:
        canon, entry = set_provider_status(
            args.disable, "cooldown", reason=args.reason, hours=args.hours, auto_wake=args.auto_wake,
        )
        until = f" until {entry['cooldown_until']}" if entry.get("cooldown_until") else ""
        print(f"disabled {canon} ({entry['reason']}{until}) in {quota_file_path()}")
        return 0
    if args.set_cycle:
        canon, entry = configure_cycle(
            args.set_cycle, hours=args.hours, token_budget=args.token_budget,
            cost_budget=args.cost_budget, reset_now=args.reset_now, effort=args.effort,
            model=args.model, max_parallel=args.max_parallel,
        )
        model_s = entry.get("model") or "cli-default"
        eff_s = entry.get("effort") or "cli-default"
        print(
            f"configured {canon}: status={entry['status']} model={model_s} "
            f"effort={eff_s} cycle_hours={entry['cycle_hours']} "
            f"next_reset_at={entry['next_reset_at']}"
        )
        return 0
    return None


def _check_recursion_guard(args):
    """Return 2 (with an error) when a dispatching mode runs inside a nested worker."""
    dispatching = (
        args.recon is not None or args.task is not None
        or args.review is not None or args.manifest is not None
    )
    if dispatching and router_depth() >= 1:
        sys.stderr.write(
            "error: recursive router dispatch blocked (ROUTER_DEPTH>=1). "
            "You are already running inside a dispatched worker — execute the task directly "
            "with your native tools instead of invoking router.\n"
        )
        return 2
    return None


def _dispatch_manifest(args):
    """Validate flags, preview routing, and run a parallel worktree manifest batch."""
    root = repo_root()
    parse_lanes(args.lane, allow_auto=True)
    if args.timeout is not None:
        parse_timeout(args.timeout)
    parse_csv_arg(args.exclude_lanes)
    jobs = max(1, min(args.jobs, MAX_JOBS))
    tasks = load_manifest(args.manifest)
    quota_data = load_quota()
    preview_lane, _, _, preview_note = select_provider(args.lane, None, quota_data)
    if not preview_lane:
        sys.stderr.write(f"error: no usable worker CLI found on this host ({preview_note})\n")
        return 2
    stripe_state = (
        "off (--no-stripe)"
        if args.no_stripe
        else f"on ({' -> '.join(quota_data.get('parallel_stripe_pool') or STRIPE_POOL)})"
    )
    print(f"default lane: {args.lane} -> {preview_lane} | jobs: {jobs} | tasks: {len(tasks)} | stripe: {stripe_state}")
    if preview_note:
        print(f"routing note: {preview_note}")
    return run_manifest_batch(args, root, quota_data, tasks, jobs)


def main(argv=None):
    """CLI entrypoint; returns a process exit code."""
    ap = build_parser()
    args = ap.parse_args(argv)
    if args.status:
        return do_status()
    if args.usage:
        return do_usage()
    rc = _handle_quota_flags(args)
    if rc is not None:
        return rc
    if args.recon is not None and args.task is not None:
        ap.error("--recon and --task are mutually exclusive")
    if args.cleanup and args.recon is None and args.task is None and args.review is None:
        do_cleanup(repo_root())
        return 0
    rc = _check_recursion_guard(args)
    if rc is not None:
        return rc
    if args.recon is not None:
        return do_recon(args)
    if args.task is not None:
        return do_single_task(args)
    if args.review is not None:
        target = args.review if args.review != "." else (args.manifest or ".")
        return do_standalone_review(target, args=args)
    if not args.manifest:
        repo_root()
        ap.error(
            "manifest path is required (or pass --recon, --task, --review, "
            "--status, --usage, --set-cycle, --enable, --disable, --cleanup)"
        )
    return _dispatch_manifest(args)


if __name__ == "__main__":
    sys.exit(main())
