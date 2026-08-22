package gemini

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/ir"
)

func parseBody(t *testing.T, body string) (*ir.Response, error) {
	t.Helper()
	return ParseResponse(&http.Response{
		StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)),
	})
}

func TestParseResponseReadsPartsAndUsage(t *testing.T) {
	got, err := parseBody(t, `{"modelVersion":"gemini-2.0-flash","candidates":[{
		"content":{"role":"model","parts":[
			{"text":"weighing","thought":true,"thoughtSignature":"sig-1"},
			{"text":"calling"},
			{"functionCall":{"id":"call_a","name":"f","args":{"x":1}}}]},
		"finishReason":"STOP"}],
		"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":4,
			"cachedContentTokenCount":3,"thoughtsTokenCount":6}}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Content) != 3 {
		t.Fatalf("content = %+v", got.Content)
	}
	if got.Content[0].Type != ir.BlockThinking || got.Content[0].Thinking.Signature != "sig-1" {
		t.Errorf("thought part = %+v", got.Content[0])
	}
	if got.Content[2].ToolUse == nil || string(got.Content[2].ToolUse.Input) != `{"x":1}` {
		t.Errorf("functionCall = %+v", got.Content[2].ToolUse)
	}
	if got.StopReason != ir.StopToolUse {
		t.Errorf("stop = %q; STOP with a functionCall present means tool use", got.StopReason)
	}
	if got.Model != "gemini-2.0-flash" {
		t.Errorf("model = %q", got.Model)
	}
	if got.Usage.InputTokens != 10 || got.Usage.OutputTokens != 4 ||
		got.Usage.CacheReadTokens != 3 || got.Usage.ReasoningTokens != 6 {
		t.Errorf("usage = %+v", got.Usage)
	}
}

func TestParseResponseStopWithoutACallIsEndTurn(t *testing.T) {
	got, err := parseBody(t, `{"candidates":[{"content":{"parts":[{"text":"hi"}]},"finishReason":"STOP"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if got.StopReason != ir.StopEndTurn {
		t.Errorf("stop = %q", got.StopReason)
	}
}

func TestParseResponseBlockedPromptIsAContentFilterError(t *testing.T) {
	_, err := parseBody(t, `{"promptFeedback":{"blockReason":"SAFETY"},"candidates":[]}`)
	var e *ir.Error
	if !errors.As(err, &e) {
		t.Fatalf("err = %v, want *ir.Error", err)
	}
	if e.Type != ir.ErrContentFilter {
		t.Errorf("error = %+v; an empty success is the failure this guards against", e)
	}
	if !strings.Contains(e.Message, "SAFETY") {
		t.Errorf("message = %q; the block reason is the only actionable detail", e.Message)
	}
}

func TestParseResponseNoCandidatesAndNoBlockIsAnEmptyTurn(t *testing.T) {
	got, err := parseBody(t, `{"candidates":[]}`)
	if err != nil {
		t.Fatalf("err = %v; a model that simply said nothing is not an error", err)
	}
	if len(got.Content) != 0 || got.StopReason != ir.StopEndTurn {
		t.Errorf("response = %+v", got)
	}
}

func TestFinishReasonTable(t *testing.T) {
	cases := []struct {
		in      string
		hasCall bool
		want    ir.StopReason
		known   bool
	}{
		{"STOP", false, ir.StopEndTurn, true},
		{"STOP", true, ir.StopToolUse, true},
		{"", false, ir.StopEndTurn, true},
		{"MAX_TOKENS", false, ir.StopMaxTokens, true},
		{"SAFETY", false, ir.StopContentFilter, true},
		{"BLOCKLIST", false, ir.StopContentFilter, true},
		{"PROHIBITED_CONTENT", false, ir.StopContentFilter, true},
		{"SPII", false, ir.StopContentFilter, true},
		{"RECITATION", false, ir.StopContentFilter, true},
		{"IMAGE_SAFETY", false, ir.StopContentFilter, true},
		{"MALFORMED_FUNCTION_CALL", false, ir.StopError, true},
		{"OTHER", false, ir.StopError, true},
		{"LANGUAGE", false, ir.StopError, true},
		{"SOMETHING_NEW", false, ir.StopEndTurn, false},
	}
	for _, tc := range cases {
		got, known := finishReason(tc.in, tc.hasCall)
		if got != tc.want || known != tc.known {
			t.Errorf("finishReason(%q, %v) = %q, %v; want %q, %v",
				tc.in, tc.hasCall, got, known, tc.want, tc.known)
		}
	}
}

func TestParseResponseWarnsOnAnUnknownFinishReason(t *testing.T) {
	got, err := parseBody(t, `{"candidates":[{"content":{"parts":[{"text":"hi"}]},
		"finishReason":"SOMETHING_NEW"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Warnings) != 1 || got.Warnings[0].Field != "finishReason" {
		t.Errorf("warnings = %+v", got.Warnings)
	}
}

func TestClassifyUsesTheSharedLadder(t *testing.T) {
	if got := Classify(&http.Response{StatusCode: 503}, nil); got != adapter.OutcomeRetryableProvider {
		t.Errorf("503 = %q", got)
	}
}
