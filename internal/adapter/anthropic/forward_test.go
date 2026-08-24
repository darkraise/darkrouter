package anthropic

import (
	"context"
	"net/http"
	"testing"

	"github.com/darkraise/darkrouter/internal/adapter"
)

func TestBuildForwardKeepsTheClientVersionHeader(t *testing.T) {
	h := http.Header{}
	h.Set("anthropic-version", "2023-01-01")
	h.Set("anthropic-beta", "context-1m-2025-08-07")

	hr, err := New().BuildForward(context.Background(),
		&adapter.Target{BaseURL: "https://up.example/v1", APIKey: "sk-ant", Model: "m"},
		&adapter.Forward{Body: []byte(`{"model":"m"}`), Header: h})
	if err != nil {
		t.Fatal(err)
	}
	if hr.URL.String() != "https://up.example/v1/messages" {
		t.Errorf("URL = %s", hr.URL)
	}
	// The client's version wins. Overwriting it with the default would defeat
	// the fidelity argument for a client pinned to an older wire contract.
	if got := hr.Header.Get("anthropic-version"); got != "2023-01-01" {
		t.Errorf("anthropic-version = %q, want the client's", got)
	}
	if got := hr.Header.Get("anthropic-beta"); got != "context-1m-2025-08-07" {
		t.Errorf("anthropic-beta = %q", got)
	}
	if got := hr.Header.Get("x-api-key"); got != "sk-ant" {
		t.Errorf("x-api-key = %q", got)
	}
}

func TestBuildForwardSuppliesTheDefaultVersionWhenTheClientSentNone(t *testing.T) {
	hr, err := New().BuildForward(context.Background(),
		&adapter.Target{BaseURL: "https://up.example/v1", APIKey: "sk-ant", Model: "m"},
		&adapter.Forward{Body: []byte(`{"model":"m"}`), Header: http.Header{}})
	if err != nil {
		t.Fatal(err)
	}
	if got := hr.Header.Get("anthropic-version"); got != DefaultVersion {
		t.Errorf("anthropic-version = %q, want %q", got, DefaultVersion)
	}
}
