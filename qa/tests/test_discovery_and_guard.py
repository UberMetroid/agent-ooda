"""Tests for live local CLI config/model/skill discovery and Gemini-pool guardrail."""

import io
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest import mock

from cli import run_cli
from dispatch.build_cmd import build_worker_command, execute_once
from envelope import (
    discover_fleet_capabilities,
    effective_lane_spec,
    load_discovery_cache,
    probe_agy_settings,
    probe_devin_config,
    probe_grok_config,
    probe_minimax_config,
    probe_muse_config,
    probe_vibe_config,
)
from fleet import configure_cycle, default_quota_data, normalize_model_for_provider
from worktree import discover_skills, load_manifest

GIT_ID = ["-c", "user.name=t", "-c", "user.email=t@invalid", "-c", "commit.gpgsign=false"]


def _write_synthetic_home(home):
    """Create non-secret fixture configs for all 6 local agent CLIs under home."""
    configs = {
        ".vibe/config.toml": (
            'active_model = "glm-5-3"\n'
            'api_key = "never-leak-this"\n'
            'models = [\n'
            '  { thinking = "high", alias = "glm-5-3" },\n'
            '  { thinking = "off", alias = "mistral-medium-3.5" },\n'
            ']\n'
        ),
        ".config/muse/settings.json": json.dumps({
            "model": "muse-spark-1.3-contributor",
            "reasoning_effort": "max",
            "model_reasoning_efforts": {
                "muse-spark-1.3-contributor": "max",
                "muse-wonder-full-1.0": "medium",
            },
        }),
        ".minimax/config.yaml": (
            "defaultModel: minimax/MiniMax-M3.1-Flash\n"
            "provider:\n  minimax:\n    models:\n      MiniMax-M3:\n      MiniMax-M3.1-Flash:\n"
            "defaultModelThinking:\n  effort: max\n"
        ),
        ".grok/config.toml": '[models]\ndefault = "grok-4.7"\ndefault_reasoning_effort = "high"\n',
        ".grok/models_cache.json": json.dumps({
            "models": {
                "grok-4.7": {"info": {"hidden": False}},
                "grok-secret-beta": {"info": {"hidden": True}},
            }
        }),
        ".config/devin/config.json": json.dumps({
            "model": "swe-2-flash-max",
            "agent": {
                "model": "swe-2-max",
                "subagent_model": "swe-2-flash-max",
                "preferred_family_models": {
                    "fusion": "fusion-claude-opus-5-5-low-sidekick-swe-2-flash-max",
                    "swe-2": "swe-2-max",
                    "swe-2-flash": "swe-2-flash-max",
                },
            },
        }),
        ".gemini/antigravity-cli/settings.json": json.dumps({"model": "Orchestrator (Carbon)"}),
    }
    for rel, body in configs.items():
        target = home / rel
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(body, encoding="utf-8")


class DiscoveryAndGuardTests(unittest.TestCase):
    """Verify live capability discovery, skill catalog parsing, and Gemini guardrail."""

    def test_local_config_probes_all_six_clis(self):
        with tempfile.TemporaryDirectory() as tmp:
            home = Path(tmp)
            _write_synthetic_home(home)
            vibe = probe_vibe_config(home)
            self.assertEqual((vibe["active_model"], vibe["active_effort"]), ("glm-5-3", "high"))
            self.assertEqual(vibe["alias_efforts"]["mistral-medium-3.5"], "off")
            self.assertNotIn("never-leak-this", json.dumps(vibe))
            muse = probe_muse_config(home)
            self.assertEqual((muse["active_model"], muse["active_effort"]), ("muse-spark-1.3-contributor", "max"))
            mm = probe_minimax_config(home)
            self.assertEqual((mm["active_model"], mm["active_effort"]), ("minimax/MiniMax-M3.1-Flash", "max"))
            self.assertIn("minimax/MiniMax-M3", mm["models"])
            grok = probe_grok_config(home)
            self.assertEqual((grok["active_model"], grok["active_effort"]), ("grok-4.7", "high"))
            self.assertEqual(grok["models"], ["grok-4.7"])
            devin = probe_devin_config(home)
            self.assertEqual(devin["lane_models"]["devin-pro"], "swe-2-max")
            self.assertEqual(devin["lane_models"]["devin-fast"], "swe-2-flash-max")
            self.assertIn("fusion-claude", devin["lane_models"]["devin-fusion"])
            agy = probe_agy_settings(home)
            self.assertTrue(agy["gemini_blocked"])

    def test_gemini_guardrail_across_all_entrypoints(self):
        for bad in ("gemini-4-carbon-high", "gemini-3.8-flash-low", "Orchestrator (Carbon)"):
            for lane in ("agy-opus", "mcode", "auto"):
                with self.assertRaises(SystemExit) as caught:
                    normalize_model_for_provider(lane, bad)
                self.assertEqual(caught.exception.code, 2)
        with tempfile.TemporaryDirectory() as tmp:
            tpath = Path(tmp)
            qpath, mpath = tpath / "quota.json", tpath / "m.json"
            mpath.write_text(json.dumps([{"id": "t1", "prompt": "p", "model": "gemini-4-argon-low"}]), encoding="utf-8")
            for fn in (
                lambda: configure_cycle("agy-opus", model="gemini-4-argon-low", quota_path=qpath),
                lambda: load_manifest(mpath),
                lambda: build_worker_command("agy-opus", "agy", "Orchestrator (Carbon)", None, "p", [], tpath, tpath, "t1"),
            ):
                with self.assertRaises(SystemExit) as caught:
                    fn()
                self.assertEqual(caught.exception.code, 2)
            with mock.patch("dispatch.build_cmd.probe_agy_settings",
                            return_value={"settings_model": "Orchestrator (Carbon)", "gemini_blocked": True}):
                res, _ = execute_once("agy-opus", {}, "agy", None, None, "p", [], tpath, tpath, "t1", 10)
            self.assertEqual(res["exit_code"], 2)
            self.assertIn("Gemini guardrail", res["output"])

    def test_cli_discovery_cache_and_agy_safe_model_resolution(self):
        with tempfile.TemporaryDirectory() as tmp:
            home = Path(tmp)
            _write_synthetic_home(home)
            cpath = home / "discovery.json"
            agy_out = "gemini-4-carbon-high\tGemini 4\nclaude-opus-5-5-high\tClaude Opus High\nclaude-opus-5-5-medium\tClaude Opus Med\n"
            devin_out = "  swe-2-max  SWE-2 Max  [262K context, Free]\n  gemini-pro  Gemini  [Paid]\n"
            grok_out = "  * grok-4.7 (default)\n  - grok-4.7-build-fast\n"
            fake_bins = {"agy": "/bin/sh", "devin": "/bin/sh", "grok": "/bin/sh"}
            fake_runs = [
                mock.Mock(returncode=0, stdout=agy_out),
                mock.Mock(returncode=0, stdout=devin_out),
                mock.Mock(returncode=0, stdout=grok_out),
            ]
            with mock.patch.dict(os.environ, {"OODA_DISCOVERY_FILE": str(cpath)}), \
                    mock.patch("envelope.discover_fleet.resolve_cli", side_effect=fake_bins.get), \
                    mock.patch("envelope.discover_fleet.run", side_effect=fake_runs):
                models = load_discovery_cache(force_refresh=True)
                self.assertEqual(models["agy"], ["claude-opus-5-5-high", "claude-opus-5-5-medium"])
                self.assertEqual(models["devin"], ["swe-2-max[free]"])
                caps = discover_fleet_capabilities(default_quota_data(), home=home)
                self.assertFalse(caps["agy-opus"]["agy_unsafe"])
                self.assertEqual(caps["agy-opus"]["dispatch_model"], "claude-opus-5-5-high")
                self.assertEqual(effective_lane_spec("agy-opus", {}, None, "medium", home=home)[0], "claude-opus-5-5-medium")
                self.assertEqual(effective_lane_spec("mistral", {}, "mistral-medium-3.5", None, home=home)[3], "off")
                m_spec = effective_lane_spec("mcode", {}, None, None, home=home)
                self.assertEqual((m_spec[0], m_spec[1], m_spec[3]), (None, "minimax/MiniMax-M3.1-Flash", "max"))

    def test_skill_catalog_and_status_discover_cli(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            sk = root / ".agents" / "skills" / "custom-verify" / "SKILL.md"
            sk.parent.mkdir(parents=True)
            sk.write_text("---\nname: custom-verify\ndescription: >-\n  Run custom\n  checks.\n---\nBody\n", encoding="utf-8")
            _write_synthetic_home(root)
            cpath, qpath = root / "discovery.json", root / "quota.json"
            with mock.patch("worktree.load_skills.Path.home", return_value=root):
                skills = discover_skills(cwd=root)
            custom = next(s for s in skills if s["name"] == "custom-verify")
            self.assertEqual((custom["description"], custom["core"]), ("Run custom checks.", False))
            env = {"AGENT_QUOTA_FILE": str(qpath), "OODA_DISCOVERY_FILE": str(cpath), "OODA_NO_AUTO_PROBE": "1"}
            for argv in (["--status"], ["--discover"]):
                buf = io.StringIO()
                with mock.patch.dict(os.environ, env), \
                        mock.patch("envelope.discover_fleet._probe_cli_models", return_value=["claude-opus-5-5-high"]), \
                        mock.patch("worktree.load_skills.Path.home", return_value=root), \
                        mock.patch("envelope.probe_configs.Path.home", return_value=root), \
                        mock.patch("sys.stdout", buf):
                    rc = run_cli.main(argv)
                self.assertEqual(rc, 0)
                out = buf.getvalue()
                self.assertIn("mistral-medium-3.5[off]", out)
                self.assertIn("custom-verify", out)


if __name__ == "__main__":
    unittest.main()
