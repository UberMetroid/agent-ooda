"""Worktree path hygiene, slot isolation, skill loading, diff gathering, and review fallback."""

import io
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest import mock

from fleet import atomic_write_json, default_quota_data, load_quota
from verify import collect_diff_context, parse_review_verdict, run_mcode_review
from worktree import (
    changed_files_in,
    ensure_slot,
    find_scope_violations,
    load_skill_blocks,
    normalize_rel_path,
)

REPO = Path(__file__).resolve().parents[2]
SKILLS = REPO / "plugins" / "ooda" / "skills"
GIT_ID = ["-c", "user.name=ooda-test", "-c", "user.email=ooda@example.invalid",
          "-c", "commit.gpgsign=false"]


def _git(cwd, *args):
    """Run an argv-only git command inside cwd and assert success."""
    proc = subprocess.run(["git", *GIT_ID, *args], cwd=str(cwd), capture_output=True,
                          text=True, timeout=60)
    if proc.returncode != 0:
        raise AssertionError(f"git {args} failed: {proc.stderr}")
    return proc.stdout


def _repo(root):
    """Initialise an empty git repo (unborn HEAD) with a .worktrees artifact present."""
    _git(root, "init", "-q")
    (root / ".worktrees").mkdir()
    (root / ".worktrees" / "junk.py").write_text("scratch = True\n", encoding="utf-8")
    return root


class WorktreeAndVerifyTests(unittest.TestCase):
    """Path normalization, slot safety, skills, diff context, and cross-model review."""

    def test_paths_and_scope(self):
        self.assertEqual(normalize_rel_path(".worktrees/foo.py"), ".worktrees/foo.py")
        self.assertEqual(normalize_rel_path("./.github/ci.yml"), ".github/ci.yml")
        self.assertEqual(normalize_rel_path("src//pkg/./mod.py"), "src/pkg/mod.py")
        bad_paths = (
            "../escape.py", "src/../../escape.py", "/etc/passwd", "\\etc\\passwd",
            ".//etc/passwd", "C:/foo.py", "C:\\foo.py", "C:foo.py",
        )
        for bad in bad_paths:
            with self.assertRaises(SystemExit) as caught:
                normalize_rel_path(bad)
            self.assertEqual(caught.exception.code, 2)
        self.assertEqual(normalize_rel_path("."), "")
        self.assertEqual(find_scope_violations([".github/ci.yml"], [".github"]), [])
        self.assertEqual(
            find_scope_violations(["a.py", "b.py", ".worktrees/x.py"], ["a.py", ".worktrees"]),
            ["b.py"],
        )

    def test_slot_and_skills(self):
        with tempfile.TemporaryDirectory() as tmp, \
                mock.patch.dict(os.environ, {"ROUTER_SLOTS_DIR": tmp}):
            slot = ensure_slot("../weird/key")
            root = Path(tmp).resolve()
            self.assertEqual(slot.resolve().parent, root)
            self.assertTrue(slot.is_dir())
            self.assertNotIn("/", slot.name)
        ooda_body = (SKILLS / "ooda" / "SKILL.md").read_text(encoding="utf-8").strip()
        router_body = (SKILLS / "router" / "SKILL.md").read_text(encoding="utf-8").strip()
        for name in ("ooda:ooda", "ooda"):
            block = load_skill_blocks([name], cwd=str(REPO))
            self.assertTrue(block.startswith("=== Skill Guidance: ooda ===\n"))
            self.assertIn(ooda_body, block)
        block = load_skill_blocks(["ooda:router"], cwd=str(REPO))
        self.assertTrue(block.startswith("=== Skill Guidance: router ===\n"))
        self.assertIn(router_body, block)

    def test_diff_unborn_head(self):
        with tempfile.TemporaryDirectory() as tmp:
            repo = _repo(Path(tmp))
            (repo / "new.txt").write_text("héllo wörld\nsecond\n", encoding="utf-8")
            (repo / "bin.dat").write_bytes(b"\x00\x01\x02binary payload")
            ctx = collect_diff_context(repo)
        diff = ctx["diff"]
        for noise in ("fatal", "bad revision", "unknown revision", "ambiguous argument"):
            self.assertNotIn(noise, diff)
        self.assertEqual(ctx["diff_stat"], "(no tracked diff)")
        self.assertTrue(ctx["has_changes"])
        self.assertIn("--- /dev/null\n+++ b/new.txt\n@@ -0,0 +1,2 @@\n+héllo wörld\n+second\n", diff)
        self.assertIn("Binary files /dev/null and b/bin.dat differ", diff)
        self.assertNotIn(".worktrees/junk.py", diff)

    def test_diff_committed_head(self):
        with tempfile.TemporaryDirectory() as tmp:
            repo = _repo(Path(tmp))
            (repo / "tracked.py").write_text("a = 1\n", encoding="utf-8")
            _git(repo, "add", "tracked.py")
            _git(repo, "commit", "-q", "-m", "seed")
            (repo / "tracked.py").write_text("a = 2\n", encoding="utf-8")
            (repo / "new.py").write_text("print('x')\n", encoding="utf-8")
            (repo / "blob.bin").write_bytes(b"PK\x00\x00zip")
            ctx = collect_diff_context(repo)
            changed = changed_files_in(repo)
        diff = ctx["diff"]
        self.assertIn("-a = 1\n+a = 2", diff)
        self.assertIn("--- /dev/null\n+++ b/new.py\n@@ -0,0 +1,1 @@\n+print('x')\n", diff)
        self.assertIn("Binary files /dev/null and b/blob.bin differ", diff)
        self.assertNotIn("fatal", diff)
        self.assertNotIn(".worktrees", diff)
        self.assertEqual(changed, ["blob.bin", "new.py", "tracked.py"])
        self.assertEqual(find_scope_violations(changed, ["tracked.py"]), ["blob.bin", "new.py"])

    def test_review_cross_model_fallback(self):
        installed = {"devin": "/opt/fake/devin", "mcode": "/opt/fake/mcode", "grok": "/opt/fake/grok"}
        runner = mock.Mock(side_effect=[
            {"exit_code": 1, "output": "HTTP 429 Too Many Requests: rate limit exceeded",
             "timed_out": False, "export_info": None},
            {"exit_code": 0, "output": "No issues found.\nVERDICT: ACCEPT",
             "timed_out": False, "export_info": None},
        ])
        diff_ctx = {"source": "working-tree", "status": "M a.py", "diff_stat": "a.py | 2 +-",
                    "diff": "-a = 1\n+a = 2\n", "has_changes": True}
        with tempfile.TemporaryDirectory() as tmp:
            qpath = Path(tmp) / "quota.json"
            quota = default_quota_data()
            self.assertEqual(quota["version"], 7)
            atomic_write_json(qpath, quota)
            env = {"AGENT_QUOTA_FILE": str(qpath), "ROUTER_HUD_DIR": str(Path(tmp) / "hud"),
                   "ROUTER_DEPTH": "0"}
            with mock.patch.dict(os.environ, env), \
                    mock.patch("envelope.select_lane.resolve_cli", side_effect=installed.get):
                result = run_mcode_review(tmp, Path(tmp) / "slot", diff_ctx,
                                          log_stream=io.StringIO(), author_lane="devin-pro",
                                          runner=runner)
                after = load_quota()
        lanes = [c.args[0] for c in runner.call_args_list]
        self.assertEqual(lanes, ["mcode", "grok"])
        self.assertIn("+a = 2", runner.call_args_list[0].args[5])
        self.assertTrue(all(c.kwargs.get("read_only") for c in runner.call_args_list))
        self.assertEqual([c.kwargs.get("review_mode") for c in runner.call_args_list],
                         ["native", True])
        self.assertEqual(parse_review_verdict("[P1] bad subtract\nmath.py:2"), "REJECT")
        self.assertEqual(parse_review_verdict("No reportable issues were found."), "ACCEPT")
        mcode = after["providers"]["mcode"]
        self.assertEqual(mcode["status"], "cooldown")
        self.assertTrue(mcode["cooldown_until"])
        self.assertIn("quota/rate-limit", mcode["reason"])
        self.assertEqual(after["providers"]["grok"]["status"], "active")
        self.assertEqual(
            {k: result[k] for k in ("ran", "lane", "verdict")},
            {"ran": True, "lane": "grok", "verdict": "ACCEPT"},
        )

    def test_review_fail_closed_fallbacks(self):
        for bad in ("[P1] bug", "[p1] lower bug", "[P0-3] issue: bad", "- [P1] bullet bug", "### [P1] heading",
                    "**Finding:** [P1] desc", "src/f.py:12: [P1] bug", "[P1] crash when no config exists",
                    "No workaround exists for [P1] null dereference",
                    "No [P0] issues, but [P1] bug", "VERDICT: REJECT - x"):
            self.assertEqual(parse_review_verdict(f"{bad}\nVERDICT: ACCEPT"), "REJECT")
        for ok in ("None of [P0-3] apply.", "No [P0-3] issues found.", "No [P1] issues found.",
                   "There are no [P0] or [P1] findings."):
            self.assertEqual(parse_review_verdict(f"{ok}\nVERDICT: ACCEPT"), "ACCEPT")
        for unk in ("1. bug\n2. more\n3. gap\nNo reportable issues were found.",
                    "VERDICT: ACCEPT\ntrailing noise", "VERDICT: ACCEPT - conditional caveat"):
            self.assertEqual(parse_review_verdict(unk), "UNKNOWN")
        installed = {"devin": "/opt/fake/devin", "mcode": "/opt/fake/mcode", "grok": "/opt/fake/grok"}
        diff_ctx = {"source": "committed", "status": "", "diff_stat": "a.py | 2 +-",
                    "diff": "-a = 1\n+a = 2\n" * 800, "has_changes": True}
        prose = {"exit_code": 0, "output": "Looks reasonable overall.", "timed_out": False,
                 "export_info": None}
        crashed = dict(prose, exit_code=1, output="VERDICT: ACCEPT")
        for first in (crashed, prose):
            runner = mock.Mock(side_effect=[first, dict(prose)])
            with tempfile.TemporaryDirectory() as tmp:
                qpath = Path(tmp) / "quota.json"
                atomic_write_json(qpath, default_quota_data())
                env = {"AGENT_QUOTA_FILE": str(qpath), "ROUTER_HUD_DIR": str(Path(tmp) / "hud"),
                       "ROUTER_DEPTH": "0"}
                with mock.patch.dict(os.environ, env), \
                        mock.patch("envelope.select_lane.resolve_cli", side_effect=installed.get):
                    result = run_mcode_review(tmp, Path(tmp) / "slot", diff_ctx,
                                              log_stream=io.StringIO(), author_lane="devin-pro",
                                              runner=runner)
            self.assertEqual([c.args[0] for c in runner.call_args_list], ["mcode", "grok"])
            self.assertEqual([c.kwargs.get("review_mode") for c in runner.call_args_list],
                             [True, True])
            self.assertEqual((result["lane"], result["verdict"]), ("grok", "UNKNOWN"))
        mb_diff = {"source": "working-tree", "status": "M u.py", "diff_stat": "u.py",
                   "diff": "+" + ("é" * 5000), "has_changes": True}
        mb_run = mock.Mock(return_value=dict(prose, output="VERDICT: ACCEPT"))
        rej_runner = mock.Mock(side_effect=[dict(prose, exit_code=1, output="[P1] defect"),
                                            dict(prose, output="VERDICT: ACCEPT")])
        with tempfile.TemporaryDirectory() as tmp:
            qpath = Path(tmp) / "quota.json"
            atomic_write_json(qpath, default_quota_data())
            env = {"AGENT_QUOTA_FILE": str(qpath), "ROUTER_HUD_DIR": str(Path(tmp) / "hud"), "ROUTER_DEPTH": "0"}
            with mock.patch.dict(os.environ, env), \
                    mock.patch("envelope.select_lane.resolve_cli", side_effect=installed.get):
                keep = run_mcode_review(tmp, Path(tmp) / "slot", diff_ctx,
                                        log_stream=io.StringIO(), author_lane="devin-pro", runner=rej_runner)
                run_mcode_review(tmp, Path(tmp) / "slot2", mb_diff, log_stream=io.StringIO(), runner=mb_run)
        self.assertEqual((len(rej_runner.call_args_list), keep["lane"], keep["verdict"]), (1, "mcode", "REJECT"))
        self.assertIs(mb_run.call_args_list[0].kwargs.get("review_mode"), True)
        for bad_json in ("{not json", '{"entries": ["bad"]}'):
            with tempfile.TemporaryDirectory() as home:
                skills = Path(home) / ".gemini" / "config" / "skills.json"
                skills.parent.mkdir(parents=True)
                skills.write_text(bad_json, encoding="utf-8")
                env = {"HOME": home, "PATH": os.environ.get("PATH", "/usr/bin:/bin")}
                proc = subprocess.run(["bash", str(REPO / "install.sh")], env=env, cwd=home,
                                      capture_output=True, text=True, timeout=120)
                self.assertEqual(proc.returncode, 1, proc.stderr)
                self.assertTrue(proc.stderr.strip())
                self.assertEqual(skills.read_text(encoding="utf-8"), bad_json)
                self.assertFalse((Path(home) / ".local" / "bin").exists())


if __name__ == "__main__":
    unittest.main()
