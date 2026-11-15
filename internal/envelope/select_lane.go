package envelope

import (
	"fmt"
	"strings"

	"github.com/UberMetroid/agent-ooda/internal/fleet"
)

// SelectReq parameterizes provider selection for a single routed task.
type SelectReq struct {
	RequestedLane string
	Complexity    string
	Quota         *fleet.QuotaState
	Exclude       map[string]bool
	StripeIndex   *int
	AvoidFamilies map[string]bool
	StrictAvoid   bool
	DeferAtCap    bool
	ResolveCLI    func(string) string
}

// SelectRes holds the chosen provider lane, config, CLI path, and skip notes.
type SelectRes struct {
	Lane        string
	Info        *fleet.ProviderConfig
	CLIPath     string
	SkippedNote string
}

func checkCandidate(
	name string, req SelectReq, depth int, resolver func(string) string,
) (SelectRes, string, bool) {
	if req.Exclude != nil && req.Exclude[name] {
		return SelectRes{}, name + ": already attempted", false
	}
	if name == "agy-opus" && depth >= 1 {
		return SelectRes{}, "agy-opus: blocked inside nested ROUTER_DEPTH>=1", false
	}
	if req.StrictAvoid && req.AvoidFamilies != nil && req.AvoidFamilies[ModelFamily(name)] {
		return SelectRes{}, name + ": excluded family (" + ModelFamily(name) + ")", false
	}
	var info *fleet.ProviderConfig
	if req.Quota != nil && req.Quota.Providers != nil {
		info = req.Quota.Providers[name]
	}
	if info == nil {
		return SelectRes{}, name + ": not configured", false
	}
	if info.Status != "active" {
		why := info.Status
		if info.Reason != nil && *info.Reason != "" {
			why = *info.Reason
		}
		return SelectRes{}, fmt.Sprintf("%s: %s (%s)", name, info.Status, why), false
	}
	cliPath := ""
	if info.CLI != "" {
		cliPath = resolver(info.CLI)
	}
	if info.CLI == "" || cliPath == "" {
		return SelectRes{}, fmt.Sprintf("%s: cli '%s' not installed", name, info.CLI), false
	}
	return SelectRes{Lane: name, Info: info, CLIPath: cliPath}, "", true
}

// SelectProvider chooses the first active, installed, non-full candidate lane.
func SelectProvider(req SelectReq) (SelectRes, error) {
	if req.Quota == nil {
		req.Quota = fleet.DefaultQuotaData()
	}
	laneArg := orStr(req.RequestedLane, "auto")
	lanes, err := fleet.ParseLanes(laneArg, true)
	if err != nil {
		return SelectRes{}, err
	}
	isAuto := len(lanes) == 1 && lanes[0] == "auto"
	cands, err := BuildCandidateOrder(laneArg, req.Complexity, req.Quota, req.StripeIndex, req.AvoidFamilies)
	if err != nil {
		return SelectRes{}, err
	}
	resolver := req.ResolveCLI
	if resolver == nil {
		resolver = fleet.ResolveCLI
	}
	depth := fleet.RouterDepth()
	var skipped []string
	var atCap []SelectRes
	for _, name := range cands {
		cand, skipMsg, ok := checkCandidate(name, req, depth, resolver)
		if !ok {
			skipped = append(skipped, skipMsg)
			continue
		}
		maxP := 0
		if cand.Info.MaxParallel != nil {
			maxP = *cand.Info.MaxParallel
		}
		if isAuto && req.DeferAtCap && maxP > 0 && fleet.CountActiveLeases(name, maxP) >= maxP {
			atCap = append(atCap, cand)
			skipped = append(skipped, fmt.Sprintf("%s: %d/%d parallel slots busy", name, maxP, maxP))
			continue
		}
		cand.SkippedNote = strings.Join(skipped, "; ")
		return cand, nil
	}
	if len(atCap) > 0 {
		pick := atCap[0]
		pick.SkippedNote = strings.Join(skipped, "; ")
		return pick, nil
	}
	return SelectRes{SkippedNote: orStr(strings.Join(skipped, "; "), "no configured providers")}, nil
}
