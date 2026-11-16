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

func buildTaskSpec(
	id, rawPrompt, comp string, files []string, opts CLIOpts, cwd string, ro bool,
) worktree.TaskSpec {
	skills := worktree.ResolveTaskSkills(worktree.ParseCSVArg(opts.Skills), rawPrompt, files, cwd)
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
	}
	if q == nil && !opts.LaneSet {
		var qerr error
		if q, qerr = fleet.LoadQuota(""); qerr != nil {
			fmt.Fprintf(os.Stderr, "warning: %v\n", qerr)
		}
	}
	return worktree.ResolveStage(stage, cliLane, opts.Model, opts.Effort, opts.Profile, cwd, q)
}

func printStageMatrix(w io.Writer, cwd string, opts CLIOpts, q *fleet.QuotaState) {
	fmt.Fprintln(w, "\nOODA Stage Routing Matrix (precedence: CLI > --local > --profile > global > auto):")
	fmt.Fprintf(w, "%-9s %-14s %-34s %s\n", "STAGE", "MODE", "ROUTE", "ROLE")
	fmt.Fprintln(w, strings.Repeat("-", 96))
	roles := map[string]string{
		"observe": "Read-only recon (--recon)",
		"orient":  "Root-cause & architecture (--orient)",
		"act":     "Coding & worktree execution (--task / manifest)",
		"verify":  "Cross-model diff review (--review / post-task)",
	}
	for _, st := range fleet.StageNames {
		sp := cliResolveStage(st, opts, cwd, q)
		mode := sp.Source
		route := "auto (quota-aware)"
		if sp.Lane != "auto" {
			mod, eff := nonEmptyOr(sp.Model, "default"), nonEmptyOr(sp.Effort, "default")
			route = fmt.Sprintf("%s (%s/%s)", sp.Lane, dispatch.ShortModel(mod), eff)
			if sp.ExpiresAt != nil && *sp.ExpiresAt != "" {
				route += " [until reset]"
			}
		}
		fmt.Fprintf(w, "%-9s %-14s %-34s %s\n", st, mode, route, roles[st])
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
		fmt.Fprintf(outW, "  %-8s -> %s (model=%s, effort=%s)\n",
			st, r.Lane, derefOr(r.Model, "cli-default"), derefOr(r.Effort, "cli-default"))
	}
	return 0, true
}

// DoOrient runs a read-only root-cause and architecture orientation pass.
func DoOrient(opts CLIOpts, outW, errW io.Writer) int {
	_, _, files, err := validateRunArgs(opts, fleet.DefaultTimeout, true)
	if err != nil {
		fmt.Fprintln(errW, err)
		return 2
	}
	slot, err := worktree.EnsureSlot("orient")
	if err != nil {
		fmt.Fprintln(errW, err)
		return 1
	}
	cwd, ok := requireCWD(errW)
	if !ok {
		return 1
	}
	comp := nonEmptyOr(opts.Complexity, "deep")
	st := cliResolveStage("orient", opts, cwd, nil)
	opts.Lane, opts.Model, opts.Effort = st.Lane, st.Model, st.Effort
	task := buildTaskSpec("orient", *opts.Orient, comp, files, opts, cwd, true)
	res, err := dispatch.RunTaskWithFallback(dispatch.TaskReq{
		Task: task, Worktree: cwd, SlotDir: slot, Lane: st.Lane, Model: st.Model,
		Effort: st.Effort, TimeoutRaw: opts.Timeout,
		Exclude: worktree.ParseCSVArg(opts.ExcludeLanes), NoPivot: opts.NoPivot, SoftPin: st.Source != "cli", LogWriter: errW,
	})
	if err != nil {
		fmt.Fprintln(errW, err)
		return 2
	}
	fmt.Fprintf(errW, "[OODA:ORIENT lane=%s model=%s effort=%s %.1fs exit=%d]\n",
		res.Lane, res.Model, res.Effort, res.DurationS, res.ExitCode)
	if res.Output != "" {
		fmt.Fprintln(outW, res.Output)
	}
	if res.ExitCode >= 0 {
		return res.ExitCode
	}
	return 1
}

func printDiscoveryFooter(
	w io.Writer, caps map[string]envelope.LaneCap, skills []worktree.SkillMeta, verbose bool,
) {
	fmt.Fprintln(w, "\nDiscovered CLI Capabilities (effort control & available models):")
	groups := [][2]string{
		{"agy", "agy-opus"}, {"devin", "devin-pro"}, {"muse", "muse"},
		{"mcode", "mcode"}, {"grok", "grok"}, {"vibe", "mistral"},
	}
	limit, moreLbl := 6, "more; run --discover"
	if verbose {
		limit, moreLbl = 20, "in discovery.json"
	}
	for _, g := range groups {
		lc := caps[g[1]]
		shown, extra, guard := "(run agent-ooda --discover)", "", ""
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
