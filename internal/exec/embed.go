package exec

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/config"
	"github.com/darkraise/darkrouter/internal/edge"
	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/router"
)

// embedOp is the embedding surface. It implements SurfaceOp directly rather
// than wrapping AuxOp because its Respond does one thing no other surface does.
type embedOp struct {
	d   edge.EmbeddingDialect
	req *ir.EmbeddingRequest
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
	return em.BuildEmbedding(ctx, tgt, o.req)
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
	out, err := em.ParseEmbedding(resp)
	if err != nil {
		return failedParse(ac, resp, err)
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
	outcome := outcomeForParseError(err)
	if last := len(ac.Rec.Attempts) - 1; last >= 0 {
		ac.Rec.Attempts[last].Outcome = string(outcome)
		ac.Rec.Attempts[last].Error = err.Error()
	}
	ac.recordHealth(outcome, resp)
	var ie *ir.Error
	if errors.As(err, &ie) {
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
