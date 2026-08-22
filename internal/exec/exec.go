// Package exec drives a request to an upstream. Phase 1 handles exactly one
// candidate; Phase 3 wraps this same call sequence in an attempt loop rather
// than restructuring it.
package exec

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"iter"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/config"
	"github.com/darkraise/darkrouter/internal/edge"
	"github.com/darkraise/darkrouter/internal/health"
	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/provider"
	"github.com/darkraise/darkrouter/internal/store"
)

// Logger receives one record per request. It must never block: the
// implementation in internal/store drops rather than waiting.
type Logger interface {
	Log(*store.RequestRecord)
}

// HealthRecorder receives one signal per attempt. It must not block: the
// breaker answers under a mutex and returns immediately.
type HealthRecorder interface {
	Record(k health.Key, s health.Signal)
}

// Deps carries the optional collaborators. A zero Deps is valid and disables
// the corresponding behavior.
type Deps struct {
	Log    Logger
	Health HealthRecorder
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
	// "llm" is the surface name the OpenAI dialect reports; it is the default
	// only for the error paths that return before ParseRequest yields one.
	rec := &store.RequestRecord{
		ID: reqID, TS: start, Dialect: d.Name(), Surface: "llm", Status: "error",
	}
	defer func() {
		total := time.Since(start).Milliseconds()
		rec.TotalMs = &total
		e.log(rec)
	}()

	// Written up front so every error path carries them, per master design §10.
	// The count is overwritten once an attempt has actually been made.
	w.Header().Set("X-Darkrouter-Request", reqID)
	w.Header().Set("X-Darkrouter-Attempts", "0")

	req, pt, err := d.ParseRequest(r, cfg.Server.MaxBodyBytes)
	if err != nil {
		rec.ErrorCode = string(ir.ErrInvalidRequest)
		_ = d.WriteError(w, &ir.Error{Type: ir.ErrInvalidRequest, Message: err.Error()})
		return
	}
	if pt != nil && pt.Surface != "" {
		rec.Surface = pt.Surface
	}
	rec.RequestedModel = req.Model

	providers, err := e.src.Providers(r.Context())
	if err != nil {
		rec.ErrorCode = string(ir.ErrDarkrouter)
		_ = d.WriteError(w, &ir.Error{Type: ir.ErrDarkrouter, Message: err.Error()})
		return
	}
	p, ok := provider.Resolve(providers, req.Model)
	if !ok {
		rec.ErrorCode = string(ir.ErrNotFound)
		_ = d.WriteError(w, &ir.Error{
			Type:    ir.ErrNotFound,
			Message: fmt.Sprintf("no configured provider offers model %q", req.Model),
		})
		return
	}
	rec.Candidates = []string{p.ID}
	rec.FinalProviderID = p.ID
	rec.FinalModel = req.Model

	// The upstream context derives from the inbound one, so a client hanging up
	// cancels the upstream call. WithCancelCause is used because the cause is
	// the only way to tell a disconnect from a Darkrouter deadline.
	ctx, cancel := context.WithCancelCause(r.Context())
	defer cancel(nil)
	// Phase 1 applies the total budget to the whole request. Phase 3 replaces
	// this with commit semantics plus policy.timeout.idle for committed streams.
	ctx, cancelTimeout := context.WithTimeoutCause(ctx, cfg.Policy.Timeout.Total,
		errDarkrouterTimeout)
	defer cancelTimeout()

	tgt := &adapter.Target{BaseURL: p.BaseURL, APIKey: p.APIKey, Model: req.Model}
	hr, err := e.ad.BuildRequest(ctx, tgt, req)
	if err != nil {
		rec.ErrorCode = string(ir.ErrDarkrouter)
		_ = d.WriteError(w, &ir.Error{Type: ir.ErrDarkrouter, Message: err.Error()})
		return
	}

	attemptStart := time.Now()
	resp, doErr := e.client.Do(hr)
	outcome := e.classify(r.Context(), ctx, resp, doErr)

	attempt := store.AttemptRecord{
		Seq: 0, ProviderID: p.ID, KeyID: p.KeyID, Model: req.Model,
		Outcome:   string(outcome),
		LatencyMs: time.Since(attemptStart).Milliseconds(),
	}
	if resp != nil {
		attempt.StatusCode = resp.StatusCode
	}
	if doErr != nil {
		attempt.Error = doErr.Error()
	}
	rec.Attempts = append(rec.Attempts, attempt)

	hk := health.Key{ProviderID: p.ID, KeyID: p.KeyID, Model: req.Model}
	sig := health.Signal{Outcome: outcome}
	if resp != nil {
		sig.StatusCode = resp.StatusCode
		// Read before the body is closed; a 429's Retry-After is the difference
		// between a precise cooldown and the generic ladder.
		if d, ok := health.ParseRetryAfter(resp.Header.Get("Retry-After"), time.Now()); ok {
			sig.RetryAfter, sig.HasRetryAfter = d, true
		}
	}
	e.recordHealth(hk, sig)

	w.Header().Set("X-Darkrouter-Provider", p.ID)
	w.Header().Set("X-Darkrouter-Model", req.Model)
	w.Header().Set("X-Darkrouter-Attempts", strconv.Itoa(1))

	if outcome != adapter.OutcomeSuccess {
		if resp != nil {
			resp.Body.Close()
		}
		e2 := errorFor(outcome, doErr)
		rec.ErrorCode = string(e2.Type)
		if outcome == adapter.OutcomeClientCancelled {
			rec.Status = "cancelled"
		}
		_ = d.WriteError(w, e2)
		return
	}

	if req.Stream {
		defer resp.Body.Close()
		// TTFT is the first content delta rather than the response header, so
		// it measures what the client actually waited for.
		events := tapStream(e.ad.ParseStream(resp.Body, cfg.Server.SSE.MaxLineBytes),
			func() {
				ttft := time.Since(start).Milliseconds()
				rec.TTFTMs = &ttft
			},
			func(u *ir.Usage) { applyUsage(rec, u) },
		)
		_ = d.WriteStream(w, events)
		rec.Status = "success"
		return
	}

	// For a non-streaming response the client waits for the whole body, so
	// first token and last token are the same moment.
	ttft := time.Since(start).Milliseconds()
	rec.TTFTMs = &ttft

	out, err := e.ad.ParseResponse(resp)
	if err != nil {
		// Design §8.2: a read or parse failure on a 2xx is a provider fault, so
		// it goes through the outcome path rather than around it. Phase 3 then
		// retries it by adding a loop, not by restructuring this branch.
		e2 := errorFor(adapter.OutcomeRetryableProvider, err)
		rec.ErrorCode = string(e2.Type)
		rec.Attempts[0].Outcome = string(adapter.OutcomeRetryableProvider)
		rec.Attempts[0].Error = err.Error()
		// A 2xx that cannot be read is a provider fault, so it must reach the
		// breaker rather than being recorded only in the log.
		e.recordHealth(hk, health.Signal{
			Outcome: adapter.OutcomeRetryableProvider, StatusCode: resp.StatusCode,
		})
		_ = d.WriteError(w, e2)
		return
	}
	applyUsage(rec, &out.Usage)
	rec.Status = "success"
	_ = d.WriteResponse(w, out)
}

func (e *Executor) recordHealth(k health.Key, s health.Signal) {
	if e.deps.Health == nil {
		return
	}
	e.deps.Health.Record(k, s)
}

func (e *Executor) log(rec *store.RequestRecord) {
	if e.deps.Log == nil {
		return
	}
	e.deps.Log.Log(rec)
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

var errDarkrouterTimeout = errors.New("darkrouter: total timeout exceeded")

// classify asks the adapter, then overrides for the two cases no adapter can
// see: a Darkrouter-imposed deadline, and a cancellation whose origin is the
// inbound request rather than the upstream.
//
// The deadline is checked first. Both cancel the same derived context, and if
// the client also disappears in that instant, checking the disconnect first
// would silently reclassify a genuine provider timeout as a client hang-up.
//
// Keeping this in exec is what lets the executor stay adapter-agnostic, which
// Phase 3 needs once more than one adapter exists.
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
