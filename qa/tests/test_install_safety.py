"""install.sh safety: preflight, foreign/null/echo statusLine restore, skills.json, tmp cleanup."""

import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest

REPO = Path(__file__).resolve().parents[2]
OURS = "~/.gemini/antigravity-cli/statusline.py"
FOREIGN_1 = {"type": "command", "command": "~/bin/status-one.sh", "padding": 1}
FOREIGN_2 = {"type": "command", "command": "~/bin/status-two.sh", "padding": 2}


class InstallSafetyTests(unittest.TestCase):
    """Guards that keep install/uninstall from destroying the repo or half-applying changes."""

    def setUp(self):
        self.home = Path(tempfile.mkdtemp(prefix="ooda-safe-"))
        self.addCleanup(shutil.rmtree, self.home, True)
        self.agy = self.home / ".gemini" / "antigravity-cli"
        self.agy.mkdir(parents=True)
        self.settings = self.agy / "settings.json"
        self.state = self.home / ".local" / "state" / "ooda"
        self.env = {"HOME": str(self.home), "PATH": os.environ.get("PATH", "/usr/bin:/bin")}
        self.links = [self.home / ".gemini" / "config" / "plugins" / "ooda", self.agy / "statusline.py",
                      self.home / ".local" / "bin" / "ooda", self.home / ".local" / "bin" / "router"]

    def run_cmd(self, *argv, rc=0, env=None, cwd=None):
        """Run argv with the temp-HOME env, assert its exit code, and return the process."""
        proc = subprocess.run(list(argv), env=dict(self.env, **(env or {})), cwd=str(cwd or self.home),
                              capture_output=True, text=True, timeout=120)
        self.assertEqual(proc.returncode, rc, proc.stdout + proc.stderr)
        return proc

    def sandbox_repo(self):
        """Copy install.sh, uninstall.sh, and plugins/ooda so destructive cases never touch REPO."""
        root = Path(tempfile.mkdtemp(prefix="ooda-repo-"))
        self.addCleanup(shutil.rmtree, root, True)
        for name in ("install.sh", "uninstall.sh"):
            shutil.copy2(str(REPO / name), str(root / name))
        shutil.copytree(str(REPO / "plugins" / "ooda"), str(root / "plugins" / "ooda"), symlinks=True,
                        ignore=shutil.ignore_patterns("__pycache__"))
        return root

    def test_help_and_unknown_option(self):
        out = self.run_cmd("bash", str(REPO / "install.sh"), "--help").stdout
        for word in ("--uninstall", "--purge", "OODA_NO_STATUSLINE"):
            self.assertIn(word, out)
        err = self.run_cmd("bash", str(REPO / "install.sh"), "--bogus", rc=2).stderr
        self.assertIn("unknown option", err)
        self.assertFalse((self.home / ".local").exists())

    def test_unsafe_state_dir_aborts_without_deleting_plugin(self):
        root = self.sandbox_repo()
        marker = root / "plugins" / "ooda" / "bin" / "ooda"
        for xdg in ("plugins", str(root / "plugins"), str(root)):
            for args in ((), ("--uninstall", "--purge")):
                proc = self.run_cmd("bash", str(root / "install.sh"), *args, rc=1,
                                    env={"XDG_STATE_HOME": xdg}, cwd=root)
                self.assertIn("XDG_STATE_HOME", proc.stderr)
                self.assertTrue(marker.is_file(), f"plugin tree damaged by {xdg} {args}")
                self.assertFalse((root / "plugins" / "ooda" / "quota.json").exists())
        self.assertFalse((self.home / ".local").exists())
        self.assertFalse(os.path.lexists(self.agy / "statusline.py"))
        self.assertFalse(self.settings.exists())
        blocker = self.home / ".local" / "bin"
        blocker.parent.mkdir(parents=True, exist_ok=True)
        blocker.write_text("not a dir\n", encoding="utf-8")
        proc = self.run_cmd("bash", str(REPO / "install.sh"), rc=1)
        self.assertIn("not a directory", proc.stderr)
        self.assertTrue(blocker.is_file())
        self.assertFalse((self.home / ".local" / "state").exists())
        self.assertFalse((self.home / ".gemini" / "config").exists())

    def test_non_file_settings_or_skills_aborts_preflight(self):
        if os.geteuid() != 0:
            cfg = self.home / ".gemini" / "config"
            cfg.mkdir()
            self.addCleanup(lambda: cfg.chmod(0o755) if cfg.is_dir() else None)
            cfg.chmod(0o555)
            err = self.run_cmd("bash", str(REPO / "install.sh"), rc=1).stderr
            self.assertIn("not writable", err)
            self.assertFalse((self.home / ".local").exists())
            cfg.chmod(0o755)
            frozen = cfg / "skills.json"
            frozen.write_text("{}\n", encoding="utf-8")
            self.addCleanup(lambda: frozen.chmod(0o644) if frozen.is_file() else None)
            frozen.chmod(0o444)
            err = self.run_cmd("bash", str(REPO / "install.sh"), rc=1).stderr
            self.assertIn("not writable", err)
            self.assertFalse((self.home / ".local").exists())
            shutil.rmtree(cfg)
        self.settings.mkdir()
        err = self.run_cmd("bash", str(REPO / "install.sh"), rc=1).stderr
        self.assertIn("not a regular file", err)
        self.settings.rmdir()
        self.settings.symlink_to(self.home / "missing-target.json")
        self.assertIn("not a regular file", self.run_cmd("bash", str(REPO / "install.sh"), rc=1).stderr)
        self.settings.unlink()
        skills = self.home / ".gemini" / "config" / "skills.json"
        skills.mkdir(parents=True)
        self.assertIn("not a regular file", self.run_cmd("bash", str(REPO / "install.sh"), rc=1).stderr)
        self.assertTrue(skills.is_dir())
        self.assertFalse((self.home / ".local").exists())
        self.assertFalse(any(os.path.lexists(p) for p in self.links))
        self.assertFalse(os.path.lexists(self.settings))
        scratch = self.home / "scratch"
        scratch.mkdir()
        code = (
            "import importlib.util,os,sys\n"
            "s=importlib.util.spec_from_file_location('w',sys.argv[1])\n"
            "m=importlib.util.module_from_spec(s);s.loader.exec_module(m)\n"
            "try:\n m.write_json(sys.argv[2],{'bad':set()})\nexcept TypeError:\n pass\nelse:\n sys.exit(3)\n"
            "raise SystemExit(any('.tmp.' in n for n in os.listdir(sys.argv[3])))\n"
        )
        wire = REPO / "plugins" / "ooda" / "src" / "cli" / "wire_settings.py"
        probe = subprocess.run([sys.executable, "-c", code, str(wire), str(scratch / "blob.json"), str(scratch)],
                               capture_output=True, text=True, timeout=30)
        self.assertEqual(probe.returncode, 0, probe.stdout + probe.stderr)
        # Patch os.replace so skills_json update fails after it creates its temp file.
        shutil.rmtree(skills)
        skills.write_text("{}\n", encoding="utf-8")
        hook = scratch / "hook"
        hook.mkdir()
        (hook / "sitecustomize.py").write_text(
            "import os\ndef boom(src, dst):\n raise OSError('boom')\nos.replace = boom\n", encoding="utf-8")
        failed = self.run_cmd("bash", str(REPO / "install.sh"), rc=1, env={"PYTHONPATH": str(hook)})
        self.assertIn("boom", failed.stderr)
        leaked = [n for _r, _d, names in os.walk(self.home) for n in names if ".tmp." in n]
        self.assertEqual(leaked, [])
        self.assertEqual(skills.read_text(encoding="utf-8"), "{}\n")

    def test_newer_foreign_statusline_restored_and_older_archived(self):
        self.assertFalse((self.home / ".gemini" / "config").exists())
        self.settings.write_text(json.dumps({"statusLine": FOREIGN_1}), encoding="utf-8")
        self.run_cmd("bash", str(REPO / "install.sh"))
        skills = json.loads((self.home / ".gemini" / "config" / "skills.json").read_text(encoding="utf-8"))
        self.assertIn({"path": "~/.agents/skills"}, skills["entries"])
        data = json.loads(self.settings.read_text(encoding="utf-8"))
        self.assertEqual(data["statusLine"]["command"], OURS)
        data["statusLine"] = FOREIGN_2
        self.settings.write_text(json.dumps(data), encoding="utf-8")
        self.run_cmd("bash", str(REPO / "install.sh"))
        prev = self.state / "statusline.prev.json"
        self.assertEqual(json.loads(prev.read_text(encoding="utf-8")), FOREIGN_2)
        archived = sorted((self.state / "backups").glob("statusline-prev-*.json"))
        self.assertEqual([json.loads(p.read_text(encoding="utf-8")) for p in archived], [FOREIGN_1])
        self.run_cmd("bash", str(REPO / "install.sh"), "--uninstall")
        self.assertEqual(json.loads(self.settings.read_text(encoding="utf-8")), {"statusLine": FOREIGN_2})
        self.assertFalse(prev.exists())
        self.assertTrue(archived[0].is_file())
        echo = {"type": "command", "command": "echo " + OURS, "padding": 4}
        self.settings.write_text(json.dumps({"statusLine": echo, "keep": 7}), encoding="utf-8")
        self.run_cmd("bash", str(REPO / "install.sh"))
        self.assertEqual(json.loads(self.settings.read_text(encoding="utf-8"))["statusLine"]["command"], OURS)
        self.assertEqual(json.loads(prev.read_text(encoding="utf-8")), echo)
        self.run_cmd("bash", str(REPO / "install.sh"), "--uninstall")
        self.assertEqual(json.loads(self.settings.read_text(encoding="utf-8")), {"statusLine": echo, "keep": 7})
        self.settings.write_text(json.dumps({"statusLine": None, "keep": 1}), encoding="utf-8")
        self.run_cmd("bash", str(REPO / "install.sh"))
        self.assertEqual(json.loads(self.settings.read_text(encoding="utf-8"))["statusLine"]["command"], OURS)
        self.assertIsNone(json.loads(prev.read_text(encoding="utf-8")))
        self.run_cmd("bash", str(REPO / "install.sh"), "--uninstall")
        self.assertEqual(json.loads(self.settings.read_text(encoding="utf-8")), {"statusLine": None, "keep": 1})

    def test_corrupt_prev_aborts_uninstall_before_removing_links(self):
        self.settings.write_text(json.dumps({"statusLine": FOREIGN_1}), encoding="utf-8")
        self.run_cmd("bash", str(REPO / "install.sh"))
        self.assertTrue(all(p.is_symlink() for p in self.links))
        if os.geteuid() != 0:
            link_dir = self.home / ".local" / "bin"
            self.addCleanup(lambda: link_dir.chmod(0o755) if link_dir.is_dir() else None)
            link_dir.chmod(0o555)
            denied = self.run_cmd("bash", str(REPO / "install.sh"), "--uninstall", rc=1).stderr
            self.assertIn("not writable", denied)
            self.assertTrue(all(p.is_symlink() and p.exists() for p in self.links))
            self.assertEqual(json.loads(self.settings.read_text(encoding="utf-8"))["statusLine"]["command"], OURS)
            link_dir.chmod(0o755)
        prev = self.state / "statusline.prev.json"
        prev.write_text("{corrupt", encoding="utf-8")
        before = self.settings.read_text(encoding="utf-8")
        for args in (("--uninstall",), ("--uninstall", "--purge")):
            err = self.run_cmd("bash", str(REPO / "install.sh"), *args, rc=1).stderr
            self.assertIn("statusline.prev.json", err)
            self.assertTrue(all(p.is_symlink() and p.exists() for p in self.links))
            self.assertEqual(self.settings.read_text(encoding="utf-8"), before)
            self.assertEqual(prev.read_text(encoding="utf-8"), "{corrupt")
            self.assertTrue((self.state / "quota.json").is_file())
        self.assertEqual(json.loads(before)["statusLine"]["command"], OURS)
        prev.unlink()
        ooda_link = self.home / ".local" / "bin" / "ooda"
        ooda_link.unlink()
        ooda_link.symlink_to(REPO / "plugins" / "ooda" / "bin" / "missing-target")
        self.run_cmd("bash", str(REPO / "install.sh"), "--uninstall")
        self.assertFalse(os.path.lexists(ooda_link))


if __name__ == "__main__":
    unittest.main()
