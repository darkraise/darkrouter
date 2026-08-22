// Package adapter holds the outbound provider kinds.
package adapter

import (
	"context"
	"io"
	"iter"
	"net/http"

	"github.com/darkraise/darkrouter/internal/ir"
)

// ModelInfo is what the catalog knows about the model this target names.
//
// It is a plain struct rather than a catalog type on purpose. health imports
// adapter, and catalog imports health so discovery can cool a credential on a
// rejected probe; an adapter that imported catalog would close the cycle. The
// translation happens at the exec boundary, exactly as Target already carries a
// base URL rather than importing provider.
//
// The zero value means the catalog knows nothing, and every adapter reading it
// must behave as it did before phase 6 in that case: honor what the client
// asked for rather than acting on a half-filled guess.
type ModelInfo struct {
	ContextWindow   int
	MaxOutputTokens int

	// The three per-generation request-shape facts. TraitsKnown gates all
	// three: an unrecognized or proxied model reaches here with TraitsKnown
	// false, which is the honest answer.
	Adaptive     bool
	ManualBudget bool
	FreeSampling bool
	TraitsKnown  bool
}

type Target struct {
	BaseURL string
	APIKey  string
	Model   string
	Info    ModelInfo
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

// TokenCounter is implemented by an adapter whose upstream offers a native
// token count. OpenAI has no such endpoint, so this is optional rather than a
// method on Adapter that two thirds of implementations would stub out.
type TokenCounter interface {
	BuildCountRequest(ctx context.Context, t *Target, req *ir.Request) (*http.Request, error)
	// ParseCountResponse takes ownership of resp.Body and always closes it.
	ParseCountResponse(resp *http.Response) (int, error)
}
