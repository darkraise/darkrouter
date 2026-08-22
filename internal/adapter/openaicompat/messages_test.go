package openaicompat

import (
	"encoding/json"
	"testing"

	"github.com/darkraise/darkrouter/internal/ir"
)

// rendered marshals through JSON so assertions read against the wire shape
// rather than against map[string]any, which is what the provider sees.
func rendered(t *testing.T, req *ir.Request) ([]map[string]any, []ir.Warning) {
	t.Helper()
	msgs, warns := renderMessages(req, "openaicompat")
	b, err := json.Marshal(msgs)
	if err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out, warns
}

func hasWarning(warns []ir.Warning, field string) bool {
	for _, w := range warns {
		if w.Field == field {
			return true
		}
	}
	return false
}

func TestRenderMessagesEmitsToolCallsOnTheAssistantTurn(t *testing.T) {
	got, warns := rendered(t, &ir.Request{Messages: []ir.Message{
		{Role: ir.RoleUser, Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "weather?"}}},
		{Role: ir.RoleAssistant, Content: []ir.ContentBlock{{
			Type:    ir.BlockToolUse,
			ToolUse: &ir.ToolUse{ID: "call_a", Name: "get_weather", Input: json.RawMessage(`{"city":"Oslo"}`)},
		}}},
	}})
	if len(warns) != 0 {
		t.Fatalf("warnings = %+v, want none", warns)
	}
	if len(got) != 2 {
		t.Fatalf("messages = %d, want 2: %v", len(got), got)
	}
	calls := got[1]["tool_calls"].([]any)
	fn := calls[0].(map[string]any)["function"].(map[string]any)
	if calls[0].(map[string]any)["id"] != "call_a" || fn["name"] != "get_weather" {
		t.Errorf("tool_calls = %v", calls)
	}
	if fn["arguments"] != `{"city":"Oslo"}` {
		t.Errorf("arguments = %v; OpenAI takes a JSON string, not an object", fn["arguments"])
	}
	if _, ok := got[1]["content"]; !ok {
		t.Error("a tool-call-only assistant message must still carry a content key")
	}
}

func TestRenderMessagesSplitsAToolResultTurn(t *testing.T) {
	got, _ := rendered(t, &ir.Request{Messages: []ir.Message{
		{Role: ir.RoleAssistant, Content: []ir.ContentBlock{{
			Type:    ir.BlockToolUse,
			ToolUse: &ir.ToolUse{ID: "call_a", Name: "f", Input: json.RawMessage(`{}`)},
		}}},
		{Role: ir.RoleUser, Content: []ir.ContentBlock{
			{Type: ir.BlockToolResult, ToolResult: &ir.ToolResult{
				ToolUseID: "call_a",
				Content:   []ir.ContentBlock{{Type: ir.BlockText, Text: "17C"}},
			}},
			{Type: ir.BlockText, Text: "and tomorrow?"},
		}},
	}})
	if len(got) != 3 {
		t.Fatalf("messages = %d, want 3: %v", len(got), got)
	}
	if got[1]["role"] != "tool" || got[1]["tool_call_id"] != "call_a" || got[1]["content"] != "17C" {
		t.Errorf("tool message = %v", got[1])
	}
	if got[2]["role"] != "user" || got[2]["content"] != "and tomorrow?" {
		t.Errorf("trailing user message = %v", got[2])
	}
}

func TestRenderMessagesKeepsParallelResultsInOrder(t *testing.T) {
	got, _ := rendered(t, &ir.Request{Messages: []ir.Message{
		{Role: ir.RoleUser, Content: []ir.ContentBlock{
			{Type: ir.BlockToolResult, ToolResult: &ir.ToolResult{
				ToolUseID: "call_a", Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "one"}}}},
			{Type: ir.BlockToolResult, ToolResult: &ir.ToolResult{
				ToolUseID: "call_b", Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "two"}}}},
		}},
	}})
	if len(got) != 2 {
		t.Fatalf("messages = %d, want 2: %v", len(got), got)
	}
	if got[0]["tool_call_id"] != "call_a" || got[1]["tool_call_id"] != "call_b" {
		t.Errorf("order = %v, %v; two calls to one function differ only by id",
			got[0]["tool_call_id"], got[1]["tool_call_id"])
	}
}

func TestRenderMessagesHoistsAnImageOutOfAToolResult(t *testing.T) {
	got, warns := rendered(t, &ir.Request{Messages: []ir.Message{
		{Role: ir.RoleTool, Content: []ir.ContentBlock{
			{Type: ir.BlockToolResult, ToolResult: &ir.ToolResult{
				ToolUseID: "call_a",
				Content: []ir.ContentBlock{
					{Type: ir.BlockText, Text: "here"},
					{Type: ir.BlockImage, Media: &ir.Media{MIME: "image/png", Data: "AAAA"}},
				},
			}},
		}},
	}})
	if len(got) != 2 {
		t.Fatalf("messages = %d, want a tool message and a hoisting user message: %v", len(got), got)
	}
	if got[0]["content"] != "here" {
		t.Errorf("tool content = %v; the image must not be stringified into it", got[0]["content"])
	}
	parts := got[1]["content"].([]any)
	img := parts[0].(map[string]any)
	if img["type"] != "image_url" {
		t.Errorf("hoisted part = %v", img)
	}
	if !hasWarning(warns, "messages[].tool_result.image") {
		t.Errorf("warnings = %+v; the hoist must be recorded", warns)
	}
}

func TestRenderMessagesDropsAnAssistantPrefill(t *testing.T) {
	got, warns := rendered(t, &ir.Request{Messages: []ir.Message{
		{Role: ir.RoleUser, Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "write json"}}},
		{Role: ir.RoleAssistant, Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "{"}}},
	}})
	if len(got) != 1 || got[0]["role"] != "user" {
		t.Fatalf("messages = %v, want the user turn alone", got)
	}
	if !hasWarning(warns, "messages[last].assistant_prefill") {
		t.Errorf("warnings = %+v", warns)
	}
}

func TestRenderMessagesDropsThinkingAndWarns(t *testing.T) {
	got, warns := rendered(t, &ir.Request{Messages: []ir.Message{
		{Role: ir.RoleAssistant, Content: []ir.ContentBlock{
			{Type: ir.BlockThinking, Thinking: &ir.Thinking{Text: "hmm", Signature: "sig"}},
			{Type: ir.BlockText, Text: "answer"},
		}},
		{Role: ir.RoleUser, Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "more"}}},
	}})
	if got[0]["content"] != "answer" {
		t.Errorf("content = %v", got[0]["content"])
	}
	if !hasWarning(warns, "messages[].assistant.thinking") {
		t.Errorf("warnings = %+v", warns)
	}
}

func TestRenderMessagesWarnsOnCacheControl(t *testing.T) {
	_, warns := rendered(t, &ir.Request{Messages: []ir.Message{
		{Role: ir.RoleUser, Content: []ir.ContentBlock{{
			Type: ir.BlockText, Text: "long prompt",
			CacheControl: &ir.CacheControl{Type: "ephemeral", TTL: "1h"},
		}}},
	}})
	if !hasWarning(warns, "cache_control") {
		t.Errorf("warnings = %+v; a paid feature must never vanish quietly", warns)
	}
}

func TestRenderMessagesEmitsTopLevelSystemFirst(t *testing.T) {
	got, _ := rendered(t, &ir.Request{
		System:   []ir.ContentBlock{{Type: ir.BlockText, Text: "be terse"}},
		Messages: []ir.Message{{Role: ir.RoleUser, Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "hi"}}}},
	})
	if len(got) != 2 || got[0]["role"] != "system" || got[0]["content"] != "be terse" {
		t.Fatalf("messages = %v", got)
	}
}

func TestRenderMessagesKeepsInlineSystemTurnsInPlace(t *testing.T) {
	got, warns := rendered(t, &ir.Request{Messages: []ir.Message{
		{Role: ir.RoleUser, Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "hi"}}},
		{Role: ir.RoleSystem, Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "now be brief"}}},
		{Role: ir.RoleUser, Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "again"}}},
	}})
	if len(got) != 3 || got[1]["role"] != "system" {
		t.Fatalf("messages = %v; OpenAI permits a mid-conversation system message", got)
	}
	if len(warns) != 0 {
		t.Errorf("warnings = %+v; nothing was lost, so nothing is warned about", warns)
	}
}
