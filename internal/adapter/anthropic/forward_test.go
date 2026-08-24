package anthropic

import (
	"context"
	"net/http"
	"testing"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/sse"
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
func TestRecognizeEventFollowsPhase3sCommitRule(t *testing.T) {
	for _, tc := range []struct {
		name    string
		typ     string
		data    string
		content bool
	}{
		{"message_start does not commit", "message_start",
			`{"type":"message_start","message":{"id":"m","model":"x","usage":{"input_tokens":7}}}`, false},
		{"an opening block does not commit", "content_block_start",
			`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`, false},
		{"a ping does not commit", "ping", `{"type":"ping"}`, false},
		{"a text delta commits", "content_block_delta",
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"He"}}`, true},
		{"a thinking delta commits", "content_block_delta",
			`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"hm"}}`, true},
		{"a tool input delta commits", "content_block_delta",
			`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"a\""}}`, true},
		{"a signature delta carries no content", "content_block_delta",
			`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig"}}`, false},
		{"an empty text delta carries no content", "content_block_delta",
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":""}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := New().RecognizeEvent(sse.Event{Type: tc.typ, Data: tc.data})
			if got.Content != tc.content {
				t.Errorf("Content = %v, want %v", got.Content, tc.content)
			}
		})
	}
}

func TestRecognizeEventReportsUsageFromBothEvents(t *testing.T) {
	// spec §7: message_start carries input and cache, message_delta output.
	start := New().RecognizeEvent(sse.Event{Type: "message_start", Data: `{"type":"message_start",
		"message":{"id":"m","model":"x","usage":{"input_tokens":100,
		"cache_read_input_tokens":40,"cache_creation_input_tokens":10}}}`})
	if start.Usage == nil {
		t.Fatal("message_start reported no usage")
	}
	if start.Usage.InputTokens != 100 || start.Usage.CacheReadTokens != 40 ||
		start.Usage.CacheWriteTokens != 10 {
		t.Errorf("message_start usage = %+v", *start.Usage)
	}

	delta := New().RecognizeEvent(sse.Event{Type: "message_delta",
		Data: `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":25}}`})
	if delta.Usage == nil || delta.Usage.OutputTokens != 25 {
		t.Errorf("message_delta usage = %+v", delta.Usage)
	}
}

func TestRecognizeEventReportsAnInStreamError(t *testing.T) {
	// Anthropic delivers overloaded_error as an SSE event under a 200. Before
	// commit that must fail over rather than reach the client as content.
	got := New().RecognizeEvent(sse.Event{Type: "error",
		Data: `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`})
	if got.ErrPayload == "" {
		t.Fatal("an error event was not recognized")
	}
	if got.Content {
		t.Error("an error event must not commit")
	}
}

func TestRecognizeUsageReadsAUnaryBody(t *testing.T) {
	u := New().RecognizeUsage([]byte(`{"id":"m","content":[{"type":"text","text":"hi"}],
		"usage":{"input_tokens":11,"output_tokens":3,"cache_read_input_tokens":5}}`))
	if u == nil {
		t.Fatal("no usage")
	}
	if u.InputTokens != 11 || u.OutputTokens != 3 || u.CacheReadTokens != 5 {
		t.Errorf("usage = %+v", *u)
	}
}
