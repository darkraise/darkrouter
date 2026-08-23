package bedrock

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/ir"
)

func respWith(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}
}

const converseBody = `{
  "output": {"message": {"role": "assistant", "content": [
    {"text": "It is 17C."},
    {"toolUse": {"toolUseId": "tu_1", "name": "get_weather", "input": {"city": "Oslo"}}}
  ]}},
  "stopReason": "tool_use",
  "usage": {"inputTokens": 12, "outputTokens": 7, "totalTokens": 19},
  "metrics": {"latencyMs": 431}
}`

func TestParseResponseCarriesContentAndUsage(t *testing.T) {
	got, err := ParseResponse(respWith(200, converseBody))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Content) != 2 {
		t.Fatalf("content = %+v", got.Content)
	}
	if got.Content[0].Type != ir.BlockText || got.Content[0].Text != "It is 17C." {
		t.Errorf("first block = %+v", got.Content[0])
	}
	tu := got.Content[1].ToolUse
	if tu == nil || tu.ID != "tu_1" || tu.Name != "get_weather" {
		t.Fatalf("tool use = %+v", got.Content[1])
	}
	if !strings.Contains(string(tu.Input), "Oslo") {
		t.Errorf("tool input = %s", tu.Input)
	}
	if got.StopReason != ir.StopToolUse {
		t.Errorf("stop reason = %q", got.StopReason)
	}
	if got.Usage.InputTokens != 12 || got.Usage.OutputTokens != 7 {
		t.Errorf("usage = %+v", got.Usage)
	}
}

func TestParseResponseMapsEveryStopReason(t *testing.T) {
	for wire, want := range map[string]ir.StopReason{
		"end_turn":             ir.StopEndTurn,
		"max_tokens":           ir.StopMaxTokens,
		"tool_use":             ir.StopToolUse,
		"stop_sequence":        ir.StopStopSequence,
		"content_filtered":     ir.StopContentFilter,
		"guardrail_intervened": ir.StopContentFilter,
	} {
		body := `{"output":{"message":{"role":"assistant","content":[]}},"stopReason":"` + wire + `"}`
		got, err := ParseResponse(respWith(200, body))
		if err != nil {
			t.Fatal(err)
		}
		if got.StopReason != want {
			t.Errorf("%s -> %q, want %q", wire, got.StopReason, want)
		}
	}
}

func TestParseResponseKeepsReasoningContent(t *testing.T) {
	body := `{"output":{"message":{"role":"assistant","content":[
	  {"reasoningContent":{"reasoningText":{"text":"thinking...","signature":"sig"}}},
	  {"text":"answer"}]}},"stopReason":"end_turn"}`
	got, err := ParseResponse(respWith(200, body))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Content) != 2 || got.Content[0].Type != ir.BlockThinking {
		t.Fatalf("content = %+v", got.Content)
	}
	if got.Content[0].Thinking.Text != "thinking..." || got.Content[0].Thinking.Signature != "sig" {
		t.Errorf("thinking = %+v", got.Content[0].Thinking)
	}
}

func TestParseResponseKeepsRedactedReasoning(t *testing.T) {
	body := `{"output":{"message":{"role":"assistant","content":[
	  {"reasoningContent":{"redactedContent":"AAAA"}}]}},"stopReason":"end_turn"}`
	got, err := ParseResponse(respWith(200, body))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Content) != 1 || got.Content[0].Type != ir.BlockRedactedThinking {
		t.Fatalf("content = %+v", got.Content)
	}
	if got.Content[0].Thinking.Data != "AAAA" {
		t.Errorf("redacted payload = %q", got.Content[0].Thinking.Data)
	}
}

func TestParseResponseClosesTheBody(t *testing.T) {
	rc := &closeTracker{Reader: strings.NewReader(converseBody)}
	resp := &http.Response{StatusCode: 200, Body: rc, Header: http.Header{}}
	if _, err := ParseResponse(resp); err != nil {
		t.Fatal(err)
	}
	if !rc.closed {
		t.Error("ParseResponse must take ownership of the body and close it")
	}
}

type closeTracker struct {
	io.Reader
	closed bool
}

func (c *closeTracker) Close() error { c.closed = true; return nil }

func TestClassifyBodyTreatsAnUnknownModelAsRetryable(t *testing.T) {
	// A failover chain must not die on the first provider that does not carry
	// the model. Bedrock says so with a 400, not a 404.
	body := []byte(`{"message":"The provided model identifier is invalid."}`)
	got := New().ClassifyBody(respWith(400, string(body)), body, nil)
	if got != adapter.OutcomeRetryableModel {
		t.Errorf("outcome = %q, want retryable_model", got)
	}
}

func TestClassifyBodyLeavesOtherValidationErrorsFatal(t *testing.T) {
	// A malformed request is the client's fault and retrying it against four
	// more providers wastes four more round trips to reach the same answer.
	body := []byte(`{"message":"messages: at least one message is required"}`)
	if got := New().ClassifyBody(respWith(400, string(body)), body, nil); got != adapter.OutcomeFatal {
		t.Errorf("outcome = %q, want fatal", got)
	}
}

func TestClassifyThrottlingIsRetryable(t *testing.T) {
	// Bedrock's ThrottlingException is a 429, which the shared status rule
	// already handles. Asserted so a future override cannot quietly break it.
	if got := Classify(respWith(429, ""), nil); got != adapter.OutcomeRetryableProvider {
		t.Errorf("outcome = %q", got)
	}
	if got := Classify(respWith(403, ""), nil); got != adapter.OutcomeRetryableCredential {
		t.Errorf("403 -> %q, want retryable_credential", got)
	}
}
