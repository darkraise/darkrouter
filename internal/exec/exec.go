// Package exec drives a request to an upstream. Phase 1 handles exactly one
// candidate; Phase 3 wraps this same call sequence in an attempt loop rather
// than restructuring it.
package exec

import (
	"context"
	"crypto/rand"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/adapter/openaicompat"
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

func New(store *config.Store, src provider.Source, ad adapter.Adapter) *Executor {
	return &Executor{
		store: store, src: src, ad: ad,
		client: &http.Client{
			// Go follows redirects by default, silently turning a redirected
			// POST into a body-less GET.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

func (e *Executor) Handle(w http.ResponseWriter, r *http.Request, d edge.Dialect) {
	cfg := e.store.Current() // one snapshot for this request's whole lifetime
	reqID := ulid.MustNew(ulid.Timestamp(time.Now()), rand.Reader).String()
	w.Header().Set("X-Darkrouter-Request", reqID)

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
	ctx, cancelTimeout := context.WithTimeoutCause(ctx, cfg.Policy.Timeout.Total,
		fmt.Errorf("darkrouter: total timeout exceeded"))
	defer cancelTimeout()

	tgt := &adapter.Target{BaseURL: p.BaseURL, APIKey: p.APIKey, Model: req.Model}
	hr, err := e.ad.BuildRequest(ctx, tgt, req)
	if err != nil {
		_ = d.WriteError(w, &ir.Error{Type: ir.ErrDarkrouter, Message: err.Error()})
		return
	}

	resp, doErr := e.client.Do(hr)
	outcome := openaicompat.ClassifyWithContext(resp, doErr, r.Context())

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
		_ = d.WriteError(w, &ir.Error{Type: ir.ErrAPI, Message: err.Error()})
		return
	}
	out.ID = reqID
	_ = d.WriteResponse(w, out)
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
