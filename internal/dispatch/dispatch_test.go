package dispatch

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/UberMetroid/agent-ooda/internal/fleet"
	"github.com/UberMetroid/agent-ooda/internal/verify"
	"github.com/UberMetroid/agent-ooda/internal/worktree"
)

func TestBuildWorkerCommandAllCLIs(t *testing.T) {
	tmp := t.TempDir()
	wt, slot := filepath.Join(tmp, "wt"), filepath.Join(tmp, "slot")
	_ = os.MkdirAll(wt, 0o755)
	_ = os.MkdirAll(slot, 0o755)
	t.Setenv("AGENT_OODA_DEPTH", "0")
	t.Setenv("ROUTER_DEPTH", "2")
	t.Setenv("ANTIGRAVITY_TOKEN", "secret-should-strip")
	for _, lane := range []string{"agy-opus", "devin-pro", "mcode", "grok", "muse", "mistral"} {
		built, err := BuildWorkerCommand(CmdReq{Provider: lane, CLIPath: "/bin/" + lane,
			Prompt: "fix bug", Worktree: wt, SlotDir: slot, TaskID: "t1"})
		if err != nil {
			t.Fatalf("%s unpinned failed: %v", lane, err)
		}
		for _, badFlag := range []string{"--model", "--effort", "--reasoning-effort"} {
			if slices.Contains(built.Argv, badFlag) {
				t.Fatalf("%s unpinned leaked flag %s: %v", lane, badFlag, built.Argv)
			}
		}
		if !slices.Contains(built.Env, "ROUTER_DEPTH=3") || !slices.Contains(built.Env, "AGENT_OODA_DEPTH=3") {
			t.Fatalf("%s missing incremented depth vars: %v", lane, built.Env)
		}
		if lane == "agy-opus" {
			for _, kv := range built.Env {
				if strings.HasPrefix(kv, "ANTIGRAVITY_") {
					t.Fatalf("agy-opus leaked env %s", kv)
				}
			}
		}
		ro, err := BuildWorkerCommand(CmdReq{Provider: lane, CLIPath: "/bin/" + lane, Model: "m-ovr",
			Effort: "low", Prompt: "audit", Worktree: wt, SlotDir: slot, TaskID: "t2", ReadOnly: true})
		if err != nil || !slices.Contains(ro.Argv, "m-ovr") {
			t.Fatalf("%s ro failed: err=%v argv=%v", lane, err, ro.Argv)
		}
	}
	nat, err := BuildWorkerCommand(CmdReq{Provider: "mcode", CLIPath: "/bin/mcode", Prompt: "note",
		Worktree: wt, SlotDir: slot, TaskID: "t3", ReadOnly: true, ReviewMode: "native"})
	if err != nil || nat.StreamFmt != "plain" || !slices.Contains(nat.Argv, "review") {
		t.Fatalf("unexpected mcode native review cmd: err=%v %+v", err, nat)
	}
}

func TestNDJSONStreamRate(t *testing.T) {
	st := &streamTracker{phase: "wait"}
	st.consumeLine(`{"type":"content_block_delta","delta":{"text":"Checking files now. "}}`, 100.0)
	thinkLine := `{"type":"content_block_delta","delta":{"thinking":"Verifying invariants carefully across package."}}`
	st.consumeLine(thinkLine, 100.5)
	rate, exact := st.currentRate(100.6)
	if st.phase != "stream" || exact || rate <= 0 {
		t.Fatalf("expected active ~tok/s > 0, got phase=%s rate=%.1f exact=%v", st.phase, rate, exact)
	}
	// Tool result dumps MUST switch phase to "tool" and NEVER inflate assistant byte rate.
	beforeSamples := len(st.samples)
	st.consumeLine(`{"type":"tool_result","error":"file not found","content":"`+strings.Repeat("HUGE DUMP ", 500)+`"}`, 100.8)
	if st.phase != "tool" || len(st.samples) != beforeSamples {
		t.Fatalf("tool_result corrupted phase=%s or samples=%d!=%d", st.phase, len(st.samples), beforeSamples)
	}
	st.consumeLine(`{"status":"success","response":"Final answer","usage":{"total_tokens":420},"cost_usd":0.012}`, 101.0)
	if st.isError || !st.hasFinal || st.finalResp != "Final answer" || st.usage.Tokens == nil || *st.usage.Tokens != 420 {
		t.Fatalf("failed to capture final result/usage: %+v", st)
	}
	errSt := &streamTracker{phase: "wait"}
	errSt.consumeLine(`{"type":"result","subtype":"error_during_execution","is_error":true,"result":"Error: boom"}`, 101.0)
	if out, code, _ := finalizeOutput(BuiltCmd{StreamFmt: "ndjson"}, errSt, "", 0, false, 10); code != 1 ||
		!strings.Contains(out, "[error] Error: boom") {
		t.Fatalf("expected [error] + exit 1 on is_error result event, got rc=%d out=%q", code, out)
	}
	if _, isErr, qKill := extractJSONErr(map[string]any{"status": "429"}, nil); !isErr || !qKill {
		t.Fatalf("expected string status 429 to abort: isErr=%v qKill=%v", isErr, qKill)
	}
}

func TestNDJSONRealCLISchemas(t *testing.T) {
	st := &streamTracker{phase: "wait"}
	st.consumeLine(`{"type":"item.started","item":{"type":"reasoning","contentDelta":"Reviewing diff lines"}}`, 10.0)
	st.consumeLine(`{"type":"item.updated","item":{"type":"reasoning","contentDelta":" across packages now"}}`, 10.4)
	st.consumeLine(`{"type":"item.started","item":{"type":"message","contentDelta":"VERDICT: "}}`, 10.8)
	st.consumeLine(`{"type":"item.completed","item":{"type":"agent_message","content":"VERDICT: ACCEPT"}}`, 11.0)
	st.consumeLine(`{"type":"turn.completed","usage":{"inputTokens":1030,"outputTokens":20,"totalTokens":1050}}`, 11.1)
	resLine := `{"type":"exec.completed","result":{"status":"succeeded",` +
		`"output":"VERDICT: ACCEPT","usage":{"totalTokens":1050}}}`
	st.consumeLine(resLine, 11.2)
	if rate, _ := st.currentRate(11.2); rate <= 0 {
		t.Fatalf("expected mcode streaming ~tok/s > 0, got %.1f", rate)
	}
	if !st.hasFinal || st.finalResp != "VERDICT: ACCEPT" || st.usage.Tokens == nil || *st.usage.Tokens != 1050 {
		t.Fatalf("mcode schema parse mismatch: hasFinal=%v resp=%q usage=%+v", st.hasFinal, st.finalResp, st.usage)
	}
	grok := &streamTracker{phase: "wait"}
	grok.consumeLine(`{"type":"thought","data":"Checking safety rules and intent carefully."}`, 20.0)
	grok.consumeLine(`{"type":"text","data":"VERDICT: ACCEPT"}`, 20.5)
	grok.consumeLine(`{"type":"end","usage":{"input_tokens":50,"output_tokens":10},"total_cost_usd":0.002}`, 20.6)
	if rate, _ := grok.currentRate(20.6); rate <= 0 || grok.cleanBuf.str() != "VERDICT: ACCEPT" {
		t.Fatalf("grok schema parse mismatch: buf=%q", grok.cleanBuf.str())
	}
	if grok.usage.Tokens == nil || *grok.usage.Tokens != 60 || grok.usage.CostUSD == nil {
		t.Fatalf("grok usage parse mismatch: %+v", grok.usage)
	}
	muse := &streamTracker{phase: "wait"}
	muse.consumeLine(`{"payload_type":"run.output.delta","payload":{"text":"Streaming OK"}}`, 30.0)
	if muse.cleanBuf.str() != "Streaming OK" || muse.phase != "stream" {
		t.Fatalf("muse delta parse mismatch: phase=%s buf=%q", muse.phase, muse.cleanBuf.str())
	}
}

func TestExecStreamAndGeminiGuard(t *testing.T) {
	tmp := t.TempDir()
	fakeHome := filepath.Join(tmp, "home")
	_ = os.MkdirAll(filepath.Join(fakeHome, ".gemini", "antigravity-cli"), 0o755)
	_ = os.WriteFile(filepath.Join(fakeHome, ".gemini", "antigravity-cli", "settings.json"),
		[]byte(`{"model":{"name":"Orchestrator (Carbon)"}}`), 0o644)
	t.Setenv("HOME", fakeHome)
	t.Setenv("ROUTER_HUD_DIR", filepath.Join(tmp, "hud"))
	t.Setenv("AGENT_OODA_DEPTH", "0")
	t.Setenv("ROUTER_DEPTH", "0")
	blocked := ExecuteOnce(ExecReq{Provider: "agy-opus", CLIPath: "/bin/true",
		Prompt: "hi", Worktree: tmp, SlotDir: tmp, TaskID: "g1", TimeoutSec: 5})
	if blocked.ExitCode != 2 || !strings.Contains(blocked.Output, "Gemini guardrail") {
		t.Fatalf("expected Gemini settings block exit 2, got %+v", blocked)
	}
	script := filepath.Join(tmp, "fake_cli.sh")
	body := "#!/usr/bin/env bash\n" +
		"printf '{\"type\":\"content_block_delta\",\"delta\":{\"text\":\"hello \"}}\\n'\n" +
		"printf '{\"type\":\"content_block_delta\",\"delta\":{\"text\":\"world this is streamed text.\"}}\\n'\n" +
		"printf '{\"type\":\"result\",\"result\":\"hello world this is streamed text.\",\"usage\":{\"total_tokens\":99}}\\n'\n"
	_ = os.WriteFile(script, []byte(body), 0o755)
	m := WorkerMarker{PID: os.Getpid(), TaskID: "s1", Mode: "observe", Lane: "mcode",
		CLI: "mcode", Model: "MiniMax", Effort: "max", Cwd: tmp, StartedAt: nowFloatSec()}
	mPath := HudWorkerStart(m)
	defer HudWorkerFinish(mPath, LastRunMarker{Mode: "observe", Lane: "mcode", ExitCode: 0})
	res := ExecuteOnce(ExecReq{Provider: "mcode", CLIPath: script, Prompt: "hi", Worktree: tmp,
		SlotDir: tmp, TaskID: "s1", TimeoutSec: 10, ReadOnly: true, MarkerPath: mPath, Marker: m})
	if res.ExitCode != 0 || !strings.Contains(res.Output, "streamed text") ||
		res.ExportInfo == nil || *res.ExportInfo.Tokens != 99 {
		t.Fatalf("unexpected ExecuteOnce stream result: %+v", res)
	}
}

func TestManifestScopeAndReject(t *testing.T) {
	repo := t.TempDir()
	runGit := func(dir string, args ...string) {
		base := []string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@invalid", "-c", "commit.gpgsign=false"}
		if out, err := exec.Command("git", append(base, args...)...).CombinedOutput(); err != nil {
			t.Fatal(string(out), err)
		}
	}
	runGit(repo, "init", "-q")
	_ = os.WriteFile(filepath.Join(repo, "a.go"), []byte("package a\n"), 0o644)
	runGit(repo, "add", "a.go")
	runGit(repo, "commit", "-q", "-m", "init")
	qPath := filepath.Join(repo, ".quota.json")
	_ = fleet.AtomicWriteJSON(qPath, fleet.DefaultQuotaData(), false, true)
	t.Setenv("AGENT_QUOTA_FILE", qPath)
	t.Setenv("ROUTER_HUD_DIR", filepath.Join(repo, ".hud"))
	t.Setenv("ROUTER_SLOTS_DIR", filepath.Join(repo, ".slots"))
	t.Setenv("AGENT_OODA_DEPTH", "0")
	t.Setenv("ROUTER_DEPTH", "0")
	tasks := []worktree.TaskSpec{{ID: "t1", Prompt: "edit a.go", Files: []string{"a.go"}, Lane: "mcode"}}
	installed := map[string]string{"mcode": "/bin/true", "grok": "/bin/true"}
	rc := RunManifestBatch(repo, tasks, BatchOpts{
		Lane: "mcode", Jobs: 1, NoStripe: true, QuotaPath: qPath,
		ResolveCLI: func(c string) string { return installed[c] },
		TaskRunner: func(r ExecReq) ExecRes {
			_ = os.WriteFile(filepath.Join(r.Worktree, "a.go"), []byte("package a\nvar X = 1\n"), 0o644)
			runGit(r.Worktree, "add", "a.go")
			runGit(r.Worktree, "commit", "-q", "-m", "worker commit")
			return ExecRes{ExitCode: 0, Output: "done"}
		},
		ReviewRunner: func(r verify.ReviewExecReq) verify.ReviewExecRes {
			if r.TimeoutSec != fleet.ReviewTimeout {
				t.Fatalf("batch review TimeoutSec=%d, want %d", r.TimeoutSec, fleet.ReviewTimeout)
			}
			return verify.ReviewExecRes{ExitCode: 0, Output: "[P1] bad var\nVERDICT: REJECT"}
		},
	})
	if rc != 1 {
		t.Fatalf("expected REJECT batch to exit 1, got %d", rc)
	}
	raw, _ := os.ReadFile(filepath.Join(repo, ".worktrees", "results.json"))
	var rows []BatchRecord
	if json.Unmarshal(raw, &rows) != nil || len(rows) != 1 || rows[0].Review.Verdict != "REJECT" {
		t.Fatalf("unexpected results.json: %s", string(raw))
	}
}

func TestCorruptQuotaAndBoundedStream(t *testing.T) {
	badQuota := filepath.Join(t.TempDir(), "bad_quota.json")
	_ = os.WriteFile(badQuota, []byte("{corrupt"), 0o600)
	req := TaskReq{Task: worktree.TaskSpec{ID: "c1", Prompt: "hi"},
		Worktree: t.TempDir(), SlotDir: t.TempDir(), QuotaPath: badQuota}
	if _, err := RunTaskWithFallback(req); err == nil {
		t.Fatal("expected RunTaskWithFallback to fail closed on corrupt quota file")
	}
	var tb tailBuf
	for i := 0; i < 80; i++ {
		tb.addStr(strings.Repeat("é", 4096))
	}
	tb.addStr("END_TAIL")
	out := tb.str()
	if len(tb.buf) > maxStreamBytes || !utf8.ValidString(out) || !strings.HasSuffix(out, "END_TAIL") {
		t.Fatalf("tailBuf invariant failed: len=%d valid=%v", len(tb.buf), utf8.ValidString(out))
	}
	vibeErr := `Error: API error occurred: Status 402. Body: {"type":"billing_vibe_budget_exhausted"}`
	if !fleet.HasErrorPattern(vibeErr, 2, fleet.QuotaErrorPats) {
		t.Fatal("expected QuotaErrorPats to match vibe HTTP 402 budget exhausted")
	}
}

func TestMidStreamQuotaAbortAndLine(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("ROUTER_HUD_DIR", filepath.Join(tmp, "hud"))
	t.Setenv("AGENT_OODA_DEPTH", "0")
	t.Setenv("ROUTER_DEPTH", "0")
	hung := filepath.Join(tmp, "hung_quota.sh")
	body := "#!/usr/bin/env bash\nprintf '{\"error\":\"insufficient_quota on account\"}\\n'\nsleep 30\n"
	_ = os.WriteFile(hung, []byte(body), 0o755)
	start := time.Now()
	res := ExecuteOnce(ExecReq{Provider: "mcode", CLIPath: hung, Prompt: "hi",
		Worktree: tmp, SlotDir: tmp, TaskID: "q1", TimeoutSec: 25})
	if res.ExitCode == 0 || time.Since(start) >= 5*time.Second || !strings.Contains(res.Output, "insufficient_quota") {
		t.Fatalf("expected instant mid-stream abort, dur=%.2fs %+v", time.Since(start).Seconds(), res)
	}
	ok429 := filepath.Join(tmp, "plain_429.sh")
	okBody := "#!/usr/bin/env bash\nprintf 'Handled HTTP 429 too many requests and usage limit retry cleanly.\\n'\n"
	_ = os.WriteFile(ok429, []byte(okBody), 0o755)
	ok := ExecuteOnce(ExecReq{Provider: "devin-fast", CLIPath: ok429, Prompt: "hi",
		Worktree: tmp, SlotDir: tmp, TaskID: "q2", TimeoutSec: 10})
	if ok.ExitCode != 0 || !strings.Contains(ok.Output, "HTTP 429") {
		t.Fatalf("expected plain devin output with HTTP 429 to pass, got %+v", ok)
	}
	if _, isErr, qKill := extractJSONErr(map[string]any{"error": "unexpected field 429"}, nil); !isErr || qKill {
		t.Fatalf("expected non-quota JSON error not to trigger qKill: isErr=%v qKill=%v", isErr, qKill)
	}
}
