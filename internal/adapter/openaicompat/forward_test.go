package openaicompat

import (
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/darkraise/darkrouter/internal/adapter"
)

func TestBuildForwardSendsTheBodyUntouched(t *testing.T) {
	body := []byte(`{"model":"target-model","messages":[{"role":"user","content":"a & b < c"}]}`)
	h := http.Header{}
	h.Set("anthropic-beta", "ignored-by-this-kind")

	hr, err := New().BuildForward(context.Background(),
		&adapter.Target{BaseURL: "https://up.example/v1", APIKey: "sk-test", Model: "target-model"},
		&adapter.Forward{Body: body, Header: h})
	if err != nil {
		t.Fatal(err)
	}
	if hr.URL.String() != "https://up.example/v1/chat/completions" {
		t.Errorf("URL = %s", hr.URL)
	}
	got, _ := io.ReadAll(hr.Body)
	if string(got) != string(body) {
		t.Errorf("body was rewritten\n got: %s\nwant: %s", got, body)
	}
	if hr.ContentLength != int64(len(body)) {
		t.Errorf("ContentLength = %d, want %d", hr.ContentLength, len(body))
	}
	if got := hr.Header.Get("Authorization"); got != "Bearer sk-test" {
		t.Errorf("Authorization = %q", got)
	}
	if got := hr.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q", got)
	}
	// An allowlisted header this kind has no opinion about still travels: the
	// whole point of the fast path is that Darkrouter is not a filter on the
	// vendor's own vocabulary.
	if got := hr.Header.Get("anthropic-beta"); got != "ignored-by-this-kind" {
		t.Errorf("forwarded header lost: %q", got)
	}
}

func TestBuildForwardWritesNoAuthWhenTheKeyIsEmpty(t *testing.T) {
	// An empty key means a non-static style, whose authorizer runs after the
	// body is materialized. A header written here would be overwritten at best
	// and would leak a placeholder at worst.
	hr, err := New().BuildForward(context.Background(),
		&adapter.Target{BaseURL: "https://up.example/v1", Model: "m"},
		&adapter.Forward{Body: []byte(`{}`), Header: http.Header{}})
	if err != nil {
		t.Fatal(err)
	}
	if _, present := hr.Header["Authorization"]; present {
		t.Errorf("Authorization written for an empty key: %q", hr.Header.Get("Authorization"))
	}
}
