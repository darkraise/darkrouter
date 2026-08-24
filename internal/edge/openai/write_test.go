package openai

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/darkraise/darkrouter/internal/ir"
)

func TestWriteResponseProducesOpenAIShape(t *testing.T) {
	rec := httptest.NewRecorder()
	err := WriteResponse(rec, &ir.Response{
		ID:         "req-1",
		Model:      "m",
		Content:    []ir.ContentBlock{{Type: ir.BlockText, Text: "hello"}},
		StopReason: ir.StopEndTurn,
		Usage:      ir.Usage{InputTokens: 3, OutputTokens: 5},
	})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["object"] != "chat.completion" {
		t.Errorf("object = %v", got["object"])
	}
	choices := got["choices"].([]any)
	msg := choices[0].(map[string]any)["message"].(map[string]any)
	if msg["content"] != "hello" {
		t.Errorf("content = %v", msg["content"])
	}
	if choices[0].(map[string]any)["finish_reason"] != "stop" {
		t.Errorf("finish_reason = %v", choices[0].(map[string]any)["finish_reason"])
	}
	usage := got["usage"].(map[string]any)
	if usage["total_tokens"].(float64) != 8 {
		t.Errorf("total_tokens = %v", usage["total_tokens"])
	}
}

func TestWriteErrorUsesOpenAIErrorObject(t *testing.T) {
	rec := httptest.NewRecorder()
	if err := WriteError(rec, &ir.Error{Type: ir.ErrNotFound, Message: "no such model"}); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 404 {
		t.Errorf("status = %d", rec.Code)
	}
	var got struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Error.Message != "no such model" || got.Error.Type != "not_found" {
		t.Fatalf("got %+v", got.Error)
	}
}

func TestStatusForMapsEveryErrorType(t *testing.T) {
	for _, tc := range []struct {
		in   ir.ErrorType
		want int
	}{
		{ir.ErrInvalidRequest, 400},
		{ir.ErrAuthentication, 401},
		{ir.ErrPermission, 403},
		{ir.ErrNotFound, 404},
		{ir.ErrRateLimit, 429},
		{ir.ErrOverloaded, 503},
		{ir.ErrContentFilter, 400},
		{ir.ErrAPI, 502},
		{ir.ErrDarkrouter, 502},
		{ir.ErrPayloadTooLarge, 413},
		{ir.ErrUnsupportedMedia, 415},
	} {
		if got := statusFor(tc.in); got != tc.want {
			t.Errorf("statusFor(%s) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

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

func TestWriteResponseEmitsToolCalls(t *testing.T) {
	got := written(t, &ir.Response{
		ID: "req-1", Model: "m", StopReason: ir.StopToolUse,
		Content: []ir.ContentBlock{{
			Type:    ir.BlockToolUse,
			ToolUse: &ir.ToolUse{ID: "call_a", Name: "f", Input: json.RawMessage(`{"x":1}`)},
		}},
	})
	choice := got["choices"].([]any)[0].(map[string]any)
	if choice["finish_reason"] != "tool_calls" {
		t.Errorf("finish_reason = %v", choice["finish_reason"])
	}
	msg := choice["message"].(map[string]any)
	if msg["content"] != nil {
		t.Errorf("content = %v; a tool-call-only reply sends null", msg["content"])
	}
	calls := msg["tool_calls"].([]any)
	fn := calls[0].(map[string]any)["function"].(map[string]any)
	if fn["arguments"] != `{"x":1}` {
		t.Errorf("arguments = %v; OpenAI takes a JSON string", fn["arguments"])
	}
}

func TestWriteResponseEmitsReasoningContent(t *testing.T) {
	got := written(t, &ir.Response{
		ID: "req-1", Model: "m", StopReason: ir.StopEndTurn,
		Content: []ir.ContentBlock{
			{Type: ir.BlockThinking, Thinking: &ir.Thinking{Text: "let me see", Signature: "sig"}},
			{Type: ir.BlockText, Text: "42"},
		},
	})
	msg := got["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)
	if msg["content"] != "42" {
		t.Errorf("content = %v", msg["content"])
	}
	if msg["reasoning_content"] != "let me see" {
		t.Errorf("reasoning_content = %v", msg["reasoning_content"])
	}
}

func TestWriteResponseReportsCachedAndReasoningTokens(t *testing.T) {
	got := written(t, &ir.Response{
		ID: "req-1", Model: "m", StopReason: ir.StopEndTurn,
		Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "hi"}},
		Usage: ir.Usage{
			InputTokens: 10, OutputTokens: 5, CacheReadTokens: 8, ReasoningTokens: 3,
		},
	})
	usage := got["usage"].(map[string]any)
	pd := usage["prompt_tokens_details"].(map[string]any)
	if pd["cached_tokens"].(float64) != 8 {
		t.Errorf("cached_tokens = %v", pd["cached_tokens"])
	}
	cd := usage["completion_tokens_details"].(map[string]any)
	if cd["reasoning_tokens"].(float64) != 3 {
		t.Errorf("reasoning_tokens = %v", cd["reasoning_tokens"])
	}
}

func TestWriteResponseMapsStopSequenceAndPauseTurn(t *testing.T) {
	for _, sr := range []ir.StopReason{ir.StopStopSequence, ir.StopPauseTurn} {
		got := written(t, &ir.Response{
			ID: "r", Model: "m", StopReason: sr,
			Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "hi"}},
		})
		fr := got["choices"].([]any)[0].(map[string]any)["finish_reason"]
		if fr != "stop" {
			t.Errorf("finish_reason for %q = %v, want stop", sr, fr)
		}
	}
}

func TestWriteResponseUsesTheClockSeam(t *testing.T) {
	orig := now
	now = func() time.Time { return time.Unix(1700000000, 0) }
	defer func() { now = orig }()

	got := written(t, &ir.Response{
		ID: "r", Model: "m", StopReason: ir.StopEndTurn,
		Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "hi"}},
	})
	if got["created"].(float64) != 1700000000 {
		t.Errorf("created = %v", got["created"])
	}
}

func TestAnOversizedPayloadIs413(t *testing.T) {
	// 400 tells a client its request is malformed and retrying is pointless.
	// 413 tells it to send a smaller file. An audio client has no other signal
	// to tell those apart.
	w := httptest.NewRecorder()
	if err := WriteError(w, &ir.Error{
		Type: ir.ErrPayloadTooLarge, Message: "upload exceeds the configured maximum",
	}); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", w.Code)
	}
}
