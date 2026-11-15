package fleet

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

var quotaMu sync.Mutex

func migratePool(cur []string, def []string, oldVer int) ([]string, bool) {
	changed := false
	var cleaned []string
	for _, p := range cur {
		if p == "cursor" {
			changed = true
			continue
		}
		cleaned = append(cleaned, p)
	}
	if oldVer < QuotaSchemaVersion || len(cleaned) == 0 {
		out := append([]string(nil), def...)
		return out, true
	}
	return cleaned, changed
}

func migrateProvider(name string, info *ProviderConfig, tmpl *ProviderConfig, oldVer int, legacyUpd time.Time) bool {
	changed := false
	if oldVer < QuotaSchemaVersion {
		if name == "mcode" && info.Tier == "unlimited" {
			info.Tier = "high-cap"
			changed = true
		}
		if name == "muse" && info.Tier == "unlimited" {
			info.Tier = "capped"
			changed = true
		}
		if name == "devin-fast" && info.MaxParallel == nil {
			info.MaxParallel = Ptr(DevinFastMaxPar)
			changed = true
		}
		if name == "mcode" && info.MaxParallel == nil {
			info.MaxParallel = Ptr(McodeMaxPar)
			info.Role = tmpl.Role
			changed = true
		}
		if info.CycleHours == nil && tmpl.CycleHours != nil {
			info.CycleHours = Ptr(*tmpl.CycleHours)
			changed = true
		}
	}
	if info.Status == "cooldown" && info.CooldownUntil == nil && info.CycleHours != nil && !info.ManualDisable {
		base := legacyUpd
		if base.IsZero() {
			base = time.Now().UTC()
		}
		dur := time.Duration(*info.CycleHours * float64(time.Hour))
		wake := FormatLocalISO(base.Add(dur))
		info.CooldownUntil = &wake
		if info.NextResetAt == nil || *info.NextResetAt == "" {
			info.NextResetAt = &wake
		}
		if info.CycleStartedAt == nil || *info.CycleStartedAt == "" {
			started := FormatLocalISO(base)
			info.CycleStartedAt = &started
		}
		changed = true
	}
	return changed
}

// MigrateQuota upgrades legacy quota state in-place to QuotaSchemaVersion.
func MigrateQuota(q *QuotaState) bool {
	changed := false
	oldVer := max(1, q.Version)
	if oldVer < QuotaSchemaVersion {
		q.Version = QuotaSchemaVersion
		changed = true
	}
	if q.Providers == nil {
		q.Providers = make(map[string]*ProviderConfig)
		changed = true
	}
	if _, hasCursor := q.Providers["cursor"]; hasCursor {
		delete(q.Providers, "cursor")
		changed = true
	}
	var c1, c2 bool
	q.DefaultFallbackChain, c1 = migratePool(q.DefaultFallbackChain, DefaultPool, oldVer)
	q.ParallelStripePool, c2 = migratePool(q.ParallelStripePool, StripePool, oldVer)
	var legacyUpd time.Time
	if q.UpdatedAt != nil {
		legacyUpd, _ = ParseISOUTC(*q.UpdatedAt)
	}
	for name, tmpl := range DefaultProviders() {
		if cur, ok := q.Providers[name]; !ok || cur == nil {
			cp := *tmpl
			q.Providers[name] = &cp
			changed = true
		} else if migrateProvider(name, cur, tmpl, oldVer, legacyUpd) {
			changed = true
		}
	}
	return changed || c1 || c2
}

// ExpireAndReset clears elapsed cycle counters and expired non-manual cooldowns.
func ExpireAndReset(q *QuotaState, nowUTC time.Time) bool {
	changed := false
	if nowUTC.IsZero() {
		nowUTC = time.Now().UTC()
	}
	for _, info := range q.Providers {
		if info == nil {
			continue
		}
		if info.NextResetAt != nil {
			if dt, ok := ParseISOUTC(*info.NextResetAt); ok && !dt.After(nowUTC) {
				info.CycleRuns, info.CycleTokens, info.CycleTokensExact, info.CycleCostUSD = 0, 0, false, 0
				info.CycleStartedAt, info.NextResetAt = nil, nil
				if info.Status == "cooldown" && !info.ManualDisable {
					info.Status, info.CooldownUntil, info.Reason = "active", nil, nil
				}
				changed = true
			}
		}
		if info.Status == "cooldown" && info.CooldownUntil != nil {
			if dt, ok := ParseISOUTC(*info.CooldownUntil); ok && !dt.After(nowUTC) {
				info.Status, info.CooldownUntil, info.Reason, info.ManualDisable = "active", nil, nil, false
				changed = true
			}
		}
	}
	return changed
}

// SaveQuotaLocked atomically persists quota state while holding WithQuotaLock.
func SaveQuotaLocked(q *QuotaState, qPath string) error {
	now := NowISO()
	q.UpdatedAt = &now
	return AtomicWriteJSON(QuotaFilePath(qPath), q, false, true)
}

func readAndPrepLocked(path string) (*QuotaState, error) {
	if raw, err := os.ReadFile(path); err == nil {
		var q QuotaState
		if jerr := json.Unmarshal(raw, &q); jerr != nil {
			return nil, UsageErr("error: cannot read quota file %s: %v", path, jerr)
		}
		if q.Providers == nil {
			return nil, UsageErr("error: quota file %s must contain a JSON object with providers", path)
		}
		MigrateQuota(&q)
		ExpireAndReset(&q, time.Now().UTC())
		return &q, nil
	} else if !os.IsNotExist(err) {
		return nil, UsageErr("error: cannot read quota file %s: %v", path, err)
	}
	var q QuotaState
	seedPath := filepath.Join(PluginRootPath(), "config", "quota.default.json")
	if raw, err := os.ReadFile(seedPath); err == nil {
		if jerr := json.Unmarshal(raw, &q); jerr == nil && q.Providers != nil {
			MigrateQuota(&q)
			ExpireAndReset(&q, time.Now().UTC())
			return &q, nil
		}
	}
	dq := DefaultQuotaData()
	MigrateQuota(dq)
	ExpireAndReset(dq, time.Now().UTC())
	return dq, nil
}

// WithQuotaLock acquires the process mutex and OS flock around quota.json I/O.
func WithQuotaLock(qPath string, fn func(string) error) error {
	resolved := QuotaFilePath(qPath)
	ext := filepath.Ext(resolved)
	lockPath := strings.TrimSuffix(resolved, ext) + ".lock"
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
	defer func() {
		_ = syscall.Flock(int(fh.Fd()), syscall.LOCK_UN)
	}()
	return fn(resolved)
}

// LoadQuota loads quota.json (seeding if missing), migrates, expires, and saves.
func LoadQuota(qPath string) (*QuotaState, error) {
	var out *QuotaState
	err := WithQuotaLock(qPath, func(path string) error {
		st, serr := os.Stat(path)
		if serr != nil || st.IsDir() {
			q, rerr := readAndPrepLocked(path)
			if rerr != nil {
				return rerr
			}
			if werr := SaveQuotaLocked(q, path); werr != nil {
				return werr
			}
			out = q
			return nil
		}
		raw, rerr := os.ReadFile(path)
		if rerr != nil {
			return UsageErr("error: cannot read quota file %s: %v", path, rerr)
		}
		var q QuotaState
		if jerr := json.Unmarshal(raw, &q); jerr != nil {
			return UsageErr("error: cannot read quota file %s: %v", path, jerr)
		}
		changed := MigrateQuota(&q)
		if ExpireAndReset(&q, time.Now().UTC()) {
			changed = true
		}
		if changed {
			if werr := SaveQuotaLocked(&q, path); werr != nil {
				return werr
			}
		}
		out = &q
		return nil
	})
	return out, err
}
