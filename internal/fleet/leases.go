package fleet

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// MuseProbe holds the live `muse usage` command result.
type MuseProbe struct {
	Installed bool
	Fresh     bool
	Summary   string
	Raw       string
}

// ProbeMuseUsage queries `muse usage` headlessly with a short timeout.
func ProbeMuseUsage(timeoutSec int) MuseProbe {
	if timeoutSec <= 0 {
		timeoutSec = 4
	}
	museBin := ResolveCLI("muse")
	if museBin == "" {
		return MuseProbe{Summary: "muse CLI not installed"}
	}
	res, err := RunCmd(CmdSpec{Argv: []string{museBin, "usage"}, TimeoutSec: timeoutSec})
	if res.TimedOut {
		return MuseProbe{Installed: true, Summary: fmt.Sprintf("timeout after %ds", timeoutSec)}
	}
	if err != nil {
		return MuseProbe{Installed: true, Summary: fmt.Sprintf("exec failed: %v", err)}
	}
	raw := strings.TrimSpace(res.Combined)
	lower := strings.ToLower(raw)
	if strings.Contains(lower, "server_sent_no_usage") || strings.Contains(lower, "no usage data") {
		return MuseProbe{
			Installed: true,
			Summary:   "telemetry omitted by server (server_sent_no_usage); using quota.json reset timer",
			Raw:       raw,
		}
	}
	for _, ln := range strings.Split(raw, "\n") {
		if t := strings.TrimSpace(ln); t != "" {
			return MuseProbe{Installed: true, Summary: t, Raw: raw}
		}
	}
	return MuseProbe{Installed: true, Summary: fmt.Sprintf("exit=%d", res.ExitCode), Raw: raw}
}

const leasePollInterval = 150 * time.Millisecond

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
	ldir := leaseDir()
	busy := 0
	for i := 0; i < maxPar; i++ {
		path := filepath.Join(ldir, fmt.Sprintf("%s-%d.lock", SafeID(provider), i))
		if _, err := os.Stat(path); err != nil {
			continue
		}
		fh, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			continue
		}
		err = syscall.Flock(int(fh.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			_ = syscall.Flock(int(fh.Fd()), syscall.LOCK_UN)
		} else {
			busy++
		}
		_ = fh.Close()
	}
	return busy
}

// AcquireParallelLease acquires an flock slot in 0..maxPar-1.
// Returns (lease, false, nil) when unblocked; (nil, true, nil) on blocking timeout.
func AcquireParallelLease(provider string, maxPar int, blocking bool, timeoutSec int) (*os.File, bool, error) {
	if maxPar <= 0 {
		return nil, false, nil
	}
	if blocking && timeoutSec <= 0 {
		timeoutSec = DefaultTimeout
	}
	ldir := leaseDir()
	start := time.Now()
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
