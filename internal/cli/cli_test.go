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
	home, repo, binDir := filepath.Join(tmp, "home"), filepath.Join(tmp, "repo"), filepath.Join(tmp, "bin")
	_ = os.MkdirAll(home, 0o755)
	_ = os.MkdirAll(repo, 0o755)
	_ = os.MkdirAll(binDir, 0o755)
	runGit := func(args ...string) {
		full := append([]string{"-c", "user.name=t", "-c", "user.email=t@invalid", "-c", "commit.gpgsign=false"}, args...)
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
	for k, v := range map[string]string{
		"HOME": home, "AGENT_QUOTA_FILE": qPath, "OODA_NO_AUTO_PROBE": "1", "NO_COLOR": "1",
		"OODA_DISCOVERY_FILE": filepath.Join(tmp, "discovery.json"), "ROUTER_HUD_DIR": filepath.Join(tmp, "hud"),
		"ROUTER_SLOTS_DIR": filepath.Join(tmp, "slots"), "AGENT_OODA_DEPTH": "0", "ROUTER_DEPTH": "0",
	} {
		t.Setenv(k, v)
	}
	gitBin, _ := exec.LookPath("git")
	t.Setenv("PATH", binDir+":"+filepath.Dir(gitBin)+":/bin:/usr/bin")
	oldWd, _ := os.Getwd()
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWd) })
	return repo, qPath, binDir
}

func TestParseArgsPermutations(t *testing.T) {
	var helpOut bytes.Buffer
	opts, err := ParseArgs("agent-ooda", []string{"-h"}, &helpOut)
	if err != nil || !opts.Help || !strings.Contains(helpOut.String(), "usage: agent-ooda") {
		t.Fatalf("unexpected help parse: opts=%+v err=%v", opts, err)
	}
	if vOpts, err := ParseArgs("agent-ooda", []string{"--version"}, nil); err != nil || !vOpts.Version {
		t.Fatalf("expected --version parsed, got %+v err=%v", vOpts, err)
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
	if oProbe, err := ParseArgs("router", []string{"--probe"}, nil); err != nil || !oProbe.Probe {
		t.Fatalf("expected --probe flag parsed, got %+v err=%v", oProbe, err)
	}
	for _, bad := range [][]string{{"--bogus-flag"}, {"--probe", "--status"},
		{"--version", "--status"}, {"--profile", "bogus"}} {
		if _, err := ParseArgs("ooda", bad, nil); err == nil {
			t.Fatalf("expected parse error for %v", bad)
		}
	}
	if o4, e4 := ParseArgs("ooda", []string{"--orient", "why"}, nil); e4 != nil || o4.Orient == nil || *o4.Orient != "why" {
		t.Fatalf("expected --orient parsed, got %+v err=%v", o4, e4)
	}
}

func TestStatusUsageAndQuotaAdmin(t *testing.T) {
	_, _, _ = setupCLIEnv(t)
	var out, errB bytes.Buffer
	for _, tc := range []struct{ arg, want string }{
		{"--version", "agent-ooda v" + fleet.Version},
		{"--status", "LANE"},
		{"--discover", "(refreshed)"},
		{"--usage", "Cycle Usage Telemetry"},
	} {
		out, errB = bytes.Buffer{}, bytes.Buffer{}
		if rc := RunCLI("agent-ooda", []string{tc.arg}, &out, &errB); rc != 0 || !strings.Contains(out.String(), tc.want) {
			t.Fatalf("%s failed rc=%d out=%q err=%q", tc.arg, rc, out.String(), errB.String())
		}
	}
	for _, tc := range []struct {
		argv []string
		want string
	}{
		{[]string{"--disable", "grok", "--reason", "maint", "--hours", "1"}, ""},
		{[]string{"--enable", "grok"}, ""},
		{[]string{"--set-cycle", "mcode", "--effort", "high", "--token-budget", "5000"}, "configured mcode:"},
		{[]string{"--set-stage", "preset=conserve"}, "devin-pro"},
	} {
		out, errB = bytes.Buffer{}, bytes.Buffer{}
		if rc := RunCLI("agent-ooda", tc.argv, &out, &errB); rc != 0 || !strings.Contains(out.String(), tc.want) {
			t.Fatalf("%v failed rc=%d out=%q err=%q", tc.argv, rc, out.String(), errB.String())
		}
	}
	out, errB = bytes.Buffer{}, bytes.Buffer{}
	if rc := RunCLI("agent-ooda", []string{"--set-cycle", "agy-opus", "--model", "gemini-4-carbon-low"}, &out, &errB); rc != 2 {
		t.Fatalf("expected Gemini guardrail exit 2, got %d (%q)", rc, errB.String())
	}
	out, errB = bytes.Buffer{}, bytes.Buffer{}
	if rc := RunCLI("agent-ooda", []string{"--recon", "hi", "--model", "gemini-4-carbon-high"}, &out, &errB); rc != 2 ||
		strings.Contains(errB.String(), "pivoting") {
		t.Fatalf("expected clean upfront Gemini block exit 2, got rc=%d err=%q", rc, errB.String())
	}
	if trc := RunSelectTUI(CLIOpts{}, strings.NewReader("jjjjj\rq"), &out, &errB); trc != 0 {
		t.Fatalf("RunSelectTUI failed rc=%d err=%q", trc, errB.String())
	}
	if q, qerr := fleet.LoadQuota(""); qerr != nil || q.Stages["act"].Lane != "auto" {
		t.Fatalf("expected RunSelectTUI reset-all to auto, err=%v got=%+v", qerr, q)
	}
}

func TestRouterDepthGuard(t *testing.T) {
	_, _, _ = setupCLIEnv(t)
	var out, errB bytes.Buffer
	if rc := RunCLI("router", []string{"--status", "--enable", "grok"}, &out, &errB); rc != 2 ||
		!strings.Contains(errB.String(), "conflicting") {
		t.Fatalf("mixed modes at ROUTER_DEPTH=0: rc=%d err=%q", rc, errB.String())
	}
	t.Setenv("AGENT_OODA_DEPTH", "0")
	t.Setenv("ROUTER_DEPTH", "1")
	blocked := [][]string{{"--recon", "hi"}, {"--task", "hi"}, {"--review", "."}, {"--probe"},
		{"--enable", "grok"}, {"--disable", "grok"}, {"--set-cycle", "grok"}, {"--cleanup"}}
	for _, argv := range blocked {
		out, errB = bytes.Buffer{}, bytes.Buffer{}
		if rc := RunCLI("agent-ooda", argv, &out, &errB); rc != 2 || !strings.Contains(errB.String(), "ROUTER_DEPTH>=1") {
			t.Fatalf("%v under ROUTER_DEPTH=1: rc=%d err=%q", argv, rc, errB.String())
		}
	}
	t.Setenv("AGENT_OODA_DEPTH", "1")
	t.Setenv("ROUTER_DEPTH", "0")
	out, errB = bytes.Buffer{}, bytes.Buffer{}
	if rc := RunCLI("agent-ooda", []string{"--recon", "hi"}, &out, &errB); rc != 2 ||
		!strings.Contains(errB.String(), "AGENT_OODA_DEPTH>=1") {
		t.Fatalf("--recon under AGENT_OODA_DEPTH=1: rc=%d err=%q", rc, errB.String())
	}
	out.Reset()
	if rc := RunCLI("agent-ooda", []string{"--status"}, &out, &errB); rc != 0 {
		t.Fatalf("--status should work when ROUTER_DEPTH=1, got %d (%q)", rc, errB.String())
	}
}

func TestSingleTaskScopeAndReject(t *testing.T) {
	repo, _, binDir := setupCLIEnv(t)
	_ = os.WriteFile(filepath.Join(repo, "dirty.txt"), []byte("keep me\n"), 0o644)
	writeExecFile(t, binDir, "grok", "echo 'package main' > escape.txt\necho '{\"content\":\"done\"}'\n")
	var out, errB bytes.Buffer
	rc := RunCLI("agent-ooda", []string{"--task", "edit", "--lane", "grok", "--files", "a.go", "--no-review"}, &out, &errB)
	if rc != 1 || !strings.Contains(out.String(), "SCOPE VIOLATIONS: escape.txt") ||
		strings.Contains(out.String(), "dirty.txt") {
		t.Fatalf("unexpected scope violation result rc=%d out=%q err=%q", rc, out.String(), errB.String())
	}
	_ = os.Remove(filepath.Join(repo, "escape.txt"))
	writeExecFile(t, binDir, "grok", "echo 'package a\nvar X = 1' > a.go\necho '{\"content\":\"edited a.go\"}'\n")
	writeExecFile(t, binDir, "mcode", "printf '[P1] bad var\\nVERDICT: REJECT\\n'\n")
	out, errB = bytes.Buffer{}, bytes.Buffer{}
	rc = RunCLI("agent-ooda", []string{"--task", "edit a", "--lane", "grok", "--files", "a.go"}, &out, &errB)
	if rc != 1 || !strings.Contains(out.String(), "[review verdict: REJECT]") {
		t.Fatalf("expected review REJECT exit 1, got rc=%d out=%q err=%q", rc, out.String(), errB.String())
	}
	// Verify inconclusive UNKNOWN review fails closed instead of exiting 0.
	_ = os.WriteFile(filepath.Join(repo, "a.go"), []byte("package a\nvar X = 2\n"), 0o644)
	writeExecFile(t, binDir, "mcode", "printf 'Looks plausible without verdict line.\\n'\n")
	out, errB = bytes.Buffer{}, bytes.Buffer{}
	rc = RunCLI("agent-ooda", []string{"--task", "edit u", "--lane", "grok", "--files", "a.go"}, &out, &errB)
	if rc != 1 || !strings.Contains(out.String(), "[review verdict: UNKNOWN]") {
		t.Fatalf("expected UNKNOWN review fail-closed exit 1, got rc=%d out=%q err=%q", rc, out.String(), errB.String())
	}
	// Verify EnsureSlot("task-review") failure fails closed instead of silently skipping review.
	_ = os.WriteFile(filepath.Join(repo, "a.go"), []byte("package a\nvar X = 2\n"), 0o644)
	writeExecFile(t, binDir, "mcode", "printf 'VERDICT: ACCEPT\\n'\n")
	revSlotPath := filepath.Join(os.Getenv("ROUTER_SLOTS_DIR"), "slot-task-review")
	_ = os.RemoveAll(revSlotPath)
	_ = os.WriteFile(revSlotPath, []byte("block"), 0o600)
	out, errB = bytes.Buffer{}, bytes.Buffer{}
	rc = RunCLI("agent-ooda", []string{"--task", "edit a2", "--lane", "grok", "--files", "a.go"}, &out, &errB)
	if rc != 1 || !strings.Contains(out.String(), "failing closed") {
		t.Fatalf("expected EnsureSlot fail-closed exit 1, got rc=%d out=%q err=%q", rc, out.String(), errB.String())
	}
}

func TestSkippedReviewFailsClosed(t *testing.T) {
	_, _, binDir := setupCLIEnv(t)
	writeExecFile(t, binDir, "grok", "echo 'package a\nvar Y = 1' > a.go\necho '{\"content\":\"edited\"}'\n")
	var out, errB bytes.Buffer
	rc := RunCLI("router", []string{"--task", "edit", "--lane", "grok", "--files", "a.go"}, &out, &errB)
	if rc != 1 || !strings.Contains(out.String(), "[review verdict: SKIPPED]") {
		t.Fatalf("expected SKIPPED review fail-closed exit 1, got rc=%d out=%q err=%q", rc, out.String(), errB.String())
	}
}

func TestOrientCommand(t *testing.T) {
	_, _, binDir := setupCLIEnv(t)
	writeExecFile(t, binDir, "grok", "printf '{\"role\":\"assistant\",\"content\":\"architecture root cause\"}\\n'\n")
	var out, errB bytes.Buffer
	rc := RunCLI("agent-ooda", []string{"--orient", "trace root cause", "--lane", "grok"}, &out, &errB)
	if rc != 0 || !strings.Contains(errB.String(), "OODA:ORIENT") || !strings.Contains(out.String(), "root cause") {
		t.Fatalf("--orient failed rc=%d out=%q err=%q", rc, out.String(), errB.String())
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--status", "--profile", "conserve"}, "profile:conserve"},
		{[]string{"--status", "--lane", "grok"}, "act       cli            grok"},
		{[]string{"--set-stage", "orient=grok", "--local"}, "orient   -> grok"},
		{[]string{"--status"}, "orient    local          grok"},
	} {
		out.Reset()
		if sRc := RunCLI("agent-ooda", tc.args, &out, &errB); sRc != 0 || !strings.Contains(out.String(), tc.want) {
			t.Fatalf("%v rc=%d out=%q err=%q", tc.args, sRc, out.String(), errB.String())
		}
	}
	writeExecFile(t, binDir, "grok", "exit 1\n")
	writeExecFile(t, binDir, "mcode", "printf 'softpin pivoted\\n'\n")
	out, errB = bytes.Buffer{}, bytes.Buffer{}
	if pRc := RunCLI("agent-ooda", []string{"--orient", "why"}, &out, &errB); pRc != 0 ||
		!strings.Contains(out.String(), "softpin pivoted") {
		t.Fatalf("expected SoftPin pivot rc=%d out=%q err=%q", pRc, out.String(), errB.String())
	}
	out.Reset()
	if rRc := RunCLI("agent-ooda", []string{"--reset-stages", "--local"}, &out, &errB); rRc != 0 ||
		!strings.Contains(out.String(), "(inherit)") {
		t.Fatalf("expected (inherit) on local reset, rc=%d out=%q", rRc, out.String())
	}
}
