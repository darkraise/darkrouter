// Package exec drives a request to an upstream, attempting each candidate the
// router produced until one commits or the chain is exhausted.
package exec

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"iter"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/darkraise/darkrouter/internal/adapter"
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

// Deps carries the optional collaborators. A zero Deps is valid and disables
// the corresponding behavior.
type Deps struct {
	Log     Logger
	Health  HealthRecorder
	Fleet   Fleet
	Catalog CatalogSource
}

type Executor struct {
	store    *config.Store
	src      provider.Source
	adapters map[string]adapter.Adapter
	client   *http.Client
	deps     Deps
}

// New builds the executor. Transport-level timeouts (connect, first_byte) are
// read once here because a shared Transport cannot vary them per request; both
// are documented restart-only. The total timeout is read per request.
func New(store *config.Store, src provider.Source, adapters map[string]adapter.Adapter, deps Deps) *Executor {
	t := store.Current().Policy.Timeout
	return &Executor{
		store: store, src: src, adapters: adapters, deps: deps,
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

// inferredWarning records that a candidate was admitted on guessed capability
// metadata for a request that actually needed a capability.
//
// Master design §6.4 admits these rather than excluding them, because
// hard-filtering on a guess would make every discovered local model refuse the
// tool requests Claude Code always sends. The cost is that a provider's own
// rejection looks like a Darkrouter failure, and this is what makes the trace
// say otherwise.
func inferredWarning(c router.Candidate, req *ir.Request) (ir.Warning, bool) {
	if !c.Inferred {
		return ir.Warning{}, false
	}
	needs := req.Needs()
	var missing []string
	if needs.Tools {
		missing = append(missing, "tools")
	}
	if needs.Vision {
		missing = append(missing, "vision")
	}
	if needs.Reasoning {
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

// catalogFor returns the live snapshot, or phase 3's provider-derived view
// when no catalog is wired. The fallback is what keeps a zero Deps usable.
func (e *Executor) catalogFor(providers []provider.Provider) catalog.Reader {
	if e.deps.Catalog != nil {
		return e.deps.Catalog.Snapshot()
	}
	return catalog.FromProviders(providers)
}

func (e *Executor) Handle(w http.ResponseWriter, r *http.Request, d edge.Dialect) {
	start := time.Now()
	cfg := e.store.Current() // one snapshot for this request's whole lifetime
	reqID := ulid.MustNew(ulid.Timestamp(start), rand.Reader).String()

	// The record is built as the request proceeds and emitted exactly once, on
	// every exit path. Status starts as "error" so an early return that forgets
	// to set it is recorded as a failure rather than a silent success.
	rec := &store.RequestRecord{
		ID: reqID, TS: start, Dialect: d.Name(), Surface: string(ir.SurfaceLLM), Status: "error",
	}
	defer func() {
		total := time.Since(start).Milliseconds()
		rec.TotalMs = &total
		e.log(rec)
	}()

	w.Header().Set("X-Darkrouter-Request", reqID)
	w.Header().Set("X-Darkrouter-Attempts", "0")

	req, pt, err := d.ParseRequest(r, cfg.Server.MaxBodyBytes)
	if err != nil {
		rec.ErrorCode = string(ir.ErrInvalidRequest)
		_ = d.WriteError(w, &ir.Error{Type: ir.ErrInvalidRequest, Message: err.Error()})
		return
	}
	// A dialect that returns no passthrough, or leaves the surface unset, is
	// serving chat. Parsing a string here was Phase 3 checking at runtime what
	// the type system can check at compile time.
	surface := ir.SurfaceLLM
	if pt != nil && pt.Surface != "" {
		surface = pt.Surface
	}
	needs := req.Needs()
	res, ok := e.resolve(r.Context(), w, d, router.Query{
		Model: req.Model, Surface: surface,
		NeedsTools: needs.Tools, NeedsVision: needs.Vision, NeedsReasoning: needs.Reasoning,
	}, rec, cfg, start)
	if !ok {
		return
	}
	e.runAttempts(w, r, d, cfg, req, res.Candidates, rec, start, res.ByID, res.Catalog)
}

// runAttempts drives the chain. The ordered list is fixed at snapshot time and
// never re-ordered; only skipping is dynamic, because another request may have
// tripped a breaker since the snapshot was taken.
func (e *Executor) runAttempts(w http.ResponseWriter, r *http.Request, d edge.Dialect,
	cfg *config.Config, req *ir.Request, cands []router.Candidate,
	rec *store.RequestRecord, start time.Time, byID map[string]provider.Provider,
	cat catalog.Reader) {

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
		outcome, status, aerr := e.attempt(w, r, d, cfg, req, c, byID[c.ProviderID], bud, rec, attempts, ad, cat)
		if aerr != nil {
			lastErr = aerr
		}

		next, action := nextIndex(cands, i, outcome, status)
		switch action {
		case actionFinish:
			rec.Status = "success"
			return
		case actionReturn:
			if outcome == adapter.OutcomeClientCancelled {
				rec.Status = "cancelled"
			}
			if lastErr != nil {
				rec.ErrorCode = string(lastErr.Type)
				e.writeErrorDiagnostics(w, rec, attempts)
				_ = d.WriteError(w, lastErr)
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
	_ = d.WriteError(w, lastErr)
}

// attempt performs one upstream call and records it. It returns the outcome,
// the upstream status code, and the dialect error to serve if this turns out to
// be the last attempt.
func (e *Executor) attempt(w http.ResponseWriter, r *http.Request, d edge.Dialect,
	cfg *config.Config, req *ir.Request, c router.Candidate, p provider.Provider,
	bud budget, rec *store.RequestRecord, seq int, ad adapter.Adapter,
	cat catalog.Reader) (adapter.Outcome, int, *ir.Error) {

	// A timer rather than a context deadline, because the bound changes at
	// commit: total stops applying and idle takes over. A deadline cannot be
	// moved once set.
	ctx, cancel := context.WithCancelCause(r.Context())
	defer cancel(nil)
	timer := time.AfterFunc(time.Until(bud.attemptDeadline(time.Now())), func() {
		cancel(errDarkrouterTimeout)
	})
	defer timer.Stop()

	tgt := &adapter.Target{
		BaseURL: p.BaseURL, APIKey: secretOf(p, c.KeyID), Model: c.Model,
		Info: modelInfo(cat, c.ProviderID, c.Model),
	}
	var warns []ir.Warning
	if w, ok := inferredWarning(c, req); ok {
		warns = append(warns, w)
	}
	hr, warns2, err := ad.BuildRequest(ctx, tgt, req)
	warns = append(warns, warns2...)
	if err != nil {
		return adapter.OutcomeFatal, 0, &ir.Error{Type: ir.ErrDarkrouter, Message: err.Error()}
	}
	if err := makeReplayable(hr); err != nil {
		return adapter.OutcomeFatal, 0, &ir.Error{Type: ir.ErrDarkrouter, Message: err.Error()}
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
	e.recordAttempt(rec, c, outcome, statusCode, doErr, time.Since(attemptStart))
	e.recordHealthFor(c, outcome, resp)

	if outcome != adapter.OutcomeSuccess {
		if resp != nil {
			resp.Body.Close()
		}
		return outcome, statusCode, errorFor(outcome, doErr)
	}

	if req.Stream {
		return e.attemptStream(w, d, cfg, c, resp, statusCode, rec, seq, timer, warns, ad)
	}

	out, perr := ad.ParseResponse(resp)
	if perr != nil {
		outcome := outcomeForParseError(perr)
		last := len(rec.Attempts) - 1
		rec.Attempts[last].Outcome = string(outcome)
		rec.Attempts[last].Error = perr.Error()
		if outcome != adapter.OutcomeFatal {
			// A 2xx that cannot be read is a provider fault, so it rejoins the
			// outcome path. A refusal is not: recording it would trip the
			// breaker on a healthy provider, and failing over would re-ask a
			// question every model in the chain will refuse.
			e.recordHealthFor(c, outcome, resp)
		}
		var ie *ir.Error
		if errors.As(perr, &ie) {
			return outcome, statusCode, ie
		}
		return outcome, statusCode, errorFor(outcome, perr)
	}

	ttft := time.Since(rec.TS).Milliseconds()
	rec.TTFTMs = &ttft
	applyUsage(rec, &out.Usage)
	rec.FinalProviderID = c.ProviderID
	rec.FinalModel = c.Model
	// Assigned, not appended: the request is re-rendered per attempt, and the
	// record must describe the translation the client actually received rather
	// than every attempt that was abandoned on the way there.
	rec.Warnings = warningStrings(append(warns, out.Warnings...))
	e.writeDiagnostics(w, rec.ID, c, seq)
	_ = d.WriteResponse(w, out)
	return adapter.OutcomeSuccess, statusCode, nil
}

// attemptStream buffers the upstream's events until one of them commits the
// response, then replays the buffer and streams the rest.
//
// Nothing reaches the client before commit, which is what makes a pre-commit
// failure invisible: the buffered events are simply discarded and the loop
// tries the next candidate. The dialect writer is not even constructed until
// commit, because building it mutates the response headers.
func (e *Executor) attemptStream(w http.ResponseWriter, d edge.Dialect,
	cfg *config.Config, c router.Candidate, resp *http.Response, statusCode int,
	rec *store.RequestRecord, seq int, timer *time.Timer,
	warns []ir.Warning, ad adapter.Adapter) (adapter.Outcome, int, *ir.Error) {

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
			return adapter.OutcomeRetryableProvider, statusCode,
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
			return adapter.OutcomeRetryableProvider, statusCode,
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
	e.writeDiagnostics(w, rec.ID, c, seq)

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
	_ = d.WriteStream(w, events)
	rec.Warnings = warningStrings(append(warns, streamWarns...))
	return adapter.OutcomeSuccess, statusCode, nil
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
	e.deps.Log.Log(rec)
}

func (e *Executor) recordHealth(k health.Key, s health.Signal) {
	if e.deps.Health == nil {
		return
	}
	e.deps.Health.Record(k, s)
}

func (e *Executor) recordAttempt(rec *store.RequestRecord, c router.Candidate,
	o adapter.Outcome, statusCode int, err error, latency time.Duration) {

	a := store.AttemptRecord{
		Seq: len(rec.Attempts), ProviderID: c.ProviderID, KeyID: c.KeyID, Model: c.Model,
		Outcome: string(o), StatusCode: statusCode, LatencyMs: latency.Milliseconds(),
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
	// CostMicros stays nil. Phase 6 supplies pricing; zero would read as free.
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
