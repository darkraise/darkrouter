package gemini

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/darkraise/darkrouter/internal/ir"
)

func contents(t *testing.T, req *ir.Request) ([]map[string]any, []ir.Warning) {
	t.Helper()
	raw, warns := NewFetcher().renderContents(context.Background(), req)
	b, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out, warns
}

func parts(t *testing.T, c map[string]any) []map[string]any {
	t.Helper()
	raw := c["parts"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, p := range raw {
		out = append(out, p.(map[string]any))
	}
	return out
}

func TestRenderContentsNamesTheAssistantRoleModel(t *testing.T) {
	got, _ := contents(t, &ir.Request{Messages: []ir.Message{
		{Role: ir.RoleUser, Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "hi"}}},
		{Role: ir.RoleAssistant, Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "hello"}}},
	}})
	if got[0]["role"] != "user" || got[1]["role"] != "model" {
		t.Fatalf("roles = %v, %v; Gemini rejects \"assistant\"", got[0]["role"], got[1]["role"])
	}
}

func TestRenderContentsCarriesThoughtSignatures(t *testing.T) {
	got, warns := contents(t, &ir.Request{Messages: []ir.Message{
		{Role: ir.RoleAssistant, Content: []ir.ContentBlock{
			{Type: ir.BlockThinking, Thinking: &ir.Thinking{Text: "weighing", Signature: "sig-1"}},
			{Type: ir.BlockText, Text: "42"},
		}},
	}})
	if len(warns) != 0 {
		t.Fatalf("warnings = %+v", warns)
	}
	ps := parts(t, got[0])
	if ps[0]["thought"] != true || ps[0]["thoughtSignature"] != "sig-1" {
		t.Errorf("thought part = %v; thought:true alone does not restore reasoning state", ps[0])
	}
	if ps[0]["text"] != "weighing" {
		t.Errorf("thought part = %v", ps[0])
	}
	if _, ok := ps[1]["thought"]; ok {
		t.Errorf("plain text part = %v", ps[1])
	}
}

func TestRenderContentsMatchesParallelCallsPositionally(t *testing.T) {
	got, _ := contents(t, &ir.Request{Messages: []ir.Message{
		{Role: ir.RoleAssistant, Content: []ir.ContentBlock{
			{Type: ir.BlockToolUse, ToolUse: &ir.ToolUse{
				ID: "call_a", Name: "lookup", Input: json.RawMessage(`{"city":"Oslo"}`)}},
			{Type: ir.BlockToolUse, ToolUse: &ir.ToolUse{
				ID: "call_b", Name: "lookup", Input: json.RawMessage(`{"city":"Bergen"}`)}},
		}},
		{Role: ir.RoleTool, Content: []ir.ContentBlock{
			{Type: ir.BlockToolResult, ToolResult: &ir.ToolResult{
				ToolUseID: "call_b", Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "rain"}}}},
			{Type: ir.BlockToolResult, ToolResult: &ir.ToolResult{
				ToolUseID: "call_a", Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "clear"}}}},
		}},
	}})
	calls := parts(t, got[0])
	if calls[0]["functionCall"].(map[string]any)["name"] != "lookup" {
		t.Fatalf("functionCall = %v", calls[0])
	}
	resps := parts(t, got[1])
	first := resps[0]["functionResponse"].(map[string]any)
	if first["name"] != "lookup" {
		t.Errorf("functionResponse = %v", first)
	}
	if first["id"] != "call_b" {
		t.Errorf("functionResponse id = %v; an id the IR knows is preserved", first["id"])
	}
}

func TestRenderContentsWrapsToolResultText(t *testing.T) {
	got, _ := contents(t, &ir.Request{Messages: []ir.Message{
		{Role: ir.RoleAssistant, Content: []ir.ContentBlock{
			{Type: ir.BlockToolUse, ToolUse: &ir.ToolUse{ID: "call_a", Name: "f"}}}},
		{Role: ir.RoleTool, Content: []ir.ContentBlock{
			{Type: ir.BlockToolResult, ToolResult: &ir.ToolResult{
				ToolUseID: "call_a", Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "17C"}}}}}},
	}})
	fr := parts(t, got[1])[0]["functionResponse"].(map[string]any)
	resp := fr["response"].(map[string]any)
	if resp["result"] != "17C" {
		t.Errorf("response = %v; a struct is required, so prose is wrapped", resp)
	}
}

func TestRenderContentsKeepsAJSONToolResultAsAStruct(t *testing.T) {
	got, _ := contents(t, &ir.Request{Messages: []ir.Message{
		{Role: ir.RoleAssistant, Content: []ir.ContentBlock{
			{Type: ir.BlockToolUse, ToolUse: &ir.ToolUse{ID: "call_a", Name: "f"}}}},
		{Role: ir.RoleTool, Content: []ir.ContentBlock{
			{Type: ir.BlockToolResult, ToolResult: &ir.ToolResult{
				ToolUseID: "call_a",
				Content:   []ir.ContentBlock{{Type: ir.BlockText, Text: `{"tempC":17}`}}}}}},
	}})
	resp := parts(t, got[1])[0]["functionResponse"].(map[string]any)["response"].(map[string]any)
	if resp["tempC"].(float64) != 17 {
		t.Errorf("response = %v; an object result is passed through rather than re-wrapped", resp)
	}
}

func TestRenderContentsHoistsAToolResultImage(t *testing.T) {
	got, warns := contents(t, &ir.Request{Messages: []ir.Message{
		{Role: ir.RoleAssistant, Content: []ir.ContentBlock{
			{Type: ir.BlockToolUse, ToolUse: &ir.ToolUse{ID: "call_a", Name: "screenshot"}}}},
		{Role: ir.RoleTool, Content: []ir.ContentBlock{
			{Type: ir.BlockToolResult, ToolResult: &ir.ToolResult{
				ToolUseID: "call_a",
				Content: []ir.ContentBlock{
					{Type: ir.BlockText, Text: "captured"},
					{Type: ir.BlockImage, Media: &ir.Media{MIME: "image/png", Data: "AAAA"}},
				}}}}},
	}})
	ps := parts(t, got[1])
	if len(ps) != 2 {
		t.Fatalf("parts = %v; the image is preserved alongside the response", ps)
	}
	if _, ok := ps[1]["inlineData"]; !ok {
		t.Errorf("hoisted part = %v", ps[1])
	}
	if !hasWarning(warns, "messages[].tool_result.image") {
		t.Errorf("warnings = %+v", warns)
	}
}

func TestRenderContentsDropsRedactedThinkingAndCacheControl(t *testing.T) {
	_, warns := contents(t, &ir.Request{Messages: []ir.Message{
		{Role: ir.RoleAssistant, Content: []ir.ContentBlock{
			{Type: ir.BlockRedactedThinking, Thinking: &ir.Thinking{Data: "enc"}},
		}},
		{Role: ir.RoleUser, Content: []ir.ContentBlock{{
			Type: ir.BlockText, Text: "long",
			CacheControl: &ir.CacheControl{Type: "ephemeral", TTL: "1h"},
		}}},
	}})
	if !hasWarning(warns, "messages[].redacted_thinking") || !hasWarning(warns, "cache_control") {
		t.Errorf("warnings = %+v", warns)
	}
}

func TestRenderContentsMergesConsecutiveSameRoleTurns(t *testing.T) {
	got, _ := contents(t, &ir.Request{Messages: []ir.Message{
		{Role: ir.RoleUser, Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "one"}}},
		{Role: ir.RoleUser, Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "two"}}},
	}})
	if len(got) != 1 || len(parts(t, got[0])) != 2 {
		t.Fatalf("contents = %v", got)
	}
}
