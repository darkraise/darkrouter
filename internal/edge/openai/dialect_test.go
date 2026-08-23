package openai

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/edge"
)

func TestProxyTokenReadsTheBearerHeader(t *testing.T) {
	cases := []struct {
		name   string
		header string
		want   string
	}{
		{"bearer", "Bearer sk-abc", "sk-abc"},
		{"lowercase scheme is still a bearer", "bearer sk-abc", "sk-abc"},
		{"surrounding space is trimmed", "Bearer   sk-abc  ", "sk-abc"},
		{"no header", "", ""},
		{"wrong scheme", "Basic sk-abc", ""},
		{"scheme only", "Bearer", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
			if tc.header != "" {
				r.Header.Set("Authorization", tc.header)
			}
			if got := New().ProxyToken(r); got != tc.want {
				t.Errorf("ProxyToken = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestResponsesDialectIsDistinguishableInTheLog(t *testing.T) {
	// The dialect column is how an operator tells a Responses client from a
	// chat client, and both speak OpenAI over the same auth.
	if got := NewResponses().Name(); got != "openai-responses" {
		t.Errorf("Name() = %q", got)
	}
	if NewResponses().Name() == New().Name() {
		t.Error("the two OpenAI dialects are indistinguishable in the request log")
	}
}

func TestResponsesDialectReadsTheSameBearer(t *testing.T) {
	r := httptest.NewRequest("POST", "/v1/responses", nil)
	r.Header.Set("Authorization", "bearer sk-x")
	if got := NewResponses().ProxyToken(r); got != "sk-x" {
		t.Errorf("ProxyToken = %q", got)
	}
}

func TestResponsesDialectSatisfiesEdgeDialect(t *testing.T) {
	var _ edge.Dialect = NewResponses()
}

func TestEachResponsesDialectHoldsItsOwnEcho(t *testing.T) {
	// A route-scoped instance would race on this field and answer one client
	// with another's tools.
	a, b := NewResponses(), NewResponses()
	if _, _, err := a.ParseRequest(httptest.NewRequest("POST", "/v1/responses",
		strings.NewReader(`{"model":"m","input":"hi","instructions":"first"}`)), 1<<20); err != nil {
		t.Fatal(err)
	}
	if _, _, err := b.ParseRequest(httptest.NewRequest("POST", "/v1/responses",
		strings.NewReader(`{"model":"m","input":"hi","instructions":"second"}`)), 1<<20); err != nil {
		t.Fatal(err)
	}
	if a.echo == nil || b.echo == nil || a.echo.Instructions != "first" {
		t.Errorf("first dialect echo = %+v", a.echo)
	}
	if b.echo.Instructions != "second" {
		t.Errorf("second dialect echo = %+v", b.echo)
	}
}
