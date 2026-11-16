package verify

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/UberMetroid/agent-ooda/internal/envelope"
	"github.com/UberMetroid/agent-ooda/internal/fleet"
)

// ReviewExecReq parameterizes one headless reviewer execution.
type ReviewExecReq struct {
	Provider, CLIPath, Model, Effort string
	Info                             *fleet.ProviderConfig
	Prompt, TargetDir, SlotDir       string
	TaskID, ReviewMode, MarkerPath   string
	TimeoutSec                       int
}

// ReviewExecRes captures raw output and exit status from one reviewer run.
type ReviewExecRes struct {
	ExitCode   int
	Output     string
	TimedOut   bool
	ExportInfo *fleet.ExportUsage
}

// ReviewRunner executes one review attempt cleanly without circular imports.
type ReviewRunner func(req ReviewExecReq) ReviewExecRes

// ReviewReq configures calibrated cross-model diff review with fallback.
type ReviewReq struct {
	TargetDir, SlotDir, AuthorLane    string
	ReqLane, Model, Effort            string
	QuotaPath                         string
	DiffCtx                           DiffContext
	ScopeViols, Declared, AuthorLanes []string
	TimeoutSec                        int
	LogWriter                         io.Writer
	Exclude                           map[string]bool
	Runner                            ReviewRunner
	ResolveCLI                        func(string) string
	OnStart                           func(mode, tid, lane, cli, mod, eff string, sk []string, cwd string) string
	OnFinish                          func(mark, mode, lane, mod, eff string, sk []string, rc int, dur float64)
}

// ReviewResult reports final review verdict, chosen lane, model, and output.
type ReviewResult struct {
	Ran, TimedOut                bool
	Lane, Model, Verdict, Reason string
	ExitCode                     int
	Output                       string
}

func reviewLease(
	prov string, info *fleet.ProviderConfig, q *fleet.QuotaState,
	tried, avoid map[string]bool, timeout int, req ReviewReq,
) (*os.File, bool) {
	maxP := 0
	if info != nil && info.MaxParallel != nil {
		maxP = *info.MaxParallel
	}
	if maxP <= 0 {
		return nil, false
	}
	lease, _, _ := fleet.AcquireParallelLease(prov, maxP, false, 0)
	if lease != nil {
		return lease, false
	}
	ex := copyBoolMap(tried)
	ex[prov] = true
	alt, _ := envelope.SelectProvider(envelope.SelectReq{
		RequestedLane: "auto", Complexity: "review", Quota: q, Exclude: ex,
		AvoidFamilies: avoid, StrictAvoid: true, DeferAtCap: true, ResolveCLI: req.ResolveCLI,
	})
	if alt.Lane != "" {
		tried[prov] = true
		return nil, true
	}
	if req.LogWriter != nil {
		fmt.Fprintf(req.LogWriter, "[verify] waiting for free %s slot (%d/%d busy)\n", prov, maxP, maxP)
	}
	lease, timedOut, lerr := fleet.AcquireParallelLease(prov, maxP, true, timeout)
	if timedOut || lerr != nil {
		tried[prov] = true
		return nil, true
	}
	return lease, false
}

func copyBoolMap(src map[string]bool) map[string]bool {
	out := make(map[string]bool, len(src)+1)
	for k, v := range src {
		if v {
			out[k] = true
		}
	}
	return out
}

func resolveReviewModel(prov string, req ReviewReq) string {
	if req.Model != "" {
		return req.Model
	}
	if prov == "mcode" {
		if envMod := fleet.ReviewModelEnv(); envMod != "" && envMod != "cli-default" {
			return envMod
		}
	}
	return ""
}

func invokeReviewer(
	req ReviewReq, sel envelope.SelectRes, lease *os.File,
	prompt, scopeNote string, nativeEligible bool,
) (ReviewExecRes, string) {
	rawM, rawEff := resolveReviewModel(sel.Lane, req), req.Effort
	if prim := strings.Split(req.ReqLane, ",")[0]; req.ReqLane != "" && req.ReqLane != "auto" {
		if cp, err := fleet.NormalizeLane(prim, true); err == nil && cp != "" {
			prim = cp
		}
		if sel.Lane != prim {
			rawM, rawEff = "", ""
			if req.LogWriter != nil {
				fmt.Fprintf(req.LogWriter, "[verify] fallback: requested %q -> %q (off-family/cooldown)\n", prim, sel.Lane)
			}
		}
	}
	spec, specErr := envelope.EffectiveLaneSpec(sel.Lane, sel.Info, rawM, rawEff, "review", "")
	if specErr != nil {
		fleet.ReleaseParallelLease(lease)
		return ReviewExecRes{ExitCode: 2, Output: specErr.Error()}, rawM
	}
	taskID := "review-" + filepath.Base(req.SlotDir)
	if req.LogWriter != nil {
		fmt.Fprintf(req.LogWriter, "▸ [OODA:ACT verify] agent=%s (%s:%s) | effort=%s\n",
			sel.Lane, sel.Info.CLI, filepath.Base(spec.DisplayModel), spec.DisplayEffort)
	}
	marker, revExit, start := "", -1, time.Now()
	defer func() {
		fleet.ReleaseParallelLease(lease)
		if req.OnFinish != nil {
			req.OnFinish(marker, "act:verify", sel.Lane, spec.DisplayModel, spec.DisplayEffort,
				nil, revExit, time.Since(start).Seconds())
		}
	}()
	if req.OnStart != nil {
		marker = req.OnStart("act:verify", taskID, sel.Lane, sel.Info.CLI,
			spec.DisplayModel, spec.DisplayEffort, nil, req.TargetDir)
	}
	rmode, callPrompt := "inline", prompt
	if sel.Lane == "mcode" && nativeEligible {
		rmode, callPrompt = "native", scopeNote
	}
	res := req.Runner(ReviewExecReq{
		Provider: sel.Lane, Info: sel.Info, CLIPath: sel.CLIPath,
		Model: spec.ArgvModel, Effort: spec.ArgvEffort, Prompt: callPrompt,
		TargetDir: req.TargetDir, SlotDir: req.SlotDir, TaskID: taskID,
		TimeoutSec: max(1, req.TimeoutSec), ReviewMode: rmode, MarkerPath: marker,
	})
	revExit = res.ExitCode
	return res, spec.DisplayModel
}

func shouldRetryReview(
	sel envelope.SelectRes, res ReviewExecRes, verdict string,
	q *fleet.QuotaState, tried, avoid map[string]bool, req ReviewReq,
) bool {
	if verdict == "REJECT" || (!res.TimedOut && res.ExitCode == 0 && verdict != "UNKNOWN") {
		return false
	}
	ex := copyBoolMap(tried)
	ex[sel.Lane] = true
	nxt, _ := envelope.SelectProvider(envelope.SelectReq{
		RequestedLane: "auto", Complexity: "review", Quota: q, Exclude: ex,
		AvoidFamilies: avoid, StrictAvoid: true, DeferAtCap: true, ResolveCLI: req.ResolveCLI,
	})
	if nxt.Lane == "" || avoid[envelope.ModelFamily(nxt.Lane)] {
		return false
	}
	tried[sel.Lane] = true
	avoid[envelope.ModelFamily(sel.Lane)] = true
	return true
}

// RunReview performs calibrated cross-model read-only diff review with off-family fallback.
func RunReview(req ReviewReq) ReviewResult {
	if req.Runner == nil {
		return ReviewResult{Verdict: "SKIPPED", Reason: "no review runner configured"}
	}
	if req.TimeoutSec <= 0 {
		req.TimeoutSec = fleet.ReviewTimeout
	}
	timeout, avoid := req.TimeoutSec, map[string]bool{}
	for _, a := range append([]string{req.AuthorLane}, req.AuthorLanes...) {
		if a != "" {
			avoid[envelope.ModelFamily(a)] = true
		}
	}
	tried := copyBoolMap(req.Exclude)
	prompt := BuildReviewPrompt(req.TargetDir, req.DiffCtx, req.ScopeViols, req.Declared)
	note := BuildNativeReviewNote(req.TargetDir, req.DiffCtx, req.ScopeViols, req.Declared)
	native := req.DiffCtx.Source == "working-tree" && len(req.DiffCtx.Diff) <= NativeReviewMaxBytes
	for {
		q, qerr := fleet.LoadQuota(req.QuotaPath)
		if qerr != nil {
			return ReviewResult{Model: fleet.ReviewModelEnv(), Verdict: "SKIPPED", ExitCode: 2, Reason: qerr.Error()}
		}
		sel, err := envelope.SelectProvider(envelope.SelectReq{
			RequestedLane: req.ReqLane, Complexity: "review", Quota: q, Exclude: tried,
			AvoidFamilies: avoid, StrictAvoid: true, DeferAtCap: true, ResolveCLI: req.ResolveCLI,
		})
		if err != nil || sel.Lane == "" {
			why := sel.SkippedNote
			if err != nil {
				why = err.Error()
			}
			return ReviewResult{Model: fleet.ReviewModelEnv(), Verdict: "SKIPPED", Reason: why}
		}
		lease, retry := reviewLease(sel.Lane, sel.Info, q, tried, avoid, timeout, req)
		if retry {
			continue
		}
		res, model := invokeReviewer(req, sel, lease, prompt, note, native)
		_, _ = fleet.RecordUsage(sel.Lane, prompt, res.Output, res.ExportInfo, req.QuotaPath)
		qHit := fleet.HasErrorPattern(res.Output, res.ExitCode, fleet.QuotaErrorPats)
		if qHit || fleet.HasErrorPattern(res.Output, res.ExitCode, fleet.AuthErrorPats) {
			kind := "auth"
			if qHit {
				kind = "quota/rate-limit"
			}
			_, _ = fleet.MarkProviderCooldown(sel.Lane,
				"Auto-detected "+kind+" error on "+fleet.NowISO(), qHit, res.Output, req.QuotaPath)
			tried[sel.Lane] = true
			continue
		}
		verdict := ParseReviewVerdict(res.Output)
		if res.ExitCode != 0 && verdict == "ACCEPT" {
			verdict = "UNKNOWN"
		}
		if shouldRetryReview(sel, res, verdict, q, tried, avoid, req) {
			continue
		}
		if len(req.ScopeViols) > 0 {
			verdict = "REJECT"
		}
		return ReviewResult{Ran: true, Lane: sel.Lane, Model: model, Verdict: verdict,
			ExitCode: res.ExitCode, TimedOut: res.TimedOut, Output: res.Output}
	}
}
