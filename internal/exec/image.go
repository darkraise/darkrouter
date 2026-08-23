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

type imageOp struct {
	d   edge.ImageDialect
	req *ir.ImageRequest
}

func (o *imageOp) Dialect() string { return o.d.Name() }

func (o *imageOp) Query() router.Query {
	return router.Query{Model: o.req.Model, Surface: ir.SurfaceImage}
}

func (o *imageOp) Build(ctx context.Context, tgt *adapter.Target, ad adapter.Adapter) (*http.Request, []ir.Warning, error) {
	g, ok := ad.(adapter.ImageGenerator)
	if !ok {
		return nil, nil, fmt.Errorf("adapter %s does not serve images", ad.Kind())
	}
	return g.BuildImage(ctx, tgt, o.req)
}

func (o *imageOp) Respond(cw *CommitWriter, resp *http.Response, ac *AttemptCtx) (adapter.Outcome, *ir.Error) {
	g, ok := ac.Adapter.(adapter.ImageGenerator)
	if !ok {
		resp.Body.Close()
		return adapter.OutcomeFatal, &ir.Error{
			Type: ir.ErrDarkrouter, Message: "adapter does not serve images",
		}
	}
	out, err := g.ParseImage(resp)
	if err != nil {
		return failedParse(ac, resp, err)
	}
	out.Model = ac.Cand.Model

	ttft := time.Since(ac.Rec.TS).Milliseconds()
	ac.Rec.TTFTMs = &ttft
	// Only when the provider reported it. A dall-e call recorded as zero tokens
	// is indistinguishable in the log from a call that genuinely cost nothing.
	if out.UsageReported {
		applyUsage(ac.Rec, &out.Usage)
	}
	ac.Rec.FinalProviderID = ac.Cand.ProviderID
	ac.Rec.FinalModel = ac.Cand.Model
	ac.Rec.Warnings = warningStrings(ac.Warns)

	ac.Rec.SurfaceMeta = map[string]any{"image_count": o.req.ImageCount()}
	for k, v := range map[string]string{"size": o.req.Size, "quality": o.req.Quality} {
		if v != "" {
			ac.Rec.SurfaceMeta[k] = v
		}
	}

	ac.Exec.writeDiagnostics(cw, ac.Rec.ID, ac.Cand, ac.Seq)
	_ = o.d.WriteImage(cw, out)
	return adapter.OutcomeSuccess, nil
}

func (o *imageOp) WriteError(w http.ResponseWriter, e *ir.Error) error {
	return o.d.WriteError(w, e)
}

var _ SurfaceOp = (*imageOp)(nil)

// HandleImages serves POST /v1/images/generations.
func (e *Executor) HandleImages(w http.ResponseWriter, r *http.Request, d edge.ImageDialect) {
	e.RunAux(w, r, d.Name(), ir.SurfaceImage, d, func(cfg *config.Config) (SurfaceOp, error) {
		req, err := d.ParseImage(r, cfg.Server.MaxBodyBytes)
		if err != nil {
			return nil, err
		}
		return &imageOp{d: d, req: req}, nil
	})
}
