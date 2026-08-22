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

// Deps carries the optional collaborators. A zero Deps is valid and disables
// the corresponding behavior.
type Deps struct {
	Log    Logger
	Health HealthRecorder
	Fleet  Fleet
}

type Executor struct {
	store  *config.Store
	src    provider.Source
	ad     adapter.Adapter
	client *http.Client
	deps   Deps
}

// New builds the executor. Transport-level timeouts (connect, first_byte) are
// read once here because a shared Transport cannot vary them per request; both
// are documented restart-only. The total timeout is read per request.
func New(store *config.Store, src provider.Source, ad adapter.Adapter, deps Deps) *Executor {
	t := store.Current().Policy.Timeout
	return &Executor{
		store: store, src: src, ad: ad, deps: deps,
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
	surface := ir.SurfaceLLM
	if pt != nil {
		if s, ok := ir.ParseSurface(pt.Surface); ok {
			surface = s
		}
	}
	rec.Surface = string(surface)
	rec.RequestedModel = req.Model

	providers, err := e.src.Providers(r.Context())
	if err != nil {
		rec.ErrorCode = string(ir.ErrDarkrouter)
		_ = d.WriteError(w, &ir.Error{Type: ir.ErrDarkrouter, Message: err.Error()})
		return
	}

	// The snapshot freezes every input the router is allowed to read. Health is
	// resolved to booleans here rather than inside Resolve, which is what keeps
	// the router a pure function of its arguments.
	snap := router.Snapshot{
		At:        start,
		Providers: providers,
		Catalog:   catalog.FromProviders(providers),
		Config:    cfg,
	}
	if e.deps.Fleet != nil {
		snap.Health = e.deps.Fleet.SnapshotAvailability(start)
		snap.LastUsed = e.deps.Fleet.LastUsedSnapshot()
	}

	needs := req.Needs()
	cands, skips, rerr := router.Resolve(router.Query{
		Model: req.Model, Surface: surface,
		NeedsTools: needs.Tools, NeedsVision: needs.Vision, NeedsReasoning: needs.Reasoning,
	}, snap)

	rec.Candidates = traceCandidates(cands)
	rec.Skips = traceSkips(skips)

	if rerr != nil {
		e2 := routerError(rerr)
		rec.ErrorCode = string(e2.Type)
		_ = d.WriteError(w, e2)
		return
	}

	byID := make(map[string]provider.Provider, len(providers))
	for _, p := range providers {
		byID[p.ID] = p
	}
	e.runAttempts(w, r, d, cfg, req, cands, rec, start, byID)
}

// runAttempts drives the chain. The ordered list is fixed at snapshot time and
// never re-ordered; only skipping is dynamic, because another request may have
// tripped a breaker since the snapshot was taken.
func (e *Executor) runAttempts(w http.ResponseWriter, r *http.Request, d edge.Dialect,
	cfg *config.Config, req *ir.Request, cands []router.Candidate,
	rec *store.RequestRecord, start time.Time, byID map[string]provider.Provider) {

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

		// At attempt start, not on success: a credential that always fails must
		// not keep a stale timestamp and sort first forever.
		if e.deps.Fleet != nil {
			e.deps.Fleet.MarkUsed(health.CredKey{ProviderID: c.ProviderID, KeyID: c.KeyID}, now)
		}

		attempts++
		outcome, status, aerr := e.attempt(w, r, d, cfg, req, c, byID[c.ProviderID], bud, rec, attempts)
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
	bud budget, rec *store.RequestRecord, seq int) (adapter.Outcome, int, *ir.Error) {

	ctx, cancel := context.WithCancelCause(r.Context())
	defer cancel(nil)
	ctx, cancelTimeout := context.WithDeadlineCause(ctx, bud.attemptDeadline(time.Now()),
		errDarkrouterTimeout)
	defer cancelTimeout()

	tgt := &adapter.Target{BaseURL: p.BaseURL, APIKey: secretOf(p, c.KeyID), Model: c.Model}
	hr, err := e.ad.BuildRequest(ctx, tgt, req)
	if err != nil {
		return adapter.OutcomeFatal, 0, &ir.Error{Type: ir.ErrDarkrouter, Message: err.Error()}
	}
	if err := makeReplayable(hr); err != nil {
		return adapter.OutcomeFatal, 0, &ir.Error{Type: ir.ErrDarkrouter, Message: err.Error()}
	}

	attemptStart := time.Now()
	resp, doErr := e.client.Do(hr)
	outcome := e.classify(r.Context(), ctx, resp, doErr)

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
		return e.attemptStream(w, d, cfg, c, resp, statusCode, rec, seq)
	}

	out, perr := e.ad.ParseResponse(resp)
	if perr != nil {
		// A 2xx that cannot be read is a provider fault, so it rejoins the
		// outcome path rather than going around it.
		e.recordHealthFor(c, adapter.OutcomeRetryableProvider, resp)
		last := len(rec.Attempts) - 1
		rec.Attempts[last].Outcome = string(adapter.OutcomeRetryableProvider)
		rec.Attempts[last].Error = perr.Error()
		return adapter.OutcomeRetryableProvider, statusCode,
			errorFor(adapter.OutcomeRetryableProvider, perr)
	}

	ttft := time.Since(rec.TS).Milliseconds()
	rec.TTFTMs = &ttft
	applyUsage(rec, &out.Usage)
	rec.FinalProviderID = c.ProviderID
	rec.FinalModel = c.Model
	e.writeDiagnostics(w, rec.ID, c, seq)
	_ = d.WriteResponse(w, out)
	return adapter.OutcomeSuccess, statusCode, nil
}

// attemptStream streams a committed response. Task 16 replaces this with a
// buffered, replayable form; for now it forwards straight through, which is the
// phase 2 behavior.
func (e *Executor) attemptStream(w http.ResponseWriter, d edge.Dialect,
	cfg *config.Config, c router.Candidate, resp *http.Response, statusCode int,
	rec *store.RequestRecord, seq int) (adapter.Outcome, int, *ir.Error) {

	defer resp.Body.Close()
	rec.FinalProviderID = c.ProviderID
	rec.FinalModel = c.Model
	e.writeDiagnostics(w, rec.ID, c, seq)

	events := tapStream(e.ad.ParseStream(resp.Body, cfg.Server.SSE.MaxLineBytes),
		func() {
			ttft := time.Since(rec.TS).Milliseconds()
			rec.TTFTMs = &ttft
		},
		func(u *ir.Usage) { applyUsage(rec, u) },
	)
	_ = d.WriteStream(w, events)
	return adapter.OutcomeSuccess, statusCode, nil
}

var errDarkrouterTimeout = errors.New("darkrouter: total timeout exceeded")

// classify asks the adapter, then overrides for the two cases no adapter can
// see: a Darkrouter-imposed deadline, and a cancellation whose origin is the
// inbound request rather than the upstream.
//
// The deadline is checked first. Both cancel the same derived context, and if
// the client also disappears in that instant, checking the disconnect first
// would silently reclassify a genuine provider timeout as a client hang-up.
func (e *Executor) classify(inbound, upstream context.Context, resp *http.Response, err error) adapter.Outcome {
	if err == nil {
		return e.ad.Classify(resp, nil)
	}
	if errors.Is(context.Cause(upstream), errDarkrouterTimeout) {
		return adapter.OutcomeRetryableProvider
	}
	if errors.Is(inbound.Err(), context.Canceled) {
		return adapter.OutcomeClientCancelled
	}
	return e.ad.Classify(resp, err)
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
