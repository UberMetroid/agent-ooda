package hud

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/UberMetroid/agent-ooda/internal/dispatch"
	"github.com/UberMetroid/agent-ooda/internal/fleet"
)

// Palette defines ANSI 256-color sequences synced with Antigravity (agy) themes.
type Palette struct {
	Name       string
	IdleIcon   string
	IdleText   string
	Muted      string
	Observe    string
	ObserveMod string
	Orient     string
	OrientMod  string
	Act        string
	ActMod     string
	Verify     string
	VerifyMod  string
	FleetFull  string
	FleetPart  string
	FleetLow   string
	Tok        [5]string
	TUIAccent  string
	TUIHeader  string
	TUIOK      string
}

var (
	// WaitArcFrames provides a subtle pulsing arc while waiting for first byte (TTFB).
	WaitArcFrames = []string{"◜", "◝", "◞", "◟"}
	// ToolScanFrames pulses back-and-forth while a worker executes tools or waits on files.
	ToolScanFrames = []string{"⠋", "⠙", "⠸", "⠴", "⠦", "⠇", "⠦", "⠴", "⠸", "⠙"}
	// CompassFrames provides a smooth 8-step dial while Orient/Decide is active.
	CompassFrames = []string{"◐", "◴", "◓", "◷", "◑", "◶", "◒", "◵"}
	palettes      = func() map[string]Palette {
		fg := func(n int) string { return fmt.Sprintf("\033[38;5;%dm", n) }
		bd := func(n int) string { return fmt.Sprintf("\033[1;38;5;%dm", n) }
		specs := map[string][19]int{
			"light":           {242, 235, 241, 25, 24, 130, 94, 28, 22, 90, 54, 28, 130, 160, 25, 30, 130, 166, 160},
			"tokyo night":     {103, 189, 60, 111, 117, 179, 222, 149, 150, 141, 183, 149, 179, 210, 111, 117, 179, 215, 210},
			"solarized dark":  {245, 250, 242, 33, 37, 136, 178, 64, 106, 61, 105, 64, 136, 160, 33, 37, 136, 166, 160},
			"solarized light": {244, 236, 244, 33, 37, 136, 130, 64, 28, 61, 54, 64, 136, 160, 33, 37, 136, 166, 160},
			"colorblind-friendly dark": {
				245, 254, 244, 117, 153, 214, 221, 43, 122, 176, 218, 43, 214, 202, 117, 43, 214, 208, 202,
			},
			"colorblind-friendly light": {
				242, 235, 241, 26, 25, 130, 94, 30, 23, 90, 54, 30, 130, 124, 26, 30, 130, 166, 124,
			},
			"halloween": {208, 220, 244, 135, 177, 208, 220, 118, 156, 196, 210, 118, 208, 196, 135, 118, 220, 208, 196},
		}
		out := make(map[string]Palette, len(specs)+1)
		out["dark"] = Palette{
			Name: "dark", IdleIcon: CIdleIcon, IdleText: CIdleText, Muted: CMuted,
			Observe: CObserve, ObserveMod: CObserveModel, Orient: COrient, OrientMod: fg(222),
			Act: CAct, ActMod: CActModel, Verify: CVerify, VerifyMod: CVerifyModel,
			FleetFull: CFleetFull, FleetPart: CFleetPartial, FleetLow: CFleetLow,
			Tok:       [5]string{CTokCold, CTokCruise, CTokWarm, CTokHot, CTokRedHot},
			TUIAccent: "\033[1;36m", TUIHeader: bd(117), TUIOK: bd(78),
		}
		for k, s := range specs {
			out[k] = Palette{
				Name: k, IdleIcon: fg(s[0]), IdleText: bd(s[1]), Muted: fg(s[2]),
				Observe: bd(s[3]), ObserveMod: fg(s[4]), Orient: bd(s[5]), OrientMod: fg(s[6]),
				Act: bd(s[7]), ActMod: fg(s[8]), Verify: bd(s[9]), VerifyMod: fg(s[10]),
				FleetFull: fg(s[11]), FleetPart: fg(s[12]), FleetLow: bd(s[13]),
				Tok:       [5]string{fg(s[14]), fg(s[15]), fg(s[16]), bd(s[17]), bd(s[18])},
				TUIAccent: bd(s[3]), TUIHeader: bd(s[9]), TUIOK: bd(s[11]),
			}
		}
		return out
	}()
)

// ResolveThemeName selects the active palette name from env, stdin payload, and agy settings.
func ResolveThemeName(data map[string]any, home string, now time.Time) string {
	pick := strings.ToLower(strings.TrimSpace(os.Getenv("AGENT_OODA_THEME")))
	if pick == "" && data != nil {
		for _, k := range []string{"colorScheme", "scheme", "theme"} {
			if s, _ := data[k].(string); strings.TrimSpace(s) != "" {
				pick = strings.ToLower(strings.TrimSpace(s))
				break
			}
		}
		if ag, ok := data["agent"].(map[string]any); pick == "" && ok {
			if s, ok := ag["colorScheme"].(string); ok && strings.TrimSpace(s) != "" {
				pick = strings.ToLower(strings.TrimSpace(s))
			}
		}
	}
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	var cfg map[string]any
	if (pick == "" || pick == "terminal" || pick == "auto") && home != "" {
		cfgPath := filepath.Join(home, ".gemini", "antigravity-cli", "settings.json")
		if raw, err := os.ReadFile(cfgPath); err == nil && json.Unmarshal(raw, &cfg) == nil {
			for _, k := range []string{"colorScheme", "theme"} {
				if s, _ := cfg[k].(string); strings.TrimSpace(s) != "" {
					pick = strings.ToLower(strings.TrimSpace(s))
					break
				}
			}
		}
	}
	if pick == "" || pick == "terminal" || pick == "auto" {
		if season, _ := cfg["seasonalThemesOptIn"].(string); strings.HasPrefix(season, "halloween") &&
			((now.Month() == time.October && now.Day() >= 25) || (now.Month() == time.November && now.Day() == 1)) {
			return "halloween"
		}
		if fgbg := strings.TrimSpace(os.Getenv("COLORFGBG")); fgbg != "" {
			parts := strings.Split(fgbg, ";")
			if bg := strings.TrimSpace(parts[len(parts)-1]); bg == "7" || bg == "15" {
				return "light"
			}
		}
		return "dark"
	}
	return PaletteFor(pick).Name
}

// PaletteFor returns the ANSI Palette for a normalized theme name, defaulting to dark.
func PaletteFor(name string) Palette {
	key := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(name)), "_", "-")
	if strings.HasPrefix(key, "halloween") {
		key = "halloween"
	} else {
		key = strings.ReplaceAll(key, "-", " ")
		key = strings.ReplaceAll(key, "colorblind friendly", "colorblind-friendly")
		key = strings.ReplaceAll(key, "colorblind dark", "colorblind-friendly dark")
		key = strings.ReplaceAll(key, "colorblind light", "colorblind-friendly light")
	}
	if p, ok := palettes[key]; ok {
		return p
	}
	return palettes["dark"]
}

// ResolvePalette resolves the live Palette from statusLine JSON and agy settings.json.
func ResolvePalette(data map[string]any, home string) Palette {
	return PaletteFor(ResolveThemeName(data, home, time.Now()))
}

// TokColor maps live tok/s velocity across the palette's 5-band thermal gradient.
func (p Palette) TokColor(tokS float64) string {
	switch {
	case tokS >= 180:
		return p.Tok[4]
	case tokS >= 110:
		return p.Tok[3]
	case tokS >= 60:
		return p.Tok[2]
	case tokS >= 25:
		return p.Tok[1]
	default:
		return p.Tok[0]
	}
}

// WorkerSpin selects a phase- and velocity-coupled spinner frame.
func WorkerSpin(phase string, tokS, now float64) string {
	t := math.Mod(max(0, now), 3600)
	switch phase {
	case "wait":
		return WaitArcFrames[int(t*2)%len(WaitArcFrames)]
	case "tool":
		return ToolScanFrames[int(t*4)%len(ToolScanFrames)]
	case "stream":
		fps := 3.0
		if tokS >= 140 {
			fps = 8.0
		} else if tokS >= 60 {
			fps = 5.0
		}
		return BrailleFrames[int(t*fps)%len(BrailleFrames)]
	default:
		return BrailleFrames[int(t*2)%len(BrailleFrames)]
	}
}

// OrientSpin returns the smooth 8-step dial frame for Orient/Decide activity.
func OrientSpin(now float64) string {
	return CompassFrames[int(now*4)%len(CompassFrames)]
}

// LoadRecentFinish returns last.json when a task finished within the 4s flash window.
func LoadRecentFinish(hudBase string, now float64) *dispatch.LastRunMarker {
	raw, err := os.ReadFile(filepath.Join(hudBase, "last.json"))
	if err != nil {
		return nil
	}
	var last dispatch.LastRunMarker
	if json.Unmarshal(raw, &last) != nil || last.FinishedAt <= 0 {
		return nil
	}
	if age := now - last.FinishedAt; age < 0 || age > 4.0 {
		return nil
	}
	return &last
}

// ResolveHudCwd resolves the active workspace directory from stdin JSON, workers, or session state.
func ResolveHudCwd(
	hudBase string, data map[string]any, workers []dispatch.WorkerMarker, now float64,
) string {
	for _, k := range []string{"cwd", "workspace_directory"} {
		if s, _ := data[k].(string); strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	if ws, ok := data["workspace"].(string); ok && strings.TrimSpace(ws) != "" {
		return strings.TrimSpace(ws)
	}
	if wsm, ok := data["workspace"].(map[string]any); ok {
		for _, k := range []string{"cwd", "path", "dir", "root"} {
			if s, _ := wsm[k].(string); strings.TrimSpace(s) != "" {
				return strings.TrimSpace(s)
			}
		}
	}
	if len(workers) > 0 && workers[0].Cwd != "" {
		return workers[0].Cwd
	}
	rawCur, err := os.ReadFile(filepath.Join(hudBase, "current_session.json"))
	if err != nil {
		return ""
	}
	var cur struct {
		ConversationID string  `json:"conversation_id"`
		UpdatedAt      float64 `json:"updated_at"`
	}
	if json.Unmarshal(rawCur, &cur) != nil || cur.ConversationID == "" || (now-cur.UpdatedAt) > sessionTTLSecs {
		return ""
	}
	rawSess, err := os.ReadFile(filepath.Join(hudBase, "sessions", fleet.SafeID(cur.ConversationID)+".json"))
	if err != nil {
		return ""
	}
	var sess struct {
		Cwd string `json:"cwd"`
	}
	if json.Unmarshal(rawSess, &sess) != nil {
		return ""
	}
	return strings.TrimSpace(sess.Cwd)
}
