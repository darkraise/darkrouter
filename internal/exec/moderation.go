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

type moderationOp struct {
	d   edge.ModerationDialect
	req *ir.ModerationRequest
}

func (o *moderationOp) Dialect() string { return o.d.Name() }

func (o *moderationOp) Query() router.Query {
	return router.Query{Model: o.req.Model, Surface: ir.SurfaceModeration}
}

func (o *moderationOp) Build(ctx context.Context, tgt *adapter.Target, ad adapter.Adapter) (*http.Request, []ir.Warning, error) {
	m, ok := ad.(adapter.Moderator)
	if !ok {
		return nil, nil, fmt.Errorf("adapter %s does not serve moderations", ad.Kind())
	}
	return m.BuildModeration(ctx, tgt, o.req)
}

func (o *moderationOp) Respond(cw *CommitWriter, resp *http.Response, ac *AttemptCtx) (adapter.Outcome, *ir.Error) {
	m, ok := ac.Adapter.(adapter.Moderator)
	if !ok {
		resp.Body.Close()
		return adapter.OutcomeFatal, &ir.Error{
			Type: ir.ErrDarkrouter, Message: "adapter does not serve moderations",
		}
	}
	out, err := m.ParseModeration(resp)
	if err != nil {
		return failedParse(ac, resp, err)
	}

	ttft := time.Since(ac.Rec.TS).Milliseconds()
	ac.Rec.TTFTMs = &ttft
	applyUsage(ac.Rec, &out.Usage)
	ac.Rec.FinalProviderID = ac.Cand.ProviderID
	ac.Rec.FinalModel = ac.Cand.Model
	ac.Rec.Warnings = warningStrings(ac.Warns)

	ac.Exec.writeDiagnostics(cw, ac.Rec.ID, ac.Cand, ac.Seq)
	_ = o.d.WriteModeration(cw, out)
	return adapter.OutcomeSuccess, nil
}

func (o *moderationOp) WriteError(w http.ResponseWriter, e *ir.Error) error {
	return o.d.WriteError(w, e)
}

var _ SurfaceOp = (*moderationOp)(nil)

// HandleModerations serves POST /v1/moderations.
func (e *Executor) HandleModerations(w http.ResponseWriter, r *http.Request, d edge.ModerationDialect) {
	e.RunAux(w, r, d.Name(), ir.SurfaceModeration, d, func(cfg *config.Config) (SurfaceOp, error) {
		req, err := d.ParseModeration(r, cfg.Server.MaxBodyBytes)
		if err != nil {
			return nil, err
		}
		return &moderationOp{d: d, req: req}, nil
	})
}
