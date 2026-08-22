package gemini

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/darkraise/darkrouter/internal/ir"
)

func written(t *testing.T, resp *ir.Response) map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	if err := WriteResponse(rec, resp); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestWriteResponseProducesTheCandidateShape(t *testing.T) {
	got := written(t, &ir.Response{
		ID: "r1", Model: "gemini-2.0-flash", StopReason: ir.StopToolUse,
		Content: []ir.ContentBlock{
			{Type: ir.BlockThinking, Thinking: &ir.Thinking{Text: "weighing", Signature: "sig-1"}},
			{Type: ir.BlockText, Text: "calling"},
			{Type: ir.BlockToolUse, ToolUse: &ir.ToolUse{
				ID: "call_a", Name: "f", Input: json.RawMessage(`{"x":1}`)}},
		},
		Usage: ir.Usage{InputTokens: 10, OutputTokens: 4, CacheReadTokens: 3, ReasoningTokens: 6},
	})
	cands := got["candidates"].([]any)
	c := cands[0].(map[string]any)
	if c["finishReason"] != "STOP" {
		t.Errorf("finishReason = %v; Gemini has no tool-use reason", c["finishReason"])
	}
	content := c["content"].(map[string]any)
	if content["role"] != "model" {
		t.Errorf("role = %v", content["role"])
	}
	ps := content["parts"].([]any)
	if len(ps) != 3 {
		t.Fatalf("parts = %v", ps)
	}
	if ps[0].(map[string]any)["thought"] != true ||
		ps[0].(map[string]any)["thoughtSignature"] != "sig-1" {
		t.Errorf("thought part = %v", ps[0])
	}
	fc := ps[2].(map[string]any)["functionCall"].(map[string]any)
	if fc["name"] != "f" || fc["id"] != "call_a" {
		t.Errorf("functionCall = %v", fc)
	}
	if _, ok := fc["args"].(map[string]any); !ok {
		t.Errorf("args = %#v; Gemini takes an object", fc["args"])
	}
	u := got["usageMetadata"].(map[string]any)
	if u["promptTokenCount"].(float64) != 10 || u["candidatesTokenCount"].(float64) != 4 ||
		u["cachedContentTokenCount"].(float64) != 3 || u["thoughtsTokenCount"].(float64) != 6 ||
		u["totalTokenCount"].(float64) != 14 {
		t.Errorf("usageMetadata = %v", u)
	}
	if got["modelVersion"] != "gemini-2.0-flash" || got["responseId"] != "r1" {
		t.Errorf("envelope = %v", got)
	}
}

func TestWriteResponseMapsFinishReasons(t *testing.T) {
	cases := map[ir.StopReason]string{
		ir.StopEndTurn:       "STOP",
		ir.StopToolUse:       "STOP",
		ir.StopStopSequence:  "STOP",
		ir.StopPauseTurn:     "STOP",
		ir.StopMaxTokens:     "MAX_TOKENS",
		ir.StopContentFilter: "SAFETY",
		ir.StopError:         "OTHER",
	}
	for in, want := range cases {
		got := written(t, &ir.Response{ID: "r", Model: "m", StopReason: in})
		c := got["candidates"].([]any)[0].(map[string]any)
		if c["finishReason"] != want {
			t.Errorf("%q -> %v, want %s", in, c["finishReason"], want)
		}
	}
}

func TestWriteErrorUsesGoogleStatusStrings(t *testing.T) {
	cases := []struct {
		in     ir.ErrorType
		status int
		name   string
	}{
		{ir.ErrInvalidRequest, 400, "INVALID_ARGUMENT"},
		{ir.ErrContentFilter, 400, "INVALID_ARGUMENT"},
		{ir.ErrAuthentication, 401, "UNAUTHENTICATED"},
		{ir.ErrPermission, 403, "PERMISSION_DENIED"},
		{ir.ErrNotFound, 404, "NOT_FOUND"},
		{ir.ErrRateLimit, 429, "RESOURCE_EXHAUSTED"},
		{ir.ErrOverloaded, 503, "UNAVAILABLE"},
		{ir.ErrAPI, 500, "INTERNAL"},
		{ir.ErrDarkrouter, 500, "INTERNAL"},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		if err := WriteError(rec, &ir.Error{Type: tc.in, Message: "nope"}); err != nil {
			t.Fatal(err)
		}
		if rec.Code != tc.status {
			t.Errorf("%s: status = %d, want %d", tc.in, rec.Code, tc.status)
		}
		var got map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		e := got["error"].(map[string]any)
		if e["status"] != tc.name || int(e["code"].(float64)) != tc.status {
			t.Errorf("%s: error = %v", tc.in, e)
		}
	}
}

func TestProxyTokenAcceptsHeaderOrQuery(t *testing.T) {
	d := New()

	r := httptest.NewRequest("POST", "/v1beta/models/m:generateContent", nil)
	r.Header.Set("x-goog-api-key", "AIza-header")
	if got := d.ProxyToken(r); got != "AIza-header" {
		t.Errorf("header = %q", got)
	}

	r2 := httptest.NewRequest("POST", "/v1beta/models/m:generateContent?key=AIza-query", nil)
	if got := d.ProxyToken(r2); got != "AIza-query" {
		t.Errorf("query = %q", got)
	}

	r3 := httptest.NewRequest("POST", "/v1beta/models/m:generateContent?key=AIza-query", nil)
	r3.Header.Set("x-goog-api-key", "AIza-header")
	if got := d.ProxyToken(r3); got != "AIza-header" {
		t.Errorf("both = %q; the header wins", got)
	}
}

func TestNewForReadsTheAltParameter(t *testing.T) {
	plain := httptest.NewRequest("POST", "/v1beta/models/m:streamGenerateContent", nil)
	if NewFor(plain).SSE {
		t.Error("no alt parameter means the JSON-array form")
	}
	sse := httptest.NewRequest("POST", "/v1beta/models/m:streamGenerateContent?alt=sse", nil)
	if !NewFor(sse).SSE {
		t.Error("alt=sse selects the event-stream form")
	}
}
