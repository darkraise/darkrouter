package admin

import (
	"net/http"
	"time"

	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/router"
)

type previewRequest struct {
	Model          string `json:"model"`
	Surface        string `json:"surface"`
	NeedsTools     bool   `json:"needs_tools"`
	NeedsVision    bool   `json:"needs_vision"`
	NeedsReasoning bool   `json:"needs_reasoning"`
}

type previewCandidate struct {
	ProviderID string `json:"provider_id"`
	KeyID      string `json:"key_id"`
	Model      string `json:"model"`
	Kind       string `json:"kind"`
	Publisher  string `json:"publisher,omitempty"`
	Inferred   bool   `json:"inferred"`
}

type previewSkip struct {
	ProviderID string `json:"provider_id"`
	KeyID      string `json:"key_id,omitempty"`
	Model      string `json:"model"`
	Reason     string `json:"reason"`
}

// handleRoutePreview resolves a query against the live snapshot without
// attempting anything.
//
// It runs the router's own pure Resolve over the executor's own snapshot
// rather than reconstructing either. §12 states the criterion as an equality:
// preview must produce the same ordered candidate list a real request would,
// and a second implementation of "the same inputs" would drift the first time
// one of them gained a field.
func (s *Server) handleRoutePreview(w http.ResponseWriter, r *http.Request) {
	if s.deps.Exec == nil || s.deps.Config == nil {
		writeError(w, http.StatusServiceUnavailable, "no executor")
		return
	}
	var body previewRequest
	if !decodeJSON(w, r, 16<<10, &body) {
		return
	}
	if body.Model == "" {
		writeError(w, http.StatusBadRequest, "model is required")
		return
	}
	surface := ir.Surface(body.Surface)
	if surface == "" {
		surface = ir.SurfaceLLM
	}

	snap, err := s.deps.Exec.RouteSnapshot(r.Context(), time.Now(),
		s.deps.Config.Current())
	if err != nil {
		internalError(w, r, err)
		return
	}
	cands, skips, rerr := router.Resolve(router.Query{
		Model:          body.Model,
		Surface:        surface,
		NeedsTools:     body.NeedsTools,
		NeedsVision:    body.NeedsVision,
		NeedsReasoning: body.NeedsReasoning,
	}, snap)

	// Initialized rather than declared: the console reads both as arrays, and
	// the skips are the only account of why a candidate list came back empty.
	outCands := []previewCandidate{}
	for _, c := range cands {
		outCands = append(outCands, previewCandidate{
			ProviderID: c.ProviderID, KeyID: c.KeyID, Model: c.Model,
			Kind: c.Kind, Publisher: c.Publisher, Inferred: c.Inferred,
		})
	}
	outSkips := []previewSkip{}
	for _, sk := range skips {
		outSkips = append(outSkips, previewSkip{
			ProviderID: sk.ProviderID, KeyID: sk.KeyID,
			Model: sk.Model, Reason: string(sk.Reason),
		})
	}

	out := map[string]any{"candidates": outCands, "skips": outSkips}
	if rerr != nil {
		// 200 with the reason rather than an error status: "nothing routes"
		// is the answer to a dry run, not a failure of the request.
		out["error"] = rerr.Error()
	}
	writeJSON(w, http.StatusOK, out)
}
