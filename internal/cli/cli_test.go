package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/UberMetroid/agent-ooda/internal/fleet"
)

func writeExecFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func setupCLIEnv(t *testing.T) (string, string, string) {
	t.Helper()
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	repo := filepath.Join(tmp, "repo")
	binDir := filepath.Join(tmp, "bin")
	_ = os.MkdirAll(home, 0o755)
	_ = os.MkdirAll(repo, 0o755)
	_ = os.MkdirAll(binDir, 0o755)
	runGit := func(args ...string) {
		full := append([]string{
			"-c", "user.name=t", "-c", "user.email=t@invalid", "-c", "commit.gpgsign=false",
		}, args...)
		cmd := exec.Command("git", full...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	runGit("init", "-q")
	_ = os.WriteFile(filepath.Join(repo, "a.go"), []byte("package a\n"), 0o644)
	runGit("add", "a.go")
	runGit("commit", "-q", "-m", "seed")

	qPath := filepath.Join(tmp, "quota.json")
	if err := fleet.AtomicWriteJSON(qPath, fleet.DefaultQuotaData(), false, true); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("AGENT_QUOTA_FILE", qPath)
	t.Setenv("OODA_DISCOVERY_FILE", filepath.Join(tmp, "discovery.json"))
	t.Setenv("OODA_NO_AUTO_PROBE", "1")
	t.Setenv("ROUTER_HUD_DIR", filepath.Join(tmp, "hud"))
	t.Setenv("ROUTER_SLOTS_DIR", filepath.Join(tmp, "slots"))
	t.Setenv("ROUTER_DEPTH", "0")
	t.Setenv("NO_COLOR", "1")
	gitBin, _ := exec.LookPath("git")
	t.Setenv("PATH", binDir+":"+filepath.Dir(gitBin)+":/bin:/usr/bin")
	return repo, qPath, binDir
}

func TestParseArgsPermutations(t *testing.T) {
	var helpOut bytes.Buffer
	opts, err := ParseArgs("router", []string{"-h"}, &helpOut)
	if err != nil || !opts.Help || !strings.Contains(helpOut.String(), "usage: router") {
		t.Fatalf("unexpected help parse: opts=%+v err=%v", opts, err)
	}
	o1, err := ParseArgs("ooda", []string{"--review"}, nil)
	if err != nil || o1.Review == nil || *o1.Review != "." {
		t.Fatalf("expected --review default '.', got %+v err=%v", o1, err)
	}
	o2, err := ParseArgs("ooda", []string{"--review", "sub", "-j6", "--files", "a.go,b.go", "--files", "c.go"}, nil)
	if err != nil || o2.Review == nil || *o2.Review != "sub" || o2.Jobs != 6 || len(o2.Files) != 2 {
		t.Fatalf("unexpected flag mix parse: %+v err=%v", o2, err)
	}
	o3, err := ParseArgs("ooda", []string{"--", "--weird-manifest"}, nil)
	if err != nil || o3.Manifest != "--weird-manifest" {
		t.Fatalf("unexpected -- parse: %+v err=%v", o3, err)
	}
	if _, err := ParseArgs("ooda", []string{"--bogus-flag"}, nil); err == nil {
		t.Fatal("expected error for unknown flag")
	}
}

func TestStatusUsageAndQuotaAdmin(t *testing.T) {
	_, _, _ = setupCLIEnv(t)
	var out, errB bytes.Buffer
	if rc := RunCLI("router", []string{"--status"}, &out, &errB); rc != 0 ||
		!strings.Contains(out.String(), "LANE") {
		t.Fatalf("--status failed rc=%d out=%q err=%q", rc, out.String(), errB.String())
	}
	out.Reset()
	if rc := RunCLI("router", []string{"--discover"}, &out, &errB); rc != 0 ||
		!strings.Contains(out.String(), "(refreshed)") {
		t.Fatalf("--discover failed rc=%d out=%q", rc, out.String())
	}
	out.Reset()
	if rc := RunCLI("router", []string{"--usage"}, &out, &errB); rc != 0 ||
		!strings.Contains(out.String(), "Cycle Usage Telemetry") {
		t.Fatalf("--usage failed rc=%d out=%q", rc, out.String())
	}
	out.Reset()
	if rc := RunCLI("router", []string{"--disable", "grok", "--reason", "maint", "--hours", "1"}, &out, &errB); rc != 0 {
		t.Fatalf("--disable failed rc=%d err=%q", rc, errB.String())
	}
	if rc := RunCLI("router", []string{"--enable", "grok"}, &out, &errB); rc != 0 {
		t.Fatalf("--enable failed rc=%d err=%q", rc, errB.String())
	}
	errB.Reset()
	if rc := RunCLI("router", []string{"--set-cycle", "agy-opus", "--model", "gemini-4-carbon-low"}, &out, &errB); rc != 2 {
		t.Fatalf("expected Gemini guardrail exit 2, got %d (%q)", rc, errB.String())
	}
	out.Reset()
	errB.Reset()
	if rc := RunCLI("router", []string{"--recon", "hi", "--model", "gemini-4-carbon-high"}, &out, &errB); rc != 2 ||
		strings.Contains(errB.String(), "pivoting") {
		t.Fatalf("expected clean upfront Gemini block exit 2, got rc=%d err=%q", rc, errB.String())
	}
	out.Reset()
	rc := RunCLI("router", []string{
		"--set-cycle", "mcode", "--effort", "high", "--token-budget", "5000",
	}, &out, &errB)
	if rc != 0 || !strings.Contains(out.String(), "configured mcode:") {
		t.Fatalf("--set-cycle mcode failed rc=%d out=%q err=%q", rc, out.String(), errB.String())
	}
}

func TestRouterDepthGuard(t *testing.T) {
	_, _, _ = setupCLIEnv(t)
	var cOut, cErr bytes.Buffer
	if rc := RunCLI("router", []string{"--status", "--enable", "grok"}, &cOut, &cErr); rc != 2 ||
		!strings.Contains(cErr.String(), "conflicting") {
		t.Fatalf("mixed modes at ROUTER_DEPTH=0: rc=%d err=%q", rc, cErr.String())
	}
	t.Setenv("ROUTER_DEPTH", "1")
	blocked := [][]string{
		{"--recon", "hi"}, {"--task", "hi"}, {"--review", "."},
		{"--enable", "grok"}, {"--disable", "grok"}, {"--set-cycle", "grok"}, {"--cleanup"},
	}
	for _, argv := range blocked {
		var out, errB bytes.Buffer
		if rc := RunCLI("router", argv, &out, &errB); rc != 2 ||
			!strings.Contains(errB.String(), "ROUTER_DEPTH>=1") {
			t.Fatalf("%v under ROUTER_DEPTH=1: rc=%d err=%q", argv, rc, errB.String())
		}
	}
	var out, errB bytes.Buffer
	if rc := RunCLI("router", []string{"--status"}, &out, &errB); rc != 0 {
		t.Fatalf("--status should work when ROUTER_DEPTH=1, got %d (%q)", rc, errB.String())
	}
}

func TestSingleTaskScopeAndReject(t *testing.T) {
	repo, _, binDir := setupCLIEnv(t)
	oldWd, _ := os.Getwd()
	_ = os.Chdir(repo)
	defer func() { _ = os.Chdir(oldWd) }()
	_ = os.WriteFile(filepath.Join(repo, "dirty.txt"), []byte("keep me\n"), 0o644)

	writeExecFile(t, binDir, "grok",
		"echo 'package main' > escape.txt\necho '{\"content\":\"done\"}'\n")
	var out, errB bytes.Buffer
	rc := RunCLI("router", []string{
		"--task", "edit", "--lane", "grok", "--files", "a.go", "--no-review",
	}, &out, &errB)
	if rc != 1 || !strings.Contains(out.String(), "SCOPE VIOLATIONS: escape.txt") ||
		strings.Contains(out.String(), "dirty.txt") {
		t.Fatalf("unexpected scope violation result rc=%d out=%q err=%q", rc, out.String(), errB.String())
	}
	_ = os.Remove(filepath.Join(repo, "escape.txt"))

	writeExecFile(t, binDir, "grok",
		"echo 'package a\nvar X = 1' > a.go\necho '{\"content\":\"edited a.go\"}'\n")
	writeExecFile(t, binDir, "mcode", "printf '[P1] bad var\\nVERDICT: REJECT\\n'\n")
	out.Reset()
	errB.Reset()
	rc = RunCLI("router", []string{
		"--task", "edit a", "--lane", "grok", "--files", "a.go",
	}, &out, &errB)
	if rc != 1 || !strings.Contains(out.String(), "[review verdict: REJECT]") {
		t.Fatalf("expected review REJECT exit 1, got rc=%d out=%q err=%q", rc, out.String(), errB.String())
	}

	// Verify inconclusive UNKNOWN review fails closed instead of exiting 0.
	_ = os.WriteFile(filepath.Join(repo, "a.go"), []byte("package a\nvar X = 2\n"), 0o644)
	writeExecFile(t, binDir, "mcode", "printf 'Looks plausible without verdict line.\\n'\n")
	out.Reset()
	errB.Reset()
	rc = RunCLI("router", []string{"--task", "edit u", "--lane", "grok", "--files", "a.go"}, &out, &errB)
	if rc != 1 || !strings.Contains(out.String(), "[review verdict: UNKNOWN]") {
		t.Fatalf("expected UNKNOWN review fail-closed exit 1, got rc=%d out=%q err=%q", rc, out.String(), errB.String())
	}

	// Verify EnsureSlot("task-review") failure fails closed instead of silently skipping review.
	_ = os.WriteFile(filepath.Join(repo, "a.go"), []byte("package a\nvar X = 2\n"), 0o644)
	writeExecFile(t, binDir, "mcode", "printf 'VERDICT: ACCEPT\\n'\n")
	revSlotPath := filepath.Join(os.Getenv("ROUTER_SLOTS_DIR"), "slot-task-review")
	if err := os.RemoveAll(revSlotPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(revSlotPath, []byte("block"), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errB.Reset()
	rc = RunCLI("router", []string{
		"--task", "edit a2", "--lane", "grok", "--files", "a.go",
	}, &out, &errB)
	if rc != 1 || !strings.Contains(out.String(), "failing closed") {
		t.Fatalf("expected EnsureSlot fail-closed exit 1, got rc=%d out=%q err=%q", rc, out.String(), errB.String())
	}
}

func TestSkippedReviewFailsClosed(t *testing.T) {
	repo, _, binDir := setupCLIEnv(t)
	oldWd, _ := os.Getwd()
	_ = os.Chdir(repo)
	defer func() { _ = os.Chdir(oldWd) }()
	writeExecFile(t, binDir, "grok",
		"echo 'package a\nvar Y = 1' > a.go\necho '{\"content\":\"edited\"}'\n")
	var out, errB bytes.Buffer
	rc := RunCLI("router", []string{"--task", "edit", "--lane", "grok", "--files", "a.go"}, &out, &errB)
	if rc != 1 || !strings.Contains(out.String(), "[review verdict: SKIPPED]") {
		t.Fatalf("expected SKIPPED review fail-closed exit 1, got rc=%d out=%q err=%q", rc, out.String(), errB.String())
	}
}
