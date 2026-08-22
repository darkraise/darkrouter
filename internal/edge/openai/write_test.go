package openai

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

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
	} {
		if got := statusFor(tc.in); got != tc.want {
			t.Errorf("statusFor(%s) = %d, want %d", tc.in, got, tc.want)
		}
	}
}
