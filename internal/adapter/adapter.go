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
	// BuildRequest returns the rendered HTTP request and every IR field this
	// kind could not express. Master design §5: a dropped field is a fact the
	// trace view must be able to show.
	BuildRequest(ctx context.Context, t *Target, req *ir.Request) (*http.Request, []ir.Warning, error)
	// ParseResponse takes ownership of resp.Body and always closes it.
	ParseResponse(resp *http.Response) (*ir.Response, error)
	ParseStream(r io.Reader, maxLine int) iter.Seq2[ir.StreamEvent, error]
	Classify(resp *http.Response, err error) Outcome
}

// BodyClassifier refines Classify for the one case a status line cannot
// express: a 400 that means "I do not have that model". An adapter implements
// it only when its upstreams distinguish the two, and exec type-asserts.
type BodyClassifier interface {
	ClassifyBody(resp *http.Response, body []byte, err error) Outcome
}
