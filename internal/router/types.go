// Package router decides, deterministically, which targets a request may be
// attempted against and in what order.
package router

import (
	"time"

	"github.com/darkraise/darkrouter/internal/catalog"
	"github.com/darkraise/darkrouter/internal/config"
	"github.com/darkraise/darkrouter/internal/health"
	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/provider"
)

// Query is what the request asks for.
type Query struct {
	Model          string
	Surface        ir.Surface
	NeedsTools     bool
	NeedsVision    bool
	NeedsReasoning bool
}

// Snapshot is every input Resolve is allowed to read. It carries an evaluation
// instant and health already resolved to booleans, because filtering on
// cooling_until means "no clock" is otherwise impossible — and reading
// time.Now() inside Resolve would destroy both purity and reproducibility.
//
// It carries providers *with their credentials* because Candidate.KeyID cannot
// be produced from a model catalog alone.
type Snapshot struct {
	At        time.Time
	Providers []provider.Provider
	Catalog   catalog.Reader
	Config    *config.Config
	Health    health.Availability
	LastUsed  map[health.CredKey]time.Time
}

// Candidate is one attemptable target.
type Candidate struct {
	ProviderID string
	KeyID      string
	Model      string
	Kind       string
	Publisher  string // vertex only; empty in phase 3

	// Inferred marks a candidate admitted on guessed capability metadata.
	// Master design §6.4 admits these rather than excluding them, and the
	// executor records a warning so the trace explains why a provider's own
	// error came back instead of a routing decision.
	Inferred bool
}

// SkipReason explains why a target did not become a candidate.
type SkipReason string

const (
	SkipDisabled     SkipReason = "disabled"
	SkipCooling      SkipReason = "cooling"
	SkipSurface      SkipReason = "surface"
	SkipCapability   SkipReason = "capability"
	SkipNoCredential SkipReason = "no_credential"
	// SkipRemoved is a model a successful listing omitted three times running.
	// It is a durable fact about the upstream rather than a health signal,
	// which is why it is reported ahead of cooling.
	SkipRemoved SkipReason = "removed_upstream"
)

// Skip records a target that was considered and rejected. These are persisted
// on the request row alongside the candidate list, because health tables are
// overwritten in place and cannot be replayed after the fact.
type Skip struct {
	ProviderID string
	KeyID      string
	Model      string
	Reason     SkipReason
}

// target is a resolved (provider, model) pair before filtering.
type target struct {
	ProviderID string
	ModelID    string
}
