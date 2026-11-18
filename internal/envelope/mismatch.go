package envelope

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/UberMetroid/agy-router/internal/fleet"
	"github.com/UberMetroid/agy-router/internal/worktree"
)

const mismatchTTLSecs = 1800

var mismatchMu sync.Mutex

// MismatchEntry records a recently failed/rejected OODA loop lane.
type MismatchEntry struct {
	Lane       string  `json:"lane"`
	Family     string  `json:"family"`
	Reason     string  `json:"reason"`
	RecordedAt float64 `json:"recorded_at"`
}

// MismatchKey builds a stable '<repo-root>::<sorted files|*>' lookup key.
func MismatchKey(cwd string, files []string) string {
	base := cwd
	if base == "" {
		base = "."
	}
	if abs, err := filepath.Abs(base); err == nil {
		base = abs
	}
	root := fleet.TryRepoRoot(base)
	if root == "" {
		root = base
	}
	var norm []string
	for _, f := range files {
		if strings.TrimSpace(f) == "" {
			continue
		}
		if n, err := worktree.NormalizeRelPath(f); err == nil {
			norm = append(norm, n)
		}
	}
	sort.Strings(norm)
	scope := "*"
	if len(norm) > 0 {
		scope = strings.Join(norm, ",")
	}
	return root + "::" + scope
}

func readMismatchMap() map[string]MismatchEntry {
	path := filepath.Join(fleet.HudDirPath(), "mismatch.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return map[string]MismatchEntry{}
	}
	var out map[string]MismatchEntry
	if err := json.Unmarshal(raw, &out); err != nil || out == nil {
		return map[string]MismatchEntry{}
	}
	return out
}

// LoadRecentMismatch returns a recent (<30m) mismatch record for cwd/files.
func LoadRecentMismatch(cwd string, files []string) *MismatchEntry {
	mapping := readMismatchMap()
	entry, ok := mapping[MismatchKey(cwd, files)]
	if !ok || entry.Family == "" {
		return nil
	}
	now := float64(time.Now().UnixNano()) / 1e9
	if now-entry.RecordedAt > mismatchTTLSecs {
		return nil
	}
	return &entry
}

// RecordMismatch persists a failed/rejected loop so the next loop rotates family.
func RecordMismatch(cwd string, files []string, lane, reason string) {
	if lane == "" {
		return
	}
	key := MismatchKey(cwd, files)
	mismatchMu.Lock()
	defer mismatchMu.Unlock()
	now := float64(time.Now().UnixNano()) / 1e9
	cleaned := make(map[string]MismatchEntry)
	for k, v := range readMismatchMap() {
		if now-v.RecordedAt <= mismatchTTLSecs {
			cleaned[k] = v
		}
	}
	cleaned[key] = MismatchEntry{
		Lane:       lane,
		Family:     ModelFamily(lane),
		Reason:     reason,
		RecordedAt: math.Round(now*100) / 100,
	}
	_ = fleet.AtomicWriteJSON(filepath.Join(fleet.HudDirPath(), "mismatch.json"), cleaned, true, true)
}

// ClearMismatch removes any stored mismatch entry after a verified green loop.
func ClearMismatch(cwd string, files []string) {
	key := MismatchKey(cwd, files)
	mismatchMu.Lock()
	defer mismatchMu.Unlock()
	mpath := filepath.Join(fleet.HudDirPath(), "mismatch.json")
	if _, err := os.Stat(mpath); err != nil {
		return
	}
	mapping := readMismatchMap()
	if _, ok := mapping[key]; ok {
		delete(mapping, key)
		_ = fleet.AtomicWriteJSON(mpath, mapping, true, true)
	}
}

// ModelRow describes one selectable CLI model backed by an internal quota pool.
type ModelRow struct {
	Spec, CLI, Pool, Window, Routing, Model string
	OptIn                                   bool
}

type catalogSeed struct{ cli, pool, model, route string }

var baseCatalog = []catalogSeed{
	{"agy", "agy-claude", "claude-sonnet-5-5-high", "auto (act/orient)"},
	{"agy", "agy-claude", fleet.DefaultAgyOpusModel, "opt-in (--model)"},
	{"devin", "devin-fusion", fleet.DefaultFusionModel, "auto (deep/act)"},
	{"devin", "devin-pro", "swe-2-high", "auto (act/orient/rev)"},
	{"devin", "devin-pro", "swe-2-max", "opt-in (--model)"},
	{"devin", "devin-fast", "swe-2-flash-high", "auto (obs/fast)"},
	{"muse", "muse", "muse-wonder-full-1.0", "auto (deep/act)"},
	{"muse", "muse", "muse-ember-1.0", "auto (orient)"},
	{"muse", "muse", "muse-spark-1.3", "auto (fast)"},
	{"mcode", "mcode", "minimax/MiniMax-M3", "auto (orient/rev/act)"},
	{"mcode", "mcode", "minimax/MiniMax-M3.1-Flash", "auto (obs/fast)"},
	{"grok", "grok", "grok-4.7", "auto (act/orient/rev)"},
	{"grok", "grok", "grok-4.7-build-fast", "auto (obs/fast)"},
	{"vibe", "mistral", "glm-5-3", "auto (act/orient/rev)"},
	{"vibe", "mistral", "mistral-large-4-0-3", "auto (fast)"},
}

// FormatSpec formats a canonical <cli>:<model> spec for display and CLI invocation.
func FormatSpec(pool, model string) string {
	cli := map[string]string{
		"agy-claude": "agy", "devin-fusion": "devin", "devin-pro": "devin", "devin-fast": "devin",
		"muse": "muse", "mcode": "mcode", "grok": "grok", "mistral": "vibe",
	}[pool]
	if cli == "" {
		cli = pool
	}
	m := strings.TrimSpace(strings.SplitN(model, "[", 2)[0])
	if pool == "mcode" && len(m) >= 8 && strings.EqualFold(m[:8], "minimax/") {
		m = m[8:]
	}
	if m == "" || m == "default" {
		return pool
	}
	return cli + ":" + m
}

// CatalogModels returns all known worker models backed by configured quota pools.
func CatalogModels(q *fleet.QuotaState, caps map[string]LaneCap) []ModelRow {
	musePayg := fleet.CachedMuseProbe(900).PayAsYouGo
	rows := make([]ModelRow, 0, len(baseCatalog))
	for _, s := range baseCatalog {
		var info *fleet.ProviderConfig
		if q != nil {
			info = q.Providers[s.pool]
		}
		if q != nil && len(q.Providers) > 0 && info == nil {
			continue
		}
		win, pinM := "", ""
		if info != nil {
			win = strings.Join(info.Windows, "+")
			if info.Model != nil {
				pinM = *info.Model
			}
		}
		route, optIn := s.route, strings.HasPrefix(s.route, "opt-in")
		if s.pool == "agy-claude" && pinM != "" && strings.EqualFold(pinM, s.model) {
			route, optIn = "pinned default", false
		} else if s.pool == "agy-claude" && pinM != "" {
			route, optIn = "opt-in (--model)", true
		} else if s.pool == "agy-claude" && s.model == fleet.DefaultAgyOpusModel {
			route, optIn = "auto (deep)", false
		} else if s.pool == "devin-pro" && s.model == "swe-2-max" &&
			(strings.EqualFold(pinM, "swe-2-max") || strings.EqualFold(caps["devin-pro"].EffectiveModel, "swe-2-max")) {
			route, optIn = "pinned default", false
		} else if s.pool == "muse" && musePayg {
			route, optIn = "opt-in (PAYG)", true
		}
		rows = append(rows, ModelRow{
			Spec: FormatSpec(s.pool, s.model), CLI: s.cli, Pool: s.pool,
			Window: win, Routing: route, Model: s.model, OptIn: optIn,
		})
	}
	return rows
}

// StageChainSummary returns the top-3 active quota-aware <cli>:<model> chain for a stage.
func StageChainSummary(stage string, q *fleet.QuotaState, caps map[string]LaneCap) string {
	comp := map[string]string{"observe": "fast", "orient": "deep", "act": "act", "verify": "review"}[stage]
	order, _ := BuildCandidateOrder("auto", comp, q, nil, nil)
	picks := make([]string, 0, 3)
	for _, p := range order {
		if q != nil && q.Providers[p] != nil && q.Providers[p].Status == "active" {
			picks = append(picks, p)
			if len(picks) == 3 {
				break
			}
		}
	}
	if len(picks) == 0 && len(order) > 0 {
		picks = order[:min(3, len(order))]
	}
	specs := make([]string, 0, len(picks))
	for _, p := range picks {
		mod := ""
		if q != nil && q.Providers[p] != nil && q.Providers[p].Model != nil {
			mod = *q.Providers[p].Model
		}
		if mod == "" {
			mod, _ = ResolveRegimeSpec(p, comp, caps[p])
		}
		if mod == "" {
			mod = caps[p].EffectiveModel
		}
		specs = append(specs, FormatSpec(p, mod))
	}
	return strings.Join(specs, " -> ")
}
