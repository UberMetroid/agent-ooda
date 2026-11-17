package envelope

import (
	"os"
	"regexp"
	"strings"

	"github.com/UberMetroid/agy-router/internal/fleet"
)

var tierSuffixRe = regexp.MustCompile(`-(low|medium|high|xhigh|max)$`)

// LaneCap summarizes discovered capabilities and resolved defaults for one lane.
type LaneCap struct {
	DispatchModel   string
	EffortMode      string
	EffectiveModel  string
	EffectiveEffort string
	Models          []string
	AliasEfforts    map[string]string
	GeminiGuard     string
	AgyUnsafe       bool
}

// LaneSpec holds final argv and display model/effort values for worker launch.
type LaneSpec struct {
	ArgvModel     string
	DisplayModel  string
	ArgvEffort    string
	DisplayEffort string
}

// DiscoverOpts configures DiscoverFleetCaps execution.
type DiscoverOpts struct {
	Quota        *fleet.QuotaState
	Home         string
	ForceRefresh bool
	ProbeAgy     bool
}

func pickAgyWorkerModel(agy AgySettings, models []string, effort string) (string, string, string, bool) {
	var clean []string
	for _, m := range models {
		base := strings.SplitN(m, "[", 2)[0]
		if !IsGeminiModel(base) && base != "" {
			clean = append(clean, base)
		}
	}
	sm := agy.SettingsModel
	if sm != "" && !agy.GeminiBlocked && !strings.Contains(sm, " ") {
		eff := "default"
		if em := tierSuffixRe.FindStringSubmatch(strings.ToLower(sm)); len(em) == 2 {
			eff = em[1]
		}
		return "", sm, eff, false
	}
	tier := strings.ToLower(effort)
	if tier != "low" && tier != "medium" && tier != "high" {
		tier = "high"
	}
	matches := []func(string) bool{
		func(m string) bool { return strings.HasPrefix(m, "claude-opus-") && strings.HasSuffix(m, "-"+tier) },
		func(m string) bool { return strings.HasPrefix(m, "claude-opus-") && strings.HasSuffix(m, "-high") },
		func(m string) bool { return strings.HasPrefix(m, "claude-opus-") },
		func(m string) bool { return strings.HasPrefix(m, "claude-") },
		func(string) bool { return true },
	}
	for _, pred := range matches {
		for _, m := range clean {
			if !pred(m) {
				continue
			}
			eff := "default"
			if em := tierSuffixRe.FindStringSubmatch(strings.ToLower(m)); len(em) == 2 {
				eff = em[1]
			}
			return m, m, eff, false
		}
	}
	return "", "", "default", agy.GeminiBlocked
}

func buildLaneCap(
	lane string, info *fleet.ProviderConfig, agy AgySettings, devin DevinConfig,
	singles map[string]CLIConfig, cliMods map[string][]string,
) LaneCap {
	pinM, pinE := "", ""
	if info != nil {
		pinM, pinE = ptrVal(info.Model), ptrVal(info.Effort)
	}
	if lane == "agy-opus" {
		dm, em, ee, unsafe := pickAgyWorkerModel(agy, cliMods["agy"], pinE)
		guard := ""
		if agy.GeminiBlocked {
			guard = "settings='" + agy.SettingsModel + "' blocked"
		}
		effM := orStr(pinM, em)
		if effM == "" && unsafe {
			effM = "BLOCKED(gemini)"
		}
		return LaneCap{
			DispatchModel: orStr(pinM, dm), EffortMode: agy.EffortMode,
			EffectiveModel: orStr(effM, "default"), EffectiveEffort: orStr(pinE, ee),
			Models: cliMods["agy"], AliasEfforts: map[string]string{},
			GeminiGuard: guard, AgyUnsafe: unsafe && pinM == "",
		}
	}
	if strings.HasPrefix(lane, "devin-") {
		dm := devin.LaneModels[lane]
		return LaneCap{
			DispatchModel: orStr(pinM, dm), EffortMode: devin.EffortMode,
			EffectiveModel:  orStr(orStr(pinM, dm), "default"),
			EffectiveEffort: orStr(orStr(pinE, devin.LaneEfforts[lane]), "default"),
			Models:          dedupStrings(append(append([]string(nil), devin.Models...), cliMods["devin"]...)),
			AliasEfforts:    map[string]string{},
		}
	}
	cfg := singles[lane]
	effM := orStr(orStr(pinM, cfg.ActiveModel), "default")
	mods := cfg.Models
	if lane == "grok" {
		mods = dedupStrings(append(append([]string(nil), mods...), cliMods["grok"]...))
	}
	return LaneCap{
		DispatchModel: pinM, EffortMode: cfg.EffortMode, EffectiveModel: effM,
		EffectiveEffort: orStr(pinE, orStr(cfg.AliasEfforts[effM], orStr(cfg.ActiveEffort, "default"))),
		Models:          mods, AliasEfforts: cfg.AliasEfforts,
	}
}

func ptrVal(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func orStr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// DiscoverFleetCaps combines local CLI configs, model caches, and Gemini guards.
func DiscoverFleetCaps(opts DiscoverOpts) map[string]LaneCap {
	q := opts.Quota
	if q == nil {
		q, _ = fleet.LoadQuota("")
		if q == nil {
			q = fleet.DefaultQuotaData()
		}
	}
	agy, devin := ProbeAgySettings(opts.Home), ProbeDevinConfig(opts.Home)
	singles := map[string]CLIConfig{
		"muse": ProbeMuseConfig(opts.Home), "mcode": ProbeMinimaxConfig(opts.Home),
		"grok": ProbeGrokConfig(opts.Home), "mistral": ProbeVibeConfig(opts.Home),
	}
	var cliMods map[string][]string
	hasDiscEnv := os.Getenv("AGY_ROUTER_DISCOVERY_FILE") != "" ||
		os.Getenv("AGENT_OODA_DISCOVERY_FILE") != "" || os.Getenv("OODA_DISCOVERY_FILE") != ""
	if opts.Home == "" || hasDiscEnv {
		cliMods = LoadDiscoveryCache(opts.ForceRefresh, opts.ProbeAgy)
	}
	caps := make(map[string]LaneCap, len(fleet.ProviderOrder))
	for _, lane := range fleet.ProviderOrder {
		caps[lane] = buildLaneCap(lane, q.Providers[lane], agy, devin, singles, cliMods)
	}
	return caps
}

// EffectiveLaneSpec resolves argv and display model/effort with Gemini guardrail.
func EffectiveLaneSpec(
	provider string, info *fleet.ProviderConfig, expModel, expEffort, complexity, home string,
) (LaneSpec, error) {
	rawModel := orStr(expModel, orStr(os.Getenv("AGY_ROUTER_MODEL"), orStr(os.Getenv("AGENT_MODEL"), os.Getenv("OODA_MODEL"))))
	if rawModel == "" && info != nil && info.Model != nil {
		rawModel = *info.Model
	}
	normModel, err := fleet.NormalizeModel(provider, rawModel)
	if err != nil {
		return LaneSpec{}, err
	}
	explicitEff := fleet.ResolveEffort(info, expEffort)
	needAgy := provider == "agy-opus" && normModel == ""
	cap := DiscoverFleetCaps(DiscoverOpts{Home: home, ProbeAgy: needAgy})[provider]
	regMod, regEff := ResolveRegimeSpec(provider, complexity, cap)
	argvModel := orStr(normModel, orStr(regMod, cap.DispatchModel))
	effChoice := orStr(explicitEff, regEff)
	if needAgy && effChoice != "" && normModel == "" && regMod == "" {
		for _, m := range cap.Models {
			if strings.HasPrefix(m, "claude-opus-") && strings.HasSuffix(m, "-"+effChoice) {
				argvModel = m
				break
			}
		}
	}
	dispModel := orStr(argvModel, orStr(cap.EffectiveModel, "default"))
	inferred := ""
	if provider == "agy-opus" || strings.HasPrefix(provider, "devin-") {
		if em := tierSuffixRe.FindStringSubmatch(strings.ToLower(dispModel)); len(em) == 2 {
			inferred = em[1]
		}
	}
	argvEff := explicitEff
	if argvEff == "" && (provider == "mcode" || provider == "grok" || provider == "muse") {
		argvEff = regEff
	}
	effFb := orStr(inferred, orStr(regEff, orStr(cap.EffectiveEffort, "default")))
	dispEff := orStr(explicitEff, orStr(cap.AliasEfforts[dispModel], effFb))
	return LaneSpec{
		ArgvModel: argvModel, DisplayModel: dispModel,
		ArgvEffort: argvEff, DisplayEffort: dispEff,
	}, nil
}
