// Package exec drives a request to an upstream, attempting each candidate the
// router produced until one commits or the chain is exhausted.
package exec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"iter"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/auth"
	"github.com/darkraise/darkrouter/internal/catalog"
	"github.com/darkraise/darkrouter/internal/config"
	"github.com/darkraise/darkrouter/internal/edge"
	"github.com/darkraise/darkrouter/internal/health"
	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/provider"
	"github.com/darkraise/darkrouter/internal/router"
	"github.com/darkraise/darkrouter/internal/store"
)

// Logger receives one record per request. It must never block: the
// implementation in internal/store drops rather than waiting.
type Logger interface {
	Log(*store.RequestRecord)
}

// HealthRecorder receives one signal per attempt.
type HealthRecorder interface {
	Record(k health.Key, s health.Signal)
}

// Fleet is the live health state the loop consults between attempts. It is the
// same *health.Breaker as Deps.Health; a separate interface keeps the recorder
// narrow for the callers that only record.
type Fleet interface {
	SnapshotAvailability(at time.Time) health.Availability
	LastUsedSnapshot() map[health.CredKey]time.Time
	MarkUsed(ck health.CredKey, at time.Time)
	Available(k health.Key) bool
}

// CatalogSource supplies the live catalog. It is an interface rather than
// *catalog.Store so a test can hand over a fixed snapshot, and it is optional:
// a nil one falls back to phase 3's provider-derived view, where every model's
// capabilities are inferred.
type CatalogSource interface {
	Snapshot() *catalog.Snapshot
}

// AuthResolver turns a credential into an authorization applied to the built
// request. It is an interface rather than *auth.Manager so a test can hand over
// a fixed authorizer without constructing a signer.
type AuthResolver interface {
	For(ctx context.Context, t auth.Target, c auth.Credential) (auth.Authorizer, error)
}

// Deps carries the optional collaborators. A zero Deps is valid and disables
// the corresponding behavior.
type Deps struct {
	Log     Logger
	Health  HealthRecorder
	Fleet   Fleet
	Catalog CatalogSource

	// Auth resolves a non-static credential into an authorizer. Nil serves
	// static styles only, which is every provider before phase 8.
	Auth AuthResolver
}

type Executor struct {
	store    *config.Store
	src      provider.Source
	adapters map[string]adapter.Adapter
	// adapterSurfaces is what each kind can render, derived from adapters at
	// construction. It cannot change afterwards, so recomputing it per request
	// would allocate for a constant — and the router snapshot is meant to hold
	// frozen inputs rather than derived work.
	adapterSurfaces map[string]adapter.SurfaceSet
	client          *http.Client
	deps            Deps
}

// New builds the executor. Transport-level timeouts (connect, first_byte) are
// read once here because a shared Transport cannot vary them per request; both
// are documented restart-only. The total timeout is read per request.
func New(store *config.Store, src provider.Source, adapters map[string]adapter.Adapter, deps Deps) *Executor {
	t := store.Current().Policy.Timeout
	surfaces := make(map[string]adapter.SurfaceSet, len(adapters))
	for kind, ad := range adapters {
		surfaces[kind] = adapter.SurfacesOf(ad)
	}
	return &Executor{
		store: store, src: src, adapters: adapters, adapterSurfaces: surfaces, deps: deps,
		client: &http.Client{
			// Go follows redirects by default, silently turning a redirected
			// POST into a body-less GET.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
			Transport: &http.Transport{
				Proxy:                 http.ProxyFromEnvironment,
				DialContext:           (&net.Dialer{Timeout: t.Connect}).DialContext,
				ResponseHeaderTimeout: t.FirstByte,
				TLSHandshakeTimeout:   10 * time.Second,
				ForceAttemptHTTP2:     true,
				MaxIdleConns:          100,
				IdleConnTimeout:       90 * time.Second,
				// Spec §8: bytes must arrive as the provider sent them, so the
				// forwarder can pass them through unchanged. Go otherwise adds
				// Accept-Encoding: gzip and decompresses transparently, which
				// would make fidelity depend on a precondition rather than on
				// a setting. The IR path pays more bandwidth for it.
				DisableCompression: true,
			},
		},
	}
}

// adapterFor resolves a candidate's provider kind. A miss is a routing fact
// rather than an error: a SQLite row may name a kind whose adapter arrives in a
// later phase, and failing the chain there would be harder to diagnose than
// stepping over it with a reason on the record.
func (e *Executor) adapterFor(kind string) (adapter.Adapter, bool) {
	ad, ok := e.adapters[kind]
	return ad, ok
}

// inferredWarningFor records that a candidate was admitted on guessed
// capability metadata for a request that actually needed a capability.
//
// Master design §6.4 admits these rather than excluding them, because
// hard-filtering on a guess would make every discovered local model refuse the
// tool requests Claude Code always sends. The cost is that a provider's own
// rejection looks like a Darkrouter failure, and this is what makes the trace
// say otherwise.
//
// It takes the query rather than the request because an auxiliary surface has
// no ir.Request — and needs no capability, so it never warns.
func inferredWarningFor(c router.Candidate, q router.Query) (ir.Warning, bool) {
	if !c.Inferred {
		return ir.Warning{}, false
	}
	var missing []string
	if q.NeedsTools {
		missing = append(missing, "tools")
	}
	if q.NeedsVision {
		missing = append(missing, "vision")
	}
	if q.NeedsReasoning {
		missing = append(missing, "reasoning")
	}
	if len(missing) == 0 {
		// Warning about a plain chat request would be noise, and noise is what
		// trains people to ignore warnings.
		return ir.Warning{}, false
	}
	return ir.Warning{
		Field:  "capabilities",
		Target: c.ProviderID + "/" + c.Model,
		Reason: "the request needs " + strings.Join(missing, ", ") +
			" and this model's capabilities are unverified; routed anyway",
	}, true
}

// modelInfo copies the catalog's view of one model into the adapter's plain
// struct. A miss leaves the zero value, which every adapter reads as "the
// catalog knows nothing".
func modelInfo(cat catalog.Reader, providerID, modelID string) adapter.ModelInfo {
	if cat == nil {
		return adapter.ModelInfo{}
	}
	m, ok := cat.Lookup(providerID, modelID)
	if !ok {
		return adapter.ModelInfo{}
	}
	return adapter.ModelInfo{
		ContextWindow:   m.ContextWindow,
		MaxOutputTokens: m.MaxOutputTokens,
		Adaptive:        m.Traits.Adaptive,
		ManualBudget:    m.Traits.ManualBudget,
		FreeSampling:    m.Traits.FreeSampling,
		TraitsKnown:     m.Traits.Known,
	}
}

// rerankPath returns the provider's preset-declared rerank path, or "" when it
// has no preset or the preset declares none. Spec §3.1: providers expose rerank
// at differing URLs, so the path is preset data rather than an adapter
// constant.
func rerankPath(preset string) string {
	if preset == "" {
		return ""
	}
	p, ok := catalog.Embedded()[preset]
	if !ok {
		return ""
	}
	v, _ := p.QuirkValue("rerank-path")
	return v
}

// catalogFor returns the live snapshot, or phase 3's provider-derived view
// when no catalog is wired. The fallback is what keeps a zero Deps usable.
func (e *Executor) catalogFor(providers []provider.Provider) catalog.Reader {
	if e.deps.Catalog != nil {
		return e.deps.Catalog.Snapshot()
	}
	return catalog.FromProviders(providers)
}

func (e *Executor) Handle(w http.ResponseWriter, r *http.Request, d edge.Dialect) {
	cfg := e.store.Current() // one snapshot for this request's whole lifetime

	if compressedBody(r) {
		// Refused before parsing, but still logged: the client sent bytes and
		// got a response, which is an event the operator's records must cover.
		start := time.Now()
		rec, done := e.newRecord(start, d.Name(), string(ir.SurfaceLLM))
		defer done()
		w.Header().Set("X-Darkrouter-Request", rec.ID)
		w.Header().Set("X-Darkrouter-Attempts", "0")
		rec.ErrorCode = string(ir.ErrUnsupportedMedia)
		_ = d.WriteError(w, &ir.Error{
			Type:    ir.ErrUnsupportedMedia,
			Message: "content-encoding is not supported: send an uncompressed request body",
		})
		return
	}

	req, pt, err := d.ParseRequest(r, cfg.Server.MaxBodyBytes)
	if err != nil {
		// The row is opened and closed here rather than in RunSurface: a body
		// that never parsed has no op to name the surface it was asking for,
		// and the operator is still owed the record.
		start := time.Now()
		rec, done := e.newRecord(start, d.Name(), string(ir.SurfaceLLM))
		defer done()
		w.Header().Set("X-Darkrouter-Request", rec.ID)
		w.Header().Set("X-Darkrouter-Attempts", "0")
		rec.ErrorCode = string(ir.ErrInvalidRequest)
		_ = d.WriteError(w, &ir.Error{Type: ir.ErrInvalidRequest, Message: err.Error()})
		return
	}
	e.RunSurface(w, r, &chatOp{d: d, req: req, pt: pt}, cfg)
}

// runAttempts drives the chain. The ordered list is fixed at snapshot time and
// never re-ordered; only skipping is dynamic, because another request may have
// tripped a breaker since the snapshot was taken.
func (e *Executor) runAttempts(w http.ResponseWriter, r *http.Request, op SurfaceOp,
	cfg *config.Config, cands []router.Candidate,
	rec *store.RequestRecord, start time.Time, byID map[string]provider.Provider,
	cat catalog.Reader) {

	// The first candidate the router produced, captured before the loop so a
	// candidate skipped by the live health re-check is still "first". Spec §8's
	// embedding warning depends on this distinction.
	var firstModel string
	if len(cands) > 0 {
		firstModel = cands[0].Model
	}

	bud := newBudget(cfg.Policy.Timeout, start)
	maxAttempts := cfg.Policy.Retry.MaxAttempts

	var lastErr *ir.Error
	attempts := 0

	for i := 0; i < len(cands) && attempts < maxAttempts; {
		c := cands[i]
		now := time.Now()

		// The budget gate: an attempt that cannot possibly complete wastes the
		// budget and the provider's quota, and replaces a clear
		// attempts-exhausted error with a bare timeout.
		if !bud.canStartAttempt(now) {
			if lastErr == nil {
				lastErr = &ir.Error{Type: ir.ErrDarkrouter, Message: "attempts exhausted by deadline"}
			} else {
				lastErr.Message += " (attempts exhausted by deadline)"
			}
			break
		}

		// Re-check live health: another request may have tripped this breaker
		// since the snapshot. Record the skip so the trace still explains the
		// realized sequence.
		hk := health.Key{ProviderID: c.ProviderID, KeyID: c.KeyID, Model: c.Model}
		if e.deps.Fleet != nil && !e.deps.Fleet.Available(hk) {
			rec.Skips = append(rec.Skips, traceSkipOf(c, "cooling"))
			i++
			continue
		}

		ad, ok := e.adapterFor(c.Kind)
		if !ok {
			rec.Skips = append(rec.Skips, traceSkipOf(c, "no_adapter"))
			i++
			continue
		}

		// At attempt start, not on success: a credential that always fails must
		// not keep a stale timestamp and sort first forever.
		if e.deps.Fleet != nil {
			e.deps.Fleet.MarkUsed(health.CredKey{ProviderID: c.ProviderID, KeyID: c.KeyID}, now)
		}

		attempts++
		res := e.attempt(w, r, op, cfg, c, byID[c.ProviderID], bud, rec, attempts, ad, cat, firstModel, true, nil)
		if res.Err != nil {
			lastErr = res.Err
		}

		// spec §9: strict openaicompat providers reject fields the IR path
		// would have dropped with a warning. Classifying that as Fatal would
		// convert a request the IR path could have served into a hard failure
		// with no failover — a silent regression introduced by an
		// optimization. The same candidate is retried once through the IR path
		// before any Fatal classification stands, and both attempts are
		// recorded.
		if res.Path == PathPassthrough && !res.Committed &&
			res.Outcome == adapter.OutcomeFatal && res.Status == http.StatusBadRequest &&
			attempts < maxAttempts && bud.canStartAttempt(time.Now()) {

			attempts++
			// The retry drops whatever the provider rejected, and a client told
			// only "200" would believe its parameter took effect. Naming the
			// field is not possible here: the edge parser unmarshals into a
			// fixed struct, so an unmodelled top-level key is already gone.
			retryWarn := []ir.Warning{{
				Field: "passthrough", Target: c.ProviderID + "/" + c.Model,
				Reason: "the provider rejected the forwarded body; it was " +
					"translated and retried, which drops any field the IR does not model",
			}}
			res = e.attempt(w, r, op, cfg, c, byID[c.ProviderID], bud, rec, attempts, ad, cat, firstModel, false, retryWarn)
			if res.Err != nil {
				lastErr = res.Err
			}
		}

		next, action := nextIndex(cands, i, res.Outcome, res.Status)
		switch action {
		case actionFinish:
			rec.Status = "success"
			return
		case actionReturn:
			if res.Outcome == adapter.OutcomeClientCancelled {
				rec.Status = "cancelled"
			}
			if lastErr != nil {
				rec.ErrorCode = string(lastErr.Type)
				e.writeErrorDiagnostics(w, rec, attempts)
				_ = op.WriteError(w, lastErr)
			}
			return
		default:
			i = next
		}
	}

	if lastErr == nil {
		lastErr = &ir.Error{Type: ir.ErrAPI, Message: "every candidate failed"}
	}
	rec.ErrorCode = string(lastErr.Type)
	e.writeErrorDiagnostics(w, rec, attempts)
	_ = op.WriteError(w, lastErr)
}

// The two renderings an attempt can use. They are the values of the request
// row's path column, so the strings are part of the schema.
const (
	PathIR          = "ir"
	PathPassthrough = "passthrough"
)

// attemptResult is what one upstream call produced. It replaced three returns
// when passthrough arrived: the loop needs to know which rendering ran and
// whether anything reached the client, and neither fits in an outcome.
type attemptResult struct {
	Outcome   adapter.Outcome
	Status    int
	Err       *ir.Error
	Path      string
	Committed bool
}

// attempt performs one upstream call and records it. allowForward gates the
// fast path: the pre-commit 400 retry (spec §9) calls back in with it false so
// the same candidate's second try cannot loop back onto the rendering that
// just failed.
func (e *Executor) attempt(w http.ResponseWriter, r *http.Request, op SurfaceOp,
	cfg *config.Config, c router.Candidate, p provider.Provider,
	bud budget, rec *store.RequestRecord, seq int, ad adapter.Adapter,
	cat catalog.Reader, firstModel string, allowForward bool,
	extraWarns []ir.Warning) attemptResult {

	// A timer rather than a context deadline, because the bound changes at
	// commit: total stops applying and idle takes over. A deadline cannot be
	// moved once set.
	ctx, cancel := context.WithCancelCause(r.Context())
	defer cancel(nil)
	timer := time.AfterFunc(time.Until(bud.attemptDeadline(time.Now())), func() {
		cancel(errDarkrouterTimeout)
	})
	defer timer.Stop()

	apiKey, authorizer, credErr := e.credentialFor(ctx, p, c)
	if credErr != nil {
		return attemptResult{Outcome: adapter.OutcomeFatal, Path: PathIR,
			Err: &ir.Error{Type: ir.ErrDarkrouter, Message: credErr.Error()}}
	}
	tgt := &adapter.Target{
		BaseURL: p.BaseURL, APIKey: apiKey, Model: c.Model,
		Info:       modelInfo(cat, c.ProviderID, c.Model),
		RerankPath: rerankPath(p.Preset),
		Region:     p.Region, Project: p.Project, Location: p.Location,
		Publisher: c.Publisher,
	}
	warns := append([]ir.Warning(nil), extraWarns...)
	if iw, ok := inferredWarningFor(c, op.Query()); ok {
		warns = append(warns, iw)
	}

	path := PathIR
	var (
		hr        *http.Request
		fw        adapter.Forwarder
		strip     bool
		streaming bool
	)
	if allowForward {
		if pop, ok := op.(passthroughOp); ok {
			pt := pop.Passthrough()
			if f, eligible := forwardable(op.Dialect(), pt, c, p, ad); eligible {
				body, injected, rerr := rewriteForward(pt, op.Query().Model, c.Model, c.Kind)
				if rerr != nil {
					// spec §9: the IR parser produces a proper dialect-shaped
					// error if the body is genuinely invalid, and it can tell
					// the difference where this cannot. Not a second attempt —
					// nothing has reached a provider.
					warns = append(warns, ir.Warning{
						Field: "passthrough", Target: c.ProviderID + "/" + c.Model,
						Reason: "the body could not be forwarded and was translated instead: " +
							rerr.Error(),
					})
				} else {
					built, berr := f.BuildForward(ctx, tgt, &adapter.Forward{
						Body: body, Header: forwardHeaders(r), Stream: pt.Stream,
						Method: pt.Method, Query: pt.Query,
					})
					if berr != nil {
						return attemptResult{Outcome: adapter.OutcomeFatal, Path: PathIR,
							Err: &ir.Error{Type: ir.ErrDarkrouter, Message: berr.Error()}}
					}
					hr, fw, strip, streaming, path = built, f, injected, pt.Stream, PathPassthrough
				}
			}
		}
	}
	if hr == nil {
		built, buildWarns, err := op.Build(ctx, tgt, ad)
		warns = append(warns, buildWarns...)
		if err != nil {
			return attemptResult{Outcome: adapter.OutcomeFatal, Path: path,
				Err: &ir.Error{Type: ir.ErrDarkrouter, Message: err.Error()}}
		}
		hr = built
	}
	if err := makeReplayable(hr); err != nil {
		return attemptResult{Outcome: adapter.OutcomeFatal, Path: path,
			Err: &ir.Error{Type: ir.ErrDarkrouter, Message: err.Error()}}
	}
	if err := applyAuthorizer(ctx, hr, authorizer); err != nil {
		// A credential that cannot be produced is a credential failure, not a
		// provider one: an expired OAuth grant must cool the account rather
		// than the upstream, which is serving everyone else fine.
		return attemptResult{Outcome: adapter.OutcomeRetryableCredential, Path: path,
			Err: &ir.Error{Type: ir.ErrAuthentication, Message: err.Error()}}
	}

	attemptStart := time.Now()
	resp, doErr := e.client.Do(hr)
	outcome := e.classify(ad, r.Context(), ctx, resp, doErr)

	// Some OpenAI-compatible providers report an unknown model as a 400 with an
	// identifying error code. Classifying that as Fatal would make failover die
	// on the first provider in a chain that does not carry the model. The 64 KiB
	// bound matters: an error body is small, and reading an unbounded one from a
	// misbehaving provider is the hazard max_body_bytes exists to prevent.
	if bc, ok := ad.(adapter.BodyClassifier); ok &&
		outcome == adapter.OutcomeFatal && resp != nil && resp.StatusCode == 400 {

		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		resp.Body = io.NopCloser(bytes.NewReader(body))
		outcome = bc.ClassifyBody(resp, body, doErr)
	}

	statusCode := 0
	if resp != nil {
		statusCode = resp.StatusCode
	}
	e.recordAttempt(rec, c, outcome, statusCode, doErr, time.Since(attemptStart), path)
	e.recordHealthFor(c, outcome, resp)

	if outcome != adapter.OutcomeSuccess {
		if resp != nil {
			resp.Body.Close()
		}
		return attemptResult{Outcome: outcome, Status: statusCode, Err: errorFor(outcome, doErr), Path: path}
	}

	cw := NewCommitWriter(w)
	ac := &AttemptCtx{
		Exec: e, Cfg: cfg, Cand: c, Rec: rec, Seq: seq, Timer: timer,
		Warns: warns, Adapter: ad, FirstModel: firstModel,
	}
	var aerr *ir.Error
	switch {
	case path == PathPassthrough && streaming:
		outcome, aerr = e.forwardStream(cw, resp, ac, fw, strip)
	case path == PathPassthrough:
		outcome, aerr = e.forwardUnary(cw, resp, ac, fw)
	default:
		outcome, aerr = op.Respond(cw, resp, ac)
	}
	demoteLastAttempt(rec, outcome, cw.Committed())
	// The loop asks the writer, not the op. An op that reports a retryable
	// outcome after bytes have gone out is describing a post-commit failure,
	// and phase 3's rule says the chain ends there regardless — a second
	// attempt would concatenate two half-responses on one connection.
	if cw.Committed() && outcome != adapter.OutcomeSuccess {
		rec.ErrorCode = string(ir.ErrAPI)
		return attemptResult{Outcome: adapter.OutcomeSuccess, Status: statusCode,
			Path: path, Committed: true}
	}
	return attemptResult{Outcome: outcome, Status: statusCode, Err: aerr,
		Path: path, Committed: cw.Committed()}
}

// The attempt row is written from the HTTP status before the body is read,
// so a 200 that fails while forwarding is recorded as a success it never
// was. A committed attempt keeps its success: bytes reached the client and
// the chain ends there regardless of what the op reports afterwards.
func demoteLastAttempt(rec *store.RequestRecord, outcome adapter.Outcome, committed bool) {
	if committed || outcome == adapter.OutcomeSuccess {
		return
	}
	if n := len(rec.Attempts); n > 0 {
		rec.Attempts[n-1].Outcome = string(outcome)
	}
}

// attemptStream buffers the upstream's events until one of them commits the
// response, then replays the buffer and streams the rest.
//
// Nothing reaches the client before commit, which is what makes a pre-commit
// failure invisible: the buffered events are simply discarded and the loop
// tries the next candidate. The dialect writer is not even constructed until
// commit, because building it mutates the response headers.
func (e *Executor) attemptStream(d edge.Dialect,
	cfg *config.Config, c router.Candidate, resp *http.Response,
	rec *store.RequestRecord, seq int, timer *time.Timer,
	warns []ir.Warning, ad adapter.Adapter, cw *CommitWriter) (adapter.Outcome, *ir.Error) {

	defer resp.Body.Close()

	// Warnings raised by the events themselves, as distinct from the ones the
	// request rendering produced.
	var streamWarns []ir.Warning

	next, stop := iter.Pull2(ad.ParseStream(resp.Body, cfg.Server.SSE.MaxLineBytes))
	defer stop()

	buf := newPreCommitBuffer(cfg.Server.SSE.MaxPrecommitBytes)
	var committed ir.StreamEvent
	haveCommitted := false

	// Phase one: drain until a content-bearing event arrives, or the attempt
	// fails. Nothing is written to w in this phase.
	for {
		ev, err, ok := next()
		if !ok {
			// The stream ended cleanly without any content-bearing event. That
			// is a legitimately empty completion, not a fault: failing over
			// here would burn the whole chain every time a model stops
			// immediately. Fall through and flush whatever was buffered.
			break
		}
		if err != nil {
			// A 2xx whose stream fails before commit is classified from the
			// stream error, not the status line. Anthropic delivers
			// overloaded_error as an in-stream event under a 200.
			return adapter.OutcomeRetryableProvider,
				e.reclassifyStream(c, resp, rec, err.Error())
		}
		if ev.Usage != nil {
			applyUsage(rec, ev.Usage)
		}
		streamWarns = append(streamWarns, ev.Warnings...)
		if IsContentBearing(ev) {
			committed, haveCommitted = ev, true
			break
		}
		if berr := buf.add(ev); berr != nil {
			// A cap breach is an attempt failure, not a client error: the
			// provider is misbehaving and another one may not.
			return adapter.OutcomeRetryableProvider,
				e.reclassifyStream(c, resp, rec, berr.Error())
		}
	}

	// Phase two: committed, or the stream ended empty. Failover is impossible
	// from here, so every later failure becomes an error event in the stream.
	if haveCommitted {
		ttft := time.Since(rec.TS).Milliseconds()
		rec.TTFTMs = &ttft
	}
	rec.FinalProviderID = c.ProviderID
	rec.FinalModel = c.Model
	rec.Warnings = warningStrings(warns)
	e.writeDiagnostics(cw, rec.ID, c, seq)

	// Post-commit, policy.timeout.total stops applying and policy.timeout.idle
	// bounds the gap between events instead: a legitimate ten-minute reasoning
	// response must not be killed, while a provider that goes silent must be.
	idle := cfg.Policy.Timeout.Idle
	resetIdle := func() {
		if idle > 0 {
			timer.Reset(idle)
		}
	}
	resetIdle()

	events := func(yield func(ir.StreamEvent, error) bool) {
		for _, buffered := range buf.events() {
			if !yield(buffered, nil) {
				return
			}
		}
		if haveCommitted && !yield(committed, nil) {
			return
		}
		for {
			ev, err, ok := next()
			if !ok {
				return
			}
			if err == nil {
				resetIdle()
				if ev.Usage != nil {
					applyUsage(rec, ev.Usage)
				}
				streamWarns = append(streamWarns, ev.Warnings...)
			}
			if !yield(ev, err) {
				return
			}
			if err != nil {
				// Failover is impossible: the client already has bytes. The
				// dialect has rendered the error and the stream now ends.
				return
			}
		}
	}
	_ = d.WriteStream(cw, events)
	rec.Warnings = warningStrings(append(warns, streamWarns...))
	return adapter.OutcomeSuccess, nil
}

// reclassifyStream records a pre-commit stream failure against health and the
// attempt row, and returns the error to serve if this was the last candidate.
func (e *Executor) reclassifyStream(c router.Candidate, resp *http.Response,
	rec *store.RequestRecord, msg string) *ir.Error {

	e.recordHealthFor(c, adapter.OutcomeRetryableProvider, resp)
	if n := len(rec.Attempts); n > 0 {
		rec.Attempts[n-1].Outcome = string(adapter.OutcomeRetryableProvider)
		rec.Attempts[n-1].Error = msg
	}
	return &ir.Error{Type: ir.ErrAPI, Message: msg}
}

var errDarkrouterTimeout = errors.New("darkrouter: total timeout exceeded")

// classify asks the adapter, then overrides for the two cases no adapter can
// see: a Darkrouter-imposed deadline, and a cancellation whose origin is the
// inbound request rather than the upstream.
//
// The deadline is checked first. Both cancel the same derived context, and if
// the client also disappears in that instant, checking the disconnect first
// would silently reclassify a genuine provider timeout as a client hang-up.
func (e *Executor) classify(ad adapter.Adapter, inbound, upstream context.Context,
	resp *http.Response, err error) adapter.Outcome {

	if err == nil {
		return ad.Classify(resp, nil)
	}
	if errors.Is(context.Cause(upstream), errDarkrouterTimeout) {
		return adapter.OutcomeRetryableProvider
	}
	if errors.Is(inbound.Err(), context.Canceled) {
		return adapter.OutcomeClientCancelled
	}
	return ad.Classify(resp, err)
}

func (e *Executor) log(rec *store.RequestRecord) {
	if e.deps.Log == nil {
		return
	}
	e.priceRecord(rec)
	e.deps.Log.Log(rec)
}

// priceRecord fills in CostMicros from the catalog price of the model that
// actually served, then does the same for every attempt against the model
// IT tried.
//
// Here rather than in applyUsage because applyUsage has eleven call sites and
// this has one: cost is a property of the finished request, not of each usage
// event that arrived on the way. A record with nothing served, no catalog, or
// an unpriced model keeps a nil cost -- the em-dash the trace already renders.
func (e *Executor) priceRecord(rec *store.RequestRecord) {
	if rec == nil || e.deps.Catalog == nil {
		return
	}
	snap := e.deps.Catalog.Snapshot()
	if snap == nil {
		return
	}
	if rec.CostMicros == nil && rec.FinalProviderID != "" && rec.FinalModel != "" {
		if m, ok := snap.Lookup(rec.FinalProviderID, rec.FinalModel); ok {
			rec.CostMicros = m.Pricing.CostMicros(
				rec.TokensIn, rec.TokensOut, rec.CacheReadTokens)
		}
	}

	// A pre-commit stream failure can report usage before it dies, and
	// nothing resets the shared record between attempts. If another attempt
	// already carries that usage, it is not the served attempt's to claim --
	// handing it over would bill one provider for tokens another one burned.
	var claimed int64
	for i := range rec.Attempts {
		claimed += rec.Attempts[i].TokensIn + rec.Attempts[i].TokensOut
	}

	// recordAttempt runs while the attempt is still in flight, before its
	// usage is known. By log time applyUsage has put the served attempt's
	// usage on the request, so this is the first point it can be attributed.
	if claimed == 0 {
		for i := range rec.Attempts {
			a := &rec.Attempts[i]
			// Identified by outcome, not by matching the request's final provider:
			// the pre-commit 400 retry re-attempts the same provider and model, so
			// a provider match would find the rejected attempt first.
			if a.Outcome == string(adapter.OutcomeSuccess) &&
				a.TokensIn == 0 && a.TokensOut == 0 {
				a.TokensIn, a.TokensOut = rec.TokensIn, rec.TokensOut
				// The same model at the same rates on the same tokens. Re-pricing
				// it separately drops the cache-read component the attempt row has
				// no column for, and the two cost surfaces stop agreeing.
				if rec.CostMicros != nil {
					c := *rec.CostMicros
					a.CostMicros = &c
				}
				break
			}
		}
	}

	// Each attempt is priced against the model IT tried, not the one that
	// served: a failover's discarded tokens were burned at the failed
	// provider's rate.
	for i := range rec.Attempts {
		a := &rec.Attempts[i]
		if a.CostMicros != nil || a.ProviderID == "" || a.Model == "" {
			continue
		}
		// No tokens recorded is not the same as nothing spent: a NULL cost
		// keeps the rollup from reporting a priced day of zero.
		if a.TokensIn == 0 && a.TokensOut == 0 {
			continue
		}
		am, ok := snap.Lookup(a.ProviderID, a.Model)
		if !ok {
			continue
		}
		a.CostMicros = am.Pricing.CostMicros(a.TokensIn, a.TokensOut, 0)
	}
}

func (e *Executor) recordHealth(k health.Key, s health.Signal) {
	if e.deps.Health == nil {
		return
	}
	e.deps.Health.Record(k, s)
}

func (e *Executor) recordAttempt(rec *store.RequestRecord, c router.Candidate,
	o adapter.Outcome, statusCode int, err error, latency time.Duration, path string) {

	a := store.AttemptRecord{
		Seq: len(rec.Attempts), ProviderID: c.ProviderID, KeyID: c.KeyID, Model: c.Model,
		Outcome: string(o), StatusCode: statusCode, LatencyMs: latency.Milliseconds(),
		Path: path,
	}
	if err != nil {
		a.Error = err.Error()
	}
	rec.Attempts = append(rec.Attempts, a)
}

func (e *Executor) recordHealthFor(c router.Candidate, o adapter.Outcome, resp *http.Response) {
	sig := health.Signal{Outcome: o}
	if resp != nil {
		sig.StatusCode = resp.StatusCode
		// Read before the body is closed; a 429's Retry-After is the difference
		// between a precise cooldown and the generic ladder.
		if d, ok := health.ParseRetryAfter(resp.Header.Get("Retry-After"), time.Now()); ok {
			sig.RetryAfter, sig.HasRetryAfter = d, true
		}
	}
	e.recordHealth(health.Key{ProviderID: c.ProviderID, KeyID: c.KeyID, Model: c.Model}, sig)
}

// writeDiagnostics names the target that served the response. Master design §8
// requires these on commit and on Darkrouter-originated errors.
func (e *Executor) writeDiagnostics(w http.ResponseWriter, reqID string, c router.Candidate, attempts int) {
	w.Header().Set("X-Darkrouter-Request", reqID)
	w.Header().Set("X-Darkrouter-Provider", c.ProviderID)
	w.Header().Set("X-Darkrouter-Model", c.Model)
	w.Header().Set("X-Darkrouter-Attempts", strconv.Itoa(attempts))
}

// writeErrorDiagnostics names the last attempted target on an error response.
// Provider and model are omitted when no attempt was made, because naming one
// would imply it was tried.
func (e *Executor) writeErrorDiagnostics(w http.ResponseWriter, rec *store.RequestRecord, attempts int) {
	w.Header().Set("X-Darkrouter-Attempts", strconv.Itoa(attempts))
	if len(rec.Attempts) == 0 {
		return
	}
	last := rec.Attempts[len(rec.Attempts)-1]
	w.Header().Set("X-Darkrouter-Provider", last.ProviderID)
	w.Header().Set("X-Darkrouter-Model", last.Model)
}

// makeReplayable sets GetBody so retries inside the transport can resend the
// body. Each attempt re-renders from the IR, so this is not what makes failover
// work — it is what stops a transport-level retry from sending an empty body.
func makeReplayable(hr *http.Request) error {
	if hr.Body == nil || hr.GetBody != nil {
		return nil
	}
	buf, err := io.ReadAll(hr.Body)
	if err != nil {
		return err
	}
	_ = hr.Body.Close()
	hr.Body = io.NopCloser(bytes.NewReader(buf))
	hr.ContentLength = int64(len(buf))
	hr.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(buf)), nil
	}
	return nil
}

// applyAuthorizer runs the authorizer against a request whose body is already
// materialized. Split out so the ordering — materialize, then authorize, then
// send — is one named thing a test can hold rather than three lines in the
// middle of the attempt loop.
func applyAuthorizer(ctx context.Context, hr *http.Request, a auth.Authorizer) error {
	if a == nil {
		return nil
	}
	return a(ctx, hr)
}

// credentialFor returns the target's authorizer and the api key the adapter
// should write. Exactly one of them is ever non-zero: a non-static style leaves
// the key empty so no adapter writes a token document into its own header.
func (e *Executor) credentialFor(ctx context.Context, p provider.Provider,
	c router.Candidate) (string, auth.Authorizer, error) {

	style := p.AuthStyle
	if style == "" {
		style = presetStyle(p.Preset)
	}
	secret := secretOf(p, c.KeyID)
	if auth.IsStatic(style) {
		return secret, nil, nil
	}
	if e.deps.Auth == nil {
		return "", nil, fmt.Errorf("provider %q needs the %s strategy, which is not wired",
			p.ID, style)
	}
	az, err := e.deps.Auth.For(ctx, auth.Target{
		ProviderID: p.ID, Style: style, Preset: p.Preset,
		Region: p.Region, Project: p.Project, Location: p.Location,
	}, auth.Credential{ID: c.KeyID, Kind: credentialKind(p, c.KeyID), Secret: secret})
	if err != nil {
		return "", nil, err
	}
	return "", az, nil
}

func credentialKind(p provider.Provider, keyID string) string {
	for _, c := range p.Credentials {
		if c.ID == keyID {
			return c.Kind
		}
	}
	return ""
}

// presetStyle reads the shipped style for a provider whose row does not
// override it. It mirrors rerankPath, which already reaches presets from here.
func presetStyle(preset string) string {
	if preset == "" {
		return ""
	}
	return catalog.Embedded()[preset].Auth.Style
}

func secretOf(p provider.Provider, keyID string) string {
	for _, c := range p.Credentials {
		if c.ID == keyID {
			return c.Secret
		}
	}
	return ""
}

func traceCandidates(cs []router.Candidate) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.ProviderID+"/"+c.KeyID+"/"+c.Model)
	}
	return out
}

func traceSkips(ss []router.Skip) []string {
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		out = append(out, s.ProviderID+"/"+s.KeyID+"/"+s.Model+":"+string(s.Reason))
	}
	return out
}

func traceSkipOf(c router.Candidate, reason string) string {
	return c.ProviderID + "/" + c.KeyID + "/" + c.Model + ":" + reason
}

// routerError maps the router's distinguishable empty-result cases onto the
// dialect's error shape. Collapsing them here would undo the whole point of
// having separate sentinels.
func routerError(err error) *ir.Error {
	switch {
	case errors.Is(err, router.ErrModelNotFound):
		return &ir.Error{Type: ir.ErrNotFound, Message: "no configured provider offers this model"}
	case errors.Is(err, router.ErrSurfaceUnsupported):
		return &ir.Error{Type: ir.ErrNotFound, Message: "no configured provider offers this model on this surface"}
	case errors.Is(err, router.ErrAllCooling):
		return &ir.Error{Type: ir.ErrAPI, Message: "every provider offering this model is cooling"}
	case errors.Is(err, router.ErrCapabilityUnsatisfied):
		return &ir.Error{Type: ir.ErrInvalidRequest, Message: "no provider offering this model has the required capabilities"}
	default:
		return &ir.Error{Type: ir.ErrDarkrouter, Message: err.Error()}
	}
}

// outcomeForParseError separates "this provider is broken" from "this provider
// answered, and the answer was a refusal". Only the first is a health signal.
func outcomeForParseError(err error) adapter.Outcome {
	var e *ir.Error
	if errors.As(err, &e) && e.Type == ir.ErrContentFilter {
		return adapter.OutcomeFatal
	}
	return adapter.OutcomeRetryableProvider
}

func errorFor(o adapter.Outcome, err error) *ir.Error {
	msg := "upstream request failed"
	if err != nil {
		msg = err.Error()
	}
	switch o {
	case adapter.OutcomeRetryableCredential:
		return &ir.Error{Type: ir.ErrAuthentication, Message: "upstream rejected the credential"}
	case adapter.OutcomeRetryableModel:
		return &ir.Error{Type: ir.ErrNotFound, Message: "upstream does not serve this model"}
	case adapter.OutcomeFatal:
		return &ir.Error{Type: ir.ErrInvalidRequest, Message: msg}
	case adapter.OutcomeClientCancelled:
		return &ir.Error{Type: ir.ErrDarkrouter, Message: "client cancelled the request"}
	default:
		return &ir.Error{Type: ir.ErrAPI, Message: msg}
	}
}

// warningStrings flattens for the request row, whose warnings column is a JSON
// array of strings. Nil stays nil so the column encodes [] rather than null.
func warningStrings(ws []ir.Warning) []string {
	if len(ws) == 0 {
		return nil
	}
	out := make([]string, 0, len(ws))
	for _, w := range ws {
		out = append(out, w.String())
	}
	return out
}

func applyUsage(rec *store.RequestRecord, u *ir.Usage) {
	if u == nil {
		return
	}
	rec.TokensIn = int64(u.InputTokens)
	rec.TokensOut = int64(u.OutputTokens)
	rec.CacheReadTokens = int64(u.CacheReadTokens)
	rec.CacheWriteTokens = int64(u.CacheWriteTokens)
	rec.ReasoningTokens = int64(u.ReasoningTokens)
	// CostMicros stays nil here: it is priced once in priceRecord, at log
	// time, from the model that actually served -- not from each usage event
	// that arrived on the way.
}

// tapStream observes events on their way to the edge writer without buffering
// them. Usage arrives on a late event and the first content delta defines TTFT,
// so neither is visible unless the sequence is wrapped.
func tapStream(events iter.Seq2[ir.StreamEvent, error],
	onFirstContent func(), onUsage func(*ir.Usage)) iter.Seq2[ir.StreamEvent, error] {

	return func(yield func(ir.StreamEvent, error) bool) {
		seenContent := false
		for ev, err := range events {
			if err == nil {
				if !seenContent && ev.Type == ir.EventContentDelta {
					seenContent = true
					onFirstContent()
				}
				if ev.Usage != nil {
					onUsage(ev.Usage)
				}
			}
			if !yield(ev, err) {
				return
			}
		}
	}
}
