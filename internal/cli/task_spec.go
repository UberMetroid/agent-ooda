package cli

import (
	"fmt"
	"io"
	"strings"

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
		prompt = fleet.ReconPrefix + rawPrompt
	}
	return worktree.TaskSpec{
		ID: id, Prompt: prompt, Files: files, Skills: skills,
		Lane: opts.Lane, Complexity: comp, Model: opts.Model, ReadOnly: ro,
	}
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
		shown, extra, guard := "(run router --discover)", "", ""
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
