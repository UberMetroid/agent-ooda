package fleet

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"
)

var quotaMu sync.Mutex

func migratePool(cur []string, def []string, oldVer int) ([]string, bool) {
	cleaned := make([]string, 0, len(cur))
	for _, p := range cur {
		if p == "agy-opus" {
			p = "agy-claude"
		}
		if p != "cursor" && !slices.Contains(cleaned, p) {
			cleaned = append(cleaned, p)
		}
	}
	if oldVer < 8 || len(cleaned) == 0 {
		return append([]string(nil), def...), true
	}
	if oldVer < 11 && !slices.Contains(cleaned, "devin-fusion") {
		return slices.Insert(cleaned, max(0, slices.Index(cleaned, "agy-claude")+1), "devin-fusion"), true
	}
	return cleaned, !slices.Equal(cur, cleaned)
}

func migrateProvider(name string, info *ProviderConfig, tmpl *ProviderConfig, oldVer int, legacyUpd time.Time) bool {
	changed := false
	if oldVer < 7 {
		if (name == "mcode" || name == "muse") && info.Tier == "unlimited" {
			info.Tier, changed = tmpl.Tier, true
		}
		if tmpl.MaxParallel != nil && info.MaxParallel == nil {
			info.MaxParallel, changed = Ptr(*tmpl.MaxParallel), true
		}
	}
	if oldVer < 8 {
		if len(tmpl.Windows) > 0 {
			info.Windows, changed = append([]string(nil), tmpl.Windows...), true
		}
		if tmpl.CycleHours != nil && (info.CycleHours == nil || *info.CycleHours == 5.0 || *info.CycleHours == 1.0) {
			info.CycleHours, changed = Ptr(*tmpl.CycleHours), true
		}
		if tmpl.OuterHours != nil {
			info.OuterHours, changed = Ptr(*tmpl.OuterHours), true
		}
		info.Role = tmpl.Role
		if (name == "grok" || name == "mistral") && info.Status == "active" && info.NextResetAt != nil {
			info.NextResetAt, info.CycleStartedAt, changed = nil, nil, true
			info.CycleRuns, info.CycleTokens, info.CycleTokensExact, info.CycleCostUSD = 0, 0, false, 0
		}
	}
	if info.Status == "cooldown" && info.CooldownUntil == nil && info.CycleHours != nil && !info.ManualDisable {
		wake := FormatLocalISO(legacyUpd.Add(time.Duration(min(*info.CycleHours, 5.0) * float64(time.Hour))))
		info.CooldownUntil, changed = &wake, true
		if *info.CycleHours <= 6.0 && (info.NextResetAt == nil || *info.NextResetAt == "") {
			info.NextResetAt = &wake
		}
		if info.CycleStartedAt == nil || *info.CycleStartedAt == "" {
			info.CycleStartedAt = Ptr(FormatLocalISO(legacyUpd))
		}
	}
	if name == "devin-fusion" && (oldVer < 10 || info.Tier == "credits") {
		info.Tier, info.Role, info.Windows = tmpl.Tier, tmpl.Role, append([]string(nil), tmpl.Windows...)
		info.CycleHours, info.OuterHours, info.MaxParallel = Ptr(*tmpl.CycleHours), Ptr(*tmpl.OuterHours), tmpl.MaxParallel
		if info.Model != nil && !strings.HasPrefix(*info.Model, "fusion-") {
			info.Model = nil
		}
		if info.Reason != nil && !info.ManualDisable && strings.Contains(*info.Reason, "Credits exhausted") {
			info.Status, info.CooldownUntil, info.Reason, info.ConsecCooldowns = "active", nil, nil, 0
		}
		changed = true
	}
	if name == "agy-claude" && oldVer < 11 {
		info.Role = tmpl.Role
		if info.Model != nil && (*info.Model == "claude-sonnet-5-5-high" || *info.Model == DefaultAgyOpusModel) {
			info.Model = nil
		}
		changed = true
	}
	if name == "mcode" && (oldVer < 12 || info.Tier == "unlimited" || info.Tier == "high-cap") {
		info.Tier, info.Role, changed = "capped", tmpl.Role, true
	}
	return changed
}

// MigrateQuota upgrades legacy quota state in-place to QuotaSchemaVersion.
func MigrateQuota(q *QuotaState) bool {
	changed, oldVer := false, max(1, q.Version)
	if oldVer < QuotaSchemaVersion {
		q.Version, changed = QuotaSchemaVersion, true
	}
	if q.Providers == nil {
		q.Providers, changed = make(map[string]*ProviderConfig), true
	}
	if _, hasCursor := q.Providers["cursor"]; hasCursor {
		delete(q.Providers, "cursor")
		changed = true
	}
	if old, ok := q.Providers["agy-opus"]; ok {
		if q.Providers["agy-claude"] == nil && old != nil {
			q.Providers["agy-claude"] = old
		}
		delete(q.Providers, "agy-opus")
		changed = true
	}
	var c1, c2 bool
	q.DefaultFallbackChain, c1 = migratePool(q.DefaultFallbackChain, DefaultPool, oldVer)
	q.ParallelStripePool, c2 = migratePool(q.ParallelStripePool, StripePool, oldVer)
	legacyUpd := time.Now().UTC()
	if q.UpdatedAt != nil {
		if dt, ok := ParseISOUTC(*q.UpdatedAt); ok && !dt.IsZero() {
			legacyUpd = dt
		}
	}
	for name, tmpl := range DefaultProviders() {
		if cur, ok := q.Providers[name]; !ok || cur == nil {
			cp := *tmpl
			q.Providers[name], changed = &cp, true
		} else if migrateProvider(name, cur, tmpl, oldVer, legacyUpd) {
			changed = true
		}
	}
	if q.Stages == nil {
		q.Stages, changed = make(map[string]StageRoute, len(StageNames)), true
	}
	for _, st := range StageNames {
		if r := q.Stages[st]; strings.Contains(r.Lane, "agy-opus") {
			r.Lane = strings.ReplaceAll(r.Lane, "agy-opus", "agy-claude")
			q.Stages[st], changed = r, true
		} else if r.Lane == "" {
			q.Stages[st], changed = StageRoute{Lane: "auto"}, true
		}
	}
	for _, info := range q.Providers {
		if info != nil && info.MaxParallel != nil && *info.MaxParallel <= 0 {
			info.MaxParallel, changed = nil, true
		} else if info != nil && info.MaxParallel != nil && *info.MaxParallel > MaxParallelCap {
			info.MaxParallel, changed = Ptr(MaxParallelCap), true
		}
	}
	return changed || c1 || c2
}

// ExpireAndReset clears elapsed cycle/outer counters and expired non-manual cooldowns.
func ExpireAndReset(q *QuotaState, nowUTC time.Time) bool {
	changed := false
	if nowUTC.IsZero() {
		nowUTC = time.Now().UTC()
	}
	for st, sr := range q.Stages {
		if sr.ExpiresAt != nil && *sr.ExpiresAt != "" {
			if dt, ok := ParseISOUTC(*sr.ExpiresAt); ok && !dt.After(nowUTC) {
				q.Stages[st], changed = StageRoute{Lane: "auto"}, true
			}
		}
	}
	for _, info := range q.Providers {
		if info == nil {
			continue
		}
		if info.OuterResetAt != nil {
			if dt, ok := ParseISOUTC(*info.OuterResetAt); ok && !dt.After(nowUTC) {
				info.OuterTokens, info.OuterStartedAt, info.OuterResetAt, info.ConsecCooldowns, changed = 0, nil, nil, 0, true
			}
		}
		if info.NextResetAt != nil {
			if dt, ok := ParseISOUTC(*info.NextResetAt); ok && !dt.After(nowUTC) {
				info.CycleRuns, info.CycleTokens, info.CycleTokensExact, info.CycleCostUSD = 0, 0, false, 0
				info.CycleStartedAt, info.NextResetAt, changed = nil, nil, true
				if info.Status == "cooldown" && !info.ManualDisable {
					info.Status, info.CooldownUntil, info.Reason = "active", nil, nil
				}
			}
		}
		if info.Status == "cooldown" && info.CooldownUntil != nil {
			if dt, ok := ParseISOUTC(*info.CooldownUntil); ok && !dt.After(nowUTC) {
				info.Status, info.CooldownUntil, info.Reason, info.ManualDisable, changed = "active", nil, nil, false, true
			}
		}
	}
	return changed
}

// SaveQuotaLocked atomically persists quota state while holding WithQuotaLock.
func SaveQuotaLocked(q *QuotaState, qPath string) error {
	q.UpdatedAt = Ptr(NowISO())
	return AtomicWriteJSON(QuotaFilePath(qPath), q, false, true)
}

func readAndPrepLocked(path string) (*QuotaState, bool, error) {
	var q QuotaState
	raw, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, false, UsageErr("error: cannot read quota file %s: %v", path, err)
	}
	dirty := os.IsNotExist(err)
	if !dirty {
		if jerr := json.Unmarshal(raw, &q); jerr != nil || q.Providers == nil {
			return nil, false, UsageErr("error: quota file %s must contain valid JSON with providers", path)
		}
	} else if r, se := os.ReadFile(filepath.Join(PluginRootPath(), "config", "quota.default.json")); se != nil ||
		json.Unmarshal(r, &q) != nil || q.Providers == nil {
		q = *DefaultQuotaData()
	}
	mig, exp := MigrateQuota(&q), ExpireAndReset(&q, time.Now().UTC())
	return &q, dirty || mig || exp, nil
}

// WithQuotaLock acquires the process mutex and OS flock around quota.json I/O.
func WithQuotaLock(qPath string, fn func(string) error) error {
	resolved := QuotaFilePath(qPath)
	lockPath := strings.TrimSuffix(resolved, filepath.Ext(resolved)) + ".lock"
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return err
	}
	quotaMu.Lock()
	defer quotaMu.Unlock()
	fh, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer fh.Close()
	if err := syscall.Flock(int(fh.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer func() { _ = syscall.Flock(int(fh.Fd()), syscall.LOCK_UN) }()
	return fn(resolved)
}

// LoadQuota loads quota.json (seeding if missing), migrates, expires, and saves.
func LoadQuota(qPath string) (*QuotaState, error) {
	var out *QuotaState
	err := WithQuotaLock(qPath, func(path string) error {
		q, dirty, rerr := readAndPrepLocked(path)
		if rerr != nil {
			return rerr
		}
		out = q
		if dirty {
			return SaveQuotaLocked(q, path)
		}
		return nil
	})
	return out, err
}
