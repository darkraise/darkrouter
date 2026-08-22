// Package exec drives a request to an upstream. Phase 1 handles exactly one
// candidate; Phase 3 wraps this same call sequence in an attempt loop rather
// than restructuring it.
package exec

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/config"
	"github.com/darkraise/darkrouter/internal/edge"
	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/provider"
)

type Executor struct {
	store  *config.Store
	src    provider.Source
	ad     adapter.Adapter
	client *http.Client
}

// New builds the executor. Transport-level timeouts (connect, first_byte) are
// read once here because a shared Transport cannot vary them per request; both
// are documented restart-only. The total timeout is read per request.
func New(store *config.Store, src provider.Source, ad adapter.Adapter) *Executor {
	t := store.Current().Policy.Timeout
	return &Executor{
		store: store, src: src, ad: ad,
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
	cfg := e.store.Current() // one snapshot for this request's whole lifetime
	reqID := ulid.MustNew(ulid.Timestamp(time.Now()), rand.Reader).String()

	// Written up front so every error path carries them, per master design §10.
	// The count is overwritten once an attempt has actually been made.
	w.Header().Set("X-Darkrouter-Request", reqID)
	w.Header().Set("X-Darkrouter-Attempts", "0")

	req, _, err := d.ParseRequest(r, cfg.Server.MaxBodyBytes)
	if err != nil {
		_ = d.WriteError(w, &ir.Error{Type: ir.ErrInvalidRequest, Message: err.Error()})
		return
	}

	providers, err := e.src.Providers(r.Context())
	if err != nil {
		_ = d.WriteError(w, &ir.Error{Type: ir.ErrDarkrouter, Message: err.Error()})
		return
	}
	p, ok := provider.Resolve(providers, req.Model)
	if !ok {
		_ = d.WriteError(w, &ir.Error{
			Type:    ir.ErrNotFound,
			Message: fmt.Sprintf("no configured provider offers model %q", req.Model),
		})
		return
	}

	// The upstream context derives from the inbound one, so a client hanging up
	// cancels the upstream call. WithCancelCause is used from the outset because
	// Phase 2 needs the cause to tell a disconnect from a Darkrouter deadline.
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
		_ = d.WriteError(w, &ir.Error{Type: ir.ErrDarkrouter, Message: err.Error()})
		return
	}

	resp, doErr := e.client.Do(hr)
	outcome := e.classify(r.Context(), resp, doErr)

	w.Header().Set("X-Darkrouter-Provider", p.ID)
	w.Header().Set("X-Darkrouter-Model", req.Model)
	w.Header().Set("X-Darkrouter-Attempts", strconv.Itoa(1))

	if outcome != adapter.OutcomeSuccess {
		if resp != nil {
			resp.Body.Close()
		}
		_ = d.WriteError(w, errorFor(outcome, doErr))
		return
	}

	if req.Stream {
		defer resp.Body.Close()
		_ = d.WriteStream(w, e.ad.ParseStream(resp.Body, cfg.Server.SSE.MaxLineBytes))
		return
	}

	out, err := e.ad.ParseResponse(resp)
	if err != nil {
		// Design §8.2: a read or parse failure on a 2xx is a provider fault, so
		// it goes through the outcome path rather than around it. Phase 3 then
		// retries it by adding a loop, not by restructuring this branch.
		_ = d.WriteError(w, errorFor(adapter.OutcomeRetryableProvider, err))
		return
	}
	_ = d.WriteResponse(w, out)
}

var errDarkrouterTimeout = errors.New("darkrouter: total timeout exceeded")

// classify asks the adapter, then overrides for the one case no adapter can see:
// a cancellation whose origin is the inbound request rather than the upstream.
// Keeping this in exec is what lets the executor stay adapter-agnostic, which
// Phase 3 needs once more than one adapter exists.
func (e *Executor) classify(inbound context.Context, resp *http.Response, err error) adapter.Outcome {
	if err != nil && errors.Is(err, context.Canceled) && errors.Is(inbound.Err(), context.Canceled) {
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
