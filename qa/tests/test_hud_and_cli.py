"""Bubble-canopy HUD rendering, skill-recording hook, and ooda CLI subprocess smoke tests."""

import io
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest
from unittest import mock

import hud.record_hook as record_hook
from hud.ansi_format import (
    C_FLEET_PARTIAL, C_IDLE_ICON, C_IDLE_TEXT, C_MUTED, C_ORIENT, C_VERIFY, C_VERIFY_MODEL, RESET,
)
from hud.render_line import render_statusline

REPO = Path(__file__).resolve().parents[2]
OODA_BIN = REPO / "plugins" / "ooda" / "bin" / "ooda"
SUMMARY = (5, 8, "1h05m", "agy-opus", "high", "devin-fast", "max")
FLEET = " │ fleet 5/8 (next 1h05m)"


def _set_workers(hud_dir, specs):
    """Replace live worker markers with (mode, model, skills) specs owned by this process."""
    active = hud_dir / "active"
    shutil.rmtree(active, ignore_errors=True)
    active.mkdir(parents=True)
    for idx, (mode, model, skills) in enumerate(specs):
        marker = {"pid": os.getpid(), "mode": mode, "model": model, "skills": skills,
                  "started_at": float(idx + 1)}
        (active / f"w{idx}.json").write_text(json.dumps(marker), encoding="utf-8")


class HudAndCliTests(unittest.TestCase):
    """Statusline output, hook skill capture, and CLI entrypoint behavior."""

    def setUp(self):
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        self.tmp = Path(tmp.name)
        self.hud = self.tmp / "hud"
        env = {"ROUTER_HUD_DIR": str(self.hud), "AGENT_QUOTA_FILE": str(self.tmp / "quota.json"),
               "NO_COLOR": "1", "ROUTER_DEPTH": "0"}
        for patcher in (mock.patch.dict(os.environ, env),
                        mock.patch("hud.render_line.load_fleet_summary", return_value=SUMMARY)):
            patcher.start()
            self.addCleanup(patcher.stop)

    def test_statusline_plain(self):
        render = lambda payload=None: render_statusline(  # noqa: E731 (keeps def budget <= 8)
            json.dumps(payload or {}), use_color=True, now_ts=1000.0)
        self.assertEqual(render(), "○ ooda" + FLEET)
        self.assertEqual(render({"agent": {"state": "thinking"}}), "◐ orient/decide" + FLEET)
        cases = (
            ([("observe", "swe-2-flash-max", [])], "⠋ observe · swe-2-flash-max"),
            ([("act", "claude-opus-5-5-high", [])], "⠋ act · claude-opus-5-5-high"),
            ([("act:verify", "minimax/MiniMax-M3.1-Flash", [])], "⠋ verify · MiniMax-M3.1-Flash"),
            ([("act", "grok-4.7", []),
              ("act", "fusion-claude-opus-5-5-high-sidekick-swe-2-medium", [])],
             "⠋ act ×2 · grok-4.7, devin-fusion"),
            ([("observe", "swe-2-flash-max", []), ("recon", "minimax/MiniMax-M3.1-Flash", [])],
             "⠋ observe ×2 · swe-2-flash-max, MiniMax-M3.1-Flash"),
            ([("act", "grok-4.7", ["ooda", "router", "ooda:ooda", "custom-skill"])],
             "⠋ act · grok-4.7 │ skill custom-skill"),
        )
        for specs, left in cases:
            _set_workers(self.hud, specs)
            self.assertEqual(render(), left + FLEET)
        narrow = render({"terminal_width": 48})
        self.assertEqual(narrow, "⠋ act · grok-4.7 │ skill custom-skill │ fleet…")
        self.assertEqual(len(narrow), 46)

    def test_statusline_ansi(self):
        os.environ.pop("NO_COLOR", None)
        sep = f"{C_MUTED} │ {RESET}"
        fleet = f"{sep}{C_FLEET_PARTIAL}fleet 5/8{RESET}{C_MUTED} (next 1h05m){RESET}"
        idle = render_statusline("{}", use_color=True, now_ts=1000.0)
        self.assertEqual(idle, f"{C_IDLE_ICON}○{RESET} {C_IDLE_TEXT}ooda{RESET}{fleet}")
        thinking = render_statusline('{"agent": {"state": "running"}}', use_color=True, now_ts=1000.0)
        self.assertEqual(thinking, f"{C_ORIENT}◐ orient/decide{RESET}{fleet}")
        _set_workers(self.hud, [("act:verify", "minimax/MiniMax-M3.1-Flash", [])])
        verify = render_statusline("{}", use_color=True, now_ts=1000.0)
        self.assertEqual(verify, f"{C_VERIFY}⠋ verify{RESET}{C_MUTED} · {RESET}"
                                 f"{C_VERIFY_MODEL}MiniMax-M3.1-Flash{RESET}{fleet}")
        plain = render_statusline("{}", use_color=False, now_ts=1000.0)
        self.assertEqual(plain, "⠋ verify · MiniMax-M3.1-Flash" + FLEET)

    def _hook(self, payload, depth="0"):
        with mock.patch.dict(os.environ, {"ROUTER_DEPTH": depth}), \
                mock.patch("sys.stdin", io.StringIO(json.dumps(payload))), \
                mock.patch("sys.stdout", new_callable=io.StringIO) as out:
            record_hook.main()
        self.assertEqual(out.getvalue(), "{}\n")
        sess = self.hud / "sessions" / f"{payload['conversationId']}.json"
        return json.loads(sess.read_text(encoding="utf-8"))["skills"] if sess.is_file() else None

    def test_record_hook(self):
        tool = lambda conv, name, args: {  # noqa: E731 (keeps def budget <= 8)
            "conversationId": conv, "hookEventName": "PostToolUse",
            "toolCall": {"name": name, "args": args}}
        read = tool("conv-a", "read_file", {"Path": "~/.agents/skills/readskill/SKILL.md"})
        self.assertEqual(self._hook(read), ["readskill"])
        hidden = tool("conv-a", "read_file", {"Path": "plugins/ooda/skills/ooda/SKILL.md"})
        self.assertEqual(self._hook(hidden), ["readskill"])
        ooda_cmd = tool("conv-a", "run_command", {"CommandLine": "ooda --skill mytool --task 'x'"})
        self.assertEqual(self._hook(ooda_cmd), ["mytool"])
        router_cmd = tool("conv-b", "run_command", {"CommandLine": "router --skill mytool --recon q"})
        self.assertEqual(self._hook(router_cmd), ["mytool"])
        nested = tool("conv-c", "read_file", {"Path": "~/.agents/skills/other/SKILL.md"})
        self.assertIsNone(self._hook(nested, depth="1"))
        start = {"conversationId": "conv-d", "hookEventName": "SessionStart"}
        self.assertIsNone(self._hook(start, depth="1"))
        pointer = json.loads((self.hud / "current_session.json").read_text(encoding="utf-8"))
        self.assertEqual(pointer["conversation_id"], "conv-b")

    def test_cli_subprocess(self):
        quota = self.tmp / "cli" / "quota.json"
        env = dict(os.environ, AGENT_QUOTA_FILE=str(quota), ROUTER_HUD_DIR=str(self.tmp / "cli-hud"),
                   ROUTER_DEPTH="0")
        for flag in ("--status", "--usage"):
            proc = subprocess.run([sys.executable, str(OODA_BIN), flag], cwd=str(REPO), env=env,
                                  capture_output=True, text=True, timeout=120)
            self.assertEqual(proc.returncode, 0, proc.stderr)
        data = json.loads(quota.read_text(encoding="utf-8"))
        self.assertEqual(data["version"], 7)
        self.assertEqual(len(data["providers"]), 8)
        env["ROUTER_DEPTH"] = "1"
        proc = subprocess.run([sys.executable, str(OODA_BIN), "--recon", "probe"], cwd=str(REPO),
                              env=env, capture_output=True, text=True, timeout=60)
        self.assertEqual(proc.returncode, 2)
        self.assertIn("ROUTER_DEPTH", proc.stderr)

    def test_installer_archives_displaced_paths(self):
        home = self.tmp / "inst-home"
        for rel, body in (
            (".local/bin/boyd", "custom boyd\n"),
            (".agents/skills/router/note.txt", "keep me\n"),
            (".gemini/antigravity-cli/statusline.py", "old hud\n"),
        ):
            target = home / rel
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_text(body, encoding="utf-8")
        env = {"HOME": str(home), "PATH": os.environ.get("PATH", "/usr/bin:/bin")}
        first = subprocess.run(["bash", str(REPO / "install.sh")], env=env, cwd=str(home),
                               capture_output=True, text=True, timeout=120)
        self.assertEqual(first.returncode, 0, first.stderr)
        self.assertTrue((home / ".local" / "bin" / "ooda").is_symlink())
        self.assertTrue((home / ".gemini" / "antigravity-cli" / "statusline.py").is_symlink())
        backups = list((home / ".local" / "state" / "ooda" / "backups").iterdir())
        self.assertEqual(len(backups), 1)
        self.assertEqual((backups[0] / "bin-boyd").read_text(encoding="utf-8"), "custom boyd\n")
        self.assertEqual((backups[0] / "skill-router" / "note.txt").read_text(encoding="utf-8"), "keep me\n")
        self.assertEqual((backups[0] / "statusline.py").read_text(encoding="utf-8"), "old hud\n")
        second = subprocess.run(["bash", str(REPO / "install.sh")], env=env, cwd=str(home),
                                capture_output=True, text=True, timeout=120)
        self.assertEqual(second.returncode, 0, second.stderr)
        self.assertEqual(len(list((home / ".local" / "state" / "ooda" / "backups").iterdir())), 1)


if __name__ == "__main__":
    unittest.main()
