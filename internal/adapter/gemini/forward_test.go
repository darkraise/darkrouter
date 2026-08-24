package gemini

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/sse"
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
func TestRecognizeEventOnCandidates(t *testing.T) {
	for _, tc := range []struct {
		name    string
		data    string
		content bool
	}{
		{"a text part commits",
			`{"candidates":[{"content":{"role":"model","parts":[{"text":"He"}]}}]}`, true},
		{"a thought part commits",
			`{"candidates":[{"content":{"role":"model","parts":[{"text":"hm","thought":true}]}}]}`, true},
		{"a function call commits",
			`{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"f","args":{}}}]}}]}`, true},
		{"a bare thought signature does not commit",
			`{"candidates":[{"content":{"role":"model","parts":[{"thoughtSignature":"sig"}]}}]}`, false},
		{"a candidate with no parts does not commit",
			`{"candidates":[{"finishReason":"STOP"}]}`, false},
		{"usage metadata alone does not commit",
			`{"usageMetadata":{"promptTokenCount":8,"candidatesTokenCount":2}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := New().RecognizeEvent(sse.Event{Data: tc.data})
			if got.Content != tc.content {
				t.Errorf("Content = %v, want %v", got.Content, tc.content)
			}
		})
	}
}

func TestRecognizeEventReportsUsageMetadata(t *testing.T) {
	got := New().RecognizeEvent(sse.Event{Data: `{"candidates":[],"usageMetadata":
		{"promptTokenCount":8,"candidatesTokenCount":2,"cachedContentTokenCount":3,
		 "thoughtsTokenCount":4}}`})
	if got.Usage == nil {
		t.Fatal("no usage")
	}
	if got.Usage.InputTokens != 8 || got.Usage.OutputTokens != 2 ||
		got.Usage.CacheReadTokens != 3 || got.Usage.ReasoningTokens != 4 {
		t.Errorf("usage = %+v", *got.Usage)
	}
}

func TestRecognizeEventReportsAPromptFeedbackBlock(t *testing.T) {
	// Gemini's SSE has no error event type; a refusal arrives as a chunk
	// carrying promptFeedback.blockReason, and before commit that is a
	// provider answer rather than content.
	got := New().RecognizeEvent(sse.Event{
		Data: `{"promptFeedback":{"blockReason":"SAFETY"}}`})
	if got.ErrPayload == "" {
		t.Fatal("a blocked prompt was not recognized")
	}
}
