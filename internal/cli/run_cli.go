package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/UberMetroid/agent-ooda/internal/dispatch"
	"github.com/UberMetroid/agent-ooda/internal/envelope"
	"github.com/UberMetroid/agent-ooda/internal/fleet"
	"github.com/UberMetroid/agent-ooda/internal/worktree"
)

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func validateRunArgs(opts CLIOpts, defTimeout int, withFiles bool) ([]string, int, []string, error) {
	if err := fleet.CheckModelGuard(opts.Model); err != nil {
		return nil, 0, nil, err
	}
	lanes, err := fleet.ParseLanes(opts.Lane, true)
	if err != nil {
		return nil, 0, nil, err
	}
	timeout := defTimeout
	if opts.Timeout != "" {
		t, terr := fleet.ParseTimeout(opts.Timeout)
		if terr != nil {
			return nil, 0, nil, terr
		}
		timeout = t
	}
	if !withFiles {
		return lanes, timeout, nil, nil
	}
	var files []string
	for _, item := range worktree.ParseCSVArg(opts.Files) {
		norm, nerr := worktree.NormalizeRelPath(item)
		if nerr != nil {
			return nil, 0, nil, nerr
		}
		if norm != "" {
			files = append(files, norm)
		}
	}
	return lanes, timeout, files, nil
}

func derefOr(p *string, fb string) string {
	if p != nil && *p != "" {
		return *p
	}
	return fb
}

func handleQuotaFlags(opts CLIOpts, outW, errW io.Writer) (int, bool) {
	acted := false
	if opts.Enable != "" {
		canon, _, err := fleet.SetProviderStatus(opts.Enable, "active", "", nil, false, "")
		if err != nil {
			fmt.Fprintln(errW, err)
			return 2, true
		}
		fmt.Fprintf(outW, "enabled %s in %s\n", canon, fleet.QuotaFilePath(""))
		acted = true
	}
	if opts.Disable != "" {
		canon, entry, err := fleet.SetProviderStatus(
			opts.Disable, "cooldown", opts.Reason, opts.Hours, opts.AutoWake, "",
		)
		if err != nil {
			fmt.Fprintln(errW, err)
			return 2, true
		}
		until := ""
		if u := derefOr(entry.CooldownUntil, ""); u != "" {
			until = " until " + u
		}
		fmt.Fprintf(outW, "disabled %s (%s%s) in %s\n",
			canon, derefOr(entry.Reason, ""), until, fleet.QuotaFilePath(""))
		acted = true
	}
	if opts.SetCycle != "" {
		canon, entry, err := fleet.ConfigureCycle(fleet.CyclePatch{
			Provider: opts.SetCycle, Hours: opts.Hours, TokenBudget: opts.TokenBudget,
			CostBudget: opts.CostBudget, ResetNow: opts.ResetNow,
			Effort: strPtr(opts.Effort), Model: strPtr(opts.Model), MaxParallel: opts.MaxParallel,
		})
		if err != nil {
			fmt.Fprintln(errW, err)
			return 2, true
		}
		hrsS := "none"
		if entry.CycleHours != nil {
			hrsS = fmt.Sprintf("%g", *entry.CycleHours)
		}
		fmt.Fprintf(outW, "configured %s: status=%s model=%s effort=%s cycle_hours=%s next_reset_at=%s\n",
			canon, entry.Status, derefOr(entry.Model, "cli-default"),
			derefOr(entry.Effort, "cli-default"), hrsS, derefOr(entry.NextResetAt, "none"))
		acted = true
	}
	return 0, acted
}

func dispatchManifest(opts CLIOpts, outW, errW io.Writer) int {
	if err := fleet.CheckModelGuard(opts.Model); err != nil {
		fmt.Fprintln(errW, err)
		return 2
	}
	root, err := fleet.RepoRoot("")
	if err != nil {
		fmt.Fprintln(errW, err)
		return 2
	}
	if _, err := fleet.ParseLanes(opts.Lane, true); err != nil {
		fmt.Fprintln(errW, err)
		return 2
	}
	if opts.Timeout != "" {
		if _, err := fleet.ParseTimeout(opts.Timeout); err != nil {
			fmt.Fprintln(errW, err)
			return 2
		}
	}
	for _, lane := range worktree.ParseCSVArg(opts.ExcludeLanes) {
		if _, err := fleet.NormalizeLane(lane, false); err != nil {
			fmt.Fprintln(errW, err)
			return 2
		}
	}
	jobs := min(max(1, opts.Jobs), fleet.MaxJobs)
	tasks, err := worktree.LoadManifest(opts.Manifest)
	if err != nil {
		fmt.Fprintln(errW, err)
		return 2
	}
	q, err := fleet.LoadQuota("")
	if err != nil {
		fmt.Fprintln(errW, err)
		return 1
	}
	sel, _ := envelope.SelectProvider(envelope.SelectReq{RequestedLane: opts.Lane, Quota: q})
	if sel.Lane == "" {
		fmt.Fprintf(errW, "error: no usable worker CLI found on this host (%s)\n", sel.SkippedNote)
		return 2
	}
	pool := q.ParallelStripePool
	if len(pool) == 0 {
		pool = fleet.StripePool
	}
	stripeStr := "on (" + strings.Join(pool, " -> ") + ")"
	if opts.NoStripe {
		stripeStr = "off (--no-stripe)"
	}
	fmt.Fprintf(outW, "default lane: %s -> %s | jobs: %d | tasks: %d | stripe: %s\n",
		opts.Lane, sel.Lane, jobs, len(tasks), stripeStr)
	if sel.SkippedNote != "" {
		fmt.Fprintf(outW, "routing note: %s\n", sel.SkippedNote)
	}
	return dispatch.RunManifestBatch(root, tasks, dispatch.BatchOpts{
		Lane: opts.Lane, Model: opts.Model, Effort: opts.Effort, Timeout: opts.Timeout,
		Skills: opts.Skills, ExcludeLanes: opts.ExcludeLanes, Jobs: jobs,
		NoStripe: opts.NoStripe, NoReview: opts.NoReview, NoPivot: opts.NoPivot, LogWriter: outW,
	})
}

func runDispatchMode(opts CLIOpts, outW, errW io.Writer) int {
	if opts.Recon != nil {
		return DoRecon(opts, outW, errW)
	}
	if opts.Task != nil {
		return DoSingleTask(opts, outW, errW)
	}
	if opts.Review != nil {
		target := *opts.Review
		if target == "." && opts.Manifest != "" {
			target = opts.Manifest
		}
		return DoStandaloneReview(target, opts, outW, errW)
	}
	return dispatchManifest(opts, outW, errW)
}

// RunCLI parses argv and executes the selected agent-ooda command.
func RunCLI(prog string, argv []string, outW, errW io.Writer) int {
	if outW == nil {
		outW = os.Stdout
	}
	if errW == nil {
		errW = os.Stderr
	}
	opts, err := ParseArgs(prog, argv, outW)
	if err != nil {
		fmt.Fprintln(errW, err)
		return 2
	}
	if opts.Help {
		return 0
	}
	if opts.Version {
		fmt.Fprintf(outW, "agent-ooda v%s\n", fleet.Version)
		return 0
	}
	if opts.Status {
		return ReportStatus(outW, false)
	}
	if opts.Discover {
		return ReportStatus(outW, true)
	}
	if opts.Usage {
		return DoUsage(outW)
	}
	if fleet.RouterDepth() >= 1 {
		fmt.Fprintln(errW,
			"error: recursive agent-ooda dispatch blocked (AGENT_OODA_DEPTH>=1 / ROUTER_DEPTH>=1). "+
				"You are already running inside a dispatched worker — execute the task directly "+
				"with your native tools instead of invoking agent-ooda.")
		return 2
	}
	if opts.Probe {
		return DoProbe(outW, errW)
	}
	if rc, handled := handleQuotaFlags(opts, outW, errW); handled {
		return rc
	}
	if opts.Cleanup && opts.Recon == nil && opts.Task == nil && opts.Review == nil {
		root, err := fleet.RepoRoot("")
		if err != nil {
			fmt.Fprintln(errW, err)
			return 2
		}
		worktree.Cleanup(root, outW)
		return 0
	}
	if opts.Recon == nil && opts.Task == nil && opts.Review == nil && opts.Manifest == "" {
		fmt.Fprintln(errW,
			"error: manifest path is required (or pass --version, --recon, --task, --review, "+
				"--status, --discover, --usage, --probe, --set-cycle, --enable, --disable, --cleanup)")
		return 2
	}
	return runDispatchMode(opts, outW, errW)
}
