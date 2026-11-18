package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/UberMetroid/agy-router/internal/dispatch"
	"github.com/UberMetroid/agy-router/internal/envelope"
	"github.com/UberMetroid/agy-router/internal/fleet"
	"github.com/UberMetroid/agy-router/internal/verify"
	"github.com/UberMetroid/agy-router/internal/worktree"
)

func buildTaskSpec(
	id, rawPrompt, comp string, files []string, opts CLIOpts, cwd string, ro bool,
) worktree.TaskSpec {
	skills := worktree.ParseCSVArg(opts.Skills)
	if !ro || len(skills) > 0 {
		skills = worktree.ResolveTaskSkills(skills, rawPrompt, files, cwd)
	}
	prompt := rawPrompt
	if ro {
		if id == "orient" {
			prompt = fleet.OrientPrefix + rawPrompt
		} else {
			prompt = fleet.ReconPrefix + rawPrompt
		}
	}
	return worktree.TaskSpec{
		ID: id, Prompt: prompt, Files: files, Skills: skills,
		Complexity: comp, Model: opts.Model, Effort: opts.Effort, ReadOnly: ro,
	}
}

func cliResolveStage(stage string, opts CLIOpts, cwd string, q *fleet.QuotaState) worktree.StageSpec {
	cliLane := ""
	if opts.LaneSet {
		cliLane = opts.Lane
		if parsed, perr := fleet.ParseLanes(opts.Lane, true); perr == nil && len(parsed) > 0 {
			cliLane = strings.Join(parsed, ",")
		}
	} else if m := strings.TrimSpace(opts.Model); m != "" {
		lm := strings.ToLower(m)
		if lm != "auto" && lm != "default" && lm != "cli-default" && fleet.CheckModelGuard(m) == nil {
			if inferred, err := fleet.NormalizeLane(m, false); err == nil {
				cliLane = inferred
			}
		}
	}
	if q == nil && !opts.LaneSet && cliLane == "" {
		var qerr error
		if q, qerr = fleet.LoadQuota(""); qerr != nil {
			fmt.Fprintf(os.Stderr, "warning: %v\n", qerr)
		}
	}
	return worktree.ResolveStage(stage, cliLane, opts.Model, opts.Effort, opts.Profile, cwd, q)
}

func printStageMatrix(
	w io.Writer, cwd string, opts CLIOpts, q *fleet.QuotaState, caps map[string]envelope.LaneCap,
) {
	fmt.Fprintln(w, "\nOODA + Verify Stage Matrix (precedence: CLI > --local > --profile > global > auto):")
	fmt.Fprintf(w, "%-9s %-14s %-44s %s\n", "STAGE", "MODE", "EFFECTIVE MODEL / CHAIN", "ROLE")
	fmt.Fprintln(w, strings.Repeat("-", 108))
	roles := map[string]string{
		"observe": "Read-only codebase recon (--recon)", "orient": "Root-cause & blast radius (--orient)",
		"act": "Scoped code & worktree edits (--task / manifest)", "verify": "Cross-model diff review (--review)",
	}
	for _, st := range fleet.StageNames {
		stOpts := opts
		if st == "verify" && opts.Review == nil && !strings.EqualFold(opts.Complexity, "review") {
			stOpts.Lane, stOpts.LaneSet, stOpts.Model, stOpts.Effort = "", false, "", ""
		}
		sp := cliResolveStage(st, stOpts, cwd, q)
		route := envelope.StageChainSummary(st, q, caps)
		if sp.Lane != "auto" {
			parts := make([]string, 0, 2)
			for _, p := range strings.Split(sp.Lane, ",") {
				parts = append(parts, envelope.FormatSpec(p, nonEmptyOr(sp.Model, caps[p].EffectiveModel)))
			}
			route = strings.Join(parts, " -> ")
			if sp.Effort != "" {
				route += " (" + sp.Effort + ")"
			}
			if sp.ExpiresAt != nil && *sp.ExpiresAt != "" {
				route += " [until reset]"
			}
		}
		fmt.Fprintf(w, "%-9s %-14s %-44s %s\n", st, sp.Source, route, roles[st])
		if st == "orient" {
			fmt.Fprintf(w, "%-9s %-14s %-44s %s\n", "decide", "orchestrator",
				"Orchestrator (Plan Mode + Human)", "Architecture, user alignment & task slicing")
		}
	}
}

func handleStageFlags(opts CLIOpts, outW, errW io.Writer) (int, bool) {
	if !opts.ResetStages && opts.SetStage == "" {
		return 0, false
	}
	cwd, ok := requireCWD(errW)
	if !ok {
		return 1, true
	}
	var stage, lane string
	if opts.ResetStages {
		stage, lane = "all", "auto"
	} else {
		k, v, hasEq := strings.Cut(opts.SetStage, "=")
		if !hasEq || strings.TrimSpace(k) == "" || strings.TrimSpace(v) == "" {
			fmt.Fprintln(errW, "error: --set-stage expects STAGE=LANE (or preset=NAME)")
			return 2, true
		}
		stage, lane = strings.TrimSpace(k), strings.TrimSpace(v)
	}
	stages, err := worktree.ConfigureStage(worktree.StagePatch{
		Stage: stage, Lane: lane, Model: opts.Model, Effort: opts.Effort,
		UntilReset: opts.UntilReset, Local: opts.Local, Cwd: cwd,
	})
	if err != nil {
		fmt.Fprintln(errW, err)
		return 2, true
	}
	scope := fleet.QuotaFilePath("")
	if opts.Local {
		scope = worktree.WorkspaceStagesPath(cwd)
	}
	fmt.Fprintf(outW, "updated stages in %s:\n", scope)
	for _, st := range fleet.StageNames {
		r := stages[st]
		if r.Lane == "" && opts.Local {
			r.Lane = "(inherit)"
		} else if r.Lane == "" {
			r.Lane = "auto"
		}
		spec := r.Lane
		if r.Model != nil && *r.Model != "" && !strings.Contains(r.Lane, ",") {
			spec = envelope.FormatSpec(r.Lane, *r.Model)
		}
		fmt.Fprintf(outW, "  %-8s -> %s (model=%s, effort=%s)\n",
			st, spec, derefOr(r.Model, "cli-default"), derefOr(r.Effort, "cli-default"))
	}
	return 0, true
}

func runReadOnlyStage(stage, prompt string, opts CLIOpts, outW, errW io.Writer) int {
	slotName, banner, defComp := "recon", "OBSERVE", "recon"
	if stage == "orient" {
		slotName, banner, defComp = "orient", "ORIENT", "deep"
	}
	_, _, files, err := validateRunArgs(opts, fleet.DefaultTimeout, true)
	if err != nil {
		fmt.Fprintln(errW, err)
		return 2
	}
	slot, err := worktree.EnsureSlot(slotName)
	if err != nil {
		fmt.Fprintln(errW, err)
		return 1
	}
	cwd, ok := requireCWD(errW)
	if !ok {
		return 1
	}
	comp := nonEmptyOr(opts.Complexity, defComp)
	st := cliResolveStage(stage, opts, cwd, nil)
	opts.Lane, opts.Model, opts.Effort = st.Lane, st.Model, st.Effort
	task := buildTaskSpec(slotName, prompt, comp, files, opts, cwd, true)
	preSnap := verify.SnapshotRepo(cwd)
	excl := worktree.ParseCSVArg(opts.ExcludeLanes)
	skBlocks, _ := worktree.LoadSkillBlocks(task.Skills, cwd)
	cPay := fmt.Sprintf("%s|%s|%s|%t|%s|%v|%t|%v|%v|%s|%s", st.Lane, st.Model, st.Effort,
		st.Source != "cli", comp, excl, opts.NoPivot, task.Files, task.Skills, skBlocks, task.Prompt)
	cKey := verify.CacheKey(stage, cPay, preSnap)
	if hit, ok := verify.LoadStageCache(cKey, opts.NoCache); ok {
		fmt.Fprintf(errW, "[ROUTER:%s lane=%s model=%s effort=%s 0.0s exit=0 cache=hit]\n",
			banner, hit.Lane, hit.Model, nonEmptyOr(hit.Effort, "default"))
		fmt.Fprintln(outW, hit.Output)
		return 0
	}
	res, err := dispatch.RunTaskWithFallback(dispatch.TaskReq{
		Task: task, Worktree: cwd, SlotDir: slot, Lane: st.Lane, Model: st.Model, Effort: st.Effort,
		TimeoutRaw: opts.Timeout, Exclude: excl, NoPivot: opts.NoPivot, SoftPin: st.Source != "cli", LogWriter: errW,
	})
	if err != nil {
		fmt.Fprintln(errW, err)
		return 2
	}
	fmt.Fprintf(errW, "[ROUTER:%s lane=%s model=%s effort=%s %.1fs exit=%d]\n",
		banner, res.Lane, res.Model, res.Effort, res.DurationS, res.ExitCode)
	if res.Output != "" {
		fmt.Fprintln(outW, res.Output)
	}
	if drift := verify.SnapshotDrift(preSnap, verify.SnapshotRepo(cwd)); len(drift) > 0 {
		fmt.Fprintf(errW, "[P0] zero-trust: read-only %s modified files: %s\n", slotName, strings.Join(drift, ", "))
		return 1
	}
	if res.ExitCode == 0 && !res.TimedOut {
		verify.SaveStageCache(cKey, verify.CacheEntry{
			Stage: stage, Lane: res.Lane, Model: res.Model, Effort: res.Effort, Output: res.Output,
		}, opts.NoCache)
		return 0
	}
	if res.ExitCode > 0 {
		return res.ExitCode
	}
	return 1
}

// DoOrient runs a read-only root-cause and architecture orientation pass.
func DoOrient(opts CLIOpts, outW, errW io.Writer) int {
	return runReadOnlyStage("orient", *opts.Orient, opts, outW, errW)
}

func printDiscoveryFooter(
	w io.Writer, caps map[string]envelope.LaneCap, skills []worktree.SkillMeta, verbose bool,
) {
	fmt.Fprintln(w, "\nDiscovered CLI Capabilities (effort control & available models):")
	groups := [][2]string{
		{"agy", "agy-claude"}, {"devin", "devin-pro"}, {"muse", "muse"},
		{"mcode", "mcode"}, {"grok", "grok"}, {"vibe", "mistral"},
	}
	limit, moreLbl := 6, "more; run --discover"
	if verbose {
		limit, moreLbl = 20, "in discovery.json"
	}
	for _, g := range groups {
		lc := caps[g[1]]
		shown, extra, guard := "(run agy-router --discover)", "", ""
		if len(lc.Models) > 0 {
			shown = strings.Join(lc.Models[:min(len(lc.Models), limit)], ", ")
		}
		if len(lc.Models) > limit {
			extra = fmt.Sprintf(" (+%d %s)", len(lc.Models)-limit, moreLbl)
		}
		if lc.GeminiGuard != "" {
			guard = " [" + lc.GeminiGuard + "]"
		}
		fmt.Fprintf(w, "  %-7s %-37s models: %s%s%s\n", g[0], nonEmptyOr(lc.EffortMode, "default"), shown, extra, guard)
	}
	fmt.Fprintln(w, "\nInjectable Worker Skills (--skill <name>):")
	if len(skills) == 0 {
		fmt.Fprintln(w, "  (none discovered)")
		return
	}
	for _, s := range skills {
		tag := ""
		if s.Core {
			tag = " [built-in]"
		}
		fmt.Fprintf(w, "  %-10s : %s%s\n", s.Name, nonEmptyOr(s.Desc, "(no description)"), tag)
	}
}
