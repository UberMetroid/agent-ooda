package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/UberMetroid/agent-ooda/internal/envelope"
	"github.com/UberMetroid/agent-ooda/internal/fleet"
	"github.com/UberMetroid/agent-ooda/internal/worktree"
)

func readTUIKey(r io.Reader) string {
	var b [1]byte
	if n, err := r.Read(b[:]); err != nil || n == 0 {
		return "quit"
	}
	if b[0] == 0x1b {
		var b2, b3 [1]byte
		if n2, err2 := r.Read(b2[:]); err2 != nil || n2 == 0 || b2[0] == 0x1b {
			return "quit"
		}
		if b2[0] == '[' || b2[0] == 'O' {
			if n3, e3 := r.Read(b3[:]); e3 == nil && n3 == 1 {
				return map[byte]string{'A': "up", 'B': "down"}[b3[0]]
			}
		}
		return ""
	}
	return map[byte]string{
		'q': "quit", 'Q': "quit", 3: "quit", '\r': "enter", '\n': "enter", ' ': "enter",
		'\t': "tab", 'l': "tab", 'k': "up", 'K': "up", 'j': "down", 'J': "down",
	}[b[0]]
}

func tuiPick(in io.Reader, out io.Writer, title, hint string, items []string) (int, string) {
	idx := 0
	for {
		fmt.Fprintf(out, "\033[H\033[2J[AGENT-OODA v%s] %s\n", fleet.Version, title)
		if hint != "" {
			fmt.Fprintf(out, "  %s\n", hint)
		}
		fmt.Fprintln(out, strings.Repeat("-", 72))
		for i, item := range items {
			mark := "  "
			if i == idx {
				mark = "\033[1;36m▸ "
			}
			fmt.Fprintf(out, "%s%s\033[0m\n", mark, item)
		}
		fmt.Fprintln(out, "\n  [↑/↓ j/k] move  [Enter] select  [q] back/quit")
		switch readTUIKey(in) {
		case "up":
			idx = (idx - 1 + len(items)) % len(items)
		case "down":
			idx = (idx + 1) % len(items)
		case "enter":
			return idx, "select"
		case "tab":
			return idx, "tab"
		case "quit":
			return -1, "quit"
		}
	}
}

func tuiLaneModels(lane string, lc envelope.LaneCap) []string {
	out := []string{"cli-default (auto/configured)"}
	seen := map[string]bool{}
	for _, m := range lc.Models {
		if m != "" && !fleet.GeminiModelRe.MatchString(m) && !seen[m] {
			seen[m] = true
			out = append(out, m)
		}
	}
	if len(out) == 1 && lane != "auto" && lc.EffectiveModel != "" && !fleet.GeminiModelRe.MatchString(lc.EffectiveModel) {
		out = append(out, lc.EffectiveModel)
	}
	return out
}

func tuiLaneEfforts(lc envelope.LaneCap) []string {
	if strings.Contains(lc.EffortMode, "via-model") {
		return nil
	}
	return []string{"cli-default", "low", "medium", "high", "xhigh", "max"}
}

func tuiStageRows(cwd string, local bool, q *fleet.QuotaState) []string {
	ws, rows := worktree.LoadWorkspaceStages(cwd), make([]string, 0, 7)
	for _, st := range fleet.StageNames {
		r := q.Stages[st]
		if local {
			if lr, ok := ws[st]; ok {
				r = lr
			} else {
				r = fleet.StageRoute{Lane: "(inherit)"}
			}
		}
		lbl := nonEmptyOr(r.Lane, "auto")
		if r.Model != nil && *r.Model != "" {
			lbl += " · " + *r.Model
		}
		if r.Effort != nil && *r.Effort != "" {
			lbl += " (" + *r.Effort + ")"
		}
		rows = append(rows, fmt.Sprintf("%-8s -> %s", st, lbl))
	}
	return append(rows,
		"Apply Workflow Preset (auto/conserve/deep/devin/grok/mcode/muse/mistral)",
		"Reset All Stages in Scope to auto", "Save & Exit")
}

func tuiEditStage(
	in io.Reader, out io.Writer, stage string, local, defUntil bool, cwd string, q *fleet.QuotaState,
) error {
	caps := envelope.DiscoverFleetCaps(envelope.DiscoverOpts{Quota: q})
	musePayg, nowUTC := fleet.CachedMuseProbe(900).PayAsYouGo, time.Now().UTC()
	lanes, items := []string{"auto"}, []string{"auto (quota-aware routing)"}
	for _, name := range fleet.ProviderOrder {
		info := q.Providers[name]
		if info == nil || info.Tier == "credits" || (name == "muse" && musePayg) {
			continue
		}
		note := info.Status
		if cd := fleet.FormatCountdown(info.CooldownUntil, nowUTC); info.Status == "cooldown" && cd != "" {
			note = "cooldown " + cd
		}
		lanes = append(lanes, name)
		items = append(items, fmt.Sprintf("%-13s %-9s (%s)", name, info.Tier, note))
	}
	li, act := tuiPick(in, out, "Select Lane for "+stage, "", items)
	if act != "select" || li < 0 {
		return nil
	}
	chosenLane, mod, eff, untilReset := lanes[li], "", "", defUntil
	if chosenLane != "auto" {
		mods := tuiLaneModels(chosenLane, caps[chosenLane])
		if mi, mact := tuiPick(in, out, chosenLane+" Model", "", mods); mact == "select" && mi > 0 {
			mod = mods[mi]
		} else if mact != "select" {
			return nil
		}
		if effs := tuiLaneEfforts(caps[chosenLane]); len(effs) > 0 {
			if ei, eact := tuiPick(in, out, chosenLane+" Effort", "", effs); eact == "select" && ei > 0 {
				eff = effs[ei]
			} else if eact != "select" {
				return nil
			}
		}
		if !defUntil {
			pi, pact := tuiPick(in, out, "Pin Duration", "",
				[]string{"Sticky (keep until changed)", "Until current window resets"})
			if pact != "select" {
				return nil
			}
			untilReset = pi == 1
		}
	}
	_, err := worktree.ConfigureStage(worktree.StagePatch{
		Stage: stage, Lane: chosenLane, Model: mod, Effort: eff, UntilReset: untilReset, Local: local, Cwd: cwd,
	})
	return err
}

func openTUIInput() (*os.File, func(), error) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return nil, nil, err
	}
	stty := func(args ...string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "stty", args...)
		cmd.Stdin = tty
		return cmd.Output()
	}
	saved, serr := stty("-g")
	if serr != nil {
		_ = tty.Close()
		return nil, nil, serr
	}
	restore := func() {
		_, _ = stty(strings.TrimSpace(string(saved)))
		_ = tty.Close()
	}
	if _, rerr := stty("cbreak", "-echo", "-isig"); rerr != nil {
		restore()
		return nil, nil, rerr
	}
	return tty, restore, nil
}

// RunSelectTUI runs the arrow-key OODA stage/lane/model selector interactively.
func RunSelectTUI(opts CLIOpts, in io.Reader, outW, errW io.Writer) int {
	if outW == nil {
		outW = os.Stdout
	}
	cwd, ok := requireCWD(errW)
	if !ok {
		return 1
	}
	if in == nil {
		tty, restore, err := openTUIInput()
		if err != nil {
			fmt.Fprintf(errW, "error: --select requires an interactive TTY (%v); use --set-stage instead\n", err)
			return 2
		}
		defer restore()
		in, outW = tty, tty
	}
	local := opts.Local
	for {
		q, err := fleet.LoadQuota("")
		if err != nil {
			fmt.Fprintln(errW, err)
			return 1
		}
		scope := "GLOBAL (quota.json)"
		if local {
			scope = "REPO-LOCAL (" + worktree.WorkspaceStagesPath(cwd) + ")"
		}
		idx, act := tuiPick(in, outW, "OODA Stage Matrix", "Scope: "+scope+"  [Tab: toggle scope]",
			tuiStageRows(cwd, local, q))
		if act == "tab" {
			local = !local
			continue
		}
		if act != "select" || idx == 6 {
			return 0
		}
		var stepErr error
		switch {
		case idx < 4:
			stepErr = tuiEditStage(in, outW, fleet.StageNames[idx], local, opts.UntilReset, cwd, q)
		case idx == 4:
			names := []string{"auto", "conserve", "deep", "devin", "grok", "mcode", "muse", "mistral"}
			if pi, pact := tuiPick(in, outW, "Apply Workflow Preset", "", names); pact == "select" && pi >= 0 {
				_, stepErr = worktree.ConfigureStage(
					worktree.StagePatch{Stage: "preset", Lane: names[pi], Local: local, Cwd: cwd})
			}
		case idx == 5:
			_, stepErr = worktree.ConfigureStage(worktree.StagePatch{Stage: "all", Lane: "auto", Local: local, Cwd: cwd})
		}
		if stepErr != nil {
			fmt.Fprintln(errW, stepErr)
			return 2
		}
	}
}
