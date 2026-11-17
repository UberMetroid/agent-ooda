package hud

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
	"time"

	"github.com/UberMetroid/agent-ooda/internal/dispatch"
	"github.com/UberMetroid/agent-ooda/internal/fleet"
)

var idleStates = map[string]bool{
	"idle": true, "ready": true, "waiting": true, "done": true,
	"complete": true, "completed": true, "stopped": true,
	"paused": true, "error": true, "none": true,
}

func stepStyle(mode string, pal Palette) (string, string, string) {
	switch mode {
	case "observe", "recon":
		return "observe", pal.Observe, pal.ObserveMod
	case "orient":
		return "orient", pal.Orient, pal.OrientMod
	case "act:verify", "review":
		return "verify", pal.Verify, pal.VerifyMod
	default:
		return "act", pal.Act, pal.ActMod
	}
}

func formatWorkerMetric(w dispatch.WorkerMarker, now float64, pal Palette, useColor bool) string {
	if w.Phase == "" {
		return ""
	}
	elapsed := max(0, int(now-w.StartedAt))
	if w.Phase == "stream" && (now-w.UpdatedAt) <= 3.0 && w.TokS >= 1 {
		txt := fmt.Sprintf("~%d tok/s", int(math.Round(w.TokS)))
		if w.TokExact {
			txt = fmt.Sprintf("%d tok/s", int(math.Round(w.TokS)))
		}
		return Colorize(txt, pal.TokColor(w.TokS), useColor)
	}
	if w.Phase == "tool" {
		return Colorize(fmt.Sprintf("tool %ds", elapsed), pal.Muted, useColor)
	}
	if elapsed >= 1 {
		return Colorize(fmt.Sprintf("%ds", elapsed), pal.Muted, useColor)
	}
	return ""
}

func formatAgentLabel(lane, model string, compact bool) string {
	sm := HudShortModel(model)
	ln := strings.TrimSpace(SanitizePlain(lane))
	if !compact && ln != "" && sm != "" && sm != "-" && !strings.EqualFold(ln, sm) {
		return ln + " (" + sm + ")"
	}
	if sm != "" && sm != "-" {
		return sm
	}
	return ln
}

func renderWorkersLines(
	workers []dispatch.WorkerMarker, useColor bool, pal Palette, dot string, now float64, maxW int,
) string {
	lim := min(8, len(workers))
	lines := make([]string, 0, lim)
	for _, w := range workers[:lim] {
		spin := WorkerSpin(w.Phase, w.TokS, now)
		step, stepCol, modCol := stepStyle(w.Mode, pal)
		build := func(compact bool) string {
			lbl := formatAgentLabel(w.Lane, w.Model, compact)
			out := Colorize(spin+" "+step, stepCol, useColor)
			if lbl != "" {
				out += dot + Colorize(lbl, modCol, useColor)
			}
			if metric := formatWorkerMetric(w, now, pal, useColor); metric != "" {
				out += dot + metric
			}
			return out
		}
		ln := build(false)
		if VisibleLen(ln) > maxW && w.Lane != "" {
			ln = build(true)
		}
		lines = append(lines, TruncateANSI(ln, maxW))
	}
	return strings.Join(lines, "\n")
}

func renderSubagents(data map[string]any, useColor bool, pal Palette, dot string, now float64, maxW int) string {
	raw, ok := data["subagents"].([]any)
	if !ok {
		return ""
	}
	var lines []string
	for _, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		st, _ := m["status"].(string)
		if !strings.EqualFold(strings.TrimSpace(st), "running") {
			continue
		}
		name, _ := m["name"].(string)
		role, _ := m["role"].(string)
		name = strings.TrimSpace(SanitizePlain(name))
		lbl := name
		if lbl == "" {
			lbl = strings.TrimSpace(SanitizePlain(role))
		}
		step, stepCol, modCol := "act", pal.Act, pal.ActMod
		if strings.EqualFold(name, "research") {
			step, stepCol, modCol = "observe", pal.Observe, pal.ObserveMod
		}
		out := Colorize(WorkerSpin("tool", 0, now)+" "+step, stepCol, useColor)
		if lbl != "" {
			out += dot + Colorize(lbl, modCol, useColor)
		}
		lines = append(lines, TruncateANSI(out, maxW))
		if len(lines) >= 8 {
			break
		}
	}
	return strings.Join(lines, "\n")
}

func orchestratorLine(
	hudBase string, data map[string]any, useColor bool, pal Palette, dot string, now float64, maxW int,
) string {
	stateStr, cycleMode, orchMod := "", "", ""
	if s, _ := data["cycle_mode"].(string); strings.TrimSpace(s) != "" {
		cycleMode = strings.ToLower(strings.TrimSpace(s))
	}
	if ag, ok := data["agent"].(map[string]any); ok {
		for _, k := range []string{"state", "status", "phase"} {
			if s, _ := ag[k].(string); strings.TrimSpace(s) != "" {
				stateStr = strings.ToLower(strings.TrimSpace(s))
				break
			}
		}
	}
	for _, k := range []string{"agent_state", "state"} {
		if s, _ := data[k].(string); stateStr == "" && strings.TrimSpace(s) != "" {
			stateStr = strings.ToLower(strings.TrimSpace(s))
		}
	}
	confirmPending, _ := data["tool_confirmation_pending"].(bool)
	if !confirmPending && (stateStr == "" || idleStates[stateStr]) {
		return ""
	}
	switch mv := data["model"].(type) {
	case string:
		orchMod = mv
	case map[string]any:
		for _, k := range []string{"display_name", "id", "name"} {
			if s, _ := mv[k].(string); strings.TrimSpace(s) != "" {
				orchMod = s
				break
			}
		}
	}
	sessMod, sessPhase, sessUpd := ReadSessionMeta(hudBase, data, now)
	if orchMod == "" && sessMod != "" && sessUpd > 0 && (now-sessUpd) <= sessionTTLSecs {
		orchMod = sessMod
	}
	recentSess := sessUpd > 0 && (now-sessUpd) >= -2
	stage, spin, stepCol, modCol := "orient", OrientSpin(now), pal.Orient, pal.OrientMod
	switch {
	case confirmPending || stateStr == "reviewing" ||
		(recentSess && sessPhase == "decide" && (stateStr == "tool_use" || (now-sessUpd) <= 30)):
		stage = "decide"
	case recentSess && sessPhase == "observe" && (stateStr == "tool_use" || (now-sessUpd) <= 15):
		stage, spin, stepCol, modCol = "observe", WorkerSpin("tool", 0, now), pal.Observe, pal.ObserveMod
	case cycleMode == "accept-edits" || cycleMode == "auto" ||
		(recentSess && sessPhase == "act" && (stateStr == "tool_use" || (now-sessUpd) <= 15)):
		stage, spin, stepCol, modCol = "act", WorkerSpin("tool", 0, now), pal.Act, pal.ActMod
	}
	out := Colorize(spin+" "+stage, stepCol, useColor)
	if sm := HudShortModel(orchMod); sm != "" && sm != "-" {
		out += dot + Colorize(sm, modCol, useColor)
	}
	return TruncateANSI(out, maxW)
}

// RenderStatusLine composes one width-bounded statusLine HUD output for rawInput JSON.
func RenderStatusLine(
	rawInput string, useColor bool, nowTS *float64, _ *FleetSummary,
) string {
	if os.Getenv("NO_COLOR") != "" {
		useColor = false
	}
	data := map[string]any{}
	if strings.TrimSpace(rawInput) != "" {
		_ = json.Unmarshal([]byte(rawInput), &data)
	}
	termWidth := 120
	for _, k := range []string{"terminal_width", "terminalWidth"} {
		if v, ok := data[k].(float64); ok && v > 0 {
			termWidth = int(v)
			break
		}
	}
	maxWidth := max(40, termWidth-2)
	now := float64(time.Now().UnixNano()) / 1e9
	if nowTS != nil {
		now = *nowTS
	}
	hudBase := fleet.HudDirPath()
	pal := ResolvePalette(data, "")
	dot := Colorize(" · ", pal.Muted, useColor)
	if workers := LoadActiveWorkers(hudBase); len(workers) > 0 {
		return renderWorkersLines(workers, useColor, pal, dot, now, maxWidth)
	}
	if sub := renderSubagents(data, useColor, pal, dot, now, maxWidth); sub != "" {
		return sub
	}
	if orch := orchestratorLine(hudBase, data, useColor, pal, dot, now, maxWidth); orch != "" {
		return orch
	}
	last := LoadRecentFinish(hudBase, now)
	if last == nil {
		return ""
	}
	step, _, _ := stepStyle(last.Mode, pal)
	lbl := formatAgentLabel(last.Lane, last.Model, false)
	mark, stCol, tail := "✓ "+step, pal.FleetFull, Colorize(fmt.Sprintf("%.1fs", last.DurationS), pal.Muted, useColor)
	if last.ExitCode != 0 {
		mark, stCol, tail = "✗ "+step, pal.FleetLow, Colorize(fmt.Sprintf("exit %d", last.ExitCode), pal.FleetLow, useColor)
	}
	out := Colorize(mark, stCol, useColor)
	if lbl != "" {
		out += dot + Colorize(lbl, pal.Muted, useColor)
	}
	return TruncateANSI(out+dot+tail, maxWidth)
}

// RunStatusLine reads stdin JSON when piped and writes HUD lines when active.
func RunStatusLine() int {
	raw := ""
	if info, err := os.Stdin.Stat(); err == nil && (info.Mode()&os.ModeCharDevice) == 0 {
		if b, rerr := io.ReadAll(os.Stdin); rerr == nil {
			raw = string(b)
		}
	}
	if out := RenderStatusLine(raw, true, nil, nil); out != "" {
		fmt.Println(out)
	}
	return 0
}
