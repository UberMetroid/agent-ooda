package fleet

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const leasePollInterval = 150 * time.Millisecond

var (
	relResetRe = regexp.MustCompile(
		`(?i)(?:resets?\s+(?:in|after)|try\s+again\s+in|retry\s+(?:in|after))\s*:?\s*` +
			`(?:(\d+)\s*d(?:ays?)?\s*,?\s*)?(?:(\d+)\s*h(?:ours?|rs?)?\s*,?\s*)?` +
			`(?:(\d+)\s*m(?:in(?:ute)?s?)?\s*,?\s*)?(?:(\d+)\s*s(?:ec(?:ond)?s?)?)?`,
	)
	retryAfterSecRe = regexp.MustCompile(`(?i)\bretry-after\s*[:=]\s*(\d+)\b`)
)

// MuseProbe holds the live `muse usage --json` command result.
type MuseProbe struct {
	Installed  bool    `json:"installed"`
	Fresh      bool    `json:"fresh"`
	PayAsYouGo bool    `json:"pay_as_you_go"`
	Summary    string  `json:"summary"`
	Raw        string  `json:"raw,omitempty"`
	ResetAt    *string `json:"reset_at,omitempty"`
	CheckedAt  float64 `json:"checked_at,omitempty"`
}

// ParseMuseUsageJSON decodes `muse usage --json` output into MuseProbe.
func ParseMuseUsageJSON(raw string, exitCode int, timedOut bool) MuseProbe {
	trimmed := strings.TrimSpace(raw)
	if timedOut {
		return MuseProbe{Installed: true, Summary: "timeout querying muse usage"}
	}
	lower := strings.ToLower(trimmed)
	for _, ln := range strings.Split(trimmed, "\n") {
		s := strings.TrimSpace(ln)
		if !strings.HasPrefix(s, "{") {
			continue
		}
		var doc map[string]any
		if json.Unmarshal([]byte(s), &doc) != nil {
			continue
		}
		reason, _ := doc["reason"].(string)
		hint, _ := doc["hint"].(string)
		out := MuseProbe{Installed: true, Raw: trimmed}
		if strings.EqualFold(reason, "api_key") {
			out.PayAsYouGo = true
			out.Summary = "pay-as-you-go api_key (Power subscription inactive)"
			return out
		}
		if allow, ok := doc["allowance"].(map[string]any); exitCode == 0 && ok && len(allow) > 0 {
			out.Fresh = true
			out.ResetAt = ExtractResetTS(s)
			lbl := hint
			if lbl == "" {
				lbl = reason
			}
			out.Summary = "subscription allowance active"
			if strings.TrimSpace(lbl) != "" {
				out.Summary = fmt.Sprintf("subscription active (%s)", strings.TrimSpace(lbl))
			}
			return out
		}
		if hint != "" {
			out.Summary = hint
			return out
		}
		if reason != "" {
			out.Summary = reason
			return out
		}
	}
	if strings.Contains(lower, "server_sent_no_usage") || strings.Contains(lower, "no usage data") {
		return MuseProbe{
			Installed: true, Raw: trimmed,
			Summary: "telemetry omitted by server (server_sent_no_usage); using quota.json reset timer",
		}
	}
	for _, ln := range strings.Split(trimmed, "\n") {
		if t := strings.TrimSpace(ln); t != "" {
			return MuseProbe{Installed: true, Summary: t, Raw: trimmed}
		}
	}
	return MuseProbe{Installed: true, Summary: fmt.Sprintf("exit=%d", exitCode), Raw: trimmed}
}

// ProbeMuseUsage queries `muse usage --json` headlessly with a short timeout.
func ProbeMuseUsage(timeoutSec int) MuseProbe {
	if timeoutSec <= 0 {
		timeoutSec = 4
	}
	museBin := ResolveCLI("muse")
	if museBin == "" {
		return MuseProbe{Summary: "muse CLI not installed"}
	}
	res, err := RunCmd(CmdSpec{Argv: []string{museBin, "usage", "--json"}, TimeoutSec: timeoutSec})
	if err != nil && !res.TimedOut {
		return MuseProbe{Installed: true, Summary: fmt.Sprintf("exec failed: %v", err)}
	}
	p := ParseMuseUsageJSON(res.Combined, res.ExitCode, res.TimedOut)
	p.CheckedAt = float64(time.Now().UnixNano()) / 1e9
	if os.Getenv("OODA_NO_AUTO_PROBE") != "1" {
		_ = AtomicWriteJSON(filepath.Join(HudDirPath(), "muse_usage.json"), p, true, true)
	}
	return p
}

// CachedMuseProbe returns a recent cached `muse usage --json` probe without delaying the HUD.
func CachedMuseProbe(maxAgeSec float64) MuseProbe {
	cachePath, ttl := filepath.Join(HudDirPath(), "muse_usage.json"), maxAgeSec
	if ttl <= 0 {
		ttl = 21600
	}
	now := float64(time.Now().UnixNano()) / 1e9
	if raw, err := os.ReadFile(cachePath); err == nil {
		var cached MuseProbe
		if json.Unmarshal(raw, &cached) == nil && cached.CheckedAt > 0 && (now-cached.CheckedAt) <= ttl {
			return cached
		}
	}
	if maxAgeSec <= 0 || os.Getenv("OODA_NO_AUTO_PROBE") == "1" || ResolveCLI("muse") == "" {
		return MuseProbe{}
	}
	return ProbeMuseUsage(3)
}

// ParseRelativeReset parses relative wait/reset strings on error/reset lines only.
func ParseRelativeReset(out string, now time.Time) *string {
	lines := strings.Split(out, "\n")
	if len(lines) > 16 {
		lines = lines[len(lines)-16:]
	}
	var kept []string
	for _, ln := range lines {
		low := strings.ToLower(ln)
		if strings.Contains(low, "reset") || strings.Contains(low, "retry-after") || HasErrorPattern(ln, 1, QuotaErrorPats) {
			kept = append(kept, ln)
		}
	}
	scoped := strings.Join(kept, "\n")
	if scoped == "" {
		return nil
	}
	mults := [4]time.Duration{24 * time.Hour, time.Hour, time.Minute, time.Second}
	for _, m := range relResetRe.FindAllStringSubmatch(scoped, -1) {
		var tot time.Duration
		anyUnit := false
		for i := 0; i < 4; i++ {
			if m[i+1] == "" {
				continue
			}
			n, err := strconv.Atoi(m[i+1])
			if err == nil && n > 0 {
				tot += time.Duration(n) * mults[i]
				anyUnit = true
			}
		}
		if anyUnit && tot > 0 && tot <= 45*24*time.Hour {
			return Ptr(FormatLocalISO(now.Add(tot)))
		}
	}
	if m := retryAfterSecRe.FindStringSubmatch(scoped); len(m) == 2 {
		if n, err := strconv.Atoi(m[1]); err == nil && n > 0 && n <= 45*86400 {
			return Ptr(FormatLocalISO(now.Add(time.Duration(n) * time.Second)))
		}
	}
	return nil
}

func leaseDir() string {
	base := HudDirPath()
	ldir := filepath.Join(base, "leases")
	_ = os.MkdirAll(base, 0o700)
	_ = os.Chmod(base, 0o700)
	_ = os.MkdirAll(ldir, 0o700)
	_ = os.Chmod(ldir, 0o700)
	return ldir
}

// CountActiveLeases counts currently locked parallel slot files for provider.
func CountActiveLeases(provider string, maxPar int) int {
	if maxPar <= 0 {
		return 0
	}
	ldir, busy := leaseDir(), 0
	for i := 0; i < maxPar; i++ {
		path := filepath.Join(ldir, fmt.Sprintf("%s-%d.lock", SafeID(provider), i))
		if _, err := os.Stat(path); err != nil {
			continue
		}
		fh, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			continue
		}
		if serr := syscall.Flock(int(fh.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); serr == nil {
			_ = syscall.Flock(int(fh.Fd()), syscall.LOCK_UN)
		} else {
			busy++
		}
		_ = fh.Close()
	}
	return busy
}

// AcquireParallelLease acquires an flock slot in 0..maxPar-1.
func AcquireParallelLease(provider string, maxPar int, blocking bool, timeoutSec int) (*os.File, bool, error) {
	if maxPar <= 0 {
		return nil, false, nil
	}
	if blocking && timeoutSec <= 0 {
		timeoutSec = DefaultTimeout
	}
	ldir, start := leaseDir(), time.Now()
	for {
		for i := 0; i < maxPar; i++ {
			path := filepath.Join(ldir, fmt.Sprintf("%s-%d.lock", SafeID(provider), i))
			fh, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
			if err != nil {
				return nil, false, err
			}
			_ = os.Chmod(path, 0o600)
			if ferr := syscall.Flock(int(fh.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); ferr == nil {
				return fh, false, nil
			}
			_ = fh.Close()
		}
		if !blocking {
			return nil, false, nil
		}
		if timeoutSec > 0 && time.Since(start) >= time.Duration(timeoutSec)*time.Second {
			return nil, true, nil
		}
		time.Sleep(leasePollInterval)
	}
}

// ReleaseParallelLease unlocks and closes an acquired parallel slot lease.
func ReleaseParallelLease(lease *os.File) {
	if lease == nil {
		return
	}
	_ = syscall.Flock(int(lease.Fd()), syscall.LOCK_UN)
	_ = lease.Close()
}
