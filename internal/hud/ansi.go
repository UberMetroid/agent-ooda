package hud

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/UberMetroid/agent-ooda/internal/fleet"
)

// Exact ANSI 256-color codes and spinner frames for the Bubble-canopy HUD.
const (
	CIdleIcon     = "\033[38;5;245m"
	CIdleText     = "\033[1;38;5;252m"
	COrient       = "\033[1;38;5;214m"
	CObserve      = "\033[1;38;5;45m"
	CObserveModel = "\033[38;5;117m"
	CAct          = "\033[1;38;5;48m"
	CActModel     = "\033[38;5;156m"
	CVerify       = "\033[1;38;5;177m"
	CVerifyModel  = "\033[38;5;219m"
	CFleetFull    = "\033[38;5;78m"
	CFleetPartial = "\033[38;5;221m"
	CFleetLow     = "\033[1;38;5;203m"
	CTokCold      = "\033[38;5;75m"
	CTokCruise    = "\033[38;5;49m"
	CTokWarm      = "\033[38;5;220m"
	CTokHot       = "\033[1;38;5;208m"
	CTokRedHot    = "\033[1;38;5;196m"
	CMuted        = "\033[38;5;244m"
	Reset         = "\033[0m"
)

var (
	ansiRe        = regexp.MustCompile(`\x1b\[[0-9;]*m`)
	BrailleFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	OrientFrames  = []string{"◐", "◓", "◑", "◒"}
	IgnoredSkills = copyIgnoredSkills()
)

func copyIgnoredSkills() map[string]bool {
	out := make(map[string]bool, len(fleet.CoreHiddenSkills)+4)
	for k, v := range fleet.CoreHiddenSkills {
		out[k] = v
	}
	out["agent-ooda"] = true
	out["agent-ooda:agent-ooda"] = true
	out["lane"] = true
	out["agent-ooda:lane"] = true
	out["ooda"] = true
	out["ooda:ooda"] = true
	out["ooda:router"] = true
	return out
}

// TokRateColor maps live tok/s throughput across a 5-band thermal gradient.
func TokRateColor(tokS float64) string {
	switch {
	case tokS >= 180:
		return CTokRedHot
	case tokS >= 110:
		return CTokHot
	case tokS >= 60:
		return CTokWarm
	case tokS >= 25:
		return CTokCruise
	default:
		return CTokCold
	}
}

// Colorize wraps text in ANSI color sequences unless color output is disabled.
func Colorize(text, code string, useColor bool) string {
	if !useColor {
		return text
	}
	return code + text + Reset
}

// VisibleLen returns printable rune width after stripping ANSI SGR escapes.
func VisibleLen(text string) int {
	return utf8.RuneCountInString(ansiRe.ReplaceAllString(text, ""))
}

// TruncateANSI clips ANSI-colored text to at most maxWidth visible runes.
func TruncateANSI(text string, maxWidth int) string {
	if VisibleLen(text) <= maxWidth {
		return text
	}
	if maxWidth <= 1 {
		return strings.Repeat(".", max(0, maxWidth))
	}
	budget := maxWidth - 1
	used := 0
	var out strings.Builder
	pos := 0
	for _, loc := range ansiRe.FindAllStringIndex(text, -1) {
		if loc[0] > pos {
			plain := []rune(text[pos:loc[0]])
			rem := budget - used
			if rem <= 0 {
				break
			}
			if len(plain) <= rem {
				out.WriteString(string(plain))
				used += len(plain)
			} else {
				out.WriteString(string(plain[:rem]))
				used += rem
				break
			}
		}
		out.WriteString(text[loc[0]:loc[1]])
		pos = loc[1]
	}
	if used < budget && pos < len(text) {
		plain := []rune(text[pos:])
		rem := budget - used
		if len(plain) > rem {
			plain = plain[:rem]
		}
		out.WriteString(string(plain))
	}
	out.WriteString("…")
	if strings.Contains(text, "\033[") {
		out.WriteString(Reset)
	}
	return out.String()
}

// FormatCD formats a non-negative second delta into compact countdown notation.
func FormatCD(deltaSec float64) string {
	secs := max(0, int(deltaSec))
	hours := secs / 3600
	mins := (secs % 3600) / 60
	if hours >= 24 {
		return fmt.Sprintf("%dd%dh", hours/24, hours%24)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh%02dm", hours, mins)
	}
	if mins > 0 {
		return fmt.Sprintf("%dm", mins)
	}
	return fmt.Sprintf("%ds", secs)
}

// HudShortModel returns a compact display label for worker models on the HUD.
func HudShortModel(modelName string) string {
	m := strings.TrimSpace(modelName)
	if m == "" {
		return "-"
	}
	if strings.Contains(m, "fusion-claude-") {
		return "devin-fusion"
	}
	if m == "minimax/MiniMax-M3.1-Flash" {
		return "MiniMax-M3.1-Flash"
	}
	if m == "mistral-large-4-0-3" {
		return "mistral-large-4"
	}
	return m
}
