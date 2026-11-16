package cli

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/UberMetroid/agent-ooda/internal/dispatch"
	"github.com/UberMetroid/agent-ooda/internal/envelope"
	"github.com/UberMetroid/agent-ooda/internal/fleet"
	"github.com/UberMetroid/agent-ooda/internal/worktree"
)

const probeTTLSecs = 900.0

func shouldSkipProbe(lane string, info *fleet.ProviderConfig, nowUTC time.Time, musePayg bool) string {
	if info == nil {
		return "not configured"
	}
	if info.Tier == "credits" {
		return "paid credits (opt-in --lane only; never probed)"
	}
	if info.ManualDisable {
		return "manually disabled"
	}
	if fleet.ResolveCLI(info.CLI) == "" {
		return "cli not installed"
	}
	if lane == "muse" && musePayg {
		return "PAYG api_key active (skipped)"
	}
	if info.Status == "cooldown" {
		rem := fleet.FormatCountdown(info.CooldownUntil, nowUTC)
		if rem != "" && rem != "due now" {
			return fmt.Sprintf("cooldown (%s remaining)", rem)
		}
	}
	if info.LastProbedAt != nil && *info.LastProbedAt != "" {
		if lp, ok := fleet.ParseISOUTC(*info.LastProbedAt); ok && nowUTC.Sub(lp).Seconds() < probeTTLSecs {
			return fmt.Sprintf("fresh (%ds ago)", int(nowUTC.Sub(lp).Seconds()))
		}
	}
	return ""
}

func probeOneLane(lane string, info *fleet.ProviderConfig, cwd string) (string, bool) {
	cliPath := fleet.ResolveCLI(info.CLI)
	if cliPath == "" {
		return "cli missing", false
	}
	spec, err := envelope.EffectiveLaneSpec(lane, info, "", "", "recon", "")
	if err != nil {
		return err.Error(), false
	}
	if lane == "agy-opus" && info.Model == nil && envelope.ProbeAgySettings("").GeminiBlocked && spec.ArgvModel == "" {
		return "blocked by Gemini guardrail (run --discover)", false
	}
	slot, serr := worktree.EnsureSlot("probe-" + lane)
	if serr != nil {
		return serr.Error(), false
	}
	res := dispatch.ExecuteOnce(dispatch.ExecReq{
		Provider: lane, Info: info, CLIPath: cliPath,
		Model: spec.ArgvModel, Effort: spec.ArgvEffort,
		Prompt: "Reply with 1.", Worktree: cwd, SlotDir: slot,
		TaskID: "probe-" + lane, TimeoutSec: 12, ReadOnly: true,
	})
	qHit := fleet.HasErrorPattern(res.Output, res.ExitCode, fleet.QuotaErrorPats)
	aHit := fleet.HasErrorPattern(res.Output, res.ExitCode, fleet.AuthErrorPats)
	if qHit || aHit {
		kind := "auth error"
		if qHit {
			kind = "quota/rate-limit error"
		}
		updated, werr := fleet.MarkProviderCooldown(
			lane, "Live probe "+kind+" on "+fleet.NowISO(), qHit, res.Output, "",
		)
		if werr != nil {
			return fmt.Sprintf("cooldown (%s; quota write error: %v)", kind, werr), false
		}
		if updated != nil {
			cd := fleet.FormatCountdown(updated.CooldownUntil, time.Now().UTC())
			return fmt.Sprintf("cooldown (%s, resets %s)", kind, nonEmptyOr(cd, "unknown")), false
		}
		return "cooldown (" + kind + ")", false
	}
	if res.ExitCode == 0 {
		if _, _, werr := fleet.SetProviderStatus(lane, "active", "probed", nil, false, ""); werr != nil {
			return fmt.Sprintf("probe ok, quota write failed: %v", werr), false
		}
		return fmt.Sprintf("active (%s, %.1fs)", dispatch.ShortModel(spec.DisplayModel), res.DurationS), true
	}
	return fmt.Sprintf("error exit=%d (%.1fs)", res.ExitCode, res.DurationS), false
}

// DoProbe runs lightweight read-only provider canaries and updates quota.json.
func DoProbe(outW, errW io.Writer) int {
	if outW == nil {
		outW = os.Stdout
	}
	if errW == nil {
		errW = os.Stderr
	}
	mp := fleet.ProbeMuseUsage(5)
	fmt.Fprintf(outW, "muse usage --json : %s\n", nonEmptyOr(mp.Summary, "unavailable"))
	q, err := fleet.LoadQuota("")
	if err != nil {
		fmt.Fprintln(errW, err)
		return 1
	}
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	nowUTC, anyFail := time.Now().UTC(), false
	targets := []string{"agy-opus", "mcode", "grok", "mistral", "devin-fast", "muse"}
	for _, lane := range targets {
		info := q.Providers[lane]
		if why := shouldSkipProbe(lane, info, nowUTC, mp.PayAsYouGo); why != "" {
			fmt.Fprintf(outW, "  %-11s : skipped (%s)\n", lane, why)
			continue
		}
		msg, ok := probeOneLane(lane, info, cwd)
		if !ok {
			anyFail = true
		}
		fmt.Fprintf(outW, "  %-11s : %s\n", lane, msg)
	}
	if anyFail {
		return 1
	}
	return 0
}
