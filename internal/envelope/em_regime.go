// Package envelope implements E-M energy envelopes, lane routing, and discovery.
package envelope

import (
	"sort"
	"time"

	"github.com/UberMetroid/agent-ooda/internal/fleet"
)

// EMEnvelope summarizes active/corner capped lanes and fleet conservation mode.
type EMEnvelope struct {
	NowUTC         time.Time
	ActiveCapped   []string
	CornerLanes    []string
	SoonestRegenS  *float64
	ConserveCapped bool
	Regimes        map[string]string
}

type cornerItem struct {
	remS float64
	idx  int
	name string
}

// ModelFamily maps a provider lane to its underlying model family name.
func ModelFamily(provider string) string {
	if fam, ok := fleet.ModelFamilies[provider]; ok {
		return fam
	}
	return provider
}

func scanProviders(
	providers map[string]*fleet.ProviderConfig, nowUTC time.Time,
) ([]string, []string, *float64) {
	var activeCapped []string
	var corners []cornerItem
	var soonest *float64
	names := make([]string, 0, len(providers))
	for k := range providers {
		names = append(names, k)
	}
	sort.Strings(names)
	for idx, name := range names {
		info := providers[name]
		if info == nil {
			continue
		}
		if info.Tier == "capped" && info.Status == "active" {
			activeCapped = append(activeCapped, name)
			rem := remainingSec(info.NextResetAt, info.CooldownUntil, nowUTC)
			if rem != nil && *rem > 0 && *rem <= fleet.CornerWindowSecs {
				corners = append(corners, cornerItem{remS: *rem, idx: idx, name: name})
			}
			continue
		}
		if info.Status != "active" && !info.ManualDisable {
			rem := remainingSec(info.CooldownUntil, info.NextResetAt, nowUTC)
			if rem != nil && *rem > 0 && (soonest == nil || *rem < *soonest) {
				v := *rem
				soonest = &v
			}
		}
	}
	sort.Slice(corners, func(i, j int) bool {
		if corners[i].remS != corners[j].remS {
			return corners[i].remS < corners[j].remS
		}
		return corners[i].name < corners[j].name
	})
	cornerNames := make([]string, len(corners))
	for i, c := range corners {
		cornerNames[i] = c.name
	}
	return activeCapped, cornerNames, soonest
}

func remainingSec(primary, fallback *string, nowUTC time.Time) *float64 {
	ts := ""
	if primary != nil && *primary != "" {
		ts = *primary
	} else if fallback != nil && *fallback != "" {
		ts = *fallback
	}
	if ts == "" {
		return nil
	}
	dt, ok := fleet.ParseISOUTC(ts)
	if !ok {
		return nil
	}
	rem := dt.Sub(nowUTC).Seconds()
	return &rem
}

func assignRegimes(
	providers map[string]*fleet.ProviderConfig, cornerSet map[string]bool, conserve bool,
) map[string]string {
	regimes := make(map[string]string, len(providers))
	for name, info := range providers {
		if info == nil {
			continue
		}
		switch {
		case info.Status != "active":
			regimes[name] = "REGEN"
		case info.Tier == "credits":
			regimes[name] = "RESERVE"
		case cornerSet[name]:
			regimes[name] = "CORNER"
		case info.Tier == "capped" && conserve:
			regimes[name] = "CONSERVE"
		default:
			regimes[name] = "CRUISE"
		}
	}
	return regimes
}

// ComputeEMEnvelope classifies the fleet into CORNER/CRUISE/CONSERVE/RESERVE/REGEN.
func ComputeEMEnvelope(q *fleet.QuotaState, nowUTC time.Time) EMEnvelope {
	if nowUTC.IsZero() {
		nowUTC = time.Now().UTC()
	}
	activeCapped, corners, soonest := scanProviders(q.Providers, nowUTC)
	conserve := len(activeCapped) <= 1 && len(corners) == 0 &&
		(soonest == nil || *soonest > fleet.SoonWakeSecs)
	cset := make(map[string]bool, len(corners))
	for _, c := range corners {
		cset[c] = true
	}
	return EMEnvelope{
		NowUTC:         nowUTC,
		ActiveCapped:   activeCapped,
		CornerLanes:    corners,
		SoonestRegenS:  soonest,
		ConserveCapped: conserve,
		Regimes:        assignRegimes(q.Providers, cset, conserve),
	}
}
