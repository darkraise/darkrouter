package anthropic

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/ir"
)

func TestBuildCountRequestTargetsTheCountingEndpoint(t *testing.T) {
	hr, err := New().BuildCountRequest(context.Background(),
		&adapter.Target{BaseURL: "https://api.anthropic.com/v1", APIKey: "sk-ant", Model: "claude-x"},
		&ir.Request{Messages: []ir.Message{userMsg("hi")}})
	if err != nil {
		t.Fatal(err)
	}
	if hr.URL.String() != "https://api.anthropic.com/v1/messages/count_tokens" {
		t.Errorf("url = %s", hr.URL)
	}
	if hr.Header.Get("anthropic-version") == "" {
		t.Error("the version header is required on every Anthropic request")
	}
	raw, err := io.ReadAll(hr.Body)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if _, ok := body["max_tokens"]; ok {
		t.Error("count_tokens rejects max_tokens; it counts input only")
	}
	if _, ok := body["stream"]; ok {
		t.Error("count_tokens rejects stream")
	}
	if body["model"] != "claude-x" {
		t.Errorf("model = %v", body["model"])
	}
}

func TestBuildCountRequestStripsOutputConfig(t *testing.T) {
	hr, err := New().BuildCountRequest(context.Background(),
		&adapter.Target{BaseURL: "https://api.anthropic.com/v1", Model: "claude-sonnet-4-5"},
		&ir.Request{
			Messages:       []ir.Message{userMsg("hi")},
			ResponseFormat: &ir.ResponseFormat{Type: "json_schema", Schema: json.RawMessage(`{"type":"object"}`)},
		})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(hr.Body)
	if strings.Contains(string(raw), "output_config") {
		t.Errorf("body = %s; an output schema shapes a response that is never generated", raw)
	}
}

func TestParseCountResponseReadsInputTokens(t *testing.T) {
	got, err := New().ParseCountResponse(&http.Response{
		StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"input_tokens":2095}`)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != 2095 {
		t.Errorf("tokens = %d", got)
	}
}

var _ adapter.TokenCounter = (*Adapter)(nil)
