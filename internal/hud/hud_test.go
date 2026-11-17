package hud

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/UberMetroid/agy-router/internal/dispatch"
	"github.com/UberMetroid/agy-router/internal/fleet"
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
	now := float64(((time.Now().Unix() / 10) + 1) * 10)
	if got := RenderStatusLine("{}", true, &now, nil); got != "ROUTER" {
		t.Fatalf("expected active ROUTER idle badge, got %q", got)
	}
	for _, tc := range [][2]string{
		{`{"agent":{"state":"thinking"}}`, "◈ orient"},
		{`{"agent_state":"reviewing","model":{"display_name":"Gemini 4 Carbon"}}`, "◇ decide · Gemini 4 Carbon"},
		{`{"agent_state":"idle","tool_confirmation_pending":true}`, "◇ decide"},
		{`{"agent_state":"working","cycle_mode":"auto","model":"gemini-4-argon"}`, "▸ act · gemini-4-argon"},
		{`{"subagents":[{"name":"research","status":"running"},{"name":"self","role":"Builder","status":"running"}]}`,
			"◎ observe · research\n▸ act · self"},
	} {
		if got := RenderStatusLine(tc[0], true, &now, nil); got != tc[1] {
			t.Fatalf("input %s: got %q, want %q", tc[0], got, tc[1])
		}
	}
	var out bytes.Buffer
	HandleHookStdin(strings.NewReader(`{"conversationId":"hc1","hookEventName":"PostToolUse",`+
		`"toolCall":{"name":"read_file","args":{"Path":"a.go"}}}`), &out)
	if got := RenderStatusLine(`{"conversation_id":"hc1","agent_state":"tool_use"}`, true, &now, nil); got != "◎ observe" {
		t.Fatalf("expected observe from read_file hook, got %q", got)
	}
	setTestWorkers(t, hudDir, []dispatch.WorkerMarker{{Mode: "observe", Model: "swe-2-flash-max"}})
	if got := RenderStatusLine("{}", true, &now, nil); got != "◎ observe · swe-2-flash-max" {
		t.Fatalf("single worker mismatch: %q", got)
	}
	setTestWorkers(t, hudDir, []dispatch.WorkerMarker{
		{Mode: "act", Lane: "muse", Model: "muse-ember-1.0", Phase: "stream", TokS: 102, UpdatedAt: now - 1},
		{Mode: "act", Lane: "muse", Model: "muse-ember-1.0", Phase: "tool", StartedAt: now - 4},
	})
	multi := strings.Split(RenderStatusLine("{}", true, &now, nil), "\n")
	if len(multi) != 2 || multi[0] != "▸ act · muse (muse-ember-1.0) · ~102 tok/s" ||
		multi[1] != "▸ act · muse (muse-ember-1.0) · tool 4s" {
		t.Fatalf("unexpected multi-line workers: %#v", multi)
	}
}

func TestStatusLineTelemetryANSI(t *testing.T) {
	tmp := t.TempDir()
	hudDir := filepath.Join(tmp, "hud")
	t.Setenv("ROUTER_HUD_DIR", hudDir)
	t.Setenv("NO_COLOR", "1")
	now := 1000.0
	for _, tc := range []struct {
		workers []dispatch.WorkerMarker
		want    string
	}{
		{[]dispatch.WorkerMarker{{Mode: "act", Lane: "mcode", Model: "minimax/MiniMax-M3.1-Flash",
			Phase: "stream", TokS: 118, UpdatedAt: 999.0, StartedAt: 995.0}},
			"▸ act · mcode (MiniMax-M3.1-Flash) · ~118 tok/s"},
		{[]dispatch.WorkerMarker{{Mode: "act", Model: "minimax/MiniMax-M3.1-Flash",
			Phase: "stream", TokS: 118, TokExact: true, UpdatedAt: 999.0, StartedAt: 995.0}},
			"▸ act · MiniMax-M3.1-Flash · 118 tok/s"},
		{[]dispatch.WorkerMarker{{Mode: "act", Model: "swe-2-flash-max",
			Phase: "tool", StartedAt: 992.0, UpdatedAt: 999.0}}, "▸ act · swe-2-flash-max · tool 8s"},
		{[]dispatch.WorkerMarker{{Mode: "act", Model: "swe-2-flash-max",
			Phase: "wait", StartedAt: 994.0, UpdatedAt: 994.0}}, "▸ act · swe-2-flash-max · 6s"},
	} {
		setTestWorkers(t, hudDir, tc.workers)
		if got := RenderStatusLine("{}", true, &now, nil); got != tc.want {
			t.Fatalf("got %q, want %q", got, tc.want)
		}
	}
	setTestWorkers(t, hudDir, []dispatch.WorkerMarker{{Mode: "act", Lane: "grok", Model: "grok-4.7"}})
	if narrow := RenderStatusLine(`{"terminal_width":40}`, true, &now, nil); narrow != "▸ act · grok (grok-4.7)" {
		t.Fatalf("width check mismatch: %q (len=%d)", narrow, VisibleLen(narrow))
	}
	t.Setenv("NO_COLOR", "")
	t.Setenv("AGENT_OODA_THEME", "dark")
	setTestWorkers(t, hudDir, []dispatch.WorkerMarker{{Mode: "act:verify", Model: "minimax/MiniMax-M3.1-Flash"}})
	ansi := RenderStatusLine("{}", true, &now, nil)
	if !strings.Contains(ansi, CVerify+"✦ verify"+Reset) ||
		!strings.Contains(ansi, CVerifyModel+"MiniMax-M3.1-Flash"+Reset) {
		t.Fatalf("unexpected ANSI output: %q", ansi)
	}
}

func TestThermalTokSAndIdlePins(t *testing.T) {
	tmp := t.TempDir()
	hudDir := filepath.Join(tmp, "hud")
	t.Setenv("ROUTER_HUD_DIR", hudDir)
	t.Setenv("NO_COLOR", "")
	t.Setenv("AGENT_OODA_THEME", "dark")
	now := 1000.0
	for _, tc := range []struct {
		tokS float64
		col  string
	}{
		{18, CTokCold}, {42, CTokCruise}, {85, CTokWarm}, {140, CTokHot}, {210, CTokRedHot},
	} {
		setTestWorkers(t, hudDir, []dispatch.WorkerMarker{{
			Mode: "act", Model: "grok-4.7", Phase: "stream", TokS: tc.tokS, UpdatedAt: 999.0, StartedAt: 995.0,
		}})
		if got := RenderStatusLine("{}", true, &now, nil); !strings.Contains(got, tc.col+"~") {
			t.Fatalf("expected thermal color %q for %.0f tok/s, got %q", tc.col, tc.tokS, got)
		}
	}
	setTestWorkers(t, hudDir, nil)
	home := t.TempDir()
	cfgDir := filepath.Join(home, ".gemini", "antigravity-cli")
	_ = os.MkdirAll(cfgDir, 0o755)
	_ = os.WriteFile(filepath.Join(cfgDir, "settings.json"), []byte(`{"colorScheme":"tokyo night"}`), 0o600)
	t.Setenv("HOME", home)
	t.Setenv("AGENT_OODA_THEME", "")
	_ = fleet.AtomicWriteJSON(filepath.Join(hudDir, "last.json"), dispatch.LastRunMarker{
		Mode: "act", Lane: "mcode", Model: "minimax/MiniMax-M3.1-Flash", ExitCode: 0, DurationS: 2.5, FinishedAt: 998.0,
	}, true, true)
	flash := RenderStatusLine("{}", true, &now, nil)
	if !strings.Contains(flash, PaletteFor("tokyo night").FleetFull+"✓ act") ||
		!strings.Contains(flash, "mcode (MiniMax-M3.1-Flash)") {
		t.Fatalf("expected tokyo night completion flash, got %q", flash)
	}
	_ = os.WriteFile(filepath.Join(cfgDir, "settings.json"), []byte(`{"colorScheme":"light"}`), 0o600)
	_ = os.Remove(filepath.Join(hudDir, "last.json"))
	if idleLight := RenderStatusLine("{}", true, &now, nil); idleLight != PaletteFor("light").IdleText+"ROUTER"+Reset {
		t.Fatalf("expected light theme ROUTER idle badge, got %q", idleLight)
	}
	setTestWorkers(t, hudDir, []dispatch.WorkerMarker{{Mode: "act", Model: "grok-4.7"}})
	if light := RenderStatusLine("{}", true, &now, nil); !strings.Contains(light, PaletteFor("light").Act+"▸ act") {
		t.Fatalf("expected live light theme swap, got %q", light)
	}
	if WorkerSpin("wait", 0, 1000.35) == WorkerSpin("stream", 190, 1000.35) ||
		PaletteFor("colorblind-friendly-dark").Name != "colorblind-friendly dark" ||
		PaletteFor("halloween-2026").Name != "halloween" || VisibleLen("\033[1;38;5;75mh\033[Ké") != 2 {
		t.Fatalf("spinner, theme slug, or VisibleLen mismatch")
	}
}
func TestHookAndDeadWorkerGC(t *testing.T) {
	tmp := t.TempDir()
	hudDir := filepath.Join(tmp, "hud")
	for k, v := range map[string]string{"ROUTER_HUD_DIR": hudDir, "AGENT_OODA_DEPTH": "0", "ROUTER_DEPTH": "0"} {
		t.Setenv(k, v)
	}
	stalePath := filepath.Join(hudDir, "active", "stale.json")
	_ = fleet.AtomicWriteJSON(stalePath, dispatch.WorkerMarker{PID: 9999999, Mode: "act", Model: "x"}, true, true)
	if live := LoadActiveWorkers(hudDir); len(live) != 0 {
		t.Fatalf("expected dead worker pruned, got %+v", live)
	}
	if _, err := os.Stat(stalePath); !os.IsNotExist(err) {
		t.Fatalf("expected dead marker deleted")
	}
	callHook := func(payload string, depth string) string {
		t.Setenv("AGENT_OODA_DEPTH", depth)
		t.Setenv("ROUTER_DEPTH", depth)
		var out bytes.Buffer
		HandleHookStdin(strings.NewReader(payload), &out)
		return out.String()
	}
	pfx := `{"conversationId":"c1","hookEventName":"PostToolUse","toolCall":{"name":`
	for _, p := range []string{
		pfx + `"read_file","args":{"Path":"~/.agents/skills/readskill/SKILL.md"}}}`,
		pfx + `"run_command","args":{"CommandLine":"agy-router --task fix --skill none,off,newskill"}}}`,
		pfx + `"run_command","args":{"CommandLine":"agy-router --orient why"}}}`,
	} {
		if out := callHook(p, "0"); out != "{}\n" {
			t.Fatalf("hook output: %q", out)
		}
	}
	raw, _ := os.ReadFile(filepath.Join(hudDir, "sessions", "c1.json"))
	var rec sessionRecord
	if json.Unmarshal(raw, &rec) != nil || len(rec.Skills) != 2 ||
		rec.Skills[0] != "readskill" || rec.Skills[1] != "newskill" || rec.LastPhase != "orient" {
		t.Fatalf("unexpected hook session record: %s", string(raw))
	}
	_ = callHook(`{"conversationId":"c2","hookEventName":"SessionStart"}`, "1")
	if _, err := os.Stat(filepath.Join(hudDir, "sessions", "c2.json")); !os.IsNotExist(err) {
		t.Fatalf("nested ROUTER_DEPTH=1 hook must be ignored")
	}
}

func TestSanitizeTerminalEscapes(t *testing.T) {
	hudDir := filepath.Join(t.TempDir(), "hud")
	t.Setenv("ROUTER_HUD_DIR", hudDir)
	t.Setenv("NO_COLOR", "1")
	if got := HudShortModel("\x1b]52;c;ZXY=\x07claude\x1b[2J\x07"); got != "claude" {
		t.Fatalf("expected stripped HudShortModel == 'claude', got %q", got)
	}
	setTestWorkers(t, hudDir, []dispatch.WorkerMarker{{
		Mode: "act", Lane: "la\x1b]0;pwn\x07ne", Model: "grok\x1b]52;c;ZXY=\x07-4",
	}})
	now := 1000.0
	line := RenderStatusLine("{}", true, &now, nil)
	for _, bad := range []string{"\x1b]52", "\x1b]0", "\x1b[2J", "\x07"} {
		if strings.Contains(line, bad) {
			t.Fatalf("HUD output leaked terminal escape %q: %q", bad, line)
		}
	}
}

func TestBoundedHudAndHookStdin(t *testing.T) {
	hudDir := filepath.Join(t.TempDir(), "hud")
	for k, v := range map[string]string{"ROUTER_HUD_DIR": hudDir, "NO_COLOR": "1",
		"AGENT_OODA_DEPTH": "0", "ROUTER_DEPTH": "0"} {
		t.Setenv(k, v)
	}
	bigPad := strings.Repeat("x", (1<<20)+4096)
	var hkOut bytes.Buffer
	HandleHookStdin(strings.NewReader(`{"conversationId":"big","modelName":"claude-opus-5-5-high","pad":"`+
		bigPad+`"}`), &hkOut)
	if _, err := os.Stat(filepath.Join(hudDir, "current_session.json")); !os.IsNotExist(err) {
		t.Fatalf("expected oversized hook stdin to skip writing session state")
	}
	inR, inW, _ := os.Pipe()
	outR, outW, _ := os.Pipe()
	oldIn, oldOut := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = inR, outW
	defer func() { os.Stdin, os.Stdout = oldIn, oldOut }()
	go func() {
		defer inW.Close()
		_, _ = inW.WriteString(`{"agent_state":"working","cycle_mode":"plan","pad":"` + bigPad + `"}`)
	}()
	RunStatusLine()
	_ = outW.Close()
	var capBuf bytes.Buffer
	_, _ = capBuf.ReadFrom(outR)
	if strings.TrimSpace(capBuf.String()) != "ROUTER" {
		t.Fatalf("expected oversized statusLine stdin to fall back to ROUTER, got %q", capBuf.String())
	}
}
