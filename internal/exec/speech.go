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

type speechOp struct {
	d   edge.SpeechDialect
	req *ir.SpeechRequest
}

func (o *speechOp) Dialect() string { return o.d.Name() }

func (o *speechOp) Query() router.Query {
	return router.Query{Model: o.req.Model, Surface: ir.SurfaceTTS}
}

func (o *speechOp) Build(ctx context.Context, tgt *adapter.Target, ad adapter.Adapter) (*http.Request, []ir.Warning, error) {
	sp, ok := ad.(adapter.Speaker)
	if !ok {
		return nil, nil, fmt.Errorf("adapter %s does not serve speech", ad.Kind())
	}
	return sp.BuildSpeech(ctx, tgt, o.req)
}

// Respond forwards the body without reading it.
//
// Spec §6: audio in SQLite is never right, so the bytes are never held. Spec §7:
// once the first byte is out there is no re-route, and unlike chat there is no
// in-stream error vocabulary to tell the client the rest is missing — so a
// truncated body reaches the client as truncated audio and the byte count on
// the request row is the only place that shows up.
func (o *speechOp) Respond(cw *CommitWriter, resp *http.Response, ac *AttemptCtx) (adapter.Outcome, *ir.Error) {
	defer resp.Body.Close()
	ac.resetIdle()
	ttft := time.Since(ac.Rec.TS).Milliseconds()
	ac.Rec.TTFTMs = &ttft

	ac.Exec.writeDiagnostics(cw, ac.Rec.ID, ac.Cand, ac.Seq)
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		cw.Header().Set("Content-Type", ct)
	}
	err := copyErr(copyFlushing(cw, resp.Body))
	// cw.Bytes() rather than the copy's own count or the provider's
	// Content-Length: the wrapper counts what actually reached the client, and
	// a truncated body's count is lower than what the provider claimed. Spec §7
	// makes this the only place that truncation appears.
	ac.Rec.ResponseBytes = cw.Bytes()
	ac.Rec.ResponseContentType = resp.Header.Get("Content-Type")
	ac.Rec.SurfaceMeta = map[string]any{"voice": o.req.Voice}
	if o.req.ResponseFormat != "" {
		ac.Rec.SurfaceMeta["response_format"] = o.req.ResponseFormat
	}
	if err != nil && !cw.Committed() {
		// Nothing reached the client, so the chain may still continue.
		return failedParse(ac, resp, err)
	}
	ac.served(ac.Warns)
	return adapter.OutcomeSuccess, nil
}

func (o *speechOp) WriteError(w http.ResponseWriter, e *ir.Error) error {
	return o.d.WriteError(w, e)
}

var _ SurfaceOp = (*speechOp)(nil)

// HandleSpeech serves POST /v1/audio/speech.
func (e *Executor) HandleSpeech(w http.ResponseWriter, r *http.Request, d edge.SpeechDialect) {
	e.RunAux(w, r, d.Name(), ir.SurfaceTTS, d, func(cfg *config.Config) (SurfaceOp, error) {
		req, err := d.ParseSpeech(r, cfg.Server.MaxBodyBytes)
		if err != nil {
			return nil, err
		}
		return &speechOp{d: d, req: req}, nil
	})
}
