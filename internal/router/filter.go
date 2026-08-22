package router

import (
	"github.com/darkraise/darkrouter/internal/catalog"
	"github.com/darkraise/darkrouter/internal/health"
	"github.com/darkraise/darkrouter/internal/provider"
)

// filterTarget turns one resolved target into candidates, recording a Skip for
// every rejection. The bool reports whether the provider exists at all, which
// the caller needs to distinguish "no such provider" from "nothing survived".
//
// The order of the checks decides which reason is recorded when several apply.
// Durable configuration problems are reported ahead of transient ones, because
// the skips are what an operator reads to work out why nothing routed — and
// "cooling" sends them looking at health when the real problem is that the
// model was never offered on that surface.
func filterTarget(t target, q Query, snap Snapshot,
	byID map[string]provider.Provider) ([]Candidate, []Skip, bool) {

	p, ok := byID[t.ProviderID]
	if !ok {
		// Either the provider does not exist or it is disabled; the snapshot
		// only carries enabled providers, so the two are indistinguishable here
		// and "disabled" is the honest label for both.
		return nil, []Skip{{ProviderID: t.ProviderID, Model: t.ModelID, Reason: SkipDisabled}}, false
	}

	m, known := snap.Catalog.Lookup(t.ProviderID, t.ModelID)
	if !known || !m.DeclaresSurface(q.Surface) {
		return nil, []Skip{{ProviderID: t.ProviderID, Model: t.ModelID, Reason: SkipSurface}}, true
	}

	if !m.Capabilities.Satisfies(catalog.Capabilities{
		Tools: q.NeedsTools, Vision: q.NeedsVision, Reasoning: q.NeedsReasoning,
	}) {
		return nil, []Skip{{ProviderID: t.ProviderID, Model: t.ModelID, Reason: SkipCapability}}, true
	}

	if len(p.Credentials) == 0 {
		return nil, []Skip{{ProviderID: t.ProviderID, Model: t.ModelID, Reason: SkipNoCredential}}, true
	}

	var cands []Candidate
	var skips []Skip
	for _, c := range orderCredentials(p.ID, p.Credentials, snap.LastUsed) {
		k := health.Key{ProviderID: p.ID, KeyID: c.ID, Model: t.ModelID}
		if !snap.Health.Available(k) {
			skips = append(skips, Skip{
				ProviderID: p.ID, KeyID: c.ID, Model: t.ModelID, Reason: SkipCooling,
			})
			continue
		}
		cands = append(cands, Candidate{
			ProviderID: p.ID, KeyID: c.ID, Model: t.ModelID, Kind: p.Kind,
		})
	}
	return cands, skips, true
}
