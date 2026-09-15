package exec

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/adapter/openaicompat"
	"github.com/darkraise/darkrouter/internal/auth"
	openaiedge "github.com/darkraise/darkrouter/internal/edge/openai"
)

// failingResolver stands in for a credential the auth manager cannot turn into
// an authorizer at all — a token that will not parse, a service-account JSON
// that is not JSON, a preset whose oauth endpoints are missing.
type failingResolver struct{ providerID string }

func (f failingResolver) For(_ context.Context, t auth.Target, c auth.Credential) (auth.Authorizer, error) {
	if t.ProviderID == f.providerID {
		return nil, fmt.Errorf("credential %s: unexpected end of JSON input", c.ID)
	}
	return nil, nil
}

// A credential that cannot be constructed says nothing about the other
// providers in the chain. Classifying it Fatal stops routing outright, so one
// malformed subscription token takes down every request that happens to name a
// model the broken provider also serves.
func TestAMalformedCredentialFailsOverToTheNextProvider(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"x","model":"m","choices":[{"message":
			{"content":"pong"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	defer up.Close()

	e := newExecutorRaw(t, []providerSpec{
		{id: "broken", kind: "openaicompat", upstreamURL: "http://broken.invalid/v1",
			models: []string{"m"}, priority: 100, preset: "anthropic-oauth"},
		{id: "good", kind: "openaicompat", upstreamURL: up.URL,
			models: []string{"m"}, priority: 50},
	}, "sk", map[string]adapter.Adapter{"openaicompat": openaicompat.New()},
		Deps{Auth: failingResolver{providerID: "broken"}}, 0, nil)

	rec := post(t, e, `{"model":"m","messages":[{"role":"user","content":"ping"}]}`)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "pong") {
		t.Fatalf("code=%d body=%s; a credential the auth manager could not "+
			"build must cool that credential, not end the chain",
			rec.Code, rec.Body.String())
	}
}

// With no healthy alternative the chain still has to say what went wrong.
// Making the outcome retryable must not turn a broken credential into a
// silence the operator has to go looking for.
func TestAMalformedCredentialWithNoAlternativeStillReports(t *testing.T) {
	logger := &captureLogger{}
	e := newExecutorRaw(t, []providerSpec{
		{id: "broken", kind: "openaicompat", upstreamURL: "http://broken.invalid/v1",
			models: []string{"m"}, preset: "anthropic-oauth"},
	}, "sk", map[string]adapter.Adapter{"openaicompat": openaicompat.New()},
		Deps{Auth: failingResolver{providerID: "broken"}, Log: logger}, 0, nil)

	rec := post(t, e, `{"model":"m","messages":[{"role":"user","content":"ping"}]}`)
	if rec.Code == 200 {
		t.Fatalf("code=200; a request with no usable credential must fail")
	}
	// The client learns that the credential is the problem and nothing about
	// what is inside it; the detail belongs to the operator's trace.
	if body := rec.Body.String(); !strings.Contains(body, "credential unavailable") ||
		strings.Contains(body, "unexpected end of JSON input") {
		t.Errorf("body = %s; want the fixed message and no credential internals", body)
	}
	r := logger.only(t)
	if len(r.Attempts) != 1 || !strings.Contains(r.Attempts[0].Error, "unexpected end of JSON input") {
		t.Errorf("attempts = %+v; the credential error must reach the trace", r.Attempts)
	}
	if rec.Header().Get("X-Darkrouter-Attempts") != "0" {
		t.Errorf("attempts header = %q; nothing was sent upstream", rec.Header().Get("X-Darkrouter-Attempts"))
	}
}

// waitingResolver hands out an authorizer that waits for a token refresh that
// never finishes, the way one queued behind another request's refresh does.
type waitingResolver struct{}

func (waitingResolver) For(context.Context, auth.Target, auth.Credential) (auth.Authorizer, error) {
	return func(ctx context.Context, _ *http.Request) error {
		<-ctx.Done()
		return ctx.Err()
	}, nil
}

// A request that stops waiting for a credential says nothing about the
// credential. Recording it as a credential failure would cool a healthy
// account every time a client hung up, or the gateway shut down, during a
// slow refresh.
func TestARequestCancelledWhileItsCredentialRefreshesDoesNotBlameTheCredential(t *testing.T) {
	logger := &captureLogger{}
	e := newExecutorRaw(t, []providerSpec{
		{id: "sub", kind: "openaicompat", upstreamURL: "http://sub.invalid/v1",
			models: []string{"m"}, preset: "anthropic-oauth"},
	}, "sk", map[string]adapter.Adapter{"openaicompat": openaicompat.New()},
		Deps{Auth: waitingResolver{}, Log: logger}, 0, nil)

	ctx, cancel := context.WithCancel(context.Background())
	r := httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"m","messages":[{"role":"user","content":"ping"}]}`)).WithContext(ctx)
	r.Header.Set("Authorization", "Bearer sk")
	done := make(chan struct{})
	go func() {
		defer close(done)
		e.Handle(httptest.NewRecorder(), r, openaiedge.New())
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done

	got := logger.only(t)
	if len(got.Attempts) != 1 || got.Attempts[0].Outcome != string(adapter.OutcomeClientCancelled) {
		t.Fatalf("attempts = %+v, want one attempt recorded as client_cancelled", got.Attempts)
	}
}
