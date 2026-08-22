package openaicompat

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"

	"github.com/darkraise/darkrouter/internal/adapter"
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
