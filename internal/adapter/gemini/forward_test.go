package gemini

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"github.com/darkraise/darkrouter/internal/adapter"
)

func TestBuildForwardRewritesOnlyTheModelSegment(t *testing.T) {
	q := url.Values{"alt": []string{"sse"}}
	hr, err := New().BuildForward(context.Background(),
		&adapter.Target{BaseURL: "https://up.example/v1beta", APIKey: "k", Model: "gemini-2.5-pro"},
		&adapter.Forward{
			Body: []byte(`{"contents":[]}`), Header: http.Header{},
			Stream: true, Method: "streamGenerateContent", Query: q,
		})
	if err != nil {
		t.Fatal(err)
	}
	want := "https://up.example/v1beta/models/gemini-2.5-pro:streamGenerateContent?alt=sse"
	if hr.URL.String() != want {
		t.Errorf("URL = %s\nwant %s", hr.URL, want)
	}
	if got := hr.Header.Get("x-goog-api-key"); got != "k" {
		t.Errorf("x-goog-api-key = %q", got)
	}
}

func TestBuildForwardEscapesTheModelSegment(t *testing.T) {
	// A slash in a model name would otherwise open a path segment the API does
	// not match, and a colon would be read as the method separator.
	hr, err := New().BuildForward(context.Background(),
		&adapter.Target{BaseURL: "https://up.example/v1beta", Model: "tuned/abc"},
		&adapter.Forward{
			Body: []byte(`{}`), Header: http.Header{}, Method: "generateContent",
		})
	if err != nil {
		t.Fatal(err)
	}
	if got := hr.URL.EscapedPath(); got != "/v1beta/models/tuned%2Fabc:generateContent" {
		t.Errorf("EscapedPath = %s", got)
	}
}

func TestBuildForwardRejectsAnEmptyMethod(t *testing.T) {
	// Without the operation there is no URL to build, and guessing
	// generateContent would silently turn a stream into a unary call.
	_, err := New().BuildForward(context.Background(),
		&adapter.Target{BaseURL: "https://up.example/v1beta", Model: "m"},
		&adapter.Forward{Body: []byte(`{}`), Header: http.Header{}})
	if err == nil {
		t.Fatal("want an error for a missing URL operation")
	}
}
