package exec

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/adapter/openaicompat"
	"github.com/darkraise/darkrouter/internal/auth"
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
	e := newExecutorRaw(t, []providerSpec{
		{id: "broken", kind: "openaicompat", upstreamURL: "http://broken.invalid/v1",
			models: []string{"m"}, preset: "anthropic-oauth"},
	}, "sk", map[string]adapter.Adapter{"openaicompat": openaicompat.New()},
		Deps{Auth: failingResolver{providerID: "broken"}}, 0, nil)

	rec := post(t, e, `{"model":"m","messages":[{"role":"user","content":"ping"}]}`)
	if rec.Code == 200 {
		t.Fatalf("code=200; a request with no usable credential must fail")
	}
	if !strings.Contains(rec.Body.String(), "unexpected end of JSON input") {
		t.Errorf("body = %s; the credential error must reach the client",
			rec.Body.String())
	}
}
