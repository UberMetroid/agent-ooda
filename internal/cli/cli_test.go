package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/UberMetroid/agent-ooda/internal/fleet"
	"github.com/UberMetroid/agent-ooda/internal/hud"
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
	if v, e := ParseArgs("agent-ooda", []string{"--version"}, nil); e != nil || !v.Version {
		t.Fatal(v, e)
	}
	if o, e := ParseArgs("ooda", []string{"--review"}, nil); e != nil || o.Review == nil || *o.Review != "." {
		t.Fatal(o, e)
	}
	o2, err := ParseArgs("ooda", []string{"--review", "sub", "-j6", "--files", "a.go,b.go", "--files", "c.go"}, nil)
	if err != nil || o2.Review == nil || *o2.Review != "sub" || o2.Jobs != 6 || len(o2.Files) != 2 {
		t.Fatalf("unexpected flag mix parse: %+v err=%v", o2, err)
	}
	if o, e := ParseArgs("ooda", []string{"--", "--weird"}, nil); e != nil || o.Manifest != "--weird" {
		t.Fatal(o, e)
	}
	if o, e := ParseArgs("router", []string{"--probe"}, nil); e != nil || !o.Probe {
		t.Fatal(o, e)
	}
	for _, bad := range [][]string{{"--bogus-flag"}, {"--probe", "--status"},
		{"--version", "--status"}, {"--profile", "bogus"}} {
		if _, err := ParseArgs("ooda", bad, nil); err == nil {
			t.Fatalf("expected parse error for %v", bad)
		}
	}
	if o, e := ParseArgs("ooda", []string{"--orient", "why"}, nil); e != nil || o.Orient == nil || *o.Orient != "why" {
		t.Fatal(o, e)
	}
}

func TestStatusUsageAndQuotaAdmin(t *testing.T) {
	_, _, _ = setupCLIEnv(t)
	var out, errB bytes.Buffer
	for _, tc := range [][]string{
		{"agent-ooda v" + fleet.Version, "--version"},
		{"LANE", "--status"},
		{"(refreshed)", "--discover"},
		{"Cycle Usage Telemetry", "--usage"},
		{"", "--disable", "grok", "--reason", "maint", "--hours", "1"},
		{"", "--enable", "grok"},
		{"configured mcode:", "--set-cycle", "mcode", "--effort", "high", "--token-budget", "5000"},
		{"devin-pro", "--set-stage", "preset=conserve"},
	} {
		out, errB = bytes.Buffer{}, bytes.Buffer{}
		if rc := RunCLI("agent-ooda", tc[1:], &out, &errB); rc != 0 || !strings.Contains(out.String(), tc[0]) {
			t.Fatalf("%v failed rc=%d out=%q err=%q", tc[1:], rc, out.String(), errB.String())
		}
	}
	out, errB = bytes.Buffer{}, bytes.Buffer{}
	if rc := RunCLI("agent-ooda", []string{"--set-cycle", "agy-opus", "--model", "gemini-4-carbon-low"}, &out, &errB); rc != 2 {
		t.Fatal(rc, errB.String())
	}
	out, errB = bytes.Buffer{}, bytes.Buffer{}
	if rc := RunCLI("agent-ooda", []string{"--recon", "hi", "--model", "gemini-4-carbon-high"}, &out, &errB); rc != 2 ||
		strings.Contains(errB.String(), "pivoting") {
		t.Fatal(rc, errB.String())
	}
	t.Setenv("AGENT_OODA_THEME", "light")
	out, errB = bytes.Buffer{}, bytes.Buffer{}
	if rc := RunSelectTUI(CLIOpts{}, strings.NewReader("b\rBjjjjj\rq"), &out, &errB); rc != 0 ||
		!strings.Contains(out.String(), hud.PaletteFor("light").TUIHeader) ||
		strings.Contains(out.String(), "REPO-LOCAL") || !strings.Contains(out.String(), "✓ Reset all stages to auto") {
		t.Fatalf("RunSelectTUI b/reset failed rc=%d err=%q", rc, errB.String())
	}
	out.Reset()
	if rc := RunSelectTUI(CLIOpts{}, strings.NewReader("\tq"), &out, &errB); rc != 0 ||
		!strings.Contains(out.String(), "REPO-LOCAL") {
		t.Fatal(rc, out.String())
	}
	if q, e := fleet.LoadQuota(""); e != nil || q.Stages["act"].Lane != "auto" {
		t.Fatal(q, e)
	}
}

func TestRouterDepthGuard(t *testing.T) {
	_, _, _ = setupCLIEnv(t)
	run := func(argv ...string) (int, string) {
		var out, errB bytes.Buffer
		return RunCLI("agent-ooda", argv, &out, &errB), errB.String()
	}
	if rc, errS := run("--status", "--enable", "grok"); rc != 2 || !strings.Contains(errS, "conflicting") {
		t.Fatalf("mixed modes at ROUTER_DEPTH=0: rc=%d err=%q", rc, errS)
	}
	t.Setenv("AGENT_OODA_DEPTH", "0")
	t.Setenv("ROUTER_DEPTH", "1")
	for _, argv := range [][]string{{"--recon", "hi"}, {"--task", "hi"}, {"--review", "."}, {"--probe"},
		{"--enable", "grok"}, {"--disable", "grok"}, {"--set-cycle", "grok"}, {"--cleanup"}} {
		if rc, errS := run(argv...); rc != 2 || !strings.Contains(errS, "ROUTER_DEPTH>=1") {
			t.Fatalf("%v under ROUTER_DEPTH=1: rc=%d err=%q", argv, rc, errS)
		}
	}
	t.Setenv("AGENT_OODA_DEPTH", "1")
	t.Setenv("ROUTER_DEPTH", "0")
	if rc, errS := run("--recon", "hi"); rc != 2 || !strings.Contains(errS, "AGENT_OODA_DEPTH>=1") {
		t.Fatalf("--recon under AGENT_OODA_DEPTH=1: rc=%d err=%q", rc, errS)
	}
	if rc, errS := run("--status"); rc != 0 {
		t.Fatalf("--status should work when ROUTER_DEPTH=1, got %d (%q)", rc, errS)
	}
}

func TestSingleTaskScopeAndReject(t *testing.T) {
	repo, _, binDir := setupCLIEnv(t)
	_ = os.WriteFile(filepath.Join(repo, "dirty.txt"), []byte("keep me\n"), 0o644)
	writeExecFile(t, binDir, "grok", "echo 'package main' > escape.txt\necho '{\"content\":\"done\"}'\n")
	run := func(argv ...string) (int, string, string) {
		var out, errB bytes.Buffer
		return RunCLI("agent-ooda", argv, &out, &errB), out.String(), errB.String()
	}
	if rc, out, errS := run("--task", "edit", "--lane", "grok", "--files", "a.go", "--no-review"); rc != 1 ||
		!strings.Contains(out, "SCOPE VIOLATIONS: escape.txt") || strings.Contains(out, "dirty.txt") {
		t.Fatalf("unexpected scope violation result rc=%d out=%q err=%q", rc, out, errS)
	}
	_ = os.Remove(filepath.Join(repo, "escape.txt"))
	writeExecFile(t, binDir, "grok", "echo 'package a\nvar X = 1' > a.go\necho '{\"content\":\"edited a.go\"}'\n")
	for i, tc := range [][2]string{
		{"printf '[P1] bad var\\nVERDICT: REJECT\\n'\n", "[review verdict: REJECT]"},
		{"printf 'Looks plausible without verdict line.\\n'\n", "[review verdict: UNKNOWN]"},
	} {
		_ = os.WriteFile(filepath.Join(repo, "a.go"), []byte(string(rune('a'+i))+"\n"), 0o644)
		writeExecFile(t, binDir, "mcode", tc[0])
		if rc, out, errS := run("--task", "edit", "--lane", "grok", "--files", "a.go"); rc != 1 ||
			!strings.Contains(out, tc[1]) {
			t.Fatalf("expected %s exit 1, rc=%d out=%q err=%q", tc[1], rc, out, errS)
		}
	}
	_ = os.WriteFile(filepath.Join(repo, "a.go"), []byte("package a\nvar X = 2\n"), 0o644)
	writeExecFile(t, binDir, "mcode", "printf 'VERDICT: ACCEPT\\n'\n")
	if rc, out, errS := run("--review", repo, "--files", "a.go", "--lane", "mcode"); rc != 0 ||
		!strings.Contains(out, "[review verdict: ACCEPT") {
		t.Fatalf("standalone --review --files failed: rc=%d out=%q err=%q", rc, out, errS)
	}
	revSlotPath := filepath.Join(os.Getenv("ROUTER_SLOTS_DIR"), "slot-task-review")
	_ = os.RemoveAll(revSlotPath)
	_ = os.WriteFile(revSlotPath, []byte("block"), 0o600)
	if rc, out, errS := run("--task", "edit a2", "--lane", "grok", "--files", "a.go"); rc != 1 ||
		!strings.Contains(out, "failing closed") {
		t.Fatalf("expected EnsureSlot fail-closed exit 1, got rc=%d out=%q err=%q", rc, out, errS)
	}
}

func TestSkippedReviewFailsClosed(t *testing.T) {
	_, _, binDir := setupCLIEnv(t)
	writeExecFile(t, binDir, "grok", "echo 'package a\nvar Y = 1' > a.go\necho '{\"content\":\"edited\"}'\n")
	var out, errB bytes.Buffer
	if rc := RunCLI("router", []string{"--task", "edit", "--lane", "grok", "--files", "a.go"}, &out, &errB); rc != 1 ||
		!strings.Contains(out.String(), "[review verdict: SKIPPED]") {
		t.Fatalf("expected SKIPPED review fail-closed exit 1, got rc=%d out=%q err=%q", rc, out.String(), errB.String())
	}
}

func TestOrientCommand(t *testing.T) {
	_, _, binDir := setupCLIEnv(t)
	run := func(argv ...string) (int, string, string) {
		var out, errB bytes.Buffer
		return RunCLI("agent-ooda", argv, &out, &errB), out.String(), errB.String()
	}
	writeExecFile(t, binDir, "grok", "printf '{\"role\":\"assistant\",\"content\":\"architecture root cause\"}\\n'\n")
	if rc, out, errS := run("--orient", "trace root cause", "--lane", "grok"); rc != 0 ||
		!strings.Contains(errS, "OODA:ORIENT") || !strings.Contains(out, "root cause") {
		t.Fatalf("--orient failed rc=%d out=%q err=%q", rc, out, errS)
	}
	for _, tc := range [][]string{
		{"profile:conserve", "--status", "--profile", "conserve"},
		{"verify    auto", "--status", "--lane", "grok"},
		{"orient   -> grok", "--set-stage", "orient=grok", "--local"},
		{"orient    local          grok", "--status"},
	} {
		if rc, out, errS := run(tc[1:]...); rc != 0 || !strings.Contains(out, tc[0]) {
			t.Fatalf("%v rc=%d out=%q err=%q", tc[1:], rc, out, errS)
		}
	}
	writeExecFile(t, binDir, "grok", "echo bad > rogue_recon.txt\necho '{\"content\":\"sneaky\"}'\n")
	if rc, _, errS := run("--recon", "check", "--lane", "grok"); rc != 1 || !strings.Contains(errS, "zero-trust") {
		t.Fatalf("expected rogue write during --recon to fail closed, rc=%d err=%q", rc, errS)
	}
	writeExecFile(t, binDir, "grok", "exit 1\n")
	writeExecFile(t, binDir, "mcode", "printf 'softpin pivoted %s\\n' \"$*\"\n")
	if rc, out, errS := run("--orient", "why", "--files", "a.go"); rc != 0 || !strings.Contains(out, "softpin pivoted") {
		t.Fatalf("expected SoftPin pivot rc=%d out=%q err=%q", rc, out, errS)
	}
	if rc, out, errS := run("--orient", "why", "--files", "b.go"); rc != 0 || !strings.Contains(out, "b.go") {
		t.Fatalf("cache leaked across different --files scope: rc=%d out=%q err=%q", rc, out, errS)
	}
	if rc, out, _ := run("--reset-stages", "--local"); rc != 0 || !strings.Contains(out, "(inherit)") {
		t.Fatalf("expected (inherit) on local reset, rc=%d out=%q", rc, out)
	}
}
