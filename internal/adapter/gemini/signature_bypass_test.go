package gemini

import (
	"encoding/json"
	"testing"

	"github.com/darkraise/darkrouter/internal/ir"
)

// Gemini 3 rejects a request whose current turn has a function call step
// without its thought signature, at every thinking level. A history from
// another provider has none, so the first call of each such step carries the
// validator bypass Google documents; calls Gemini signed keep theirs.
func TestBuildRequestSignsUnsignedCallsInTheCurrentTurnForGemini3(t *testing.T) {
	call := func(id, sig string) ir.ContentBlock {
		return ir.ContentBlock{Type: ir.BlockToolUse, ToolUse: &ir.ToolUse{
			ID: id, Name: "f", Input: json.RawMessage(`{}`), Signature: sig,
		}}
	}
	result := func(id string) ir.Message {
		return ir.Message{Role: ir.RoleTool, Content: []ir.ContentBlock{{
			Type: ir.BlockToolResult, ToolResult: &ir.ToolResult{ToolUseID: id,
				Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "ok"}}},
		}}}
	}
	req := &ir.Request{Messages: []ir.Message{
		userMsg("earlier"),
		{Role: ir.RoleAssistant, Content: []ir.ContentBlock{call("old", "")}},
		result("old"),
		userMsg("now"),
		{Role: ir.RoleAssistant, Content: []ir.ContentBlock{call("a", ""), call("b", "")}},
		result("a"), result("b"),
		{Role: ir.RoleAssistant, Content: []ir.ContentBlock{call("c", "real-sig")}},
		result("c"),
		{Role: ir.RoleAssistant, Content: []ir.ContentBlock{
			{Type: ir.BlockText, Text: "checking"}, call("d", ""),
		}},
		result("d"),
	}}

	sigs := func(body map[string]any) map[string]any {
		out := map[string]any{}
		for _, c := range body["contents"].([]any) {
			for _, p := range c.(map[string]any)["parts"].([]any) {
				part := p.(map[string]any)
				if fc, ok := part["functionCall"].(map[string]any); ok {
					out[fc["id"].(string)] = part["thoughtSignature"]
				}
			}
		}
		return out
	}

	body, warns := builtFor(t, "gemini-3-flash-preview", req)
	got := sigs(body)
	want := map[string]any{
		"old": nil,
		"a":   "skip_thought_signature_validator",
		"b":   nil,
		"c":   "real-sig",
		"d":   "skip_thought_signature_validator",
	}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("call %s thoughtSignature = %v, want %v", id, got[id], w)
		}
	}
	if !hasWarning(warns, "messages[].tool_calls") {
		t.Errorf("warnings = %v; the bypass degrades the model and the client is told", warns)
	}

	body, warns = builtFor(t, "gemini-2.5-flash", req)
	for id, sig := range sigs(body) {
		if id != "c" && sig != nil {
			t.Errorf("gemini-2.5-flash call %s thoughtSignature = %v; only Gemini 3 validates", id, sig)
		}
	}
	if hasWarning(warns, "messages[].tool_calls") {
		t.Errorf("warnings = %v", warns)
	}
}
