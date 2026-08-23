package exec

import (
	"context"
	"crypto/rand"
	"net/http"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/edge"
	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/provider"
	"github.com/darkraise/darkrouter/internal/router"
	"github.com/darkraise/darkrouter/internal/store"
	"github.com/darkraise/darkrouter/internal/tokenize"
)

// HandleCount serves a token-counting request.
//
// It does not run the attempt loop: there is no commit, no failover, and no
// stream. The router is consulted only to learn which target would serve the
// request, because that decides whether a native count is possible at all.
//
// nativeKind is the adapter kind whose counting endpoint speaks this inbound
// dialect — "anthropic" for /v1/messages/count_tokens, "gemini" for
// :countTokens. Any other target is estimated locally.
func (e *Executor) HandleCount(w http.ResponseWriter, r *http.Request, d edge.CountWriter, nativeKind string) {
	start := time.Now()
	cfg := e.store.Current()
	reqID := ulid.MustNew(ulid.Timestamp(start), rand.Reader).String()

	rec := &store.RequestRecord{
		ID: reqID, TS: start, Dialect: d.Name(),
		Surface: string(ir.SurfaceLLM), Status: "error",
	}
	defer func() {
		total := time.Since(start).Milliseconds()
		rec.TotalMs = &total
		e.log(rec)
	}()
	w.Header().Set("X-Darkrouter-Request", reqID)

	req, _, err := d.ParseRequest(r, cfg.Server.MaxBodyBytes)
	if err != nil {
		rec.ErrorCode = string(ir.ErrInvalidRequest)
		_ = d.WriteError(w, &ir.Error{Type: ir.ErrInvalidRequest, Message: err.Error()})
		return
	}
	needs := req.Needs()
	res, ok := e.resolve(r.Context(), w, d, router.Query{
		Model: req.Model, Surface: ir.SurfaceLLM,
		NeedsTools: needs.Tools, NeedsVision: needs.Vision, NeedsReasoning: needs.Reasoning,
	}, rec, cfg, start)
	if !ok {
		return
	}
	cands, byID := res.Candidates, res.ByID

	model := req.Model
	if len(cands) > 0 {
		model = cands[0].Model
	}
	if tokens, ok := e.nativeCount(r.Context(), req, cands, byID, nativeKind); ok {
		rec.Status = "success"
		rec.TokensIn = int64(tokens)
		_ = d.WriteCount(w, tokens)
		return
	}

	// The body cannot carry a marker: clients parse these responses strictly.
	w.Header().Set("X-Darkrouter-Estimated", "true")
	tokens := tokenize.Count(req, model)
	rec.Status = "success"
	rec.TokensIn = int64(tokens)
	rec.Warnings = []string{"count -> " + d.Name() + ": estimated locally"}
	_ = d.WriteCount(w, tokens)
}

// nativeCount tries the first candidate that speaks the inbound counting
// dialect. A failure reports false rather than an error: this endpoint is
// advisory, and an estimate beats a 502 for a client sizing a context window.
func (e *Executor) nativeCount(ctx context.Context, req *ir.Request, cands []router.Candidate,
	byID map[string]provider.Provider, nativeKind string) (int, bool) {

	for _, c := range cands {
		if c.Kind != nativeKind {
			continue
		}
		ad, ok := e.adapterFor(c.Kind)
		if !ok {
			continue
		}
		tc, ok := ad.(adapter.TokenCounter)
		if !ok {
			continue
		}
		p := byID[c.ProviderID]
		tgt := &adapter.Target{BaseURL: p.BaseURL, APIKey: secretOf(p, c.KeyID), Model: c.Model}
		hr, err := tc.BuildCountRequest(ctx, tgt, req)
		if err != nil {
			return 0, false
		}
		resp, err := e.client.Do(hr)
		if err != nil {
			return 0, false
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			resp.Body.Close()
			return 0, false
		}
		tokens, err := tc.ParseCountResponse(resp)
		if err != nil {
			return 0, false
		}
		return tokens, true
	}
	return 0, false
}
