package gemini

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/ir"
)

func TestBuildCountRequestUsesTheCountTokensMethod(t *testing.T) {
	hr, err := New().BuildCountRequest(context.Background(),
		&adapter.Target{
			BaseURL: "https://generativelanguage.googleapis.com/v1beta",
			APIKey:  "AIza", Model: "gemini-2.0-flash",
		},
		&ir.Request{Messages: []ir.Message{userMsg("hi")}})
	if err != nil {
		t.Fatal(err)
	}
	want := "https://generativelanguage.googleapis.com/v1beta/models/gemini-2.0-flash:countTokens"
	if hr.URL.String() != want {
		t.Errorf("url = %s, want %s", hr.URL, want)
	}
	if hr.Header.Get("x-goog-api-key") != "AIza" {
		t.Errorf("key header = %q", hr.Header.Get("x-goog-api-key"))
	}
}

func TestParseCountResponseReadsTotalTokens(t *testing.T) {
	got, err := New().ParseCountResponse(&http.Response{
		StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"totalTokens":31}`)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != 31 {
		t.Errorf("tokens = %d", got)
	}
}

var _ adapter.TokenCounter = (*Adapter)(nil)
