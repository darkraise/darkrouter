package exec

import (
	"context"
	"net/http"
	"time"

	"github.com/darkraise/darkrouter/internal/catalog"
	"github.com/darkraise/darkrouter/internal/config"
	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/provider"
	"github.com/darkraise/darkrouter/internal/router"
	"github.com/darkraise/darkrouter/internal/store"
)

// errorWriter is the slice of a dialect the prologue needs. Both edge.Dialect
// and edge.CountWriter satisfy it, and so will every auxiliary surface's
// writer — the prologue must be able to report a failure in whatever shape the
// client speaks, per master design §14.
type errorWriter interface {
	WriteError(w http.ResponseWriter, e *ir.Error) error
}

// resolved is everything the prologue produced, frozen for the attempt loop.
type resolved struct {
	Candidates []router.Candidate
	ByID       map[string]provider.Provider
	Catalog    catalog.Reader
	Cfg        *config.Config
}

// resolve runs the prologue every route shares: fetch the provider set, freeze
// the router snapshot, resolve candidates, and record the trace.
//
// It reports false when it has already written an error to w — the caller must
// return immediately rather than inspecting the zero resolved. Returning a
// bool rather than an error keeps the "who writes the response" question
// answered in exactly one place.
//
// The snapshot freezes every input the router is allowed to read, and health is
// resolved to booleans here rather than inside Resolve, which is what keeps the
// router a pure function of its arguments.
func (e *Executor) resolve(ctx context.Context, w http.ResponseWriter, ew errorWriter,
	q router.Query, rec *store.RequestRecord, cfg *config.Config, start time.Time) (resolved, bool) {

	rec.Surface = string(q.Surface)
	rec.RequestedModel = q.Model

	providers, err := e.src.Providers(ctx)
	if err != nil {
		rec.ErrorCode = string(ir.ErrDarkrouter)
		_ = ew.WriteError(w, &ir.Error{Type: ir.ErrDarkrouter, Message: err.Error()})
		return resolved{}, false
	}

	cat := e.catalogFor(providers)
	snap := router.Snapshot{At: start, Providers: providers, Catalog: cat, Config: cfg}
	if e.deps.Fleet != nil {
		snap.Health = e.deps.Fleet.SnapshotAvailability(start)
		snap.LastUsed = e.deps.Fleet.LastUsedSnapshot()
	}

	cands, skips, rerr := router.Resolve(q, snap)
	// Recorded before the error check: the skips are what explain an empty
	// candidate list, so discarding them on the failure path throws away the
	// only evidence of why nothing routed.
	rec.Candidates = traceCandidates(cands)
	rec.Skips = traceSkips(skips)

	if rerr != nil {
		e2 := routerError(rerr)
		rec.ErrorCode = string(e2.Type)
		_ = ew.WriteError(w, e2)
		return resolved{}, false
	}

	byID := make(map[string]provider.Provider, len(providers))
	for _, p := range providers {
		byID[p.ID] = p
	}
	return resolved{Candidates: cands, ByID: byID, Catalog: cat, Cfg: cfg}, true
}
