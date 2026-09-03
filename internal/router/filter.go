package router

import (
	"github.com/darkraise/darkrouter/internal/auth"
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

	// A model a successful listing omitted three times running is gone. A 404
	// classifies as RetryableModel and never penalizes the provider, so without
	// this the wasted attempt happens on every request, forever.
	if !m.Routable() {
		return nil, []Skip{{ProviderID: t.ProviderID, Model: t.ModelID, Reason: SkipRemoved}}, true
	}

	// Access the vendor has not sanctioned is a risk the operator has to accept
	// deliberately. The model keeps its place in the catalogue either way; what
	// the opt-in gates is production traffic being sent through it.
	if m.FreeTier.Unsanctioned() && !p.AllowUnsanctionedFree {
		return nil, []Skip{{
			ProviderID: t.ProviderID, Model: t.ModelID, Reason: SkipUnsanctioned,
		}}, true
	}

	// The catalog said the upstream offers this surface; this asks whether
	// Darkrouter can render it. Spec §4 makes an unimplemented surface a
	// routing filter rather than a runtime error, because an operator reading
	// "no provider offers this model on this surface" learns more than one
	// reading a 404 the provider produced.
	if q.Surface != "" {
		// A kind with no entry is one whose adapter is not registered at all.
		// That is a different fact — the executor reports it as no_adapter once
		// it reaches the loop — and answering it here would relabel it as a
		// surface gap the operator cannot act on.
		if ss, known := snap.AdapterSurfaces[p.Kind]; known && !ss.Has(q.Surface) {
			return nil, []Skip{{
				ProviderID: t.ProviderID, Model: t.ModelID, Reason: SkipAdapterSurface,
			}}, true
		}
	}

	if !m.Capabilities.Satisfies(catalog.Capabilities{
		Tools: q.NeedsTools, Vision: q.NeedsVision, Reasoning: q.NeedsReasoning,
	}) {
		return nil, []Skip{{ProviderID: t.ProviderID, Model: t.ModelID, Reason: SkipCapability}}, true
	}

	// A keyless provider is served by one attempt carrying no credential.
	// Everything below is keyed on a credential id, and the empty string is
	// the honest one to key on: there is no credential to rotate, to cool
	// independently, or to name in a trace.
	creds := orderCredentials(p.ID, p.Credentials, snap.LastUsed)
	if len(creds) == 0 {
		if !Keyless(p) {
			return nil, []Skip{{
				ProviderID: t.ProviderID, Model: t.ModelID, Reason: SkipNoCredential,
			}}, true
		}
		creds = []provider.Credential{{Enabled: true}}
	}

	var cands []Candidate
	var skips []Skip
	for _, c := range creds {
		k := health.Key{ProviderID: p.ID, KeyID: c.ID, Model: t.ModelID}
		if !snap.Health.Available(k) {
			skips = append(skips, Skip{
				ProviderID: p.ID, KeyID: c.ID, Model: t.ModelID, Reason: SkipCooling,
			})
			continue
		}
		cands = append(cands, Candidate{
			ProviderID: p.ID, KeyID: c.ID, Model: t.ModelID, Kind: p.Kind,
			// Declared in phase 3 and never populated until phase 8. Without
			// it every Vertex request takes the Google builder regardless of
			// the model, and every Claude call 400s.
			Publisher: m.Publisher,
			// Recorded per candidate rather than per request: a chain can mix
			// a models.dev-backed model with a locally discovered one, and the
			// warning belongs to whichever actually served.
			Inferred: !m.Capabilities.Known,
		})
	}
	return cands, skips, true
}

// Keyless reports whether this provider is reached with no credential at all.
//
// A local runtime on the loopback interface and a public keyless gateway both
// answer an unauthenticated request, and requiring an invented secret to route
// to one is a configuration step that protects nothing. The style is read off
// the provider row rather than its preset: the row is what the source builds
// from, it is NOT NULL in the schema, and an operator who overrode the style is
// the authority on how their own endpoint is reached.
func Keyless(p provider.Provider) bool {
	return auth.IsKeyless(p.AuthStyle)
}
