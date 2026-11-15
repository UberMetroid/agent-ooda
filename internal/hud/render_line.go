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

func stepStyle(mode string) (string, string, string) {
	switch mode {
	case "observe", "recon":
		return "observe", CObserve, CObserveModel
	case "act:verify", "review":
		return "verify", CVerify, CVerifyModel
	default:
		return "act", CAct, CActModel
	}
}

func formatWorkerMetric(w dispatch.WorkerMarker, now float64) string {
	if w.Phase == "" {
		return ""
	}
	elapsed := max(0, int(now-w.StartedAt))
	if w.Phase == "stream" && (now-w.UpdatedAt) <= 3.0 && w.TokS >= 1 {
		if w.TokExact {
			return fmt.Sprintf("%d tok/s", int(math.Round(w.TokS)))
		}
		return fmt.Sprintf("~%d tok/s", int(math.Round(w.TokS)))
	}
	if w.Phase == "tool" {
		return fmt.Sprintf("tool %ds", elapsed)
	}
	if elapsed >= 1 {
		return fmt.Sprintf("%ds", elapsed)
	}
	return ""
}

func aggregateRateMetric(workers []dispatch.WorkerMarker, now float64) string {
	var sum float64
	active := false
	for _, w := range workers {
		if w.Phase == "stream" && (now-w.UpdatedAt) <= 3.0 && w.TokS >= 1 {
			sum += w.TokS
			active = true
		}
	}
	if active && sum >= 1 {
		return fmt.Sprintf("~%d tok/s", int(math.Round(sum)))
	}
	return ""
}

func buildLeftSegment(
	workers []dispatch.WorkerMarker, thinking bool,
	frameIdx int, useColor bool, dot string, now float64,
) string {
	if len(workers) == 1 {
		w := workers[0]
		spin := BrailleFrames[frameIdx%len(BrailleFrames)]
		step, stepCol, modCol := stepStyle(w.Mode)
		out := Colorize(spin+" "+step, stepCol, useColor) + dot +
			Colorize(HudShortModel(w.Model), modCol, useColor)
		if metric := formatWorkerMetric(w, now); metric != "" {
			out += dot + Colorize(metric, CMuted, useColor)
		}
		return out
	}
	if len(workers) >= 2 {
		spin := BrailleFrames[frameIdx%len(BrailleFrames)]
		allObs := true
		for _, w := range workers {
			if w.Mode != "observe" && w.Mode != "recon" {
				allObs = false
				break
			}
		}
		step, stepCol, modCol := fmt.Sprintf("act ×%d", len(workers)), CAct, CActModel
		if allObs {
			step, stepCol, modCol = fmt.Sprintf("observe ×%d", len(workers)), CObserve, CObserveModel
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
		if agg := aggregateRateMetric(workers, now); agg != "" {
			out += dot + Colorize(agg, CMuted, useColor)
		}
		return out
	}
	if thinking {
		spin := OrientFrames[frameIdx%len(OrientFrames)]
		return Colorize(spin+" orient/decide", COrient, useColor)
	}
	return Colorize("○", CIdleIcon, useColor) + " " + Colorize("ooda", CIdleText, useColor)
}

func buildLine(
	workers []dispatch.WorkerMarker, skills []string, sum FleetSummary,
	thinking bool, frameIdx int, useColor, includeWake bool, now float64,
) string {
	fCol := CFleetPartial
	if sum.Total > 0 && sum.Active == sum.Total {
		fCol = CFleetFull
	} else if sum.Active <= 1 {
		fCol = CFleetLow
	}
	sep := Colorize(" │ ", CMuted, useColor)
	dot := Colorize(" · ", CMuted, useColor)
	skSeg := ""
	if len(skills) == 1 {
		skSeg = sep + Colorize("skill "+skills[0], CMuted, useColor)
	} else if len(skills) > 1 {
		skSeg = sep + Colorize("skills "+strings.Join(skills, ", "), CMuted, useColor)
	}
	fleetSeg := Colorize(fmt.Sprintf("fleet %d/%d", sum.Active, sum.Total), fCol, useColor)
	if includeWake && sum.WakeCD != "" {
		fleetSeg += Colorize(" (next "+sum.WakeCD+")", CMuted, useColor)
	}
	left := buildLeftSegment(workers, thinking, frameIdx, useColor, dot, now)
	return left + skSeg + sep + fleetSeg
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
	sum := LoadFleetSummary(time.Now().UTC())
	if sumOverride != nil {
		sum = *sumOverride
	}
	thinking := agentThinking(data)
	frame := int(now * 4)
	line := buildLine(workers, skills, sum, thinking, frame, useColor, true, now)
	if VisibleLen(line) > maxWidth && sum.WakeCD != "" {
		line = buildLine(workers, skills, sum, thinking, frame, useColor, false, now)
	}
	if VisibleLen(line) > maxWidth && len(skills) > 1 {
		compact := []string{fmt.Sprintf("%s+%d", skills[0], len(skills)-1)}
		line = buildLine(workers, compact, sum, thinking, frame, useColor, false, now)
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
