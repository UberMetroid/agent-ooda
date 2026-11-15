package worktree

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/UberMetroid/agent-ooda/internal/fleet"
)

func TestRelPathAndManifest(t *testing.T) {
	for _, bad := range []string{"../escape.py", "src/../../x.go", "/etc/passwd", `C:\foo`, `\\etc\pw`} {
		if _, err := NormalizeRelPath(bad); err == nil {
			t.Fatalf("expected refusal for unsafe path %q", bad)
		}
	}
	if got, err := NormalizeRelPath("./a/b/../c.go"); err != nil || got != "a/c.go" {
		t.Fatalf("unexpected normalize result: %q (%v)", got, err)
	}
	dir := t.TempDir()
	overlap := filepath.Join(dir, "tasks.json")
	content := `[{"id":"a","prompt":"x","files":["src"]},{"id":"b","prompt":"y","files":["src/a.go"]}]`
	if err := os.WriteFile(overlap, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadManifest(overlap); err == nil {
		t.Fatal("expected overlap refusal between src and src/a.go")
	}
	valid := filepath.Join(dir, "ok.json")
	okBody := `{"tasks":[{"id":"t1","prompt":"hi","files":["cmd/a.go"],"timeout":"2m"}]}`
	if err := os.WriteFile(valid, []byte(okBody), 0o644); err != nil {
		t.Fatal(err)
	}
	tasks, err := LoadManifest(valid)
	if err != nil || len(tasks) != 1 || tasks[0].ID != "t1" {
		t.Fatalf("expected valid manifest load, got %v (%v)", tasks, err)
	}
}

func TestSkillsAndFrontmatter(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	ws := t.TempDir()
	skDir := filepath.Join(ws, ".agents", "skills", "demo-skill")
	if err := os.MkdirAll(skDir, 0o755); err != nil {
		t.Fatal(err)
	}
	md := "---\nname: demo-skill\ndescription: >-\n  First line\n  second line.\n---\nBody text.\n"
	if err := os.WriteFile(filepath.Join(skDir, "SKILL.md"), []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}
	skills := DiscoverSkills(ws)
	found := false
	for _, sk := range skills {
		if sk.Name == "demo-skill" && sk.Desc == "First line second line." && !sk.Core {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected discovered demo-skill with folded description, got %+v", skills)
	}
	block, err := LoadSkillBlocks([]string{"ooda:demo-skill"}, ws)
	if err != nil || !strings.Contains(block, "Body text.") {
		t.Fatalf("LoadSkillBlocks failed: %v", err)
	}
	if _, err := LoadSkillBlocks([]string{"../bad"}, ws); err == nil {
		t.Fatal("expected invalid skill name error")
	}
}

func TestDirtyStateAndScope(t *testing.T) {
	repo := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		res, err := fleet.RunCmd(fleet.CmdSpec{Argv: append([]string{"git"}, args...), Cwd: repo})
		if err != nil || res.ExitCode != 0 {
			t.Fatalf("git %v failed: %v %s", args, err, res.Combined)
		}
	}
	runGit("init")
	runGit("config", "user.email", "test@example.com")
	runGit("config", "user.name", "Tester")
	if err := os.WriteFile(filepath.Join(repo, "init.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "init.txt")
	runGit("commit", "-m", "init")
	pre := filepath.Join(repo, "pre space.txt")
	if err := os.WriteFile(pre, []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	base := DirtyState(repo)
	if len(ChangedFilesIn(repo, base)) != 0 {
		t.Fatalf("expected zero changes vs matching baseline")
	}
	if err := os.WriteFile(pre, []byte("v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	changed := ChangedFilesIn(repo, base)
	if len(changed) != 1 || changed[0] != "pre space.txt" {
		t.Fatalf("expected mutated pre-dirty file detected, got %v", changed)
	}
	if viols := FindScopeViolations(changed, []string{"other.txt"}); len(viols) != 1 {
		t.Fatalf("expected scope violation, got %v", viols)
	}
}
