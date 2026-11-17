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

func aggregateRateMetric(workers []dispatch.WorkerMarker, now float64, pal Palette, useColor bool) string {
	var sum float64
	active := false
	for _, w := range workers {
		if w.Phase == "stream" && (now-w.UpdatedAt) <= 3.0 && w.TokS >= 1 {
			sum += w.TokS
			active = true
		}
	}
	if active && sum >= 1 {
		return Colorize(fmt.Sprintf("~%d tok/s", int(math.Round(sum))), pal.TokColor(sum), useColor)
	}
	return ""
}

func buildLeftSegment(
	workers []dispatch.WorkerMarker, last *dispatch.LastRunMarker,
	thinking, useColor bool, pal Palette, dot string, now float64,
) string {
	if len(workers) == 1 {
		w := workers[0]
		spin := WorkerSpin(w.Phase, w.TokS, now)
		step, stepCol, modCol := stepStyle(w.Mode, pal)
		out := Colorize(spin+" "+step, stepCol, useColor) + dot +
			Colorize(HudShortModel(w.Model), modCol, useColor)
		if metric := formatWorkerMetric(w, now, pal, useColor); metric != "" {
			out += dot + metric
		}
		return out
	}
	if len(workers) >= 2 {
		allObs, aggTok, spinPhase := true, 0.0, workers[0].Phase
		for _, w := range workers {
			if w.Mode != "observe" && w.Mode != "recon" {
				allObs = false
			}
			aggTok += w.TokS
			if w.Phase == "stream" {
				spinPhase = "stream"
			}
		}
		spin := WorkerSpin(spinPhase, aggTok, now)
		step, stepCol, modCol := fmt.Sprintf("act ×%d", len(workers)), pal.Act, pal.ActMod
		if allObs {
			step, stepCol, modCol = fmt.Sprintf("observe ×%d", len(workers)), pal.Observe, pal.ObserveMod
		}
		lim := min(3, len(workers))
		mods := make([]string, lim)
		for i := 0; i < lim; i++ {
			mods[i] = HudShortModel(workers[i].Model)
		}
		modStr := strings.Join(mods, ", ")
		if len(workers) > 3 {
			modStr += ", …"
		}
		out := Colorize(spin+" "+step, stepCol, useColor) + dot + Colorize(modStr, modCol, useColor)
		if agg := aggregateRateMetric(workers, now, pal, useColor); agg != "" {
			out += dot + agg
		}
		return out
	}
	if thinking {
		return Colorize(OrientSpin(now)+" orient/decide", pal.Orient, useColor)
	}
	if last != nil {
		step, _, _ := stepStyle(last.Mode, pal)
		if last.ExitCode == 0 {
			return Colorize("✓ "+step, pal.FleetFull, useColor) + dot +
				Colorize(HudShortModel(last.Model), pal.Muted, useColor) +
				dot + Colorize(fmt.Sprintf("%.1fs", last.DurationS), pal.Muted, useColor)
		}
		return Colorize("✗ "+step, pal.FleetLow, useColor) + dot +
			Colorize(HudShortModel(last.Model), pal.Muted, useColor) +
			dot + Colorize(fmt.Sprintf("exit %d", last.ExitCode), pal.FleetLow, useColor)
	}
	return Colorize("○", pal.IdleIcon, useColor) + " " + Colorize("agent-ooda", pal.IdleText, useColor)
}

func buildLine(
	left string, skills []string, sum FleetSummary, useColor, includeWake bool, pal Palette,
) string {
	fCol := pal.FleetPart
	if sum.Total > 0 && sum.Active == sum.Total {
		fCol = pal.FleetFull
	} else if sum.Active <= 1 {
		fCol = pal.FleetLow
	}
	sep := Colorize(" │ ", pal.Muted, useColor)
	skSeg := ""
	if len(skills) == 1 {
		skSeg = sep + Colorize("skill "+skills[0], pal.Muted, useColor)
	} else if len(skills) > 1 {
		skSeg = sep + Colorize("skills "+strings.Join(skills, ", "), pal.Muted, useColor)
	}
	pinSeg := ""
	if sum.Pins != "" {
		pinSeg = sep + Colorize("pin "+sum.Pins, pal.Muted, useColor)
	}
	fleetSeg := Colorize(fmt.Sprintf("fleet %d/%d", sum.Active, sum.Total), fCol, useColor)
	if includeWake && sum.WakeCD != "" {
		fleetSeg += Colorize(" (next "+sum.WakeCD+")", pal.Muted, useColor)
	}
	return left + skSeg + pinSeg + sep + fleetSeg
}

func agentThinking(data map[string]any) bool {
	if ag, ok := data["agent"].(map[string]any); ok {
		for _, k := range []string{"state", "status", "phase"} {
			if s, _ := ag[k].(string); strings.TrimSpace(s) != "" {
				return !idleStates[strings.ToLower(strings.TrimSpace(s))]
			}
		}
	}
	for _, k := range []string{"agent_state", "state"} {
		if s, _ := data[k].(string); strings.TrimSpace(s) != "" {
			return !idleStates[strings.ToLower(strings.TrimSpace(s))]
		}
	}
	return false
}

// RenderStatusLine composes one width-bounded statusLine HUD line for rawInput JSON.
func RenderStatusLine(
	rawInput string, useColor bool, nowTS *float64, sumOverride *FleetSummary,
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
	workers := LoadActiveWorkers(hudBase)
	skills := CollectSkills(hudBase, data, workers, now)
	pal := ResolvePalette(data, "")
	var sum FleetSummary
	if sumOverride != nil {
		sum = *sumOverride
	} else {
		hudCwd := ResolveHudCwd(hudBase, data, workers, now)
		sum = LoadFleetSummary(time.Now().UTC(), hudCwd)
	}
	sum.Pins = strings.TrimSpace(SanitizePlain(sum.Pins))
	sum.WakeCD = strings.TrimSpace(SanitizePlain(sum.WakeCD))
	thinking := agentThinking(data)
	var last *dispatch.LastRunMarker
	if len(workers) == 0 && !thinking {
		last = LoadRecentFinish(hudBase, now)
	} else {
		sum.Pins = ""
	}
	left := buildLeftSegment(workers, last, thinking, useColor, pal, Colorize(" · ", pal.Muted, useColor), now)
	line := buildLine(left, skills, sum, useColor, true, pal)
	if VisibleLen(line) > maxWidth && sum.Pins != "" {
		sum.Pins = ""
		line = buildLine(left, skills, sum, useColor, true, pal)
	}
	if VisibleLen(line) > maxWidth && sum.WakeCD != "" {
		line = buildLine(left, skills, sum, useColor, false, pal)
	}
	if VisibleLen(line) > maxWidth && len(skills) > 1 {
		compact := []string{fmt.Sprintf("%s+%d", skills[0], len(skills)-1)}
		line = buildLine(left, compact, sum, useColor, false, pal)
	}
	return TruncateANSI(line, maxWidth)
}

// RunStatusLine reads stdin JSON when piped and writes one HUD status line.
func RunStatusLine() int {
	raw := ""
	if info, err := os.Stdin.Stat(); err == nil && (info.Mode()&os.ModeCharDevice) == 0 {
		if b, rerr := io.ReadAll(os.Stdin); rerr == nil {
			raw = string(b)
		}
	}
	fmt.Println(RenderStatusLine(raw, true, nil, nil))
	return 0
}
