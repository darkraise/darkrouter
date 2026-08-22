package router

import (
	"errors"

	"github.com/darkraise/darkrouter/internal/provider"
)

// The empty-result cases are separate sentinels because they are separate
// problems with separate fixes. Collapsing them into one "no candidates" error
// turns a five-second diagnosis into a hunt.
var (
	ErrModelNotFound         = errors.New("no provider offers this model")
	ErrSurfaceUnsupported    = errors.New("no provider offers this model on this surface")
	ErrAllCooling            = errors.New("every provider offering this model is cooling")
	ErrCapabilityUnsatisfied = errors.New("no provider offering this model has the required capabilities")
)

// Resolve produces the ordered candidate sequence and the skips explaining
// every target that did not make it.
//
// It is a pure function of its inputs: no clock, no database, no network. The
// evaluation instant and resolved health arrive in the snapshot, which is what
// makes the time-dependent cooling cases testable without fixtures.
//
// The returned list is not truncated to policy.retry.max_attempts. The trace
// records what was eligible; the attempt loop decides how much of it to spend.
func Resolve(q Query, snap Snapshot) ([]Candidate, []Skip, error) {
	byID := make(map[string]provider.Provider, len(snap.Providers))
	providerIDs := make(map[string]bool, len(snap.Providers))
	for _, p := range snap.Providers {
		byID[p.ID] = p
		providerIDs[p.ID] = true
	}

	var aliases map[string][]string
	if snap.Config != nil {
		aliases = snap.Config.Aliases
	}

	targets, _ := resolveModel(q.Model, aliases, providerIDs, snap.Catalog)
	if len(targets) == 0 {
		return nil, nil, ErrModelNotFound
	}

	var cands []Candidate
	var skips []Skip
	for _, t := range targets {
		c, s, _ := filterTarget(t, q, snap, byID)
		cands = append(cands, c...)
		skips = append(skips, s...)
	}

	if len(cands) == 0 {
		return nil, skips, emptyReason(skips)
	}
	return cands, skips, nil
}

// emptyReason picks the error that best explains an empty candidate list.
//
// Cooling wins only when it explains *everything*: a mixed result where one
// provider lacks the surface and another is cooling is a configuration problem
// wearing a health problem's clothes, and reporting "cooling" would send the
// operator to the wrong place.
func emptyReason(skips []Skip) error {
	if len(skips) == 0 {
		return ErrModelNotFound
	}
	allCooling := true
	sawSurface, sawCapability := false, false
	for _, s := range skips {
		switch s.Reason {
		case SkipCooling:
		case SkipSurface:
			allCooling, sawSurface = false, true
		case SkipCapability:
			allCooling, sawCapability = false, true
		default:
			allCooling = false
		}
	}
	switch {
	case allCooling:
		return ErrAllCooling
	case sawSurface:
		return ErrSurfaceUnsupported
	case sawCapability:
		return ErrCapabilityUnsatisfied
	default:
		return ErrModelNotFound
	}
}
