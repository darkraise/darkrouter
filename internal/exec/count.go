package exec

import (
	"context"
	"crypto/rand"
	"io"
	"net/http"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/catalog"
	"github.com/darkraise/darkrouter/internal/edge"
	"github.com/darkraise/darkrouter/internal/health"
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
		Source: sourceOfRequest(r),
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
	if tokens, ok := e.nativeCount(r.Context(), req, cands, byID, nativeKind,
		newBudget(cfg.Policy.Timeout, start), cat(res)); ok {
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

// maxCountBodyBytes bounds a counting response, which is a handful of
// integers; anything larger is a provider fault and falls back to the estimate.
const maxCountBodyBytes = 64 << 10

func cat(res resolved) catalog.Reader { return res.Catalog }

// nativeCount tries the first candidate that speaks the inbound counting
// dialect. A failure reports false rather than an error: this endpoint is
// advisory, and an estimate beats a 502 for a client sizing a context window.
//
// It is one call rather than the attempt loop, but it shares the loop's
// discipline: the candidate's breaker is consulted and told the outcome,
// the credential is resolved the same way, and the call runs under the same
// per-attempt deadline. A counting endpoint that is down is the same
// provider that is down.
func (e *Executor) nativeCount(ctx context.Context, req *ir.Request, cands []router.Candidate,
	byID map[string]provider.Provider, nativeKind string, bud budget, cat catalog.Reader) (int, bool) {

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
		hk := health.Key{ProviderID: c.ProviderID, KeyID: c.KeyID, Model: c.Model}
		if e.deps.Fleet != nil && !e.deps.Fleet.Available(hk) {
			continue
		}
		tokens, ok := e.countOnce(ctx, req, c, byID[c.ProviderID], tc, ad, bud, cat)
		return tokens, ok
	}
	return 0, false
}

func (e *Executor) countOnce(ctx context.Context, req *ir.Request, c router.Candidate,
	p provider.Provider, tc adapter.TokenCounter, ad adapter.Adapter, bud budget,
	cat catalog.Reader) (tokens int, ok bool) {

	ctx, cancel := context.WithDeadline(ctx, bud.attemptDeadline(time.Now()))
	defer cancel()

	outcome := adapter.OutcomeRetryableProvider
	var resp *http.Response
	defer func() { e.recordHealthFor(c, outcome, resp) }()

	apiKey, authorizer, err := e.credentialFor(ctx, p, c)
	if err != nil {
		outcome = adapter.OutcomeRetryableCredential
		return 0, false
	}
	tgt := &adapter.Target{
		BaseURL: p.BaseURL, APIKey: apiKey, Model: c.Model,
		Info:   modelInfo(cat, c.ProviderID, c.Model),
		Region: p.Region, Project: p.Project, Location: p.Location, Publisher: c.Publisher,
	}
	hr, err := tc.BuildCountRequest(ctx, tgt, req)
	if err != nil {
		outcome = adapter.OutcomeFatal
		return 0, false
	}
	if err := makeReplayable(hr); err != nil {
		outcome = adapter.OutcomeFatal
		return 0, false
	}
	if err := applyAuthorizer(ctx, hr, authorizer); err != nil {
		outcome = adapter.OutcomeRetryableCredential
		return 0, false
	}

	resp, doErr := e.client.Do(hr)
	outcome = e.classify(ad, ctx, ctx, resp, doErr)
	if outcome != adapter.OutcomeSuccess {
		if resp != nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxErrorBodyBytes))
			resp.Body.Close()
		}
		return 0, false
	}
	// A count response is a handful of integers; a body past the cap is a
	// provider fault, and a 200 that cannot be read counts as one.
	resp.Body = io.NopCloser(io.LimitReader(resp.Body, maxCountBodyBytes))
	tokens, err = tc.ParseCountResponse(resp)
	if err != nil {
		outcome = adapter.OutcomeRetryableProvider
		return 0, false
	}
	return tokens, true
}
