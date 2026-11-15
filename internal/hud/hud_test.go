package hud

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/UberMetroid/agent-ooda/internal/dispatch"
	"github.com/UberMetroid/agent-ooda/internal/fleet"
)

func setTestWorkers(t *testing.T, hudDir string, workers []dispatch.WorkerMarker) {
	t.Helper()
	active := filepath.Join(hudDir, "active")
	_ = os.RemoveAll(active)
	if err := os.MkdirAll(active, 0o755); err != nil {
		t.Fatal(err)
	}
	for i, w := range workers {
		w.PID = os.Getpid()
		if w.StartedAt == 0 {
			w.StartedAt = float64(i + 1)
		}
		_ = fleet.AtomicWriteJSON(filepath.Join(active, fmt.Sprintf("w%d.json", i)), w, true, true)
	}
}

func TestStatusLineModes(t *testing.T) {
	tmp := t.TempDir()
	hudDir := filepath.Join(tmp, "hud")
	t.Setenv("ROUTER_HUD_DIR", hudDir)
	t.Setenv("NO_COLOR", "1")
	sum, now := FleetSummary{Active: 5, Total: 8, WakeCD: "1h05m"}, 1000.0
	fleetTail := " │ fleet 5/8 (next 1h05m)"

	if got := RenderStatusLine("{}", true, &now, &sum); got != "○ ooda"+fleetTail {
		t.Fatalf("idle mismatch: %q", got)
	}
	thinking := RenderStatusLine(`{"agent":{"state":"thinking"}}`, true, &now, &sum)
	if thinking != "◐ orient/decide"+fleetTail {
		t.Fatalf("orient mismatch: %q", thinking)
	}

	cases := []struct {
		workers []dispatch.WorkerMarker
		want    string
	}{
		{
			[]dispatch.WorkerMarker{{Mode: "observe", Model: "swe-2-flash-max"}},
			"⠋ observe · swe-2-flash-max",
		},
		{
			[]dispatch.WorkerMarker{{Mode: "act", Model: "claude-opus-5-5-high"}},
			"⠋ act · claude-opus-5-5-high",
		},
		{
			[]dispatch.WorkerMarker{{Mode: "act:verify", Model: "minimax/MiniMax-M3.1-Flash"}},
			"⠋ verify · MiniMax-M3.1-Flash",
		},
		{
			[]dispatch.WorkerMarker{
				{Mode: "act", Model: "grok-4.7"},
				{Mode: "act", Model: "fusion-claude-opus-5-5-high-sidekick-swe-2-medium"},
			},
			"⠋ act ×2 · grok-4.7, devin-fusion",
		},
		{
			[]dispatch.WorkerMarker{
				{Mode: "act", Model: "grok-4.7", Skills: []string{"ooda", "router", "custom-skill"}},
			},
			"⠋ act · grok-4.7 │ skill custom-skill",
		},
	}
	for _, tc := range cases {
		setTestWorkers(t, hudDir, tc.workers)
		if got := RenderStatusLine("{}", true, &now, &sum); got != tc.want+fleetTail {
			t.Fatalf("got %q, want %q", got, tc.want+fleetTail)
		}
	}
}

func TestStatusLineTelemetryANSI(t *testing.T) {
	tmp := t.TempDir()
	hudDir := filepath.Join(tmp, "hud")
	t.Setenv("ROUTER_HUD_DIR", hudDir)
	t.Setenv("NO_COLOR", "1")
	sum, now := FleetSummary{Active: 5, Total: 8, WakeCD: "1h05m"}, 1000.0
	fleetTail := " │ fleet 5/8 (next 1h05m)"

	cases := []struct {
		workers []dispatch.WorkerMarker
		want    string
	}{
		{
			[]dispatch.WorkerMarker{{
				Mode: "act", Model: "minimax/MiniMax-M3.1-Flash",
				Phase: "stream", TokS: 118, UpdatedAt: 999.0, StartedAt: 995.0,
			}},
			"⠋ act · MiniMax-M3.1-Flash · ~118 tok/s",
		},
		{
			[]dispatch.WorkerMarker{{
				Mode: "act", Model: "minimax/MiniMax-M3.1-Flash",
				Phase: "stream", TokS: 118, TokExact: true, UpdatedAt: 999.0, StartedAt: 995.0,
			}},
			"⠋ act · MiniMax-M3.1-Flash · 118 tok/s",
		},
		{
			[]dispatch.WorkerMarker{{
				Mode: "act", Model: "swe-2-flash-max",
				Phase: "tool", StartedAt: 992.0, UpdatedAt: 999.0,
			}},
			"⠋ act · swe-2-flash-max · tool 8s",
		},
		{
			[]dispatch.WorkerMarker{{
				Mode: "act", Model: "swe-2-flash-max",
				Phase: "wait", StartedAt: 994.0, UpdatedAt: 994.0,
			}},
			"⠋ act · swe-2-flash-max · 6s",
		},
	}
	for _, tc := range cases {
		setTestWorkers(t, hudDir, tc.workers)
		if got := RenderStatusLine("{}", true, &now, &sum); got != tc.want+fleetTail {
			t.Fatalf("got %q, want %q", got, tc.want+fleetTail)
		}
	}
	setTestWorkers(t, hudDir, []dispatch.WorkerMarker{
		{Mode: "act", Model: "grok-4.7", Skills: []string{"custom-skill"}},
	})
	narrow := RenderStatusLine(`{"terminal_width":48}`, true, &now, &sum)
	if narrow != "⠋ act · grok-4.7 │ skill custom-skill │ fleet…" || VisibleLen(narrow) != 46 {
		t.Fatalf("narrow truncation mismatch: %q (len=%d)", narrow, VisibleLen(narrow))
	}

	t.Setenv("NO_COLOR", "")
	setTestWorkers(t, hudDir, []dispatch.WorkerMarker{
		{Mode: "act:verify", Model: "minimax/MiniMax-M3.1-Flash"},
	})
	ansi := RenderStatusLine("{}", true, &now, &sum)
	if !strings.Contains(ansi, CVerify+"⠋ verify"+Reset) ||
		!strings.Contains(ansi, CVerifyModel+"MiniMax-M3.1-Flash"+Reset) {
		t.Fatalf("unexpected ANSI output: %q", ansi)
	}
}

func TestHookAndDeadWorkerGC(t *testing.T) {
	tmp := t.TempDir()
	hudDir := filepath.Join(tmp, "hud")
	t.Setenv("ROUTER_HUD_DIR", hudDir)
	t.Setenv("ROUTER_DEPTH", "0")

	stalePath := filepath.Join(hudDir, "active", "stale.json")
	_ = fleet.AtomicWriteJSON(stalePath, dispatch.WorkerMarker{PID: 9999999, Mode: "act", Model: "x"}, true, true)
	if live := LoadActiveWorkers(hudDir); len(live) != 0 {
		t.Fatalf("expected dead worker pruned, got %+v", live)
	}
	if _, err := os.Stat(stalePath); !os.IsNotExist(err) {
		t.Fatalf("expected dead marker deleted")
	}

	callHook := func(payload string, depth string) string {
		t.Setenv("ROUTER_DEPTH", depth)
		var out bytes.Buffer
		HandleHookStdin(strings.NewReader(payload), &out)
		return out.String()
	}
	p1 := `{"conversationId":"c1","hookEventName":"PostToolUse",` +
		`"toolCall":{"name":"read_file","args":{"Path":"~/.agents/skills/readskill/SKILL.md"}}}`
	if out := callHook(p1, "0"); out != "{}\n" {
		t.Fatalf("hook output: %q", out)
	}
	p2 := `{"conversationId":"c1","hookEventName":"PostToolUse",` +
		`"toolCall":{"name":"run_command","args":{"CommandLine":"router --status"}}}`
	_ = callHook(p2, "0")
	p3 := `{"conversationId":"c1","hookEventName":"PostToolUse",` +
		`"toolCall":{"name":"run_command","args":{"CommandLine":"router --task fix --skill newskill"}}}`
	_ = callHook(p3, "0")
	raw, _ := os.ReadFile(filepath.Join(hudDir, "sessions", "c1.json"))
	var rec sessionRecord
	if json.Unmarshal(raw, &rec) != nil || len(rec.Skills) != 2 ||
		rec.Skills[0] != "readskill" || rec.Skills[1] != "newskill" {
		t.Fatalf("skills not preserved/appended across router calls: %s", string(raw))
	}
	_ = callHook(`{"conversationId":"c2","hookEventName":"SessionStart"}`, "1")
	if _, err := os.Stat(filepath.Join(hudDir, "sessions", "c2.json")); !os.IsNotExist(err) {
		t.Fatalf("nested ROUTER_DEPTH=1 hook must be ignored")
	}
}
