package cli

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/UberMetroid/agent-ooda/internal/fleet"
)

// CLIOpts holds parsed command-line options for the ooda / router CLI.
type CLIOpts struct {
	Manifest, Complexity, Lane, Model, Effort, Timeout        string
	Enable, Disable, SetCycle, Reason                         string
	Recon, Task, Review                                       *string
	Files, Skills, ExcludeLanes                               []string
	Jobs                                                      int
	Hours, CostBudget                                         *float64
	TokenBudget, MaxParallel                                  *int
	NoStripe, NoPivot, NoReview, Status                       bool
	Discover, Usage, Probe, AutoWake, ResetNow, Cleanup, Help bool
}

func usageText(prog string) string {
	return fmt.Sprintf(
		"usage: %s [--status | --discover | --usage | --probe | --recon PROMPT | --task PROMPT |\n"+
			"             --review [PATH] | --cleanup | --enable P | --disable P | --set-cycle P | manifest]\n\n"+
			"options:\n"+
			"  -h, --help             show this help message and exit\n"+
			"  --recon/--task PROMPT  read-only recon or coding task in CWD\n"+
			"  --review [PATH]        standalone read-only cross-model review (default CWD)\n"+
			"  --files / --skill CSV  scoped paths / skills to inject (repeatable)\n"+
			"  --complexity HINT      heavy, deep, architecture, fast, tests, recon, review\n"+
			"  --lane / --exclude-lane  auto or comma-separated lanes\n"+
			"  --model / --effort     override worker model id or reasoning effort\n"+
			"  --timeout DUR          per-task timeout (e.g. 120s, 8m)\n"+
			"  -j, --jobs, --slots N  max parallel workers for manifest mode (default %d, max %d)\n"+
			"  --no-stripe/--no-pivot/--no-review  control striping, family pivot, post-task verify\n"+
			"  --status/--discover/--usage/--probe  fleet readiness, CLI cache, telemetry, canary\n"+
			"  --enable/--disable/--set-cycle P  quota/cycle admin (--auto-wake, --reason, --hours,\n"+
			"                         --token-budget, --cost-budget, --max-parallel, --reset-now)\n"+
			"  --cleanup              remove .worktrees and ooda/* branches\n",
		prog, fleet.DefaultJobs, fleet.MaxJobs,
	)
}

func takeArg(argv []string, idx *int, hasEq bool, eqVal, flag string) (string, error) {
	if hasEq {
		return eqVal, nil
	}
	if *idx+1 >= len(argv) {
		return "", fleet.UsageErr("error: argument %s: expected one argument", flag)
	}
	*idx++
	return argv[*idx], nil
}

func applyNumFlag(opts *CLIOpts, name, val string) error {
	switch name {
	case "--jobs", "-j", "--slots":
		n, err := strconv.Atoi(val)
		if err != nil {
			return fleet.UsageErr("error: %s: invalid integer %q", name, val)
		}
		opts.Jobs = n
	case "--token-budget":
		n, err := strconv.Atoi(val)
		if err != nil {
			return fleet.UsageErr("error: --token-budget: invalid integer %q", val)
		}
		opts.TokenBudget = &n
	case "--max-parallel":
		n, err := strconv.Atoi(val)
		if err != nil {
			return fleet.UsageErr("error: --max-parallel: invalid integer %q", val)
		}
		opts.MaxParallel = &n
	case "--hours":
		f, err := strconv.ParseFloat(val, 64)
		if err != nil {
			return fleet.UsageErr("error: --hours: invalid number %q", val)
		}
		opts.Hours = &f
	case "--cost-budget":
		f, err := strconv.ParseFloat(val, 64)
		if err != nil {
			return fleet.UsageErr("error: --cost-budget: invalid number %q", val)
		}
		opts.CostBudget = &f
	}
	return nil
}

func applyStrFlag(opts *CLIOpts, name, val string) error {
	switch name {
	case "--lane":
		opts.Lane = val
	case "--exclude-lane":
		opts.ExcludeLanes = append(opts.ExcludeLanes, val)
	case "--model":
		opts.Model = val
	case "--effort":
		opts.Effort = val
	case "--timeout":
		opts.Timeout = val
	case "--recon":
		opts.Recon = fleet.Ptr(val)
	case "--task":
		opts.Task = fleet.Ptr(val)
	case "--files":
		opts.Files = append(opts.Files, val)
	case "--skill", "--skills":
		opts.Skills = append(opts.Skills, val)
	case "--complexity":
		opts.Complexity = val
	case "--enable":
		opts.Enable = val
	case "--disable":
		opts.Disable = val
	case "--reason":
		opts.Reason = val
	case "--set-cycle":
		opts.SetCycle = val
	default:
		return applyNumFlag(opts, name, val)
	}
	return nil
}

func applyBoolFlag(opts *CLIOpts, name string) bool {
	switch name {
	case "--no-stripe":
		opts.NoStripe = true
	case "--no-pivot":
		opts.NoPivot = true
	case "--no-review":
		opts.NoReview = true
	case "--status":
		opts.Status = true
	case "--discover":
		opts.Discover = true
	case "--usage":
		opts.Usage = true
	case "--probe":
		opts.Probe = true
	case "--auto-wake":
		opts.AutoWake = true
	case "--reset-now":
		opts.ResetNow = true
	case "--cleanup":
		opts.Cleanup = true
	case "-h", "--help":
		opts.Help = true
	default:
		return false
	}
	return true
}

func consumeFlag(opts *CLIOpts, argv []string, i *int) error {
	arg := argv[*i]
	if strings.HasPrefix(arg, "-j") && len(arg) > 2 && arg[2] != '=' {
		return applyNumFlag(opts, "-j", arg[2:])
	}
	name, eqVal, hasEq := strings.Cut(arg, "=")
	if !hasEq && applyBoolFlag(opts, name) {
		return nil
	}
	if name == "--review" {
		if hasEq {
			opts.Review = fleet.Ptr(eqVal)
			return nil
		}
		if *i+1 < len(argv) && !strings.HasPrefix(argv[*i+1], "-") {
			*i++
			opts.Review = fleet.Ptr(argv[*i])
			return nil
		}
		opts.Review = fleet.Ptr(".")
		return nil
	}
	knownVal := map[string]bool{
		"--lane": true, "--exclude-lane": true, "--model": true, "--effort": true, "--timeout": true,
		"--recon": true, "--task": true, "--files": true, "--skill": true, "--skills": true, "--complexity": true,
		"--jobs": true, "-j": true, "--slots": true, "--enable": true, "--disable": true, "--reason": true,
		"--hours": true, "--set-cycle": true, "--token-budget": true, "--cost-budget": true, "--max-parallel": true,
	}
	if !knownVal[name] {
		return fleet.UsageErr("error: unrecognized arguments: %s", arg)
	}
	val, err := takeArg(argv, i, hasEq, eqVal, name)
	if err != nil {
		return err
	}
	return applyStrFlag(opts, name, val)
}

func validateModes(opts CLIOpts) error {
	if opts.Recon != nil && opts.Task != nil {
		return fleet.UsageErr("error: --recon and --task are mutually exclusive")
	}
	quotaAdmin := opts.Enable != "" || opts.Disable != "" || opts.SetCycle != ""
	modes := 0
	for _, on := range []bool{
		opts.Status, opts.Discover, opts.Usage, opts.Probe, quotaAdmin,
		opts.Cleanup, opts.Recon != nil, opts.Task != nil, opts.Review != nil,
	} {
		if on {
			modes++
		}
	}
	if modes > 1 {
		return fleet.UsageErr("error: conflicting mode flags; specify one action")
	}
	if opts.Manifest != "" && modes > 0 && opts.Review == nil {
		return fleet.UsageErr("error: unexpected positional manifest %q with mode flag", opts.Manifest)
	}
	return nil
}

// ParseArgs parses ooda / router CLI flags and positional manifest path.
func ParseArgs(prog string, argv []string, helpOut io.Writer) (CLIOpts, error) {
	opts := CLIOpts{Lane: "auto", Jobs: fleet.DefaultJobs}
	for i := 0; i < len(argv); i++ {
		arg := argv[i]
		if arg == "--" {
			for j := i + 1; j < len(argv); j++ {
				if opts.Manifest != "" {
					return opts, fleet.UsageErr("error: unrecognized arguments: %s", argv[j])
				}
				opts.Manifest = argv[j]
			}
			break
		}
		if strings.HasPrefix(arg, "-") && arg != "-" {
			if err := consumeFlag(&opts, argv, &i); err != nil {
				return opts, err
			}
			continue
		}
		if opts.Manifest != "" {
			return opts, fleet.UsageErr("error: unrecognized arguments: %s", arg)
		}
		opts.Manifest = arg
	}
	if opts.Help {
		if helpOut == nil {
			helpOut = os.Stdout
		}
		_, _ = io.WriteString(helpOut, usageText(prog))
		return opts, nil
	}
	return opts, validateModes(opts)
}
