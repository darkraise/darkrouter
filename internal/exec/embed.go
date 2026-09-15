package exec

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/config"
	"github.com/darkraise/darkrouter/internal/edge"
	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/redact"
	"github.com/darkraise/darkrouter/internal/router"
)

// embedOp is the embedding surface. It implements SurfaceOp directly rather
// than wrapping AuxOp because its Respond does one thing no other surface does.
type embedOp struct {
	d   edge.EmbeddingDialect
	req *ir.EmbeddingRequest

	// The current attempt's context, target and sub-batch sizes, set by Build.
	// Attempts run one at a time, so each Build replaces the last attempt's.
	ctx     context.Context
	tgt     *adapter.Target
	batches []int
}

func (o *embedOp) Dialect() string { return o.d.Name() }

// Query sets no capability needs: an embedding request does not ask for tools,
// vision or reasoning, and requiring them would filter out every real embedding
// model.
func (o *embedOp) Query() router.Query {
	return router.Query{Model: o.req.Model, Surface: ir.SurfaceEmbedding}
}

func (o *embedOp) Build(ctx context.Context, tgt *adapter.Target, ad adapter.Adapter) (*http.Request, []ir.Warning, error) {
	em, ok := ad.(adapter.Embedder)
	if !ok {
		// Unreachable through the router, which filters on adapter surfaces.
		// It is checked anyway because the alternative to failing here is
		// sending a chat body to an embedding endpoint.
		return nil, nil, fmt.Errorf("adapter %s does not serve embeddings", ad.Kind())
	}
	o.ctx, o.tgt, o.batches = ctx, tgt, nil
	if b, ok := ad.(adapter.EmbeddingBatcher); ok {
		o.batches = b.EmbeddingBatches(tgt, o.req)
	}
	if len(o.batches) == 0 {
		o.batches = []int{o.req.InputCount()}
	}
	return em.BuildEmbedding(ctx, tgt, o.batch(0))
}

// batch is the request carrying sub-batch i's inputs.
func (o *embedOp) batch(i int) *ir.EmbeddingRequest {
	if len(o.batches) == 1 {
		return o.req
	}
	start := 0
	for _, n := range o.batches[:i] {
		start += n
	}
	end := start + o.batches[i]
	sub := *o.req
	if len(o.req.Tokens) > 0 {
		sub.Tokens = o.req.Tokens[start:end]
	} else {
		sub.Input = o.req.Input[start:end]
	}
	return &sub
}

func (o *embedOp) Respond(cw *CommitWriter, resp *http.Response, ac *AttemptCtx) (adapter.Outcome, *ir.Error) {
	em, ok := ac.Adapter.(adapter.Embedder)
	if !ok {
		resp.Body.Close()
		return adapter.OutcomeFatal, &ir.Error{
			Type: ir.ErrDarkrouter, Message: "adapter does not serve embeddings",
		}
	}
	ac.resetIdle()
	out, err := o.parse(em, resp, 0)
	if err != nil {
		return failedParse(ac, resp, err)
	}
	// Nothing is written until every sub-batch has answered, so a failure
	// part-way through still fails over rather than serving half the vectors.
	for i := 1; i < len(o.batches); i++ {
		sub, outcome, aerr := o.fetch(em, ac, i)
		if aerr != nil {
			return outcome, aerr
		}
		offset := len(out.Embeddings)
		for _, e := range sub.Embeddings {
			e.Index += offset
			out.Embeddings = append(out.Embeddings, e)
		}
		out.Usage.InputTokens += sub.Usage.InputTokens
	}
	if len(o.batches) > 1 {
		if err := adapter.ValidateEmbeddings(out.Embeddings); err != nil {
			return failedParse(ac, resp, err)
		}
	}

	// Spec §8. The comparison is against the first candidate the router
	// produced — supplied by the loop, not inferred from this op's own Build
	// calls, because a first candidate skipped by the live cooling re-check
	// never reaches Build and is exactly when this warning must still fire.
	warns := ac.Warns
	if ac.FirstModel != "" && ac.Cand.Model != ac.FirstModel {
		warns = append(warns, ir.Warning{
			Field:  "model",
			Target: ac.Cand.ProviderID + "/" + ac.Cand.Model,
			Reason: "embeddings served by " + ac.Cand.Model + " after " + ac.FirstModel +
				" was not used; vectors from two models are not in the same vector space " +
				"and an index filled across this failover is corrupt",
		})
	}

	applyUsage(ac.Rec, &out.Usage)
	ac.served(warns)

	ac.Rec.SurfaceMeta = map[string]any{
		"input_count": o.req.InputCount(),
		"encoding":    o.req.EncodingOrDefault(),
	}
	// Omitted rather than zero: dimensions has no legal zero, so recording one
	// would claim the client asked for a value it did not send.
	if o.req.Dimensions > 0 {
		ac.Rec.SurfaceMeta["dimensions"] = o.req.Dimensions
	}

	ac.Exec.writeDiagnostics(cw, ac.Rec.ID, ac.Cand, ac.Seq)
	_ = o.d.WriteEmbedding(cw, out)
	return adapter.OutcomeSuccess, nil
}

// parse reads sub-batch i's response.
func (o *embedOp) parse(em adapter.Embedder, resp *http.Response, i int) (*ir.EmbeddingResponse, error) {
	out, err := em.ParseEmbedding(resp)
	if err != nil {
		return nil, err
	}
	// Vectors answer inputs by position, so a short or long batch cannot be
	// matched back to the inputs it was meant to embed.
	if len(out.Embeddings) != o.batches[i] {
		return nil, fmt.Errorf("embedding response carried %d vectors for %d inputs",
			len(out.Embeddings), o.batches[i])
	}
	return out, nil
}

// fetch sends sub-batch i on the attempt's credential and reads its vectors. A
// failure is classified as the loop classifies its own send, so a rejected
// credential or a rate limit on a later sub-batch steps the chain the same
// way it would on the first.
func (o *embedOp) fetch(em adapter.Embedder, ac *AttemptCtx, i int) (*ir.EmbeddingResponse, adapter.Outcome, *ir.Error) {
	// The attempt row was written when the first sub-batch's headers arrived.
	// Its latency is moved on as each later one answers, so it spans them all.
	spanLatency := func() {
		if last := len(ac.Rec.Attempts) - 1; last >= 0 {
			ac.Rec.Attempts[last].LatencyMs = time.Since(ac.sent).Milliseconds()
		}
	}
	fail := func(outcome adapter.Outcome, resp *http.Response, err error, ie *ir.Error) (*ir.EmbeddingResponse, adapter.Outcome, *ir.Error) {
		spanLatency()
		if last := len(ac.Rec.Attempts) - 1; last >= 0 {
			ac.Rec.Attempts[last].Outcome = string(outcome)
			ac.Rec.Attempts[last].Error = err.Error()
		}
		ac.recordFailure(outcome, resp, err)
		return nil, outcome, ie
	}
	hr, _, err := em.BuildEmbedding(o.ctx, o.tgt, o.batch(i))
	if err == nil {
		err = makeReplayable(hr)
	}
	if err != nil {
		err = fmt.Errorf("render embedding batch %d of %d: %w", i+1, len(o.batches), err)
		return fail(adapter.OutcomeFatal, nil, err, errorFor(adapter.OutcomeFatal, err))
	}
	if err := applyAuthorizer(o.ctx, hr, ac.authorize); err != nil {
		return fail(adapter.OutcomeRetryableCredential, nil, err,
			&ir.Error{Type: ir.ErrAuthentication, Message: msgCredentialUnavailable})
	}
	ac.resetSend()
	resp, doErr := ac.Exec.client.Do(hr)
	spanLatency()
	doErr = redact.Error(doErr, ac.secret)
	ac.resp = resp
	outcome := ac.Exec.classify(ac.Adapter, ac.inbound, ac.upstream, resp, doErr)
	if outcome != adapter.OutcomeSuccess {
		cause := doErr
		if resp != nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxErrorBodyBytes))
			resp.Body.Close()
			if cause == nil {
				cause = fmt.Errorf("embedding batch %d of %d: upstream returned %s", i+1, len(o.batches), resp.Status)
			}
		}
		ie := errorFor(outcome, cause)
		if resp != nil && resp.StatusCode == http.StatusTooManyRequests {
			// The loop reads a rate limit off the error type once Respond has
			// returned, to step to the next credential as a 429 would.
			ie = &ir.Error{Type: ir.ErrRateLimit, Message: cause.Error()}
		}
		return fail(outcome, resp, cause, ie)
	}
	ac.resetIdle()
	resp.Body = &idleBody{ReadCloser: resp.Body, ac: ac}
	sub, err := o.parse(em, resp, i)
	if err != nil {
		outcome, ie := failedParse(ac, resp, err)
		return nil, outcome, ie
	}
	return sub, adapter.OutcomeSuccess, nil
}

func (o *embedOp) WriteError(w http.ResponseWriter, e *ir.Error) error {
	return o.d.WriteError(w, e)
}

var _ SurfaceOp = (*embedOp)(nil)

// failedParse is the parse-failure tail every surface shares.
//
// A 2xx that cannot be read is a provider fault, so it rejoins the outcome path
// and counts against the breaker like a 5xx would. A refusal is not: it is
// recorded as fatal, which proves the provider reachable without failing over
// to re-ask a question every model in the chain will refuse.
func failedParse(ac *AttemptCtx, resp *http.Response, err error) (adapter.Outcome, *ir.Error) {
	outcome := ac.readOutcome(err)
	if last := len(ac.Rec.Attempts) - 1; last >= 0 {
		ac.Rec.Attempts[last].Outcome = string(outcome)
		ac.Rec.Attempts[last].Error = err.Error()
	}
	ac.recordFailure(outcome, resp, err)
	var ie *ir.Error
	if outcome != adapter.OutcomeClientCancelled && errors.As(err, &ie) {
		return outcome, ie
	}
	return outcome, errorFor(outcome, err)
}

// HandleEmbeddings serves POST /v1/embeddings.
func (e *Executor) HandleEmbeddings(w http.ResponseWriter, r *http.Request, d edge.EmbeddingDialect) {
	e.RunAux(w, r, d.Name(), ir.SurfaceEmbedding, d, func(cfg *config.Config) (SurfaceOp, error) {
		req, err := d.ParseEmbedding(r, cfg.Server.MaxBodyBytes)
		if err != nil {
			return nil, err
		}
		return &embedOp{d: d, req: req}, nil
	})
}
