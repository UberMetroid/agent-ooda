package fleet

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func setupFleetEnv(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	qPath := filepath.Join(dir, "quota.json")
	hudDir := filepath.Join(dir, "hud")
	t.Setenv("AGENT_QUOTA_FILE", qPath)
	t.Setenv("ROUTER_HUD_DIR", hudDir)
	return qPath, hudDir
}

func TestLanesAndGeminiGuard(t *testing.T) {
	lanes, err := ParseLanes("mcode, flash, vibe, mcode", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"mcode", "devin-fast", "mistral"}
	if len(lanes) != len(want) || lanes[0] != want[0] || lanes[1] != want[1] || lanes[2] != want[2] {
		t.Fatalf("got lanes %v, want %v", lanes, want)
	}
	if _, err := ParseLanes("auto,mcode", true); err == nil {
		t.Fatal("expected error when mixing auto with explicit lanes")
	}
	for _, bad := range []string{"gemini-4-carbon-high", "Orchestrator (Carbon)", "gemini-3.8-flash-low"} {
		if err := CheckModelGuard(bad); err == nil {
			t.Fatalf("expected Gemini guardrail error for %q", bad)
		} else {
			var ee *ExitError
			if !errors.As(err, &ee) || ee.Code != 2 {
				t.Fatalf("expected ExitError(2) for %q, got %v", bad, err)
			}
		}
	}
	if m, err := NormalizeModel("agy-opus", "sonnet"); err != nil || m != "claude-sonnet-5-5-high" {
		t.Fatalf("got %q (%v)", m, err)
	}
}

func TestResetTSAndMigrate(t *testing.T) {
	future := ExtractResetTS("HTTP 429 Too Many Requests: resets at 2099-01-02T03:04:05Z")
	if future == nil {
		t.Fatal("expected parsed future reset timestamp")
	}
	if dt, ok := ParseISOUTC(*future); !ok || dt.Year() != 2099 {
		t.Fatalf("unexpected parsed UTC timestamp: %v (%v)", dt, ok)
	}
	if ExtractResetTS("429 quota exceeded, resets at 2001-01-01T00:00:00Z") != nil {
		t.Fatal("expected past reset timestamp to be ignored")
	}
	wake := FormatLocalISO(time.Now().Add(90 * time.Minute))
	q := &QuotaState{
		Version:              5,
		DefaultFallbackChain: []string{"cursor", "agy-opus", "muse"},
		Providers: map[string]*ProviderConfig{
			"cursor": {CLI: "cursor"},
			"mcode":  {CLI: "mcode", Tier: "unlimited"},
			"muse": {
				CLI: "muse", Tier: "capped", Status: "cooldown",
				CooldownUntil: &wake, CycleHours: Ptr(5.0),
			},
		},
	}
	if !MigrateQuota(q) {
		t.Fatal("expected MigrateQuota to report changes")
	}
	if q.Version != QuotaSchemaVersion || q.Providers["cursor"] != nil {
		t.Fatalf("unexpected migration result: version=%d", q.Version)
	}
	if q.Providers["mcode"].Tier != "high-cap" || q.Providers["mcode"].MaxParallel == nil {
		t.Fatalf("mcode upgrade failed: %+v", q.Providers["mcode"])
	}
	if q.Providers["muse"].Status != "cooldown" || q.Providers["muse"].CooldownUntil == nil {
		t.Fatalf("expected active future cooldown preserved: %+v", q.Providers["muse"])
	}
}

func TestUsageBudgetsAndLock(t *testing.T) {
	qPath, _ := setupFleetEnv(t)
	if _, _, err := ConfigureCycle(CyclePatch{Provider: "grok", TokenBudget: Ptr(50), QuotaPath: qPath}); err != nil {
		t.Fatalf("ConfigureCycle failed: %v", err)
	}
	_, _, err := ConfigureCycle(CyclePatch{
		Provider: "agy-opus", Model: Ptr("gemini-4-argon-low"), QuotaPath: qPath,
	})
	if err == nil {
		t.Fatal("expected ConfigureCycle to block gemini model")
	}
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = RecordUsage("devin-fast", "prompt", "out", &ExportUsage{Tokens: Ptr(10)}, qPath)
		}()
	}
	wg.Wait()
	q, err := LoadQuota(qPath)
	if err != nil {
		t.Fatalf("LoadQuota failed: %v", err)
	}
	if q.Providers["devin-fast"].CycleRuns != 6 || q.Providers["devin-fast"].CycleTokens != 60 {
		t.Fatalf("lost concurrent usage updates: runs=%d tokens=%d",
			q.Providers["devin-fast"].CycleRuns, q.Providers["devin-fast"].CycleTokens)
	}
	entry, err := RecordUsage("grok", "big prompt", "big out", &ExportUsage{Tokens: Ptr(55)}, qPath)
	if err != nil {
		t.Fatalf("RecordUsage failed: %v", err)
	}
	if entry.Status != "cooldown" {
		t.Fatalf("expected grok to auto-cooldown on token budget, got %q", entry.Status)
	}
}

func TestParallelLeasesAndTimeout(t *testing.T) {
	setupFleetEnv(t)
	if sec, err := ParseTimeout("10m"); err != nil || sec != 600 {
		t.Fatalf("got timeout %d (%v)", sec, err)
	}
	if sec, err := ParseTimeout("0.5s"); err != nil || sec != 1 {
		t.Fatalf("got sub-second timeout %d (%v)", sec, err)
	}
	if _, err := ParseTimeout("0s"); err == nil {
		t.Fatal("expected refusal for 0s timeout")
	}
	var held []*os.File
	for i := 0; i < 4; i++ {
		fh, timedOut, err := AcquireParallelLease("devin-fast", 4, false, 0)
		if err != nil || timedOut || fh == nil {
			t.Fatalf("lease %d failed: fh=%v timedOut=%v err=%v", i, fh, timedOut, err)
		}
		held = append(held, fh)
	}
	if CountActiveLeases("devin-fast", 4) != 4 {
		t.Fatalf("expected 4 active leases, got %d", CountActiveLeases("devin-fast", 4))
	}
	if extra, _, err := AcquireParallelLease("devin-fast", 4, false, 0); err != nil || extra != nil {
		t.Fatalf("expected nil lease when 4/4 busy, got %v (%v)", extra, err)
	}
	ReleaseParallelLease(held[0])
	fifth, _, err := AcquireParallelLease("devin-fast", 4, false, 0)
	if err != nil || fifth == nil {
		t.Fatalf("expected lease after releasing slot 0: %v", err)
	}
	ReleaseParallelLease(fifth)
	for _, fh := range held[1:] {
		ReleaseParallelLease(fh)
	}
	if CountActiveLeases("devin-fast", 4) != 0 {
		t.Fatalf("expected 0 leases after cleanup")
	}
}
