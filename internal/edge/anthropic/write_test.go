package anthropic

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

func TestWriteResponseProducesTheMessageShape(t *testing.T) {
	got := written(t, &ir.Response{
		ID: "msg_1", Model: "claude-x", StopReason: ir.StopToolUse,
		Content: []ir.ContentBlock{
			{Type: ir.BlockThinking, Thinking: &ir.Thinking{Text: "hmm", Signature: "sig-1"}},
			{Type: ir.BlockText, Text: "calling"},
			{Type: ir.BlockToolUse, ToolUse: &ir.ToolUse{
				ID: "call_a", Name: "f", Input: json.RawMessage(`{"x":1}`)}},
		},
		Usage: ir.Usage{InputTokens: 10, OutputTokens: 4, CacheReadTokens: 3, CacheWriteTokens: 7},
	})
	if got["type"] != "message" || got["role"] != "assistant" {
		t.Fatalf("envelope = %v", got)
	}
	if got["stop_reason"] != "tool_use" {
		t.Errorf("stop_reason = %v", got["stop_reason"])
	}
	blocks := got["content"].([]any)
	if len(blocks) != 3 {
		t.Fatalf("content = %v", blocks)
	}
	if blocks[0].(map[string]any)["signature"] != "sig-1" {
		t.Errorf("thinking = %v", blocks[0])
	}
	if _, ok := blocks[2].(map[string]any)["input"].(map[string]any); !ok {
		t.Errorf("tool_use input = %#v; Anthropic takes an object", blocks[2].(map[string]any)["input"])
	}
	usage := got["usage"].(map[string]any)
	if usage["input_tokens"].(float64) != 10 || usage["cache_read_input_tokens"].(float64) != 3 ||
		usage["cache_creation_input_tokens"].(float64) != 7 {
		t.Errorf("usage = %v", usage)
	}
}

func TestWriteResponseEmitsAnEmptyContentArray(t *testing.T) {
	got := written(t, &ir.Response{ID: "m", Model: "c", StopReason: ir.StopEndTurn})
	blocks, ok := got["content"].([]any)
	if !ok || blocks == nil {
		t.Fatalf("content = %#v; Anthropic clients index it unconditionally", got["content"])
	}
	if len(blocks) != 0 {
		t.Errorf("content = %v", blocks)
	}
}

func TestWriteResponseMapsContentFilterToRefusal(t *testing.T) {
	got := written(t, &ir.Response{ID: "m", Model: "c", StopReason: ir.StopContentFilter})
	if got["stop_reason"] != "refusal" {
		t.Errorf("stop_reason = %v; refusal is Anthropic's content filter", got["stop_reason"])
	}
}

func TestWriteErrorUsesTheAnthropicShapeAndStatus(t *testing.T) {
	cases := []struct {
		in     ir.ErrorType
		status int
		typ    string
	}{
		{ir.ErrInvalidRequest, 400, "invalid_request_error"},
		{ir.ErrAuthentication, 401, "authentication_error"},
		{ir.ErrPermission, 403, "permission_error"},
		{ir.ErrNotFound, 404, "not_found_error"},
		{ir.ErrRateLimit, 429, "rate_limit_error"},
		{ir.ErrOverloaded, 529, "overloaded_error"},
		{ir.ErrContentFilter, 400, "invalid_request_error"},
		{ir.ErrDarkrouter, 502, "api_error"},
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
		if got["type"] != "error" {
			t.Errorf("%s: envelope = %v", tc.in, got)
		}
		if got["error"].(map[string]any)["type"] != tc.typ {
			t.Errorf("%s: error type = %v, want %s", tc.in, got["error"], tc.typ)
		}
	}
}

func TestProxyTokenAcceptsBothForms(t *testing.T) {
	d := New()
	r := httptest.NewRequest("POST", "/v1/messages", nil)
	r.Header.Set("x-api-key", "sk-ant")
	if got := d.ProxyToken(r); got != "sk-ant" {
		t.Errorf("x-api-key = %q", got)
	}

	r2 := httptest.NewRequest("POST", "/v1/messages", nil)
	r2.Header.Set("Authorization", "Bearer sk-bearer")
	if got := d.ProxyToken(r2); got != "sk-bearer" {
		t.Errorf("bearer = %q", got)
	}

	r3 := httptest.NewRequest("POST", "/v1/messages", nil)
	r3.Header.Set("x-api-key", "sk-ant")
	r3.Header.Set("Authorization", "Bearer sk-bearer")
	if got := d.ProxyToken(r3); got != "sk-ant" {
		t.Errorf("both = %q; x-api-key is Anthropic's own form and wins", got)
	}
}

func TestWriteCountUsesInputTokens(t *testing.T) {
	rec := httptest.NewRecorder()
	if err := New().WriteCount(rec, 2095); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["input_tokens"].(float64) != 2095 {
		t.Errorf("body = %v", got)
	}
}
