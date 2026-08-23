package exec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/config"
	"github.com/darkraise/darkrouter/internal/edge"
	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/router"
)

// maxTranscriptBytes bounds a JSON transcription response. verbose_json for a
// long recording carries a segment object per phrase, so the cap is generous;
// a cap that rejects a legitimate transcript is worse than no cap.
const maxTranscriptBytes = 64 << 20

type transcriptionOp struct {
	d    edge.Dialect
	form *Form
	// model is the name the client put in the form, kept for routing. The name
	// sent upstream is the candidate's, written into the form by Render.
	model string
}

func (o *transcriptionOp) Dialect() string { return o.d.Name() }

func (o *transcriptionOp) Query() router.Query {
	return router.Query{Model: o.model, Surface: ir.SurfaceSTT}
}

func (o *transcriptionOp) Build(ctx context.Context, tgt *adapter.Target, ad adapter.Adapter) (*http.Request, []ir.Warning, error) {
	tr, ok := ad.(adapter.Transcriber)
	if !ok {
		return nil, nil, fmt.Errorf("adapter %s does not serve transcriptions", ad.Kind())
	}
	// Re-rendered per attempt, not once per request: the model field lives
	// inside the form and the second candidate's name is usually different.
	body, ct, err := o.form.Render(tgt.Model)
	if err != nil {
		return nil, nil, err
	}
	return tr.BuildTranscription(ctx, tgt, body, ct)
}

// Respond dispatches on the response Content-Type rather than on the route.
// Spec §6: one route returns JSON, plain text and SSE depending on a
// response_format field buried in the multipart form, and the header the
// provider actually sent is the only thing that is right in every case.
func (o *transcriptionOp) Respond(cw *CommitWriter, resp *http.Response, ac *AttemptCtx) (adapter.Outcome, *ir.Error) {
	defer resp.Body.Close()

	ct := resp.Header.Get("Content-Type")
	ac.Rec.FinalProviderID = ac.Cand.ProviderID
	ac.Rec.FinalModel = ac.Cand.Model
	ac.Rec.Warnings = warningStrings(ac.Warns)
	ac.Rec.SurfaceMeta = map[string]any{"file_name": o.form.FileName("file")}
	ac.Rec.ResponseContentType = ct

	if strings.HasPrefix(ct, "application/json") {
		raw, err := io.ReadAll(io.LimitReader(resp.Body, maxTranscriptBytes+1))
		if err != nil {
			return failedParse(ac, resp, fmt.Errorf("read transcription response: %w", err))
		}
		if int64(len(raw)) > maxTranscriptBytes {
			// Forwarding the truncated prefix would send the client invalid
			// JSON under a 200. An oversized body is a provider fault.
			return failedParse(ac, resp,
				fmt.Errorf("transcription response exceeds %d bytes", int64(maxTranscriptBytes)))
		}
		// Read for the record only. The bytes go out unchanged, because
		// verbose_json carries per-segment timings and log-probabilities that
		// re-emitting from a narrow IR would drop.
		applyTranscriptUsage(ac, raw)
		ttft := time.Since(ac.Rec.TS).Milliseconds()
		ac.Rec.TTFTMs = &ttft
		ac.Exec.writeDiagnostics(cw, ac.Rec.ID, ac.Cand, ac.Seq)
		cw.Header().Set("Content-Type", ct)
		_, _ = cw.Write(raw)
		ac.Rec.ResponseBytes = cw.Bytes()
		return adapter.OutcomeSuccess, nil
	}

	// Text and SSE alike are opaque and are forwarded with a flush per chunk.
	// Buffering an SSE transcript would turn incremental output into one blob
	// at the end, which is the whole thing the client asked to avoid.
	ttft := time.Since(ac.Rec.TS).Milliseconds()
	ac.Rec.TTFTMs = &ttft
	ac.Exec.writeDiagnostics(cw, ac.Rec.ID, ac.Cand, ac.Seq)
	if ct != "" {
		cw.Header().Set("Content-Type", ct)
	}
	cerr := copyErr(copyFlushing(cw, resp.Body))
	ac.Rec.ResponseBytes = cw.Bytes()
	if cerr != nil && !cw.Committed() {
		return failedParse(ac, resp, cerr)
	}
	// Once bytes have gone out the chain ends whatever happened next. The loop
	// enforces this by consulting the writer, and the byte count is what the
	// trace has instead of an in-stream error the format cannot carry.
	return adapter.OutcomeSuccess, nil
}

func (o *transcriptionOp) WriteError(w http.ResponseWriter, e *ir.Error) error {
	return o.d.WriteError(w, e)
}

var _ SurfaceOp = (*transcriptionOp)(nil)

// applyTranscriptUsage reads the token counts a transcription model may report.
// whisper-1 reports none; the gpt-4o transcription models report a usage object
// whose type is "tokens". A "duration" type carries seconds rather than tokens
// and is deliberately not recorded as tokens.
func applyTranscriptUsage(ac *AttemptCtx, raw []byte) {
	var env struct {
		Usage *struct {
			Type         string `json:"type"`
			InputTokens  int    `json:"input_tokens"`
			OutputTokens int    `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &env); err != nil || env.Usage == nil {
		return
	}
	if env.Usage.Type != "" && env.Usage.Type != "tokens" {
		return
	}
	applyUsage(ac.Rec, &ir.Usage{
		InputTokens: env.Usage.InputTokens, OutputTokens: env.Usage.OutputTokens,
	})
}

// copyFlushing copies src to dst, flushing after every chunk.
//
// io.Copy alone would let the ResponseWriter buffer, which turns an SSE
// transcript or a streamed audio body into a single delivery at the end. It is
// shared with the speech surface, which has the same requirement for the same
// reason.
func copyFlushing(dst *CommitWriter, src io.Reader) (int64, error) {
	buf := make([]byte, 32<<10)
	var total int64
	for {
		n, rerr := src.Read(buf)
		if n > 0 {
			w, werr := dst.Write(buf[:n])
			total += int64(w)
			if werr != nil {
				return total, werr
			}
			dst.Flush()
		}
		if rerr != nil {
			if errors.Is(rerr, io.EOF) {
				return total, nil
			}
			return total, rerr
		}
	}
}

// copyErr drops copyFlushing's byte count. Callers record cw.Bytes() instead,
// which is what reached the client rather than what the copy read.
func copyErr(_ int64, err error) error { return err }

// HandleTranscriptions serves POST /v1/audio/transcriptions.
func (e *Executor) HandleTranscriptions(w http.ResponseWriter, r *http.Request, d edge.Dialect) {
	e.RunAux(w, r, d.Name(), ir.SurfaceSTT, d, func(cfg *config.Config) (SurfaceOp, error) {
		form, err := ParseForm(r, cfg.Server.MaxBodyBytes)
		if err != nil {
			return nil, err
		}
		return &transcriptionOp{d: d, form: form, model: form.Field("model")}, nil
	})
}
