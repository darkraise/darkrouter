package anthropic

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/ir"
)

func parsed(t *testing.T, body string, headers map[string]string) *ir.Request {
	t.Helper()
	r := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body))
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	req, pt, err := ParseRequest(r, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if pt == nil || pt.Surface != ir.SurfaceLLM || pt.ModelField != "model" {
		t.Fatalf("passthrough = %+v", pt)
	}
	return req
}

func TestParseRequestReadsSystemAsStringOrBlocks(t *testing.T) {
	one := parsed(t, `{"model":"claude-x","max_tokens":10,"system":"be terse","messages":[]}`, nil)
	if len(one.System) != 1 || one.System[0].Text != "be terse" {
		t.Fatalf("system = %+v", one.System)
	}
	many := parsed(t, `{"model":"claude-x","max_tokens":10,"messages":[],
		"system":[{"type":"text","text":"be terse","cache_control":{"type":"ephemeral","ttl":"1h"}}]}`, nil)
	if len(many.System) != 1 || many.System[0].CacheControl == nil || many.System[0].CacheControl.TTL != "1h" {
		t.Fatalf("system = %+v; the TTL is a paid feature", many.System)
	}
}

func TestParseRequestKeepsToolResultsInTheUserTurn(t *testing.T) {
	req := parsed(t, `{"model":"claude-x","max_tokens":10,"messages":[
		{"role":"assistant","content":[{"type":"tool_use","id":"call_a","name":"f","input":{"x":1}}]},
		{"role":"user","content":[
			{"type":"tool_result","tool_use_id":"call_a","is_error":true,
			 "content":[{"type":"text","text":"boom"},
			            {"type":"image","source":{"type":"base64","media_type":"image/png","data":"AAAA"}}]},
			{"type":"text","text":"what now?"}]}]}`, nil)

	if len(req.Messages) != 2 {
		t.Fatalf("messages = %d", len(req.Messages))
	}
	if req.Messages[0].Content[0].ToolUse.ID != "call_a" ||
		string(req.Messages[0].Content[0].ToolUse.Input) != `{"x":1}` {
		t.Errorf("tool_use = %+v", req.Messages[0].Content[0].ToolUse)
	}
	turn := req.Messages[1]
	if turn.Role != ir.RoleUser {
		t.Errorf("role = %q; Anthropic carries results inside a user turn", turn.Role)
	}
	if len(turn.Content) != 2 {
		t.Fatalf("blocks = %+v", turn.Content)
	}
	tr := turn.Content[0].ToolResult
	if tr == nil || !tr.IsError || len(tr.Content) != 2 {
		t.Fatalf("tool_result = %+v", tr)
	}
	if tr.Content[1].Type != ir.BlockImage || tr.Content[1].Media.Data != "AAAA" {
		t.Errorf("nested image = %+v", tr.Content[1])
	}
}

func TestParseRequestReadsThinkingBlocksBack(t *testing.T) {
	req := parsed(t, `{"model":"claude-x","max_tokens":10,"messages":[
		{"role":"assistant","content":[
			{"type":"thinking","thinking":"step one","signature":"sig-1"},
			{"type":"redacted_thinking","data":"enc"}]}]}`, nil)
	blocks := req.Messages[0].Content
	if blocks[0].Thinking.Signature != "sig-1" {
		t.Errorf("thinking = %+v; the signature must round-trip byte for byte", blocks[0].Thinking)
	}
	if blocks[1].Type != ir.BlockRedactedThinking || blocks[1].Thinking.Data != "enc" {
		t.Errorf("redacted = %+v", blocks[1])
	}
}

func TestParseRequestReadsToolChoiceAndParallelFlag(t *testing.T) {
	req := parsed(t, `{"model":"claude-x","max_tokens":10,"messages":[],
		"tools":[{"name":"f","description":"d","input_schema":{"type":"object"}}],
		"tool_choice":{"type":"tool","name":"f","disable_parallel_tool_use":true}}`, nil)
	if len(req.Tools) != 1 || string(req.Tools[0].Schema) != `{"type":"object"}` {
		t.Fatalf("tools = %+v", req.Tools)
	}
	if req.ToolChoice == nil || req.ToolChoice.Mode != "tool" || req.ToolChoice.Name != "f" {
		t.Fatalf("tool_choice = %+v", req.ToolChoice)
	}
	if req.ParallelToolCalls == nil || *req.ParallelToolCalls {
		t.Errorf("parallel = %v; disable_parallel_tool_use inverts", req.ParallelToolCalls)
	}
}

func TestParseRequestRoundTripsTheThinkingType(t *testing.T) {
	for _, mode := range []string{"adaptive", "disabled", "enabled"} {
		req := parsed(t, `{"model":"claude-x","max_tokens":10,"messages":[],
			"thinking":{"type":"`+mode+`"}}`, nil)
		if req.Metadata["anthropic_thinking_type"] != mode {
			t.Errorf("%s: metadata = %v; the mode is transport state the adapter needs",
				mode, req.Metadata)
		}
	}
}

func TestParseRequestReadsThinkingBudgetAndVersion(t *testing.T) {
	req := parsed(t, `{"model":"claude-x","max_tokens":10000,"messages":[],
		"thinking":{"type":"enabled","budget_tokens":8000},"metadata":{"user_id":"u1"}}`,
		map[string]string{"anthropic-version": "2024-10-22"})
	if req.Reasoning == nil || req.Reasoning.Budget != 8000 {
		t.Errorf("reasoning = %+v", req.Reasoning)
	}
	if req.Metadata["anthropic_version"] != "2024-10-22" {
		t.Errorf("metadata = %v; the inbound version is forwarded upstream", req.Metadata)
	}
	if req.Metadata["user_id"] != "u1" {
		t.Errorf("metadata = %v", req.Metadata)
	}
}

func TestParseRequestRejectsAnOversizedBody(t *testing.T) {
	r := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(strings.Repeat("x", 100)))
	if _, _, err := ParseRequest(r, 10); err == nil {
		t.Fatal("want an error for a body over the cap")
	}
}
