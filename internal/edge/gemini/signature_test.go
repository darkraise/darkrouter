package gemini

import (
	"encoding/json"
	"testing"

	"github.com/darkraise/darkrouter/internal/ir"
)

const seg = "gemini-2.5-flash:generateContent"

func TestParseRequestReadsBuiltInTools(t *testing.T) {
	req := parsed(t, seg, "", `{"contents":[],"tools":[
		{"functionDeclarations":[{"name":"lookup","parameters":{"type":"object"}}]},
		{"googleSearch":{}},
		{"codeExecution":{},"urlContext":{}}]}`)
	if len(req.Tools) != 4 || req.Tools[0].Name != "lookup" || req.Tools[0].BuiltIn() {
		t.Fatalf("tools = %+v", req.Tools)
	}
	for i, key := range []string{"googleSearch", "codeExecution", "urlContext"} {
		tool := req.Tools[i+1]
		if !tool.BuiltIn() || string(tool.Extra[key]) != "{}" {
			t.Errorf("tools[%d] = %+v, want built-in %s", i+1, tool, key)
		}
	}
}

func TestParseRequestReadsFormatsCacheAndThinkingControls(t *testing.T) {
	obj := parsed(t, seg, "", `{"contents":[],"cachedContent":"cachedContents/abc",
		"generationConfig":{"responseMimeType":"application/json","thinkingConfig":{"thinkingBudget":0}}}`)
	if obj.ResponseFormat == nil || obj.ResponseFormat.Type != "json_object" {
		t.Fatalf("response_format = %+v", obj.ResponseFormat)
	}
	if obj.Reasoning == nil || !obj.Reasoning.Disabled {
		t.Fatalf("reasoning = %+v; thinkingBudget 0 is an explicit off", obj.Reasoning)
	}
	if obj.Metadata["gemini_cached_content"] != "cachedContents/abc" {
		t.Fatalf("metadata = %v", obj.Metadata)
	}

	schema := parsed(t, seg, "", `{"contents":[],"generationConfig":{
		"responseMimeType":"application/json","responseJsonSchema":{"type":"object"},
		"thinkingConfig":{"thinkingLevel":"HIGH"}}}`)
	if schema.ResponseFormat == nil || schema.ResponseFormat.Type != "json_schema" ||
		string(schema.ResponseFormat.Schema) != `{"type":"object"}` {
		t.Fatalf("response_format = %+v", schema.ResponseFormat)
	}
	if schema.Reasoning == nil || schema.Reasoning.Effort != "high" {
		t.Fatalf("reasoning = %+v", schema.Reasoning)
	}

	plain := parsed(t, seg, "", `{"contents":[],"generationConfig":{"thinkingConfig":{"includeThoughts":true}}}`)
	if plain.Reasoning != nil || plain.ResponseFormat != nil || plain.Metadata != nil {
		t.Fatalf("request = %+v; nothing was asked for", plain)
	}
}

func TestParseRequestCarriesSignaturesAndMatchesResultsByID(t *testing.T) {
	req := parsed(t, seg, "", `{"contents":[
		{"role":"user","parts":[{"text":"weather?"}]},
		{"role":"model","parts":[
			{"functionCall":{"id":"call_a","name":"lookup","args":{"city":"Oslo"}},"thoughtSignature":"sig-a"},
			{"functionCall":{"name":"lookup","args":{"city":"Bergen"}}},
			{"text":"","thoughtSignature":"sig-t"}]},
		{"role":"user","parts":[
			{"functionResponse":{"name":"lookup","response":{"result":"rain"}}},
			{"functionResponse":{"id":"call_a","name":"lookup","response":{"result":"clear"}}}]}]}`)
	model := req.Messages[1].Content
	if model[0].ToolUse.Signature != "sig-a" || model[1].ToolUse.Signature != "" {
		t.Fatalf("calls = %+v", model)
	}
	if model[2].Type != ir.BlockText || model[2].ExtraString(ir.ExtraThoughtSignature) != "sig-t" {
		t.Fatalf("sealed text = %+v", model[2])
	}
	results := req.Messages[2].Content
	if results[0].ToolResult.ToolUseID != model[1].ToolUse.ID {
		t.Fatalf("positional result = %+v, want %s", results[0].ToolResult, model[1].ToolUse.ID)
	}
	if results[1].ToolResult.ToolUseID != "call_a" {
		t.Fatalf("named result = %+v", results[1].ToolResult)
	}
}

func TestWriteResponseCarriesSignaturesOnCallsAndText(t *testing.T) {
	sealed := ir.ContentBlock{Type: ir.BlockText, Text: "No."}
	sealed.SetExtraString(ir.ExtraThoughtSignature, "sig-t")
	body := written(t, &ir.Response{Content: []ir.ContentBlock{
		{Type: ir.BlockToolUse, ToolUse: &ir.ToolUse{Name: "lookup", Input: json.RawMessage(`{}`), Signature: "sig-fc"}},
		sealed,
	}})
	parts := body["candidates"].([]any)[0].(map[string]any)["content"].(map[string]any)["parts"].([]any)
	if parts[0].(map[string]any)["thoughtSignature"] != "sig-fc" {
		t.Fatalf("call part = %v", parts[0])
	}
	if p := parts[1].(map[string]any); p["text"] != "No." || p["thoughtSignature"] != "sig-t" {
		t.Fatalf("text part = %v", p)
	}
}

func TestWriteStreamCarriesSignaturesOnCallsAndText(t *testing.T) {
	got := sseChunks(t, []ir.StreamEvent{
		{Type: ir.EventMessageStart, ID: "r", Model: "m"},
		{Type: ir.EventBlockStart, Index: 0, Delta: &ir.Delta{Type: ir.BlockToolUse, ToolName: "lookup"}},
		{Type: ir.EventContentDelta, Index: 0, Delta: &ir.Delta{Type: ir.BlockToolUse, ToolInput: `{}`, Signature: "sig-fc"}},
		{Type: ir.EventBlockStop, Index: 0},
		{Type: ir.EventContentDelta, Index: 1, Delta: &ir.Delta{Type: ir.BlockText, Text: "No.", Signature: "sig-t"}},
		{Type: ir.EventMessageStop, StopReason: ir.StopToolUse},
	}, nil)
	var sawCall, sawText bool
	for _, c := range got {
		cands, _ := c["candidates"].([]any)
		if len(cands) == 0 {
			continue
		}
		parts, _ := cands[0].(map[string]any)["content"].(map[string]any)["parts"].([]any)
		for _, p := range parts {
			m := p.(map[string]any)
			if _, ok := m["functionCall"]; ok && m["thoughtSignature"] == "sig-fc" {
				sawCall = true
			}
			if m["text"] == "No." && m["thoughtSignature"] == "sig-t" {
				sawText = true
			}
		}
	}
	if !sawCall || !sawText {
		t.Fatalf("chunks = %v", got)
	}
}
