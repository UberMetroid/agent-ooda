package install

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSetupCheckStateAndDirs(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	repo := filepath.Join(tmp, "repo")
	state := filepath.Join(tmp, "state", "ooda")
	_ = os.MkdirAll(home, 0o755)
	_ = os.MkdirAll(repo, 0o755)

	var errB bytes.Buffer
	if rc := RunSetupCmd([]string{"check-state", filepath.Dir(state), state, home, repo}, &errB); rc != 0 {
		t.Fatalf("valid check-state failed: %s", errB.String())
	}
	if rc := RunSetupCmd([]string{"check-state", "rel", state, home, repo}, &errB); rc != 1 {
		t.Fatalf("relative base must fail")
	}
	if rc := RunSetupCmd([]string{"check-state", home, home, home, repo}, &errB); rc != 1 {
		t.Fatalf("state == $HOME must fail")
	}
	if rc := RunSetupCmd([]string{"check-state", repo, filepath.Join(repo, "sub"), home, repo}, &errB); rc != 1 {
		t.Fatalf("state inside repo must fail")
	}
	sym := filepath.Join(tmp, "sym-into-repo")
	_ = os.Symlink(repo, sym)
	if rc := RunSetupCmd([]string{"check-state", sym, filepath.Join(sym, "s"), home, repo}, &errB); rc != 1 {
		t.Fatalf("symlink into repo must fail")
	}

	goodDir := filepath.Join(tmp, "nested", "newdir")
	if rc := RunSetupCmd([]string{"check-dirs", goodDir}, &errB); rc != 0 {
		t.Fatalf("check-dirs valid failed: %s", errB.String())
	}
	_ = os.MkdirAll(goodDir, 0o755)
	symDir, badSym := filepath.Join(tmp, "symdir"), filepath.Join(tmp, "badsym")
	_ = os.Symlink(goodDir, symDir)
	_ = os.Symlink(filepath.Join(tmp, "missing-target"), badSym)
	if rc := RunSetupCmd([]string{"check-dirs", filepath.Join(symDir, "sub")}, &errB); rc != 0 {
		t.Fatalf("symlinked dir must pass check-dirs: %s", errB.String())
	}
	if rc := RunSetupCmd([]string{"check-dirs", badSym}, &errB); rc != 1 {
		t.Fatalf("broken symlink must fail check-dirs")
	}
	blocker := filepath.Join(tmp, "blocker")
	_ = os.WriteFile(blocker, []byte("x"), 0o644)
	if rc := RunSetupCmd([]string{"check-dirs", filepath.Join(blocker, "child")}, &errB); rc != 1 {
		t.Fatalf("file blocker must fail check-dirs")
	}

	qOK := filepath.Join(tmp, "q_ok.json")
	qBad := filepath.Join(tmp, "q_bad.json")
	_ = os.WriteFile(qOK, []byte(`{"providers":{"mcode":{"status":"active"}}}`), 0o644)
	_ = os.WriteFile(qBad, []byte(`{"providers":{}}`), 0o644)
	if RunSetupCmd([]string{"valid-quota", qOK}, &errB) != 0 || RunSetupCmd([]string{"valid-quota", qBad}, &errB) != 1 {
		t.Fatalf("valid-quota check mismatch")
	}
}

func TestSetupSkillsAndOwnedLink(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	t.Setenv("HOME", home)
	skPath := filepath.Join(home, ".gemini", "config", "skills.json")
	_ = os.MkdirAll(filepath.Dir(skPath), 0o755)
	_ = writeWireJSON(skPath, map[string]any{
		"entries": []any{
			map[string]any{"path": "~/.agents/skills/router"},
			map[string]any{"path": "~/.agents/skills/ooda"},
			map[string]any{"path": "~/.agents/third-party"},
		},
	})
	var errB bytes.Buffer
	if RunSetupCmd([]string{"skills-check", skPath}, &errB) != 0 {
		t.Fatalf("skills-check failed: %s", errB.String())
	}
	if RunSetupCmd([]string{"skills-update", skPath}, &errB) != 0 {
		t.Fatalf("skills-update failed: %s", errB.String())
	}
	raw, _ := os.ReadFile(skPath)
	var data map[string][]map[string]any
	_ = json.Unmarshal(raw, &data)
	if len(data["entries"]) != 2 || data["entries"][0]["path"] != "~/.agents/third-party" ||
		data["entries"][1]["path"] != "~/.agents/skills" {
		t.Fatalf("unexpected updated skills.json: %s", raw)
	}
	_ = os.WriteFile(skPath, []byte(`{"entries":"nope"}`), 0o644)
	if RunSetupCmd([]string{"skills-check", skPath}, &errB) != 1 {
		t.Fatalf("malformed skills.json entries must fail")
	}

	pDir := filepath.Join(tmp, "plugins", "agent-ooda")
	_ = os.MkdirAll(filepath.Join(pDir, "bin"), 0o755)
	_ = os.WriteFile(filepath.Join(pDir, "bin", "agent-ooda"), []byte("x"), 0o755)
	ownLink := filepath.Join(tmp, "own")
	brokenOwn := filepath.Join(tmp, "broken-own")
	foreignLink := filepath.Join(tmp, "foreign")
	_ = os.Symlink(filepath.Join(pDir, "bin", "agent-ooda"), ownLink)
	_ = os.Symlink(filepath.Join(pDir, "bin", "missing"), brokenOwn)
	_ = os.Symlink(filepath.Join(tmp, "other"), foreignLink)
	if RunSetupCmd([]string{"owned-link", ownLink, pDir}, &errB) != 0 ||
		RunSetupCmd([]string{"owned-link", brokenOwn, pDir}, &errB) != 0 ||
		RunSetupCmd([]string{"owned-link", foreignLink, pDir}, &errB) != 1 {
		t.Fatalf("owned-link classification mismatch")
	}
}

func runBash(t *testing.T, dir string, env []string, script string, args ...string) string {
	t.Helper()
	cmd := exec.Command("bash", append([]string{script}, args...)...)
	cmd.Dir = dir
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v failed: %v\n%s", script, args, err, out)
	}
	return string(out)
}

func stageTestRepo(t *testing.T, srcRoot, dstRoot string) {
	t.Helper()
	_ = os.MkdirAll(filepath.Join(dstRoot, "plugins", "agent-ooda", "bin"), 0o755)
	_ = os.MkdirAll(filepath.Join(dstRoot, "plugins", "agent-ooda", "config"), 0o755)
	for _, rel := range []string{
		"install.sh", "uninstall.sh",
		filepath.Join("plugins", "agent-ooda", "bin", "agent-ooda"),
		filepath.Join("plugins", "agent-ooda", "config", "quota.default.json"),
		filepath.Join("plugins", "agent-ooda", "plugin.json"),
	} {
		mode := os.FileMode(0o644)
		if strings.HasSuffix(rel, ".sh") || strings.HasSuffix(rel, "agent-ooda") {
			mode = 0o755
		}
		_ = os.WriteFile(filepath.Join(dstRoot, rel), readWireRaw(t, filepath.Join(srcRoot, rel)), mode)
	}
}

func TestInstallAndUninstallScript(t *testing.T) {
	repoRoot, _ := filepath.Abs(filepath.Join("..", ".."))
	binPath := filepath.Join(repoRoot, "plugins", "agent-ooda", "bin", "agent-ooda")
	if _, err := os.Stat(binPath); err != nil {
		t.Fatalf("compiled %s missing: %v", binPath, err)
	}
	tmp := t.TempDir()
	staged := filepath.Join(tmp, "staged-repo")
	stageTestRepo(t, repoRoot, staged)
	fakeHome := filepath.Join(tmp, "home")
	_ = os.MkdirAll(fakeHome, 0o755)
	env := []string{
		"HOME=" + fakeHome, "XDG_STATE_HOME=" + filepath.Join(fakeHome, ".local", "state"),
		"AGENT_OODA_NO_BUILD=1", "PATH=" + os.Getenv("PATH"),
	}
	if instOut := runBash(t, staged, env, filepath.Join(staged, "install.sh")); !strings.Contains(
		instOut, "agent-ooda install complete") {
		t.Fatalf("unexpected install output: %s", instOut)
	}
	setPath := filepath.Join(fakeHome, ".gemini", "antigravity-cli", "settings.json")
	if !strings.Contains(string(readWireRaw(t, setPath)), "statusline.py") {
		t.Fatalf("settings.json missing statusLine after install")
	}
	if _, rerr := os.Lstat(filepath.Join(fakeHome, ".local", "bin", "agent-ooda")); rerr != nil {
		t.Fatalf("expected agent-ooda bin symlink: %v", rerr)
	}
	if _, rerr := os.Lstat(filepath.Join(fakeHome, ".local", "bin", "router")); !os.IsNotExist(rerr) {
		t.Fatalf("legacy router symlink should not be created")
	}
	if unOut := runBash(t, staged, env, filepath.Join(staged, "uninstall.sh"), "--purge"); !strings.Contains(
		unOut, "purged") {
		t.Fatalf("unexpected uninstall --purge output: %s", unOut)
	}

	noBinRepo := filepath.Join(tmp, "nobin-repo")
	_ = os.MkdirAll(filepath.Join(noBinRepo, "plugins", "agent-ooda"), 0o755)
	for _, name := range []string{"install.sh", "uninstall.sh"} {
		_ = os.WriteFile(filepath.Join(noBinRepo, name), readWireRaw(t, filepath.Join(repoRoot, name)), 0o755)
	}
	symTarget := filepath.Join(fakeHome, ".local", "bin", "agent-ooda")
	_ = os.MkdirAll(filepath.Dir(symTarget), 0o755)
	_ = os.Symlink(filepath.Join(noBinRepo, "plugins", "agent-ooda", "bin", "agent-ooda"), symTarget)
	if noBinOut := runBash(t, noBinRepo, []string{
		"HOME=" + fakeHome, "XDG_STATE_HOME=" + filepath.Join(fakeHome, ".local", "state"), "PATH=/usr/bin:/bin",
	}, filepath.Join(noBinRepo, "uninstall.sh"), "--purge"); !strings.Contains(noBinOut, symTarget) {
		t.Fatalf("uninstall without binary failed: %s", noBinOut)
	}
}

func readWireRaw(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestInstallPrebuiltWithoutGo(t *testing.T) {
	repoRoot, _ := filepath.Abs(filepath.Join("..", ".."))
	binSrc := readWireRaw(t, filepath.Join(repoRoot, "plugins", "agent-ooda", "bin", "agent-ooda"))
	tmp := t.TempDir()
	noGoBin, relDir, repoDir, fakeHome := filepath.Join(tmp, "bin"), filepath.Join(tmp, "rel"),
		filepath.Join(tmp, "repo"), filepath.Join(tmp, "home")
	for _, d := range []string{noGoBin, relDir, filepath.Join(repoDir, "plugins", "agent-ooda", "config"), fakeHome} {
		_ = os.MkdirAll(d, 0o755)
	}
	tools := []string{"bash", "dirname", "cat", "uname", "mktemp", "curl", "awk", "sha256sum",
		"shasum", "install", "mkdir", "chmod", "cp", "mv", "ln", "date", "readlink", "sed", "rm"}
	for _, name := range tools {
		if p, err := exec.LookPath(name); err == nil {
			_ = os.Symlink(p, filepath.Join(noGoBin, name))
		}
	}
	asset := fmt.Sprintf("agent-ooda-%s-%s", runtime.GOOS, runtime.GOARCH)
	_ = os.WriteFile(filepath.Join(relDir, asset), binSrc, 0o755)
	_ = os.WriteFile(filepath.Join(relDir, "SHA256SUMS"),
		[]byte(fmt.Sprintf("%x  %s\n", sha256.Sum256(binSrc), asset)), 0o644)
	for _, name := range []string{"install.sh", "uninstall.sh"} {
		_ = os.WriteFile(filepath.Join(repoDir, name), readWireRaw(t, filepath.Join(repoRoot, name)), 0o755)
	}
	qDef := filepath.Join("plugins", "agent-ooda", "config", "quota.default.json")
	_ = os.WriteFile(filepath.Join(repoDir, qDef), readWireRaw(t, filepath.Join(repoRoot, qDef)), 0o644)

	runInst := func() error {
		cmd := exec.Command("bash", filepath.Join(repoDir, "install.sh"))
		cmd.Dir = repoDir
		cmd.Env = []string{
			"HOME=" + fakeHome, "XDG_STATE_HOME=" + filepath.Join(fakeHome, ".local", "state"),
			"PATH=" + noGoBin, "AGENT_OODA_RELEASE_BASE=file://" + relDir,
		}
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("%v: %s", err, out)
		}
		return nil
	}
	if err := runInst(); err != nil {
		t.Fatalf("prebuilt no-Go install failed: %v", err)
	}
	_ = os.Remove(filepath.Join(repoDir, "plugins", "agent-ooda", "bin", "agent-ooda"))
	_ = os.WriteFile(filepath.Join(relDir, "SHA256SUMS"), []byte(strings.Repeat("0", 64)+"  "+asset+"\n"), 0o644)
	if err := runInst(); err == nil {
		t.Fatal("expected SHA-256 mismatch to fail closed")
	}
}
