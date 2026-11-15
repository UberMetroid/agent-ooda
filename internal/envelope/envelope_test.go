package envelope

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/UberMetroid/agent-ooda/internal/fleet"
)

func testLane(tier, status string, resetAt, cool *string) *fleet.ProviderConfig {
	return &fleet.ProviderConfig{
		CLI: tier, Tier: tier, Status: status, NextResetAt: resetAt, CooldownUntil: cool,
	}
}

func TestEMRegimesAndOrder(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	ts60 := now.Add(60 * time.Minute).Format(time.RFC3339)
	qCorner := &fleet.QuotaState{Providers: map[string]*fleet.ProviderConfig{
		"agy-opus": testLane("capped", "active", &ts60, nil),
		"muse":     testLane("capped", "active", nil, nil),
	}}
	em := ComputeEMEnvelope(qCorner, now)
	if len(em.CornerLanes) != 1 || em.CornerLanes[0] != "agy-opus" {
		t.Fatalf("expected agy-opus corner, got %v", em.CornerLanes)
	}
	if em.Regimes["agy-opus"] != "CORNER" || em.Regimes["muse"] != "CRUISE" {
		t.Fatalf("unexpected regimes: %v", em.Regimes)
	}
	qConserve := &fleet.QuotaState{Providers: map[string]*fleet.ProviderConfig{
		"grok": testLane("capped", "active", nil, nil),
		"muse": testLane("capped", "cooldown", nil, &ts60),
	}}
	if !ComputeEMEnvelope(qConserve, now).ConserveCapped {
		t.Fatal("expected ConserveCapped=true")
	}
	dq := fleet.DefaultQuotaData()
	rev, err := BuildCandidateOrder("auto", "review", dq, nil, map[string]bool{"swe-2": true})
	if err != nil {
		t.Fatalf("BuildCandidateOrder failed: %v", err)
	}
	if len(rev) == 0 || rev[0] != "mcode" {
		t.Fatalf("expected mcode first in review order, got %v", rev)
	}
	t.Setenv("ROUTER_DEPTH", "0")
	t.Setenv("ROUTER_HUD_DIR", t.TempDir())
	sel, err := SelectProvider(SelectReq{
		RequestedLane: "auto", Complexity: "review", Quota: dq,
		Exclude: map[string]bool{"mcode": true}, AvoidFamilies: map[string]bool{"swe-2": true},
		ResolveCLI: func(string) string { return "/opt/fake/cli" },
	})
	if err != nil || sel.Lane != "grok" {
		t.Fatalf("expected grok selection, got %+v (%v)", sel, err)
	}
}

func TestProbeConfigsAllCLIs(t *testing.T) {
	home := t.TempDir()
	mustWrite := func(rel, body string) {
		p := filepath.Join(home, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite(".vibe/config.toml", `active_model = "glm-5-3"`+"\n"+`models = [{alias = "glm-5-3", thinking = "high"}]`)
	mustWrite(".config/muse/settings.json", `{"model":"muse-spark-1.3","model_reasoning_efforts":{"muse-spark-1.3":"high"}}`)
	mustWrite(".minimax/config.yaml", "defaultModel: minimax/MiniMax-M3.1-Flash\ndefaultModelThinking:\n  effort: max\n")
	mustWrite(".grok/config.toml", `default = "grok-4.7"`+"\n"+`default_reasoning_effort = "high"`)
	devinCfg := `{"agent":{"preferred_family_models":{"fusion":"f-1",` +
		`"swe-2":"swe-2-max","swe-2-flash":"swe-2-flash-max"}}}`
	mustWrite(".config/devin/config.json", devinCfg)
	mustWrite(".gemini/antigravity-cli/settings.json", `{"model":"Orchestrator (Carbon)"}`)

	if v := ProbeVibeConfig(home); v.ActiveModel != "glm-5-3" || v.ActiveEffort != "high" {
		t.Fatalf("unexpected vibe probe: %+v", v)
	}
	if m := ProbeMuseConfig(home); m.ActiveModel != "muse-spark-1.3" || m.ActiveEffort != "high" {
		t.Fatalf("unexpected muse probe: %+v", m)
	}
	if mm := ProbeMinimaxConfig(home); mm.ActiveEffort != "max" {
		t.Fatalf("unexpected mcode probe: %+v", mm)
	}
	if g := ProbeGrokConfig(home); g.ActiveModel != "grok-4.7" || g.ActiveEffort != "high" {
		t.Fatalf("unexpected grok probe: %+v", g)
	}
	if d := ProbeDevinConfig(home); d.LaneModels["devin-pro"] != "swe-2-max" {
		t.Fatalf("unexpected devin probe: %+v", d)
	}
	if a := ProbeAgySettings(home); !a.GeminiBlocked {
		t.Fatalf("expected gemini blocked for Orchestrator (Carbon)")
	}
}

func TestDiscoveryCacheAndSpec(t *testing.T) {
	home := t.TempDir()
	discFile := filepath.Join(home, "discovery.json")
	t.Setenv("OODA_DISCOVERY_FILE", discFile)
	t.Setenv("OODA_NO_AUTO_PROBE", "1")
	t.Setenv("ROUTER_HUD_DIR", filepath.Join(home, "hud"))
	agySettings := filepath.Join(home, ".gemini", "antigravity-cli", "settings.json")
	if err := os.MkdirAll(filepath.Dir(agySettings), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(agySettings, []byte(`{"model":"Orchestrator (Carbon)"}`), 0o600)
	_ = fleet.AtomicWriteJSON(discFile, DiscoveryCache{
		Version: 1, TS: float64(time.Now().Unix()),
		CLIModels: map[string][]string{"agy": {"claude-opus-5-5-medium", "claude-opus-5-5-high"}},
	}, false, false)

	dq := fleet.DefaultQuotaData()
	spec, err := EffectiveLaneSpec("agy-opus", dq.Providers["agy-opus"], "", "", "", home)
	if err != nil || spec.ArgvModel != "claude-opus-5-5-high" {
		t.Fatalf("expected non-Gemini claude-opus-5-5-high, got %+v (%v)", spec, err)
	}
	if _, err := EffectiveLaneSpec("agy-opus", dq.Providers["agy-opus"], "gemini-4-carbon-high", "", "", home); err == nil {
		t.Fatal("expected Gemini guardrail refusal on explicit model")
	}
	RecordMismatch(home, []string{"a.go"}, "mcode", "failed")
	if mm := LoadRecentMismatch(home, []string{"a.go"}); mm == nil || mm.Family != "minimax" {
		t.Fatalf("expected recent minimax mismatch, got %+v", mm)
	}
	ClearMismatch(home, []string{"a.go"})
	if LoadRecentMismatch(home, []string{"a.go"}) != nil {
		t.Fatal("expected cleared mismatch")
	}
}
