"""Parallel manifest fan-out: per-task worktree execution, M3.1 auto-review, results.json."""

import concurrent.futures
from pathlib import Path
import queue
import sys

from fleet import REVIEW_MODEL, atomic_write_json, normalize_lane
from verify import collect_diff_context, run_mcode_review
from worktree import (
    changed_files_in,
    diff_stat,
    ensure_slot,
    ensure_worktree,
    find_scope_violations,
    parse_csv_arg,
)

from .build_cmd import execute_once
from .run_fallback import run_task_with_fallback
from .telemetry import hud_review_hooks


def _skipped_review(reason=None):
    """Return a not-run review record (reason omitted for harness errors)."""
    rec = {"ran": False, "lane": None, "model": REVIEW_MODEL, "verdict": "SKIPPED", "exit_code": None}
    if reason is not None:
        rec["reason"] = reason
    rec["output"] = ""
    return rec


def _review_worktree(task, worktree, slot_num, changed, violations, args, author_lane=None):
    """Run cross-model review (mcode first) on a changed worktree unless disabled or clean."""
    if args.no_review or task.get("review", True) is False:
        return _skipped_review("disabled")
    if not changed:
        return _skipped_review("no changes in worktree")
    on_start, on_finish = hud_review_hooks()
    return run_mcode_review(
        worktree,
        ensure_slot(f"review-{slot_num}"),
        collect_diff_context(worktree, include_last_commit_fallback=False),
        scope_violations=violations,
        declared_files=task.get("files") or [],
        log_stream=sys.stdout,
        on_start=on_start,
        on_finish=on_finish,
        author_lane=author_lane,
        runner=execute_once,
    )


def process_task(task_index, slot_pool, task, worktree, branch, args, default_timeout):
    """Execute one subtask in its worktree and optionally run M3.1 auto-review."""
    slot_num = slot_pool.get()
    try:
        slot = ensure_slot(slot_num)
        cli_skills = parse_csv_arg(args.skills)
        if cli_skills:
            task = dict(task)
            task["skills"] = list(dict.fromkeys(list(task.get("skills") or []) + cli_skills))
        task_excl = [normalize_lane(x, allow_auto=False) for x in (task.get("exclude_lanes") or [])]
        cli_excl = [normalize_lane(x, allow_auto=False) for x in parse_csv_arg(args.exclude_lanes)]
        exec_res = run_task_with_fallback(
            task, worktree, slot, args.lane, args.model, args.effort, default_timeout,
            stripe_index=None if args.no_stripe else task_index,
            is_parallel=True,
            exclude_lanes=list(dict.fromkeys(task_excl + cli_excl)),
            no_pivot=args.no_pivot,
        )
        changed = changed_files_in(worktree)
        violations = find_scope_violations(changed, task.get("files") or [])
        stat = diff_stat(worktree)
        review = _review_worktree(task, worktree, slot_num, changed, violations, args,
                                  author_lane=exec_res.get("lane"))
        rec = {"id": task["id"], "branch": branch, "worktree": worktree.name, "slot": slot.name}
        for key in ("requested_lane", "lane", "cli", "model", "effort", "fallback_from",
                    "attempts", "exit_code", "timed_out", "duration_s"):
            rec[key] = exec_res[key]
        rec.update({"changed_files": changed, "scope_violations": violations, "diff_stat": stat,
                    "output": exec_res["output"], "review": review})
        return rec
    finally:
        slot_pool.put(slot_num)


def _harness_error_record(task, path, branch, root, args, exc):
    """Build a failed result record when process_task raised unexpectedly (never raises)."""
    try:
        rel = str(path.relative_to(root))
    except ValueError:
        rel = path.name
    try:
        stat = diff_stat(path)
    except Exception as stat_exc:  # noqa: BLE001
        stat = f"<diff failed: {stat_exc}>"
    return {
        "id": task["id"], "branch": branch, "worktree": rel, "slot": None,
        "requested_lane": task.get("lane") or args.lane, "lane": None, "cli": None, "model": None,
        "effort": args.effort, "fallback_from": None, "attempts": [], "exit_code": -1,
        "timed_out": False, "duration_s": 0.0, "changed_files": [], "scope_violations": [],
        "diff_stat": stat, "output": f"<harness error: {exc}>",
        "review": _skipped_review(),
    }


def run_manifest_batch(args, root, quota_data, tasks, jobs):
    """Fan tasks out over worktrees/slots, write .worktrees/results.json, return exit code.

    quota_data is accepted for caller API parity (routing preview happens upstream).
    """
    root = Path(root).resolve()
    jobs = max(1, int(jobs or 1))
    for lane in parse_csv_arg(args.exclude_lanes):
        normalize_lane(lane, allow_auto=False)  # fail fast on bad CLI lanes before fan-out
    default_timeout = args.timeout
    slot_pool = queue.Queue()
    for n in range(jobs):
        ensure_slot(n)
        slot_pool.put(n)
    wt_dir = root / ".worktrees"
    wt_dir.mkdir(exist_ok=True)
    prepared = []
    for i, t in enumerate(tasks):
        path, branch = ensure_worktree(root, t["id"])
        prepared.append((i, t, path, branch))
        print(f"[{t['id']}] worktree={path} branch={branch}")

    results = []
    with concurrent.futures.ThreadPoolExecutor(max_workers=jobs) as ex:
        futs = {
            ex.submit(process_task, idx, slot_pool, t, path, branch, args, default_timeout): (t, path, branch)
            for (idx, t, path, branch) in prepared
        }
        for fut in concurrent.futures.as_completed(futs):
            t, path, branch = futs[fut]
            try:
                rec = fut.result()
            except (Exception, SystemExit) as e:  # noqa: BLE001
                rec = _harness_error_record(t, path, branch, root, args, e)
            else:
                rec["worktree"] = str(path.relative_to(root))
            results.append(rec)
            status = "OK" if rec["exit_code"] == 0 else f"FAIL({rec['exit_code']})"
            rev_verdict = (rec.get("review") or {}).get("verdict", "SKIPPED")
            viol = f" SCOPE_VIOLATION({len(rec['scope_violations'])})" if rec.get("scope_violations") else ""
            print(f"[{rec['id']}] {status} lane={rec.get('lane')} effort={rec.get('effort')} "
                  f"{rec['duration_s']}s review={rev_verdict}{viol}\n{rec['diff_stat']}\n---")

    results.sort(key=lambda r: r["id"])
    out = wt_dir / "results.json"
    atomic_write_json(wt_dir / "results.json", results)
    print(f"wrote {out}")
    failed = [
        r["id"]
        for r in results
        if r.get("exit_code") != 0
        or r.get("scope_violations")
        or (r.get("review") or {}).get("verdict") == "REJECT"
    ]
    if failed:
        print(f"{len(failed)} task(s) failed/rejected: {', '.join(failed)}")
        return 1
    print("all tasks exited 0")
    return 0
