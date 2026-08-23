package openaicompat

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/ir"
)

func TestClassifyStatusCodes(t *testing.T) {
	for code, want := range map[int]adapter.Outcome{
		200: adapter.OutcomeSuccess,
		400: adapter.OutcomeFatal,
		401: adapter.OutcomeRetryableCredential,
		402: adapter.OutcomeRetryableCredential,
		403: adapter.OutcomeRetryableCredential,
		404: adapter.OutcomeRetryableModel,
		408: adapter.OutcomeRetryableProvider,
		413: adapter.OutcomeFatal,
		422: adapter.OutcomeFatal,
		429: adapter.OutcomeRetryableProvider,
		500: adapter.OutcomeRetryableProvider,
		503: adapter.OutcomeRetryableProvider,
		301: adapter.OutcomeRetryableProvider, // redirects are never followed
	} {
		if got := Classify(&http.Response{StatusCode: code}, nil); got != want {
			t.Errorf("status %d -> %s, want %s", code, got, want)
		}
	}
}

func TestClassifyClientCancellationIsNotAProviderFault(t *testing.T) {
	// Marking a provider unhealthy because someone pressed Ctrl-C is a
	// self-inflicted outage.
	inbound, cancel := context.WithCancelCause(context.Background())
	cancel(context.Canceled)
	if got := ClassifyWithContext(nil, inbound.Err(), inbound); got != adapter.OutcomeClientCancelled {
		t.Fatalf("got %s", got)
	}
}

func TestClassifyDarkrouterDeadlineIsAProviderFault(t *testing.T) {
	// A Darkrouter-imposed timeout must not be mistaken for a client disconnect,
	// or a slow provider never gets penalized.
	inbound := context.Background()
	if got := ClassifyWithContext(nil, context.DeadlineExceeded, inbound); got != adapter.OutcomeRetryableProvider {
		t.Fatalf("got %s", got)
	}
}

func TestClassifyTransportErrorsAreRetryable(t *testing.T) {
	for _, err := range []error{
		errors.New("dial tcp: connection refused"),
		&net.DNSError{Err: "no such host", IsNotFound: true},
	} {
		if got := Classify(nil, err); got != adapter.OutcomeRetryableProvider {
			t.Errorf("%v -> %s", err, got)
		}
	}
}

func TestClassifyBodyDetectsAnUnknownModel400(t *testing.T) {
	cases := []struct {
		name string
		body string
		want adapter.Outcome
	}{
		{
			name: "model_not_found code",
			body: `{"error":{"message":"The model does not exist","code":"model_not_found"}}`,
			want: adapter.OutcomeRetryableModel,
		},
		{
			name: "invalid_model code",
			body: `{"error":{"message":"bad model","code":"invalid_model"}}`,
			want: adapter.OutcomeRetryableModel,
		},
		{
			name: "model named in the message",
			body: `{"error":{"message":"model \"llama-9\" does not exist","type":"invalid_request_error"}}`,
			want: adapter.OutcomeRetryableModel,
		},
		{
			name: "a genuinely malformed request stays fatal",
			body: `{"error":{"message":"messages: field required","type":"invalid_request_error"}}`,
			want: adapter.OutcomeFatal,
		},
		{
			name: "an unparseable body stays fatal",
			body: `not json`,
			want: adapter.OutcomeFatal,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := &http.Response{StatusCode: 400, Header: http.Header{}}
			if got := ClassifyBody(resp, []byte(tc.body), nil); got != tc.want {
				t.Errorf("ClassifyBody = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestClassifyBodyDefersToClassifyForEveryOtherStatus(t *testing.T) {
	for _, code := range []int{200, 401, 404, 429, 500, 503} {
		resp := &http.Response{StatusCode: code, Header: http.Header{}}
		want := Classify(resp, nil)
		if got := ClassifyBody(resp, []byte(`{"error":{"code":"model_not_found"}}`), nil); got != want {
			t.Errorf("status %d: ClassifyBody = %q, want %q", code, got, want)
		}
	}
}

func TestOpenAICompatDeclaresTheMatrixSurfaces(t *testing.T) {
	// Phase 5 spec §4: openaicompat is the only kind serving more than chat
	// and embeddings. Getting this wrong makes a route unreachable with a
	// confusing "no provider offers this" rather than a clear gap.
	got := New().Surfaces()
	for _, want := range []ir.Surface{
		ir.SurfaceLLM, ir.SurfaceEmbedding, ir.SurfaceImage,
		ir.SurfaceTTS, ir.SurfaceSTT, ir.SurfaceRerank, ir.SurfaceModeration,
	} {
		if !got.Has(want) {
			t.Errorf("openaicompat does not declare %q", want)
		}
	}
}
