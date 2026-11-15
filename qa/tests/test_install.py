"""install.sh / uninstall.sh / wire_settings.py tests against a throwaway HOME."""

import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest

REPO = Path(__file__).resolve().parents[2]
WIRE = REPO / "plugins" / "ooda" / "src" / "cli" / "wire_settings.py"
OURS = "~/.gemini/antigravity-cli/statusline.py"
FOREIGN = {"type": "command", "command": "~/bin/my-status.sh", "padding": 3}


class InstallTests(unittest.TestCase):
    """statusLine wiring, malformed-settings preflight, uninstall scope, purge, and opt-out."""

    def setUp(self):
        self.home = Path(tempfile.mkdtemp(prefix="ooda-inst-"))
        self.addCleanup(shutil.rmtree, self.home, True)
        self.agy = self.home / ".gemini" / "antigravity-cli"
        self.agy.mkdir(parents=True)
        self.settings = self.agy / "settings.json"
        self.state = self.home / ".local" / "state" / "ooda"
        self.env = {"HOME": str(self.home), "PATH": os.environ.get("PATH", "/usr/bin:/bin")}

    def run_cmd(self, *argv, rc=0, env=None):
        """Run argv with the temp-HOME env, assert its exit code, and return the process."""
        proc = subprocess.run(list(argv), env=dict(self.env, **(env or {})), cwd=str(self.home),
                              capture_output=True, text=True, timeout=120)
        self.assertEqual(proc.returncode, rc, proc.stdout + proc.stderr)
        return proc

    def test_wire_preserves_keys_and_is_idempotent(self):
        self.settings.write_text(json.dumps({"theme": "dark", "model": {"name": "x"}}), encoding="utf-8")
        args = (sys.executable, str(WIRE), "wire", str(self.settings), str(self.state))
        self.run_cmd(*args)
        data = json.loads(self.settings.read_text(encoding="utf-8"))
        self.assertEqual(data["theme"], "dark")
        self.assertEqual(data["model"], {"name": "x"})
        self.assertEqual(data["statusLine"], {"type": "command", "command": OURS, "enabled": True,
                                              "padding": 0, "stack_with_default": True})
        self.assertTrue(self.settings.read_text(encoding="utf-8").endswith("}\n"))
        data["statusLine"].update(enabled=False, padding=2)
        self.settings.write_text(json.dumps(data, indent=2) + "\n", encoding="utf-8")
        self.run_cmd(*args)
        again = json.loads(self.settings.read_text(encoding="utf-8"))
        self.assertEqual(again, data)
        self.assertFalse((self.state / "statusline.prev.json").exists())

    def test_foreign_statusline_saved_and_restored(self):
        self.settings.write_text(json.dumps({"statusLine": FOREIGN, "keep": 1}), encoding="utf-8")
        base = (sys.executable, str(WIRE))
        self.run_cmd(*base, "wire", str(self.settings), str(self.state))
        prev = self.state / "statusline.prev.json"
        self.assertEqual(json.loads(prev.read_text(encoding="utf-8")), FOREIGN)
        self.assertEqual(json.loads(self.settings.read_text(encoding="utf-8"))["statusLine"]["command"], OURS)
        self.run_cmd(*base, "unwire", str(self.settings), str(self.state))
        self.assertEqual(json.loads(self.settings.read_text(encoding="utf-8")),
                         {"statusLine": FOREIGN, "keep": 1})
        self.assertFalse(prev.exists())
        self.run_cmd(*base, "unwire", str(self.home / "absent.json"), str(self.state))

    def test_malformed_settings_aborts_install_without_changes(self):
        self.settings.write_text("{not json", encoding="utf-8")
        proc = self.run_cmd("bash", str(REPO / "install.sh"), rc=1)
        self.assertIn("malformed JSON", proc.stderr)
        self.assertEqual(self.settings.read_text(encoding="utf-8"), "{not json")
        self.assertFalse((self.home / ".local").exists())
        self.assertFalse((self.agy / "statusline.py").exists())
        self.run_cmd(sys.executable, str(WIRE), "wire", str(self.settings), str(self.state), rc=1)
        self.assertEqual(self.settings.read_text(encoding="utf-8"), "{not json")

    def test_install_uninstall_then_purge(self):
        self.settings.write_text(json.dumps({"theme": "dark"}), encoding="utf-8")
        self.run_cmd("bash", str(REPO / "install.sh"))
        links = [self.home / ".gemini" / "config" / "plugins" / "ooda", self.agy / "statusline.py",
                 self.home / ".local" / "bin" / "ooda", self.home / ".local" / "bin" / "router"]
        self.assertTrue(all(p.is_symlink() for p in links))
        self.assertEqual(json.loads(self.settings.read_text(encoding="utf-8"))["statusLine"]["command"], OURS)
        out = self.run_cmd("bash", str(REPO / "install.sh"), "--uninstall").stdout
        self.assertFalse(any(os.path.lexists(p) for p in links))
        self.assertEqual(json.loads(self.settings.read_text(encoding="utf-8")), {"theme": "dark"})
        self.assertTrue((self.state / "quota.json").is_file())
        self.assertIn("~/.agents/skills", out)
        again = self.run_cmd("bash", str(REPO / "uninstall.sh")).stdout
        self.assertIn("nothing to remove", again)
        self.run_cmd("bash", str(REPO / "uninstall.sh"), "--purge")
        self.assertFalse(self.state.exists())
        self.assertEqual(json.loads(self.settings.read_text(encoding="utf-8")), {"theme": "dark"})

    def test_uninstall_leaves_foreign_router(self):
        self.run_cmd("bash", str(REPO / "install.sh"))
        router = self.home / ".local" / "bin" / "router"
        router.unlink()
        router.write_text("#!/bin/sh\necho mine\n", encoding="utf-8")
        out = self.run_cmd("bash", str(REPO / "install.sh"), "--uninstall").stdout
        self.assertEqual(router.read_text(encoding="utf-8"), "#!/bin/sh\necho mine\n")
        self.assertIn("left alone", out)
        self.assertFalse(os.path.lexists(self.home / ".local" / "bin" / "ooda"))

    def test_opt_out_skips_statusline_wiring(self):
        self.settings.write_text(json.dumps({"statusLine": FOREIGN}), encoding="utf-8")
        out = self.run_cmd("bash", str(REPO / "install.sh"), env={"OODA_NO_STATUSLINE": "1"}).stdout
        self.assertIn("OODA_NO_STATUSLINE", out)
        self.assertEqual(json.loads(self.settings.read_text(encoding="utf-8")), {"statusLine": FOREIGN})
        self.assertFalse((self.state / "statusline.prev.json").exists())
        self.assertTrue((self.agy / "statusline.py").is_symlink())
        self.assertIn("lane CLIs", out)


if __name__ == "__main__":
    unittest.main()
