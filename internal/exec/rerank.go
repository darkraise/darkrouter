package exec

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/config"
	"github.com/darkraise/darkrouter/internal/edge"
	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/router"
)

type rerankOp struct {
	d   edge.RerankDialect
	req *ir.RerankRequest
}

func (o *rerankOp) Dialect() string { return o.d.Name() }

func (o *rerankOp) Query() router.Query {
	return router.Query{Model: o.req.Model, Surface: ir.SurfaceRerank}
}

func (o *rerankOp) Build(ctx context.Context, tgt *adapter.Target, ad adapter.Adapter) (*http.Request, []ir.Warning, error) {
	rr, ok := ad.(adapter.Reranker)
	if !ok {
		return nil, nil, fmt.Errorf("adapter %s does not serve rerank", ad.Kind())
	}
	return rr.BuildRerank(ctx, tgt, o.req)
}

func (o *rerankOp) Respond(cw *CommitWriter, resp *http.Response, ac *AttemptCtx) (adapter.Outcome, *ir.Error) {
	rr, ok := ac.Adapter.(adapter.Reranker)
	if !ok {
		resp.Body.Close()
		return adapter.OutcomeFatal, &ir.Error{
			Type: ir.ErrDarkrouter, Message: "adapter does not serve rerank",
		}
	}
	out, err := rr.ParseRerank(resp)
	if err != nil {
		return failedParse(ac, resp, err)
	}
	// The provider's own id is echoed, but the model it ranked with is the
	// candidate's: a rerank response carries no model field.
	out.Model = ac.Cand.Model

	// Cohere v2 returns no documents, so return_documents is honored here from
	// the request Darkrouter sent. Forwarding the parameter instead would
	// return results with no documents while the client believed it had asked
	// for them. An out-of-range index is a provider fault and is left empty
	// rather than panicking on a slice the response does not agree with.
	if o.req.ReturnDocuments {
		for i, r := range out.Results {
			if r.Index >= 0 && r.Index < len(o.req.Documents) {
				out.Results[i].Document = o.req.Documents[r.Index]
			}
		}
	}

	ttft := time.Since(ac.Rec.TS).Milliseconds()
	ac.Rec.TTFTMs = &ttft
	applyUsage(ac.Rec, &out.Usage)
	ac.Rec.FinalProviderID = ac.Cand.ProviderID
	ac.Rec.FinalModel = ac.Cand.Model
	ac.Rec.Warnings = warningStrings(ac.Warns)

	ac.Rec.SurfaceMeta = map[string]any{"document_count": o.req.DocumentCount()}
	if o.req.TopN > 0 {
		ac.Rec.SurfaceMeta["top_n"] = o.req.TopN
	}

	ac.Exec.writeDiagnostics(cw, ac.Rec.ID, ac.Cand, ac.Seq)
	_ = o.d.WriteRerank(cw, out)
	return adapter.OutcomeSuccess, nil
}

func (o *rerankOp) WriteError(w http.ResponseWriter, e *ir.Error) error {
	return o.d.WriteError(w, e)
}

var _ SurfaceOp = (*rerankOp)(nil)

// HandleRerank serves POST /v1/rerank.
func (e *Executor) HandleRerank(w http.ResponseWriter, r *http.Request, d edge.RerankDialect) {
	e.RunAux(w, r, d.Name(), ir.SurfaceRerank, d, func(cfg *config.Config) (SurfaceOp, error) {
		req, err := d.ParseRerank(r, cfg.Server.MaxBodyBytes)
		if err != nil {
			return nil, err
		}
		return &rerankOp{d: d, req: req}, nil
	})
}
