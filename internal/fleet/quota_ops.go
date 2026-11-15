package fleet

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// ExportUsage captures exact provider-reported tokens and USD cost.
type ExportUsage struct {
	Tokens  *int
	CostUSD *float64
}

// CyclePatch specifies updates for ConfigureCycle.
type CyclePatch struct {
	Provider    string
	Hours       *float64
	TokenBudget *int
	CostBudget  *float64
	ResetNow    bool
	Effort      *string
	Model       *string
	MaxParallel *int
	QuotaPath   string
}

func mutateProviderLocked(
	qPath, rawName string, createIfMissing bool, fn func(*ProviderConfig) error,
) (string, *ProviderConfig, error) {
	canon, err := NormalizeLane(rawName, false)
	if err != nil {
		return "", nil, err
	}
	if createIfMissing {
		if _, lerr := LoadQuota(qPath); lerr != nil {
			return "", nil, lerr
		}
	}
	var updated ProviderConfig
	err = WithQuotaLock(qPath, func(path string) error {
		q, rerr := readAndPrepLocked(path)
		if rerr != nil {
			return rerr
		}
		entry, ok := q.Providers[canon]
		if !ok || entry == nil {
			return UsageErr("error: provider %q not defined in %s", canon, path)
		}
		if ferr := fn(entry); ferr != nil {
			return ferr
		}
		if werr := SaveQuotaLocked(q, path); werr != nil {
			return werr
		}
		updated = *entry
		return nil
	})
	if err != nil {
		return "", nil, err
	}
	return canon, &updated, nil
}

func computeQuotaWake(entry *ProviderConfig, prefix string, outText string, now time.Time) {
	if srv := ExtractResetTS(outText); srv != nil {
		entry.CooldownUntil, entry.NextResetAt = srv, srv
		entry.Reason = Ptr(fmt.Sprintf("%s (resets %s)", prefix, *srv))
		return
	}
	if entry.NextResetAt != nil {
		if dt, ok := ParseISOUTC(*entry.NextResetAt); ok && dt.After(now.UTC()) {
			entry.CooldownUntil = Ptr(*entry.NextResetAt)
			entry.Reason = Ptr(prefix + " (auto-wakes at cycle reset)")
			return
		}
	}
	if entry.CycleHours != nil {
		wake := FormatLocalISO(now.Add(time.Duration(*entry.CycleHours * float64(time.Hour))))
		entry.CooldownUntil, entry.NextResetAt = &wake, &wake
		if entry.CycleStartedAt == nil || *entry.CycleStartedAt == "" {
			entry.CycleStartedAt = Ptr(FormatLocalISO(now))
		}
		entry.Reason = Ptr(fmt.Sprintf("%s (auto-wakes in %gh)", prefix, *entry.CycleHours))
		return
	}
	entry.CooldownUntil = nil
	entry.Reason = Ptr("Credits exhausted — waiting for top-up")
}

// SetProviderStatus marks a provider active or in cooldown inside quota.json.
func SetProviderStatus(
	name, status, reason string, hours *float64, autoWake bool, qPath string,
) (string, *ProviderConfig, error) {
	return mutateProviderLocked(qPath, name, true, func(entry *ProviderConfig) error {
		entry.Status = status
		now := time.Now()
		if status == "active" {
			entry.CooldownUntil, entry.Reason, entry.ManualDisable = nil, nil, false
			return nil
		}
		entry.ManualDisable = !autoWake
		r := reason
		if r == "" {
			r = "Manually set to cooldown"
		}
		entry.Reason = &r
		if hours != nil {
			entry.CooldownUntil = Ptr(FormatLocalISO(now.Add(time.Duration(*hours * float64(time.Hour)))))
			return nil
		}
		if !autoWake || entry.CycleHours == nil {
			entry.CooldownUntil = nil
			return nil
		}
		if entry.NextResetAt != nil {
			if dt, ok := ParseISOUTC(*entry.NextResetAt); ok && dt.After(now.UTC()) {
				entry.CooldownUntil = Ptr(*entry.NextResetAt)
				return nil
			}
		}
		until := FormatLocalISO(now.Add(time.Duration(*entry.CycleHours * float64(time.Hour))))
		entry.NextResetAt, entry.CooldownUntil = &until, &until
		if entry.CycleStartedAt == nil || *entry.CycleStartedAt == "" {
			entry.CycleStartedAt = Ptr(NowISO())
		}
		return nil
	})
}

// MarkProviderCooldown puts a provider into quota or auth cooldown.
func MarkProviderCooldown(
	name, prefix string, isQuota bool, outText, qPath string,
) (*ProviderConfig, error) {
	_, entry, err := mutateProviderLocked(qPath, name, false, func(entry *ProviderConfig) error {
		now := time.Now()
		entry.Status, entry.ManualDisable = "cooldown", false
		if isQuota {
			computeQuotaWake(entry, prefix, outText, now)
			return nil
		}
		entry.CooldownUntil = Ptr(FormatLocalISO(now.Add(time.Hour)))
		entry.Reason = Ptr(prefix + " (run login or --enable)")
		return nil
	})
	return entry, err
}

// RecordUsage accumulates cycle runs, tokens, USD cost, and auto-cooldown budgets.
func RecordUsage(
	name, prompt, output string, exp *ExportUsage, qPath string,
) (*ProviderConfig, error) {
	_, entry, err := mutateProviderLocked(qPath, name, false, func(entry *ProviderConfig) error {
		now := time.Now()
		if entry.CycleHours != nil && (entry.CycleStartedAt == nil || *entry.CycleStartedAt == "") {
			entry.CycleStartedAt = Ptr(FormatLocalISO(now))
			entry.NextResetAt = Ptr(FormatLocalISO(now.Add(time.Duration(*entry.CycleHours * float64(time.Hour)))))
		}
		entry.CycleRuns++
		if exp != nil && exp.Tokens != nil {
			entry.CycleTokens += *exp.Tokens
			entry.CycleTokensExact = entry.CycleRuns == 1 || entry.CycleTokensExact
		} else {
			entry.CycleTokens += max(1, (len(prompt)+len(output))/4)
			entry.CycleTokensExact = false
		}
		if exp != nil && exp.CostUSD != nil {
			entry.CycleCostUSD = math.Round((entry.CycleCostUSD+*exp.CostUSD)*1e6) / 1e6
		}
		var hit string
		if entry.CycleTokenBudget != nil && entry.CycleTokens >= *entry.CycleTokenBudget {
			hit = fmt.Sprintf("Cycle token budget (%d) reached", *entry.CycleTokenBudget)
		} else if entry.CycleCostBudget != nil && entry.CycleCostUSD >= *entry.CycleCostBudget {
			hit = fmt.Sprintf("Cycle cost budget ($%.2f) reached", *entry.CycleCostBudget)
		}
		if hit != "" && entry.Status == "active" {
			entry.Status, entry.ManualDisable, entry.Reason = "cooldown", false, &hit
			entry.CooldownUntil = entry.NextResetAt
		}
		return nil
	})
	return entry, err
}

func applyCycleHours(entry *ProviderConfig, hrs float64) {
	if hrs <= 0 {
		entry.CycleHours, entry.NextResetAt = nil, nil
		return
	}
	entry.CycleHours = Ptr(hrs)
	if entry.CycleStartedAt == nil {
		return
	}
	if started, ok := ParseISOUTC(*entry.CycleStartedAt); ok {
		wake := FormatLocalISO(started.Add(time.Duration(hrs * float64(time.Hour))))
		entry.NextResetAt = &wake
		if entry.Status == "cooldown" && !entry.ManualDisable {
			entry.CooldownUntil = &wake
		}
	}
}

// ConfigureCycle updates cycle schedule, budgets, defaults, or resets counters.
func ConfigureCycle(p CyclePatch) (string, *ProviderConfig, error) {
	canon, err := NormalizeLane(p.Provider, false)
	if err != nil {
		return "", nil, err
	}
	var normModel *string
	if p.Model != nil {
		m, merr := NormalizeModel(canon, *p.Model)
		if merr != nil {
			return "", nil, merr
		}
		if m != "" {
			normModel = &m
		}
	}
	return mutateProviderLocked(p.QuotaPath, canon, false, func(entry *ProviderConfig) error {
		if p.Hours != nil {
			applyCycleHours(entry, *p.Hours)
		}
		if p.TokenBudget != nil && *p.TokenBudget <= 0 {
			entry.CycleTokenBudget = nil
		} else if p.TokenBudget != nil {
			entry.CycleTokenBudget = Ptr(*p.TokenBudget)
		}
		if p.CostBudget != nil && *p.CostBudget <= 0 {
			entry.CycleCostBudget = nil
		} else if p.CostBudget != nil {
			entry.CycleCostBudget = Ptr(*p.CostBudget)
		}
		if p.MaxParallel != nil && *p.MaxParallel <= 0 {
			entry.MaxParallel = nil
		} else if p.MaxParallel != nil {
			entry.MaxParallel = Ptr(*p.MaxParallel)
		}
		if p.Effort != nil {
			entry.Effort = Ptr(strings.ToLower(strings.TrimSpace(*p.Effort)))
		}
		if p.Model != nil {
			entry.Model = normModel
		}
		if p.ResetNow {
			entry.CycleRuns, entry.CycleTokens, entry.CycleTokensExact, entry.CycleCostUSD = 0, 0, false, 0
			entry.CycleStartedAt, entry.NextResetAt, entry.Status = nil, nil, "active"
			entry.CooldownUntil, entry.Reason, entry.ManualDisable = nil, nil, false
		}
		return nil
	})
}
