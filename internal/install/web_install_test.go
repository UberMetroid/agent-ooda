package install

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitEnv(home string) []string {
	env := []string{
		"HOME=" + home,
		"XDG_CONFIG_HOME=" + filepath.Join(home, ".config"),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_AUTHOR_NAME=tester",
		"GIT_AUTHOR_EMAIL=tester@example.com",
		"GIT_COMMITTER_NAME=tester",
		"GIT_COMMITTER_EMAIL=tester@example.com",
	}
	if p := os.Getenv("PATH"); p != "" {
		env = append(env, "PATH="+p)
	}
	return env
}

func gitRun(t *testing.T, home, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = gitEnv(home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func commitFile(t *testing.T, home, repo, name, body, msg string, mode os.FileMode) string {
	t.Helper()
	dst := filepath.Join(repo, name)
	if err := os.WriteFile(dst, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	gitRun(t, home, repo, "add", name)
	gitRun(t, home, repo, "-c", "commit.gpgsign=false", "commit", "-m", msg)
	return gitRun(t, home, repo, "rev-parse", "HEAD")
}

func runWebInst(t *testing.T, script, home, instHome, repoURL string, args ...string) (string, error) {
	t.Helper()
	cmdArgs := append([]string{script}, args...)
	cmd := exec.Command("bash", cmdArgs...)
	env := append(gitEnv(home), "AGY_ROUTER_HOME="+instHome, "AGY_ROUTER_REPO_URL="+repoURL)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func seedBrokenShallow(t *testing.T, home, upURL, dst, c1 string) {
	t.Helper()
	gitRun(t, home, "", "clone", "--depth=1", upURL, dst)
	if got := gitRun(t, home, dst, "rev-parse", "HEAD"); got != c1 {
		t.Fatalf("expected clone at %s, got %s", c1, got)
	}
	if sh := gitRun(t, home, dst, "rev-parse", "--is-shallow-repository"); sh != "true" {
		t.Fatalf("expected initial shallow clone, got %s", sh)
	}
}

func breakShallowFetch(t *testing.T, home, upURL, dst string) {
	t.Helper()
	gitRun(t, home, dst, "fetch", "--depth=1", upURL, "+refs/heads/main:refs/remotes/origin/main")
	if ahead := gitRun(t, home, dst, "rev-list", "--count", "FETCH_HEAD..HEAD"); ahead != "1" {
		t.Fatalf("expected broken two-root ahead=1, got %s", ahead)
	}
}

func assertLocalCommitRefused(t *testing.T, script, home, clUpdate, upURL string) {
	t.Helper()
	commitFile(t, home, clUpdate, "local.txt", "mine\n", "local change", 0o644)
	out, err := runWebInst(t, script, home, clUpdate, upURL)
	if err == nil || !strings.Contains(out, "has 1 unpushed local commit(s); push or reset before updating") {
		t.Fatalf("expected update refusal on unpushed commit, err=%v out=%s", err, out)
	}
	out, err = runWebInst(t, script, home, clUpdate, upURL, "--uninstall", "--purge")
	if err == nil || !strings.Contains(out, "has 1 unpushed local commit(s); refusing --purge") {
		t.Fatalf("expected purge refusal on unpushed commit, err=%v out=%s", err, out)
	}
	if _, statErr := os.Stat(clUpdate); statErr != nil {
		t.Fatalf("refused --purge deleted checkout %s: %v", clUpdate, statErr)
	}
}

func TestWebInstallShallowHeal(t *testing.T) {
	repoRoot, _ := filepath.Abs(filepath.Join("..", ".."))
	script := filepath.Join(repoRoot, "docs", "install.sh")
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	upDir := filepath.Join(tmp, "upstream")
	_ = os.MkdirAll(home, 0o755)
	_ = os.MkdirAll(upDir, 0o755)

	gitRun(t, home, upDir, "init", "-b", "main")
	c1 := commitFile(t, home, upDir, "install.sh", "#!/usr/bin/env bash\necho \"stub-ok:$*\"\n", "c1", 0o755)
	upURL := "file://" + upDir

	clUpdate := filepath.Join(tmp, "cl-update")
	clPurge := filepath.Join(tmp, "cl-purge")
	clFresh := filepath.Join(tmp, "cl-fresh")
	seedBrokenShallow(t, home, upURL, clUpdate, c1)
	seedBrokenShallow(t, home, upURL, clPurge, c1)

	commitFile(t, home, upDir, "ver.txt", "v2\n", "c2", 0o644)
	commitFile(t, home, upDir, "ver.txt", "v3\n", "c3", 0o644)
	breakShallowFetch(t, home, upURL, clUpdate)
	breakShallowFetch(t, home, upURL, clPurge)
	c4 := commitFile(t, home, upDir, "ver.txt", "v4\n", "c4", 0o644)

	out, err := runWebInst(t, script, home, clPurge, upURL, "--uninstall", "--purge")
	if err != nil || !strings.Contains(out, "stub-ok:--uninstall --purge") {
		t.Fatalf("purge failed on broken shallow clone: err=%v out=%s", err, out)
	}
	if _, statErr := os.Stat(clPurge); !os.IsNotExist(statErr) {
		t.Fatalf("expected %s to be purged, statErr=%v", clPurge, statErr)
	}

	out, err = runWebInst(t, script, home, clUpdate, upURL)
	if err != nil || !strings.Contains(out, "stub-ok:") {
		t.Fatalf("update failed on broken shallow clone: err=%v out=%s", err, out)
	}
	if got := gitRun(t, home, clUpdate, "rev-parse", "HEAD"); got != c4 {
		t.Fatalf("expected updated HEAD %s, got %s", c4, got)
	}
	if sh := gitRun(t, home, clUpdate, "rev-parse", "--is-shallow-repository"); sh != "false" {
		t.Fatalf("expected unshallowed update clone, got %s", sh)
	}

	if out, err = runWebInst(t, script, home, clFresh, upURL); err != nil || !strings.Contains(out, "stub-ok:") {
		t.Fatalf("fresh clone failed: err=%v out=%s", err, out)
	}
	if got := gitRun(t, home, clFresh, "rev-parse", "HEAD"); got != c4 {
		t.Fatalf("expected fresh HEAD %s, got %s", c4, got)
	}
	if sh := gitRun(t, home, clFresh, "rev-parse", "--is-shallow-repository"); sh != "false" {
		t.Fatalf("expected non-shallow fresh clone, got %s", sh)
	}

	assertLocalCommitRefused(t, script, home, clUpdate, upURL)
}
