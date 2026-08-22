package openaicompat

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"iter"
	"net/http"
	"strings"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/ir"
)

// Classify buckets an upstream result. The default buckets matter as much as the
// listed codes: without them, DNS failures and TLS errors get classified
// differently by different call sites.
func Classify(resp *http.Response, err error) adapter.Outcome {
	if err != nil || resp == nil {
		return adapter.OutcomeRetryableProvider
	}
	switch code := resp.StatusCode; {
	case code >= 200 && code < 300:
		return adapter.OutcomeSuccess
	case code == 401, code == 402, code == 403:
		return adapter.OutcomeRetryableCredential
	case code == 404:
		return adapter.OutcomeRetryableModel
	case code == 408, code == 429:
		return adapter.OutcomeRetryableProvider
	case code >= 300 && code < 400:
		// Redirects are never followed: Go's client converts a redirected POST
		// into a body-less GET.
		return adapter.OutcomeRetryableProvider
	case code >= 500:
		return adapter.OutcomeRetryableProvider
	default:
		return adapter.OutcomeFatal
	}
}

// ClassifyWithContext distinguishes a client disconnect from a Darkrouter
// deadline. Both surface as a cancellation at the transport layer, so the
// inbound context's own state is the only reliable discriminator.
func ClassifyWithContext(resp *http.Response, err error, inbound context.Context) adapter.Outcome {
	if err != nil && errors.Is(inbound.Err(), context.Canceled) {
		return adapter.OutcomeClientCancelled
	}
	return Classify(resp, err)
}

type Adapter struct{}

func New() *Adapter { return &Adapter{} }

func (a *Adapter) Kind() string { return "openaicompat" }

func (a *Adapter) BuildRequest(ctx context.Context, t *adapter.Target, req *ir.Request) (*http.Request, error) {
	return BuildRequest(ctx, t, req)
}

func (a *Adapter) ParseResponse(resp *http.Response) (*ir.Response, error) {
	return ParseResponse(resp)
}

func (a *Adapter) ParseStream(r io.Reader, maxLine int) iter.Seq2[ir.StreamEvent, error] {
	return ParseStream(r, maxLine)
}

func (a *Adapter) Classify(resp *http.Response, err error) adapter.Outcome {
	return Classify(resp, err)
}

var _ adapter.Adapter = (*Adapter)(nil)

// unknownModelCodes are the error codes OpenAI-compatible providers use when a
// 400 means "I do not have that model" rather than "your request is malformed".
var unknownModelCodes = map[string]bool{
	"model_not_found": true,
	"invalid_model":   true,
	"unknown_model":   true,
}

// ClassifyBody refines Classify for the one case the status line cannot express:
// a 400 that means the model is unknown.
//
// Treating that as Fatal would make failover die on the first provider in a
// chain that does not carry the model — which is exactly the arrangement an
// alias exists to create.
func ClassifyBody(resp *http.Response, body []byte, err error) adapter.Outcome {
	base := Classify(resp, err)
	if base != adapter.OutcomeFatal || resp == nil || resp.StatusCode != 400 {
		return base
	}

	var parsed struct {
		Error struct {
			Message string `json:"message"`
			Code    string `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &parsed) != nil {
		return base
	}
	if unknownModelCodes[strings.ToLower(parsed.Error.Code)] {
		return adapter.OutcomeRetryableModel
	}
	// Some providers send no code and only say so in prose. Requiring both
	// "model" and a not-found phrase keeps this from swallowing every 400 that
	// happens to mention a model name.
	msg := strings.ToLower(parsed.Error.Message)
	if strings.Contains(msg, "model") &&
		(strings.Contains(msg, "does not exist") ||
			strings.Contains(msg, "not found") ||
			strings.Contains(msg, "unknown model")) {
		return adapter.OutcomeRetryableModel
	}
	return base
}
