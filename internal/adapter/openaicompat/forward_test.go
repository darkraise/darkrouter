package openaicompat

import (
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/sse"
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
func TestRecognizeEventOnChunks(t *testing.T) {
	for _, tc := range []struct {
		name      string
		data      string
		content   bool
		usageOnly bool
	}{
		{"the role-only first chunk does not commit",
			`{"choices":[{"index":0,"delta":{"role":"assistant"}}]}`, false, false},
		{"a content delta commits",
			`{"choices":[{"index":0,"delta":{"content":"He"}}]}`, true, false},
		{"a reasoning delta commits",
			`{"choices":[{"index":0,"delta":{"reasoning_content":"hm"}}]}`, true, false},
		{"a tool call delta commits",
			`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"name":"f"}}]}}]}`, true, false},
		{"an empty content delta does not commit",
			`{"choices":[{"index":0,"delta":{"content":""}}]}`, false, false},
		{"the final usage chunk carries no choices",
			`{"choices":[],"usage":{"prompt_tokens":9,"completion_tokens":4}}`, false, true},
		{"a chunk carrying both is not usage-only",
			`{"choices":[{"index":0,"delta":{"content":"x"}}],"usage":{"prompt_tokens":9}}`, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := New().RecognizeEvent(sse.Event{Data: tc.data})
			if got.Content != tc.content {
				t.Errorf("Content = %v, want %v", got.Content, tc.content)
			}
			if got.UsageOnly != tc.usageOnly {
				t.Errorf("UsageOnly = %v, want %v", got.UsageOnly, tc.usageOnly)
			}
		})
	}
}

func TestRecognizeEventIgnoresTheDoneSentinel(t *testing.T) {
	// [DONE] is not JSON. Treating it as a parse failure would be harmless but
	// would also mean the log could not tell it from a real one.
	got := New().RecognizeEvent(sse.Event{Data: "[DONE]"})
	if got.Content || got.ErrPayload != "" || got.Usage != nil {
		t.Errorf("[DONE] recognized as %+v", got)
	}
}

func TestRecognizeEventReportsAnInStreamError(t *testing.T) {
	got := New().RecognizeEvent(sse.Event{
		Data: `{"error":{"message":"upstream is on fire","type":"server_error"}}`})
	if got.ErrPayload == "" {
		t.Fatal("an error payload was not recognized")
	}
}

func TestRecognizeUsageReadsCachedAndReasoningDetails(t *testing.T) {
	u := New().RecognizeUsage([]byte(`{"choices":[],"usage":{"prompt_tokens":40,
		"completion_tokens":9,"prompt_tokens_details":{"cached_tokens":12},
		"completion_tokens_details":{"reasoning_tokens":5}}}`))
	if u == nil {
		t.Fatal("no usage")
	}
	if u.InputTokens != 40 || u.OutputTokens != 9 ||
		u.CacheReadTokens != 12 || u.ReasoningTokens != 5 {
		t.Errorf("usage = %+v", *u)
	}
}
