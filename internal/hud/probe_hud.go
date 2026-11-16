package hud

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/UberMetroid/agent-ooda/internal/dispatch"
	"github.com/UberMetroid/agent-ooda/internal/fleet"
)

const sessionTTLSecs = 43200.0

var fastChain = []string{"devin-fast", "mcode", "devin-pro", "muse"}

// FleetSummary summarizes ready lanes and next wake countdown without writing disk.
type FleetSummary struct {
	Active   int
	Total    int
	WakeCD   string
	CodeLane string
	CodeEff  string
	FastLane string
	FastEff  string
}

// PidAlive returns true if pid belongs to a living process.
func PidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// LoadActiveWorkers loads active worker markers from hudBase/active and prunes stale PIDs.
func LoadActiveWorkers(hudBase string) []dispatch.WorkerMarker {
	dir := filepath.Join(hudBase, "active")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []dispatch.WorkerMarker
	for _, de := range entries {
		if de.IsDir() || !strings.HasSuffix(de.Name(), ".json") {
			continue
		}
		full := filepath.Join(dir, de.Name())
		raw, rerr := os.ReadFile(full)
		if rerr != nil {
			continue
		}
		var m dispatch.WorkerMarker
		if json.Unmarshal(raw, &m) != nil {
			_ = os.Remove(full)
			continue
		}
		if !PidAlive(m.PID) {
			_ = os.Remove(full)
			continue
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].StartedAt < out[j].StartedAt
	})
	return out
}

func extractStdinSkills(data map[string]any) []string {
	raw, ok := data["skills"].([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, item := range raw {
		switch x := item.(type) {
		case string:
			if s := strings.TrimSpace(x); s != "" {
				out = append(out, s)
			}
		case map[string]any:
			if s, _ := x["name"].(string); strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
	}
	return out
}

func readSessionSkills(hudBase string, data map[string]any, now float64) []string {
	convID := ""
	for _, k := range []string{"conversation_id", "conversationId"} {
		if s, _ := data[k].(string); strings.TrimSpace(s) != "" {
			convID = strings.TrimSpace(s)
			break
		}
	}
	if convID == "" {
		raw, err := os.ReadFile(filepath.Join(hudBase, "current_session.json"))
		if err == nil {
			var cur struct {
				ConversationID string  `json:"conversation_id"`
				UpdatedAt      float64 `json:"updated_at"`
			}
			if json.Unmarshal(raw, &cur) == nil && (now-cur.UpdatedAt) <= sessionTTLSecs {
				convID = strings.TrimSpace(cur.ConversationID)
			}
		}
	}
	if convID == "" {
		return nil
	}
	raw, err := os.ReadFile(filepath.Join(hudBase, "sessions", fleet.SafeID(convID)+".json"))
	if err != nil {
		return nil
	}
	var sess struct {
		Skills []string `json:"skills"`
	}
	if json.Unmarshal(raw, &sess) != nil {
		return nil
	}
	return sess.Skills
}

// CollectSkills merges stdin, session, and live-worker skills cleanly.
func CollectSkills(
	hudBase string, data map[string]any, workers []dispatch.WorkerMarker, now float64,
) []string {
	groups := [][]string{extractStdinSkills(data), readSessionSkills(hudBase, data, now)}
	for _, w := range workers {
		groups = append(groups, w.Skills)
	}
	seen := make(map[string]bool)
	var merged []string
	for _, g := range groups {
		for _, s := range g {
			name := strings.TrimSpace(s)
			if name == "agent-router" {
				name = "router"
			}
			if name != "" && !IgnoredSkills[name] && !seen[name] {
				seen[name] = true
				merged = append(merged, name)
			}
		}
	}
	return merged
}

func readFleetProviders() (*fleet.QuotaState, bool) {
	qPath := fleet.QuotaFilePath("")
	raw, err := os.ReadFile(qPath)
	if err != nil {
		return nil, false
	}
	var q fleet.QuotaState
	if json.Unmarshal(raw, &q) != nil || len(q.Providers) == 0 {
		return nil, false
	}
	return &q, true
}

// LoadFleetSummary reads quota.json read-only to compute active lane counts and wake timer.
func LoadFleetSummary(nowUTC time.Time) FleetSummary {
	def := FleetSummary{
		CodeLane: "agy-opus", CodeEff: "high", FastLane: "devin-fast", FastEff: "max",
	}
	q, ok := readFleetProviders()
	if !ok {
		return def
	}
	active := make(map[string]bool, len(q.Providers))
	var soonest *float64
	for name, info := range q.Providers {
		if info == nil {
			continue
		}
		if info.Status == "active" {
			active[name] = true
			continue
		}
		if info.Status != "cooldown" {
			continue
		}
		var ts string
		if info.CooldownUntil != nil && *info.CooldownUntil != "" {
			ts = *info.CooldownUntil
		} else if info.NextResetAt != nil {
			ts = *info.NextResetAt
		}
		dt, dtOK := fleet.ParseISOUTC(ts)
		if !dtOK {
			continue
		}
		if !dt.After(nowUTC) && !info.ManualDisable {
			active[name] = true
		} else if rem := dt.Sub(nowUTC).Seconds(); rem > 0 && (soonest == nil || rem < *soonest) {
			soonest = fleet.Ptr(rem)
		}
	}
	if active["muse"] && fleet.CachedMuseProbe(0).PayAsYouGo {
		delete(active, "muse")
	}
	wakeCD := ""
	if soonest != nil {
		wakeCD = FormatCD(*soonest)
	}
	return FleetSummary{
		Active: len(active), Total: len(q.Providers), WakeCD: wakeCD,
		CodeLane: firstActive(q.DefaultFallbackChain, active, "none"),
		FastLane: firstActive(fastChain, active, "none"),
	}
}

func firstActive(chain []string, active map[string]bool, fallback string) string {
	for _, c := range chain {
		if active[c] {
			return c
		}
	}
	return fallback
}
