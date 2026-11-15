// Package fleet manages provider quotas, aliases, paths, and slot leases.
package fleet

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// ExitError carries an explicit process exit code for CLI error returns.
type ExitError struct {
	Code int
	Msg  string
}

func (e *ExitError) Error() string {
	return e.Msg
}

// UsageErr builds a code-2 usage/validation ExitError.
func UsageErr(format string, args ...any) error {
	return &ExitError{Code: 2, Msg: fmt.Sprintf(format, args...)}
}

const (
	QuotaSchemaVersion  = 7
	CornerWindowSecs    = 5400
	SoonWakeSecs        = 2700
	DevinFastMaxPar     = 4
	McodeMaxPar         = 4
	DefaultJobs         = 4
	MaxJobs             = 8
	DefaultTimeout      = 480
	ReviewTimeout       = 240
	DefaultAgyOpusModel = "claude-opus-5-5-high"
	DefaultFusionModel  = "fusion-claude-opus-5-5-high-sidekick-swe-2-medium"
	ReconPrefix         = "READ-ONLY RECONNAISSANCE: Do NOT modify, create, or delete any files. " +
		"Answer concisely with exact file paths, line numbers, and findings:\n\n"
)

var (
	// GeminiModelRe blocks Gemini/Orchestrator models on worker lanes.
	GeminiModelRe = regexp.MustCompile(`(?i)(?:gemini|orchestrator)`)
	SafeIDRe      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	DurationRe    = regexp.MustCompile(`(?i)^(\d+(?:\.\d+)?)\s*([smhd]?)$`)
	ResetTSRe     = regexp.MustCompile(
		`(?i)(?:resets?\s+at|try\s+again\s+at|available\s+at|until)\s+` +
			`(\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:?\d{2}))`,
	)
	VerdictRe = regexp.MustCompile(
		`(?i)^\s*(?:\*\*|__)?VERDICT(?:\*\*|__)?\s*:\s*(?:\*\*|__)?\s*(ACCEPT|REJECT)\b`,
	)
)

var (
	CoreHiddenSkills = map[string]bool{
		"router": true, "agent-router": true, "ooda": true,
		"ooda:ooda": true, "ooda:router": true,
		"agy-customizations": true, "antigravity-guide": true,
		"antigravity_guide": true, "run-workflow": true,
		"swarm": true, "swarm_monitor": true,
	}
	DefaultPool = []string{
		"agy-opus", "muse", "grok", "mistral",
		"devin-fusion", "devin-fast", "devin-pro", "mcode",
	}
	StripePool = []string{
		"agy-opus", "muse", "grok", "mistral",
		"devin-fast", "devin-pro", "mcode", "devin-fusion",
	}
	FastOrder = []string{"devin-fast", "mcode", "devin-pro", "muse"}
	DeepOrder = []string{
		"agy-opus", "muse", "grok", "mistral",
		"devin-fusion", "devin-pro", "devin-fast", "mcode",
	}
	ReviewOrder = []string{
		"mcode", "devin-fast", "devin-pro", "grok", "mistral", "muse", "agy-opus",
	}
	ProviderOrder = []string{
		"agy-opus", "devin-fusion", "devin-pro", "devin-fast",
		"muse", "mcode", "grok", "mistral",
	}
	ModelFamilies = map[string]string{
		"agy-opus": "claude-opus", "devin-fusion": "claude-opus",
		"devin-pro": "swe-2", "devin-fast": "swe-2",
		"muse": "muse", "mcode": "minimax",
		"grok": "grok", "mistral": "glm",
	}
	FastComplexities = map[string]bool{
		"fast": true, "mechanical": true, "simple": true, "flash": true,
		"tests": true, "test": true, "docs": true, "doc": true, "recon": true,
	}
	DeepComplexities = map[string]bool{
		"deep": true, "architecture": true, "arch": true,
		"reasoning": true, "audit": true, "ember": true,
	}
	RegimeTimeouts = map[string]int{
		"observe": 180, "fast": 300, "act": 480,
		"heavy": 600, "deep": 900, "verify": 240,
	}
)

// ProviderSpec captures static baseline defaults for a fleet lane.
type ProviderSpec struct {
	CLI         string
	Tier        string
	Role        string
	CycleHours  *float64
	MaxParallel *int
}

// ProviderConfig mirrors one provider entry in quota.json.
type ProviderConfig struct {
	CLI              string   `json:"cli"`
	Model            *string  `json:"model"`
	Effort           *string  `json:"effort"`
	Tier             string   `json:"tier"`
	Role             string   `json:"role"`
	Status           string   `json:"status"`
	CooldownUntil    *string  `json:"cooldown_until"`
	Reason           *string  `json:"reason"`
	ManualDisable    bool     `json:"manual_disable"`
	MaxParallel      *int     `json:"max_parallel"`
	CycleHours       *float64 `json:"cycle_hours"`
	CycleStartedAt   *string  `json:"cycle_started_at"`
	NextResetAt      *string  `json:"next_reset_at"`
	CycleRuns        int      `json:"cycle_runs"`
	CycleTokens      int      `json:"cycle_tokens"`
	CycleTokensExact bool     `json:"cycle_tokens_exact"`
	CycleCostUSD     float64  `json:"cycle_cost_usd"`
	CycleTokenBudget *int     `json:"cycle_token_budget"`
	CycleCostBudget  *float64 `json:"cycle_cost_budget_usd"`
}

// QuotaState mirrors the top-level quota.json state schema.
type QuotaState struct {
	Version              int                        `json:"version"`
	UpdatedAt            *string                    `json:"updated_at"`
	DrainCappedFirst     bool                       `json:"drain_capped_first"`
	ParallelStripePool   []string                   `json:"parallel_stripe_pool"`
	DefaultFallbackChain []string                   `json:"default_fallback_chain"`
	Providers            map[string]*ProviderConfig `json:"providers"`
}

// ReviewModelEnv returns the env-configured auto-review model or cli-default.
func ReviewModelEnv() string {
	if v := os.Getenv("AGENT_REVIEW_MODEL"); v != "" {
		return v
	}
	if v := os.Getenv("OODA_REVIEW_MODEL"); v != "" {
		return v
	}
	return "cli-default"
}

// CLIFallbackPaths returns default install locations for headless agent CLIs.
func CLIFallbackPaths(cliName string) []string {
	home, _ := os.UserHomeDir()
	localBin := filepath.Join(home, ".local", "bin")
	if cliName == "agy" || cliName == "muse" {
		return []string{filepath.Join(localBin, cliName)}
	}
	table := map[string][]string{
		"devin": {
			filepath.Join(localBin, "devin"),
			filepath.Join(home, ".local", "share", "devin", "cli", "_versions", "current", "bin", "devin"),
		},
		"mcode": {filepath.Join(home, ".minimax-code", "bin", "mcode"), filepath.Join(localBin, "mcode")},
		"grok":  {filepath.Join(localBin, "grok"), filepath.Join(home, ".grok", "bin", "grok")},
		"vibe": {
			filepath.Join(localBin, "vibe"),
			filepath.Join(home, ".local", "share", "uv", "tools", "mistral-vibe", "bin", "vibe"),
		},
	}
	return table[cliName]
}

// Ptr returns a heap-allocated pointer to v for optional JSON fields.
func Ptr[T any](v T) *T {
	return &v
}

// ProviderSpecs returns static defaults keyed by canonical provider name.
func ProviderSpecs() map[string]ProviderSpec {
	return map[string]ProviderSpec{
		"agy-opus": {
			CLI: "agy", Tier: "capped", CycleHours: Ptr(5.0),
			Role: "Antigravity agent pool (separate 5h quota pool; uses CLI default unless overridden)",
		},
		"devin-fusion": {
			CLI: "devin", Tier: "credits",
			Role: "Devin Fusion pool (paid credits; uses CLI default unless overridden)",
		},
		"devin-pro": {
			CLI: "devin", Tier: "unlimited", CycleHours: Ptr(1.0),
			Role: "Heavy multi-file implementation, complex bugs, refactors (Devin Pro pool)",
		},
		"devin-fast": {
			CLI: "devin", Tier: "unlimited", CycleHours: Ptr(1.0), MaxParallel: Ptr(DevinFastMaxPar),
			Role: "Fast mechanical sweeps, tests, boilerplate, read-only recon (max 4 parallel)",
		},
		"muse": {
			CLI: "muse", Tier: "capped", CycleHours: Ptr(5.0),
			Role: "Deep reasoning, architecture, second-opinion implementation (Muse pool)",
		},
		"mcode": {
			CLI: "mcode", Tier: "high-cap", CycleHours: Ptr(5.0), MaxParallel: Ptr(McodeMaxPar),
			Role: "Primary code review + fast parallel coding & recon (5h cap, max 4 parallel)",
		},
		"grok": {
			CLI: "grok", Tier: "capped", CycleHours: Ptr(5.0),
			Role: "xAI Grok Build coding agent (Grok pool)",
		},
		"mistral": {
			CLI: "vibe", Tier: "capped", CycleHours: Ptr(5.0),
			Role: "Mistral Vibe coding agent (Mistral pool)",
		},
	}
}

// DefaultProviders builds fresh active ProviderConfig entries.
func DefaultProviders() map[string]*ProviderConfig {
	specs := ProviderSpecs()
	out := make(map[string]*ProviderConfig, len(ProviderOrder))
	for _, name := range ProviderOrder {
		sp := specs[name]
		out[name] = &ProviderConfig{
			CLI:         sp.CLI,
			Tier:        sp.Tier,
			Role:        sp.Role,
			Status:      "active",
			MaxParallel: sp.MaxParallel,
			CycleHours:  sp.CycleHours,
		}
	}
	return out
}

// DefaultQuotaData returns baseline schema-v7 quota state.
func DefaultQuotaData() *QuotaState {
	now := time.Now().Format(time.RFC3339)
	return &QuotaState{
		Version:              QuotaSchemaVersion,
		UpdatedAt:            &now,
		DrainCappedFirst:     true,
		ParallelStripePool:   append([]string(nil), StripePool...),
		DefaultFallbackChain: append([]string(nil), DefaultPool...),
		Providers:            DefaultProviders(),
	}
}
