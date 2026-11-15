"""Tests for unpinned model/effort defaults, dynamic routing, locking, and safety fixes."""

import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest import mock

from cli import run_actions, run_cli
from dispatch import fanout_batch
from dispatch.build_cmd import build_worker_command
from envelope import build_candidate_order
from fleet import (
    PROVIDER_ORDER,
    default_quota_data,
    normalize_model_for_provider,
    parse_lanes,
    resolve_effort,
)
from hud import record_hook
from worktree import changed_files_in, dirty_state, load_manifest

REPO = Path(__file__).resolve().parents[2]
GIT_ID = ["-c", "user.name=t", "-c", "user.email=t@invalid", "-c", "commit.gpgsign=false"]


def _git(cwd, *args):
    """Run git in cwd and raise on non-zero exit."""
    p = subprocess.run(["git", *GIT_ID, *args], cwd=str(cwd), capture_output=True, text=True, timeout=30)
    if p.returncode != 0:
        raise AssertionError(p.stderr)


class DispatchAndFixesTests(unittest.TestCase):
    """Verify unpinned defaults, comma-lane priority, read-only lockdown, baselines, and flock."""

    def test_defaults_unpinned_and_explicit_overrides(self):
        q = default_quota_data()
        for name in PROVIDER_ORDER:
            self.assertIsNone(q["providers"][name]["model"])
            self.assertIsNone(q["providers"][name]["effort"])
            self.assertIsNone(normalize_model_for_provider(name, None))
            self.assertIsNone(normalize_model_for_provider(name, "cli-default"))
            self.assertIsNone(resolve_effort(name, q["providers"][name], None, "deep"))
        self.assertEqual(normalize_model_for_provider("agy-opus", "sonnet"), "claude-sonnet-5-5-high")
        self.assertEqual(resolve_effort("muse", {}, "xhigh", "deep"), "xhigh")

    def test_comma_lanes_and_build_cmd_flags(self):
        self.assertEqual(parse_lanes("mcode, devin-fast, mcode"), ["mcode", "devin-fast"])
        with self.assertRaises(SystemExit) as bad:
            parse_lanes("auto,mcode")
        self.assertEqual(bad.exception.code, 2)
        order = build_candidate_order("mcode,devin-fast", "normal", default_quota_data())
        self.assertEqual(order[:2], ["mcode", "devin-fast"])
        with tempfile.TemporaryDirectory() as tmp:
            wt = Path(tmp) / "wt"
            slot = Path(tmp) / "slot"
            wt.mkdir()
            slot.mkdir()
            for lane, cli in (("agy-opus", "agy"), ("devin-pro", "devin"), ("mcode", "mcode"),
                              ("grok", "grok"), ("muse", "muse"), ("mistral", "vibe")):
                cmd, _, _ = build_worker_command(
                    lane, cli, None, None, "p", [], wt, slot, "t1", read_only=False)
                for flag in ("--model", "--effort", "--reasoning-effort"):
                    self.assertNotIn(flag, cmd)
                ro, _, _ = build_worker_command(
                    lane, cli, "m-ovr", "low", "p", [], wt, slot, "t1", read_only=True)
                self.assertIn("m-ovr", ro)
                if lane == "agy-opus":
                    self.assertIn("--sandbox", ro)
                    self.assertIn("--effort", ro)
                    self.assertNotIn("--dangerously-skip-permissions", ro)
                elif lane == "grok":
                    self.assertIn("plan", ro)
                    self.assertIn("--reasoning-effort", ro)
                    self.assertNotIn("bypassPermissions", ro)
                elif lane == "muse":
                    self.assertIn("--disable-write", ro)
                    self.assertIn("--reasoning-effort", ro)
                elif lane == "mcode":
                    self.assertIn("smart", ro)
                    self.assertIn("--effort", ro)
                elif lane == "devin-pro":
                    self.assertIn("auto", ro)
                elif lane == "mistral":
                    self.assertIn("--smart-approve", ro)

    def test_manifest_dir_overlap_and_batch_reject_exit(self):
        with tempfile.TemporaryDirectory() as tmp:
            mpath = Path(tmp) / "m.json"
            ok = [{"id": "t1", "prompt": "p", "files": ["src/a.py"], "lane": "mcode,devin-fast"}]
            mpath.write_text(json.dumps(ok), encoding="utf-8")
            self.assertEqual(len(load_manifest(mpath)), 1)
            for pair in (["src", "src/a.py"], ["src/a.py", "src"]):
                bad = [{"id": "a", "prompt": "p", "files": [pair[0]]},
                       {"id": "b", "prompt": "p", "files": [pair[1]]}]
                mpath.write_text(json.dumps(bad), encoding="utf-8")
                with self.assertRaises(SystemExit) as caught:
                    load_manifest(mpath)
                self.assertEqual(caught.exception.code, 2)
            wt = Path(tmp) / ".worktrees"
            wt.mkdir()
            args = mock.Mock(no_stripe=True, lane="auto", no_review=True, exclude_lanes=[])
            base_row = {"id": "t1", "exit_code": 0, "duration_s": 0.1, "diff_stat": ""}
            for row, expected in (
                (dict(base_row, scope_violations=[], review={"verdict": "ACCEPT"}), 0),
                (dict(base_row, scope_violations=["x.py"], review=None), 1),
                (dict(base_row, scope_violations=[], review={"verdict": "REJECT"}), 1),
            ):
                with mock.patch("dispatch.fanout_batch.ensure_worktree", return_value=(wt, "ooda/t1")), \
                        mock.patch("dispatch.fanout_batch.process_task", return_value=row):
                    rc = fanout_batch.run_manifest_batch(args, Path(tmp), default_quota_data(), [ok[0]], 1)
                self.assertEqual(rc, expected)

    def test_dirty_baseline_and_single_task_exits(self):
        with tempfile.TemporaryDirectory() as tmp:
            repo = Path(tmp)
            _git(repo, "init", "-q")
            (repo / "keep.py").write_text("x = 1\n", encoding="utf-8")
            (repo / "scoped.py").write_text("y = 1\n", encoding="utf-8")
            _git(repo, "add", ".")
            _git(repo, "commit", "-q", "-m", "init")
            (repo / "keep.py").write_text("x = 2\n", encoding="utf-8")
            (repo / "untracked file.py").write_text("u = 1\n", encoding="utf-8")
            base = dirty_state(repo)
            self.assertEqual(changed_files_in(repo, baseline=base), [])
            self.assertIn("untracked file.py", base)
            ok_exec = {"lane": "mcode", "model": "default", "effort": "default", "duration_s": 0.1,
                       "exit_code": 0, "fallback_from": None, "output": "ok"}
            mk = lambda target, text: (lambda *a, **kw: (target.write_text(text, encoding="utf-8") and False) or ok_exec)  # noqa: E731
            args = mock.Mock(lane="auto", timeout=None, exclude_lanes=[], no_pivot=True,
                             files=["scoped.py"], task="go", skills=[], complexity=None,
                             model=None, effort=None, no_review=False)
            old_cwd = os.getcwd()
            os.chdir(repo)
            try:
                with mock.patch("cli.run_actions.run_task_with_fallback", side_effect=mk(repo / "scoped.py", "y = 2\n")), \
                        mock.patch("cli.run_actions.run_mcode_review",
                                   return_value={"verdict": "ACCEPT", "lane": "grok", "output": "ok"}) as rev:
                    self.assertEqual(run_actions.do_single_task(args), 0)
                self.assertEqual(rev.call_args.kwargs["scope_violations"], [])
                with mock.patch("cli.run_actions.run_task_with_fallback", side_effect=mk(repo / "scoped.py", "y = 3\n")), \
                        mock.patch("cli.run_actions.run_mcode_review",
                                   return_value={"verdict": "REJECT", "lane": "grok", "output": "[P1] x"}):
                    self.assertEqual(run_actions.do_single_task(args), 1)
                args.no_review = True
                with mock.patch("cli.run_actions.run_task_with_fallback", side_effect=mk(repo / "out space.py", "z = 1\n")):
                    self.assertEqual(run_actions.do_single_task(args), 1)
            finally:
                os.chdir(old_cwd)

    def test_review_flags_and_hook_skill_preservation(self):
        with tempfile.TemporaryDirectory() as tmp:
            repo = Path(tmp)
            _git(repo, "init", "-q")
            diff = {"has_changes": True, "source": "working-tree", "diff_stat": "a", "diff": "+x"}
            rev = {"ran": True, "lane": "grok", "model": "grok-x", "verdict": "ACCEPT",
                   "exit_code": 0, "output": "VERDICT: ACCEPT"}
            with mock.patch("cli.run_actions.collect_diff_context", return_value=diff), \
                    mock.patch("cli.run_actions.run_mcode_review", return_value=rev) as mocked:
                rc = run_cli.main(["--review", str(repo), "--lane", "grok,muse",
                                   "--model", "grok-x", "--effort", "low"])
            self.assertEqual(rc, 0)
            kw = mocked.call_args.kwargs
            self.assertEqual((kw["requested_lane"], kw["model_override"], kw["effort_override"]),
                             ("grok,muse", "grok-x", "low"))
        kept = record_hook._apply_tool_call(
            ["prior"], {"toolCall": {"name": "run_command", "args": {"CommandLine": "router --status"}}},
        )
        self.assertEqual(kept, ["prior"])
        replaced = record_hook._apply_tool_call(
            ["prior"], {"toolCall": {"name": "run_command",
                                     "args": {"CommandLine": "router --skill fresh --task x"}}},
        )
        self.assertEqual(replaced, ["fresh"])

    def test_quota_lock_concurrent_processes(self):
        with tempfile.TemporaryDirectory() as tmp:
            qpath = Path(tmp) / "quota.json"
            worker = (
                "import json, sys\nfrom pathlib import Path\n"
                "from fleet import load_quota, quota_lock, save_quota_unlocked\n"
                "p = Path(sys.argv[1])\n"
                "for _ in range(15):\n"
                "    with quota_lock(p):\n"
                "        d = json.loads(p.read_text()) if p.is_file() else {'n': 0}\n"
                "        d['n'] = d.get('n', 0) + 1\n"
                "        save_quota_unlocked(d, p)\n"
            )
            env = dict(os.environ, PYTHONPATH=str(REPO / "plugins" / "ooda" / "src"))
            procs = [subprocess.Popen([sys.executable, "-c", worker, str(qpath)], env=env)
                     for _ in range(2)]
            for proc in procs:
                self.assertEqual(proc.wait(timeout=30), 0)
            self.assertEqual(json.loads(qpath.read_text(encoding="utf-8"))["n"], 30)


if __name__ == "__main__":
    unittest.main()
