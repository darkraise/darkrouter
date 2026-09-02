package exec

import (
	"context"
	"crypto/rand"
	"errors"
	"net/http"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/config"
	"github.com/darkraise/darkrouter/internal/edge"
	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/router"
	"github.com/darkraise/darkrouter/internal/store"
)

// SurfaceOp is what varies between surfaces. Everything else — the budget gate,
// the live health re-check, credential rotation, adapter resolution, the send,
// outcome classification, attempt records, health signals and the request log —
// is surface-invariant and stays in the loop, because that is where phase 3's
// subtle bugs were fixed and it must not be reimplemented six more times.
//
// Beyond naming its dialect it is deliberately narrow, split at two joints:
// rendering the outbound request, and turning a 2xx into client bytes.
type SurfaceOp interface {
	// Dialect names the inbound wire form, for the request row's dialect
	// column. An op knows it; the loop cannot infer it, and the six auxiliary
	// routes are not all the same dialect as the chat route they share a
	// package with.
	Dialect() string

	// Query is what the router filters on. Auxiliary surfaces set no capability
	// needs — an embedding request does not ask for tools.
	Query() router.Query

	// Build renders the outbound request for one resolved target. It is called
	// once per attempt, not once per request: the target's model name differs
	// per candidate, and a multipart body must be re-rendered with the new name
	// inside the form.
	Build(ctx context.Context, tgt *adapter.Target, ad adapter.Adapter) (*http.Request, []ir.Warning, error)

	// Respond turns a successful upstream response into client bytes. It is
	// called only when the loop classified the response as OutcomeSuccess, and
	// it owns closing resp.Body.
	//
	// Writing to cw is what commits the response. The op decides what counts as
	// content-bearing for its wire format; the loop decides what that means for
	// failover, by consulting the writer rather than the returned outcome.
	Respond(cw *CommitWriter, resp *http.Response, ac *AttemptCtx) (adapter.Outcome, *ir.Error)

	// WriteError renders a Darkrouter error in the shape the client speaks.
	// Master design §14: an error is normalized into the inbound dialect.
	WriteError(w http.ResponseWriter, e *ir.Error) error
}

// passthroughOp is implemented by a SurfaceOp whose inbound bytes can be
// forwarded rather than re-rendered.
//
// Optional, matching adapter.TokenCounter: an op that says nothing takes the IR
// path, which is every auxiliary surface. Multipart and binary bodies are
// excluded by master design §4.1 and by having nothing to return here.
type passthroughOp interface {
	Passthrough() *edge.Passthrough
	// WriteStreamError renders a terminal in-stream error in the inbound
	// dialect's own wire form, for a forwarded stream that fails after commit.
	// The forwarder holds no dialect writer, and a stream that simply stops is
	// indistinguishable to the client from one that finished.
	WriteStreamError(w http.ResponseWriter, e *ir.Error)
}

// AttemptCtx is what Respond needs from the attempt around it. It is a struct
// rather than six parameters because auxiliary surfaces use different subsets
// and the list would otherwise grow with every one of them.
type AttemptCtx struct {
	Exec *Executor
	Cfg  *config.Config
	Cand router.Candidate
	Rec  *store.RequestRecord
	Seq  int
	// Timer bounds the attempt. Respond resets it at commit, when the total
	// timeout stops applying and idle takes over.
	Timer *time.Timer
	// Warns are the warnings Build produced, plus the inferred-capability
	// warning when the loop admitted a guess. Respond appends whatever the
	// response itself raised and assigns the union to the record — assigned,
	// never appended across attempts, so an abandoned attempt's warnings do not
	// describe the translation the client received.
	Warns   []ir.Warning
	Adapter adapter.Adapter

	// FirstModel is the model of the first candidate the router produced, not
	// of the first attempt that ran. Spec §8's embedding warning fires when the
	// serving model differs from it, and the difference matters: a first
	// candidate skipped by the live cooling re-check never reaches Build, so an
	// op inferring "first" from its own calls would stay silent in exactly the
	// case the warning exists for.
	FirstModel string

	// resp is the upstream response, kept so the breaker signal can read its
	// status and Retry-After whichever path emits it.
	resp *http.Response
	// healthDone guards the one breaker signal an attempt may emit. The first
	// caller wins: a surface reporting a pre-commit fault beats the loop's
	// deferred record on the way out, and a success reported once the body
	// was read beats nothing, because nothing else reports one.
	healthDone bool
}

// recordHealth emits the attempt's breaker signal, once.
func (ac *AttemptCtx) recordHealth(o adapter.Outcome, resp *http.Response) {
	if ac.healthDone {
		return
	}
	ac.healthDone = true
	ac.Exec.recordHealthFor(ac.Cand, o, resp)
}

// resetIdle moves the attempt's bound from the pre-commit deadline to
// policy.timeout.idle. Post-commit, total stops applying and idle bounds the
// gap between events; a unary body is bounded the same way once its headers
// have arrived, because connect+first_byte was never meant to cover a
// multi-megabyte body on a slow link.
func (ac *AttemptCtx) resetIdle() {
	if ac.Timer == nil {
		return
	}
	if d := ac.Cfg.Policy.Timeout.Idle; d > 0 {
		ac.Timer.Reset(d)
	}
}

// served marks this attempt as the one that answered: the record names its
// target, carries its warnings and its time to first byte, and the breaker
// hears a success. It is called once the body has been parsed or the stream
// has committed — never from the status line alone, which a provider can
// send ahead of a body it then fails to deliver.
//
// Warnings are assigned, not appended: the request is re-rendered per
// attempt, and the record must describe the translation the client actually
// received rather than every attempt that was abandoned on the way there.
func (ac *AttemptCtx) served(warns []ir.Warning) {
	rec, c := ac.Rec, ac.Cand
	if rec.TTFTMs == nil {
		ttft := time.Since(rec.TS).Milliseconds()
		rec.TTFTMs = &ttft
	}
	rec.FinalProviderID = c.ProviderID
	rec.FinalModel = c.Model
	rec.Warnings = warningStrings(warns)
	ac.recordHealth(adapter.OutcomeSuccess, ac.resp)
}

// chatOp is the llm surface. It is the first SurfaceOp and its behavior is
// identical to phase 4's: the whole point of the extraction is that this file
// contains a move, not a rewrite.
type chatOp struct {
	d   edge.Dialect
	req *ir.Request
	pt  *edge.Passthrough
}

func (o *chatOp) Passthrough() *edge.Passthrough { return o.pt }

func (o *chatOp) WriteStreamError(w http.ResponseWriter, e *ir.Error) {
	_ = o.d.WriteStream(w, func(yield func(ir.StreamEvent, error) bool) {
		yield(ir.StreamEvent{}, e)
	})
}

func (o *chatOp) Dialect() string { return o.d.Name() }

func (o *chatOp) Query() router.Query {
	needs := o.req.Needs()
	return router.Query{
		Model: o.req.Model, Surface: ir.SurfaceLLM,
		NeedsTools: needs.Tools, NeedsVision: needs.Vision, NeedsReasoning: needs.Reasoning,
	}
}

func (o *chatOp) Build(ctx context.Context, tgt *adapter.Target, ad adapter.Adapter) (*http.Request, []ir.Warning, error) {
	hr, warns, err := ad.BuildRequest(ctx, tgt, o.req)
	// The inbound parse's losses travel with the outbound ones. Until phase 5
	// no dialect produced any, so nothing carried them and the responses
	// parser's dropped reasoning item would have been recorded nowhere.
	return hr, append(warns, o.req.Warnings...), err
}

func (o *chatOp) WriteError(w http.ResponseWriter, e *ir.Error) error {
	return o.d.WriteError(w, e)
}

func (o *chatOp) Respond(cw *CommitWriter, resp *http.Response, ac *AttemptCtx) (adapter.Outcome, *ir.Error) {
	if o.req.Stream {
		return ac.Exec.attemptStream(o.d, resp, ac, cw)
	}

	ac.resetIdle()
	out, perr := ac.Adapter.ParseResponse(resp)
	if perr != nil {
		return failedParse(ac, resp, perr)
	}
	applyUsage(ac.Rec, &out.Usage)
	ac.served(append(ac.Warns, out.Warnings...))
	ac.Exec.writeDiagnostics(cw, ac.Rec.ID, ac.Cand, ac.Seq)
	_ = o.d.WriteResponse(cw, out)
	return adapter.OutcomeSuccess, nil
}

// RunSurface is the entry point for a route whose request is already parsed.
// Handle uses it; the seam tests drive an op through it directly.
//
// cfg is passed rather than fetched because every caller already holds one: it
// needs max_body_bytes to parse. Taking a second snapshot here would break the
// one-config-per-request-lifetime rule the chat path has held since phase 3.
func (e *Executor) RunSurface(w http.ResponseWriter, r *http.Request, op SurfaceOp, cfg *config.Config) {
	start := time.Now()
	rec, done := e.newRecord(r, start, op.Dialect(), string(op.Query().Surface))
	defer done()
	e.beginResponse(w, rec)
	e.runOp(w, r, op, rec, start, cfg)
}

// RunAux is RunSurface with the parse step moved inside the record's lifetime.
//
// A route that parsed first would produce no request row for a malformed body,
// and chat does not behave that way: Handle opens its record before parsing so
// that a 400 is a request the gateway received and refused rather than one that
// never happened. Six routes dropping their 400s from the log would be a real
// regression in the only place an operator can see them.
//
// ew rather than the op writes the error, because on a parse failure there is
// no op yet — the dialect is what knows the client's error shape.
func (e *Executor) RunAux(w http.ResponseWriter, r *http.Request,
	dialect string, surface ir.Surface, ew errorWriter,
	build func(cfg *config.Config) (SurfaceOp, error)) {

	start := time.Now()
	rec, done := e.newRecord(r, start, dialect, string(surface))
	defer done()
	e.beginResponse(w, rec)

	cfg := e.store.Current() // one snapshot for this request's whole lifetime
	op, err := build(cfg)
	if err != nil {
		// A parser reporting an oversized body says so in the error it
		// returns, because only it knows the cap it was given. The typed error
		// is used whole rather than re-wrapped: err.Error() prepends the type
		// and would reach the client as "payload_too_large: request body …".
		e2 := &ir.Error{Type: ir.ErrInvalidRequest, Message: err.Error()}
		var ie *ir.Error
		if errors.As(err, &ie) && ie.Type != "" {
			e2 = ie
		}
		rec.ErrorCode = string(e2.Type)
		_ = ew.WriteError(w, e2)
		return
	}
	e.runOp(w, r, op, rec, start, cfg)
}

// beginResponse sets the two headers every route emits before it knows whether
// it will succeed. Attempts is overwritten by the diagnostics on both the
// success and the error path; the zero here is what a response that never
// attempted anything carries.
func (e *Executor) beginResponse(w http.ResponseWriter, rec *store.RequestRecord) {
	w.Header().Set("X-Darkrouter-Request", rec.ID)
	w.Header().Set("X-Darkrouter-Attempts", "0")
}

func (e *Executor) runOp(w http.ResponseWriter, r *http.Request, op SurfaceOp,
	rec *store.RequestRecord, start time.Time, cfg *config.Config) {

	res, ok := e.resolve(r.Context(), w, op, op.Query(), rec, cfg, start)
	if !ok {
		return
	}
	e.runAttempts(w, r, op, cfg, res.Candidates, rec, start, res.ByID, res.Catalog)
}

// newRecord opens the request row and returns the closer that emits it. The
// record is built as the request proceeds and emitted exactly once on every
// exit path, and Status starts as "error" so a path that forgets to set it is
// recorded as a failure rather than a silent success.
//
// It takes the dialect and surface as strings rather than a SurfaceOp because a
// body that failed to parse has no op yet and still owes the operator a row.
func (e *Executor) newRecord(r *http.Request, start time.Time, dialect, surface string) (*store.RequestRecord, func()) {
	rec := &store.RequestRecord{
		ID:      ulid.MustNew(ulid.Timestamp(start), rand.Reader).String(),
		TS:      start,
		Dialect: dialect,
		Surface: surface,
		Status:  "error",
		Source:  sourceOfRequest(r),
	}
	return rec, func() {
		total := time.Since(start).Milliseconds()
		rec.TotalMs = &total
		e.log(rec)
	}
}
