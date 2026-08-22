// Package adapter holds the outbound provider kinds.
package adapter

import (
	"context"
	"io"
	"iter"
	"net/http"

	"github.com/darkraise/darkrouter/internal/ir"
)

type Target struct {
	BaseURL string
	APIKey  string
	Model   string
}

// Outcome is the classification that drives failover. Phase 1 has nowhere to
// fail over to, but defining the full taxonomy now keeps Phase 3 from having to
// revisit every adapter.
type Outcome string

const (
	OutcomeSuccess             Outcome = "success"
	OutcomeRetryableProvider   Outcome = "retryable_provider"
	OutcomeRetryableCredential Outcome = "retryable_credential"
	OutcomeRetryableModel      Outcome = "retryable_model"
	OutcomeFatal               Outcome = "fatal"
	OutcomeClientCancelled     Outcome = "client_cancelled"
)

type Adapter interface {
	Kind() string
	BuildRequest(ctx context.Context, t *Target, req *ir.Request) (*http.Request, error)
	ParseResponse(resp *http.Response) (*ir.Response, error)
	ParseStream(r io.Reader, maxLine int) iter.Seq2[ir.StreamEvent, error]
	Classify(resp *http.Response, err error) Outcome
}
