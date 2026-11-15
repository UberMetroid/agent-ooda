"""Single-shot CLI actions: standalone review, recon, direct task, and worktree cleanup."""

import json
from pathlib import Path
import shutil
import sys

from dispatch import hud_review_hooks, run_task_with_fallback
from envelope import clear_mismatch, load_recent_mismatch, record_mismatch
from fleet import (
    ID_RE, RECON_PREFIX, REVIEW_MODEL, normalize_lane, parse_timeout, repo_root,
    resolve_cli, run, slots_root_path, try_repo_root,
)
from verify import collect_diff_context, run_mcode_review
from worktree import changed_files_in, ensure_slot, find_scope_violations, parse_csv_arg, parse_files_arg


def do_standalone_review(target_arg):
    """Run standalone M3.1 code review against a directory."""
    target = Path(target_arg or ".").expanduser().resolve()
    if not target.is_dir():
        sys.stderr.write(f"error: review target is not a directory: {target}\n")
        return 2
    root = repo_root(target)
    if not resolve_cli("mcode"):
        sys.stderr.write("error: mcode binary not found (tried PATH and ~/.minimax-code/bin/mcode)\n")
        return 2
    slot = ensure_slot("review-standalone")
    diff_ctx = collect_diff_context(root, include_last_commit_fallback=True)
    if not diff_ctx["has_changes"]:
        print(f"No uncommitted changes or commit diff found to review in {root}.")
        return 0
    print(f"Reviewing {diff_ctx['source']} in {root} with {REVIEW_MODEL}...")
    on_start, on_finish = hud_review_hooks()
    res = run_mcode_review(root, slot, diff_ctx, on_start=on_start, on_finish=on_finish)
    print(res["output"])
    print(f"\n[review verdict: {res['verdict']}]")
    return 0 if res.get("exit_code") == 0 else (res.get("exit_code") or 1)


def do_recon(args):
    """Execute a read-only reconnaissance query in CWD without creating worktrees."""
    normalize_lane(args.lane, allow_auto=True)
    if args.timeout is not None:
        parse_timeout(args.timeout)
    cli_excluded = [normalize_lane(x, allow_auto=False) for x in parse_csv_arg(args.exclude_lanes)]
    slot = ensure_slot("recon")
    task = {
        "id": "recon", "prompt": RECON_PREFIX + args.recon,
        "files": parse_files_arg(args.files), "skills": parse_csv_arg(args.skills),
        "lane": args.lane, "complexity": args.complexity or "recon",
        "model": args.model, "read_only": True,
    }
    exec_res = run_task_with_fallback(
        task, Path.cwd(), slot, args.lane, args.model, args.effort, args.timeout,
        exclude_lanes=cli_excluded, no_pivot=args.no_pivot, log_stream=sys.stderr,
    )
    sys.stderr.write(
        f"[OODA:OBSERVE lane={exec_res['lane']} model={exec_res['model']} effort={exec_res['effort']} "
        f"{exec_res['duration_s']}s exit={exec_res['exit_code']}]\n"
    )
    if exec_res["output"]:
        print(exec_res["output"])
    code = exec_res["exit_code"]
    return code if isinstance(code, int) and code >= 0 else 1


def _review_and_track(args, exec_res, target_dir, files):
    """Scope-check + M3.1 review the CWD diff, then record or clear the mismatch log."""
    review_verdict = None
    if try_repo_root(target_dir) is not None:
        changed = changed_files_in(target_dir)
        violations = find_scope_violations(changed, files)
        if violations:
            print(f"SCOPE VIOLATIONS: {', '.join(violations)}")
        if not args.no_review and changed:
            diff_ctx = collect_diff_context(target_dir, include_last_commit_fallback=False)
            on_start, on_finish = hud_review_hooks()
            review = run_mcode_review(
                target_dir, ensure_slot("task-review"), diff_ctx,
                scope_violations=violations, declared_files=files,
                on_start=on_start, on_finish=on_finish,
            )
            review_verdict = review.get("verdict")
            print(f"\n--- M3.1-Flash Review ({review['verdict']}) ---")
            print(review["output"])
            print(f"[review verdict: {review['verdict']}]")
    elif not args.no_review:
        print("note: skipped M3.1 diff review (working directory is not a git repository)")
    if exec_res["exit_code"] != 0:
        record_mismatch(target_dir, files, exec_res.get("lane"), f"exited {exec_res['exit_code']}")
    elif review_verdict == "REJECT":
        record_mismatch(target_dir, files, exec_res.get("lane"), "was REJECTED by verify")
    else:
        clear_mismatch(target_dir, files)


def do_single_task(args):
    """Execute a single coding task directly in CWD with optional scope check & M3.1 review."""
    canon_req = normalize_lane(args.lane, allow_auto=True)
    if args.timeout is not None:
        parse_timeout(args.timeout)
    cli_excluded = [normalize_lane(x, allow_auto=False) for x in parse_csv_arg(args.exclude_lanes)]
    target_dir = Path.cwd()
    slot = ensure_slot("task")
    files = parse_files_arg(args.files)
    avoid_families = set()
    if canon_req == "auto" and not args.no_pivot:
        mm = load_recent_mismatch(target_dir, files)
        if mm is not None:
            avoid_families.add(mm["family"])
            print(
                f"▸ [OODA:MISMATCH PIVOT] prior loop on {mm['lane']} ({mm['family']}) "
                f"{mm['reason']} — rotating model family"
            )
    task = {
        "id": "task", "prompt": args.task, "files": files, "skills": parse_csv_arg(args.skills),
        "lane": args.lane, "complexity": args.complexity, "model": args.model,
    }
    exec_res = run_task_with_fallback(
        task, target_dir, slot, args.lane, args.model, args.effort, args.timeout,
        exclude_lanes=cli_excluded, avoid_families=avoid_families, no_pivot=args.no_pivot,
    )
    print(
        f"[OODA:ACT lane={exec_res['lane']} model={exec_res['model']} effort={exec_res['effort']} "
        f"{exec_res['duration_s']}s exit={exec_res['exit_code']}]"
    )
    if exec_res["fallback_from"]:
        print(f"routing note: {exec_res['fallback_from']}")
    if exec_res["output"]:
        print(exec_res["output"])
    _review_and_track(args, exec_res, target_dir, files)
    code = exec_res["exit_code"]
    return code if isinstance(code, int) and code >= 0 else 1


def _result_ids(results_file):
    """Return task ids recorded in a prior .worktrees/results.json (empty on error)."""
    ids = []
    if not results_file.is_file():
        return ids
    try:
        loaded = json.loads(results_file.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError):
        return ids
    rows = loaded.get("results", []) if isinstance(loaded, dict) else loaded
    if isinstance(rows, list):
        ids.extend(r["id"] for r in rows if isinstance(r, dict) and r.get("id"))
    return ids


def do_cleanup(root):
    """Remove .worktrees and temporary ooda/* branches (slot caches are kept)."""
    wt_dir = root / ".worktrees"
    ids = _result_ids(wt_dir / "results.json")
    if wt_dir.is_dir():
        wt_resolved = wt_dir.resolve()
        lst = run(["git", "worktree", "list", "--porcelain"], cwd=root).stdout
        registered = [
            line[len("worktree "):].strip() for line in lst.splitlines() if line.startswith("worktree ")
        ]
        for wt in registered:
            wt_path = Path(wt).resolve(strict=False)
            if wt_path == wt_resolved or wt_resolved in wt_path.parents:
                run(["git", "worktree", "remove", "--force", wt], cwd=root)
                print(f"removed worktree {wt}")
        for child in sorted(wt_dir.iterdir()):
            if child.is_dir() and ID_RE.match(child.name) and child.name not in ids:
                ids.append(child.name)
    run(["git", "worktree", "prune"], cwd=root)
    if wt_dir.is_dir():
        shutil.rmtree(wt_dir, ignore_errors=True)
        print(f"removed {wt_dir}")
    for tid in ids:
        if run(["git", "branch", "-D", f"ooda/{tid}"], cwd=root).returncode == 0:
            print(f"deleted branch ooda/{tid}")
    print(f"cleanup done (slots in {slots_root_path()} kept for warm caches)")
