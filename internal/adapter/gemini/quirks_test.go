package gemini

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/adapter"
	anthropicedge "github.com/darkraise/darkrouter/internal/edge/anthropic"
	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/sse"
)

func builtFor(t *testing.T, model string, req *ir.Request) (map[string]any, []ir.Warning) {
	t.Helper()
	hr, warns, err := NewFetcher().BuildRequest(context.Background(),
		&adapter.Target{BaseURL: "https://generativelanguage.googleapis.com/v1beta", Model: model}, req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(hr.Body)
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	return body, warns
}

func thinking(body map[string]any) map[string]any {
	cfg, _ := body["generationConfig"].(map[string]any)
	tc, _ := cfg["thinkingConfig"].(map[string]any)
	return tc
}

func TestThinkingConfigClampsToTheFamilyCeiling(t *testing.T) {
	cases := []struct {
		model  string
		budget int
		want   float64
		warned bool
	}{
		{"gemini-2.5-flash", 60000, 24576, true},
		{"gemini-2.5-flash-lite", 30000, 24576, true},
		{"gemini-2.5-pro", 60000, 32768, true},
		{"gemini-2.5-pro", 8000, 8000, false},
		{"some-preview", 40000, 32768, true},
	}
	for _, tc := range cases {
		body, warns := builtFor(t, tc.model, &ir.Request{Reasoning: &ir.Reasoning{Budget: tc.budget}})
		if got := thinking(body)["thinkingBudget"]; got != tc.want {
			t.Errorf("%s budget %d: thinkingBudget = %v, want %v", tc.model, tc.budget, got, tc.want)
		}
		if (len(warns) > 0) != tc.warned {
			t.Errorf("%s budget %d: warnings = %v", tc.model, tc.budget, warns)
		}
	}
}

func TestThinkingConfigCoversTheWholeEffortVocabulary(t *testing.T) {
	want := map[string]float64{"minimal": 1024, "low": 4096, "medium": 16384, "high": 24576, "xhigh": 24576, "max": 24576}
	for effort, budget := range want {
		body, _ := builtFor(t, "gemini-2.5-flash", &ir.Request{Reasoning: &ir.Reasoning{Effort: effort}})
		if got := thinking(body)["thinkingBudget"]; got != budget {
			t.Errorf("%s: thinkingBudget = %v, want %v", effort, got, budget)
		}
	}
}

func TestThinkingConfigDisabledSendsAZeroBudget(t *testing.T) {
	body, _ := builtFor(t, "gemini-2.5-flash", &ir.Request{Reasoning: &ir.Reasoning{Disabled: true, Budget: 500}})
	tc := thinking(body)
	if tc["thinkingBudget"] != float64(0) {
		t.Fatalf("thinkingConfig = %v; an explicit off must reach the model as zero", tc)
	}
	if _, ok := tc["includeThoughts"]; ok {
		t.Fatalf("thinkingConfig = %v; nothing to include when thinking is off", tc)
	}
}

// Google's per-model ranges: 2.5 Pro takes 128 to 32768 and cannot turn
// thinking off; 2.5 Flash-Lite takes 512 to 24576 or 0. A budget outside
// the range is a 400, not a clamp.
func TestThinkingConfigHonorsTheFamilyFloor(t *testing.T) {
	cases := []struct {
		model  string
		budget int
		want   float64
		warned bool
	}{
		{"gemini-2.5-pro", 64, 128, true},
		{"gemini-2.5-flash-lite", 100, 512, true},
		{"gemini-2.5-flash-lite", 600, 600, false},
		{"gemini-2.5-flash", 64, 64, false},
	}
	for _, tc := range cases {
		body, warns := builtFor(t, tc.model, &ir.Request{Reasoning: &ir.Reasoning{Budget: tc.budget}})
		if got := thinking(body)["thinkingBudget"]; got != tc.want {
			t.Errorf("%s budget %d: thinkingBudget = %v, want %v", tc.model, tc.budget, got, tc.want)
		}
		if (len(warns) > 0) != tc.warned {
			t.Errorf("%s budget %d: warnings = %v", tc.model, tc.budget, warns)
		}
	}
}

func TestThinkingConfigDisabledOnAModelThatAlwaysThinks(t *testing.T) {
	body, warns := builtFor(t, "gemini-2.5-pro", &ir.Request{Reasoning: &ir.Reasoning{Disabled: true}})
	if tc := thinking(body); tc["thinkingBudget"] != float64(128) {
		t.Errorf("thinkingConfig = %v; 2.5 Pro rejects zero, and its floor is the nearest to off", tc)
	}
	if !hasWarning(warns, "reasoning") {
		t.Errorf("warnings = %v; the client's off switch was not honored", warns)
	}

	body, warns = builtFor(t, "gemini-3.1-pro-preview", &ir.Request{Reasoning: &ir.Reasoning{Disabled: true}})
	if tc := thinking(body); tc["thinkingLevel"] != "low" {
		t.Errorf("thinkingConfig = %v; Gemini 3 Pro cannot turn thinking off and has no minimal level", tc)
	}
	if !hasWarning(warns, "reasoning") {
		t.Errorf("warnings = %v", warns)
	}

	body, _ = builtFor(t, "gemini-3-pro-preview", &ir.Request{Reasoning: &ir.Reasoning{Effort: "minimal"}})
	if tc := thinking(body); tc["thinkingLevel"] != "low" {
		t.Errorf("thinkingConfig = %v; minimal is an error on Gemini 3 Pro", tc)
	}
}

func TestThinkingConfigSendsALevelToGemini3(t *testing.T) {
	body, warns := builtFor(t, "gemini-3-pro-preview", &ir.Request{Reasoning: &ir.Reasoning{Effort: "xhigh"}})
	tc := thinking(body)
	if tc["thinkingLevel"] != "high" || tc["includeThoughts"] != true {
		t.Fatalf("thinkingConfig = %v", tc)
	}
	if _, ok := tc["thinkingBudget"]; ok || len(warns) != 0 {
		t.Fatalf("thinkingConfig = %v warnings = %v", tc, warns)
	}
	body, warns = builtFor(t, "gemini-3-flash", &ir.Request{Reasoning: &ir.Reasoning{Budget: 2000}})
	if tc := thinking(body); tc["thinkingLevel"] != "low" || len(warns) != 1 {
		t.Fatalf("thinkingConfig = %v warnings = %v; a budget becomes the nearest level", tc, warns)
	}
}

func TestBuildRequestSendsJSONObjectModeWithoutASchema(t *testing.T) {
	body, _ := builtFor(t, "gemini-2.5-flash", &ir.Request{ResponseFormat: &ir.ResponseFormat{Type: "json_object"}})
	cfg := body["generationConfig"].(map[string]any)
	if cfg["responseMimeType"] != "application/json" {
		t.Fatalf("generationConfig = %v", cfg)
	}
	if _, ok := cfg["responseJsonSchema"]; ok {
		t.Fatalf("generationConfig = %v; json_object carries no schema", cfg)
	}
}

// responseSchema and parameters take Gemini's OpenAPI subset, which has no
// $defs, $ref or additionalProperties. A client's JSON Schema belongs in the
// fields that accept one.
func TestBuildRequestSendsJSONSchemaThroughTheJSONSchemaFields(t *testing.T) {
	schema := json.RawMessage(`{"$defs":{"city":{"type":"string"}},"type":"object","properties":{"city":{"$ref":"#/$defs/city"}},"additionalProperties":false}`)
	body, _ := builtFor(t, "gemini-2.5-flash", &ir.Request{
		Tools:          []ir.Tool{{Name: "lookup", Schema: schema}},
		ResponseFormat: &ir.ResponseFormat{Type: "json_schema", Schema: schema},
	})
	cfg := body["generationConfig"].(map[string]any)
	if _, ok := cfg["responseSchema"]; ok {
		t.Errorf("generationConfig = %v; responseSchema is the OpenAPI subset", cfg)
	}
	if got, _ := cfg["responseJsonSchema"].(map[string]any); got["$defs"] == nil {
		t.Errorf("generationConfig = %v", cfg)
	}
	decl := body["tools"].([]any)[0].(map[string]any)["functionDeclarations"].([]any)[0].(map[string]any)
	if _, ok := decl["parameters"]; ok {
		t.Errorf("declaration = %v; parameters is the OpenAPI subset", decl)
	}
	if got, _ := decl["parametersJsonSchema"].(map[string]any); got["$defs"] == nil {
		t.Errorf("declaration = %v", decl)
	}
}

func TestBuildRequestReEmitsCachedContentFromMetadata(t *testing.T) {
	body, warns := builtFor(t, "gemini-2.5-flash", &ir.Request{
		Metadata: map[string]string{MetadataCachedContent: "cachedContents/abc"},
	})
	if body["cachedContent"] != "cachedContents/abc" {
		t.Fatalf("body = %v", body)
	}
	if len(warns) != 0 {
		t.Fatalf("warnings = %v; the cache handle is this adapter's own metadata", warns)
	}
}

func TestBuildRequestDeclaresBuiltInToolsSeparately(t *testing.T) {
	body, warns := builtFor(t, "gemini-2.5-flash", &ir.Request{Tools: []ir.Tool{
		{Name: "lookup", Schema: json.RawMessage(`{"type":"object"}`),
			Extra: map[string]json.RawMessage{"strict": json.RawMessage(`true`)}},
		{Extra: map[string]json.RawMessage{"googleSearch": json.RawMessage(`{}`)}},
		{Extra: map[string]json.RawMessage{"codeExecution": json.RawMessage(`{}`)}},
	}})
	tools := body["tools"].([]any)
	if len(tools) != 3 {
		t.Fatalf("tools = %v", tools)
	}
	if _, ok := tools[0].(map[string]any)["functionDeclarations"]; !ok {
		t.Fatalf("tools[0] = %v; declarations come first", tools[0])
	}
	if _, ok := tools[1].(map[string]any)["googleSearch"]; !ok {
		t.Fatalf("tools[1] = %v", tools[1])
	}
	if _, ok := tools[2].(map[string]any)["codeExecution"]; !ok {
		t.Fatalf("tools[2] = %v", tools[2])
	}
	if len(warns) != 1 || warns[0].Field != "tools[].strict" {
		t.Fatalf("warnings = %v", warns)
	}
	only, _ := builtFor(t, "gemini-2.5-flash", &ir.Request{Tools: []ir.Tool{
		{Extra: map[string]json.RawMessage{"urlContext": json.RawMessage(`{}`)}},
	}})
	if tools := only["tools"].([]any); len(tools) != 1 {
		t.Fatalf("tools = %v; no empty functionDeclarations entry", tools)
	}
}

func TestBuildRequestDropsANamelessTypedTool(t *testing.T) {
	body, warns := builtFor(t, "gemini-2.5-flash", &ir.Request{Tools: []ir.Tool{
		{Extra: map[string]json.RawMessage{
			"type": json.RawMessage(`"mcp_toolset"`), "mcp_server_name": json.RawMessage(`"srv"`)}},
	}})
	if tools, ok := body["tools"]; ok {
		t.Errorf("tools = %v; an Anthropic typed tool is not a Gemini built-in", tools)
	}
	if len(warns) != 1 || warns[0].Field != "tools[].type" {
		t.Errorf("warnings = %v", warns)
	}
}

func TestRenderContentsCarriesFunctionCallSignaturesAndMatchesResultsByID(t *testing.T) {
	body, _ := builtFor(t, "gemini-2.5-flash", &ir.Request{Messages: []ir.Message{
		{Role: ir.RoleUser, Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "weather in Oslo and Bergen?"}}},
		{Role: ir.RoleAssistant, Content: []ir.ContentBlock{
			{Type: ir.BlockToolUse, ToolUse: &ir.ToolUse{ID: "call_a", Name: "lookup",
				Input: json.RawMessage(`{"city":"Oslo"}`), Signature: "sig-a"}},
			{Type: ir.BlockToolUse, ToolUse: &ir.ToolUse{ID: "call_b", Name: "forecast",
				Input: json.RawMessage(`{"city":"Bergen"}`)}},
		}},
		{Role: ir.RoleTool, Content: []ir.ContentBlock{
			{Type: ir.BlockToolResult, ToolResult: &ir.ToolResult{ToolUseID: "call_b",
				Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "rain"}}}},
			{Type: ir.BlockToolResult, ToolResult: &ir.ToolResult{ToolUseID: "call_a",
				Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "clear"}}}},
		}},
	}})
	contents := body["contents"].([]any)
	model := contents[1].(map[string]any)["parts"].([]any)
	if model[0].(map[string]any)["thoughtSignature"] != "sig-a" {
		t.Fatalf("functionCall part = %v", model[0])
	}
	if _, ok := model[1].(map[string]any)["thoughtSignature"]; ok {
		t.Fatalf("functionCall part = %v; no signature was issued", model[1])
	}
	results := contents[2].(map[string]any)["parts"].([]any)
	first := results[0].(map[string]any)["functionResponse"].(map[string]any)
	second := results[1].(map[string]any)["functionResponse"].(map[string]any)
	if first["name"] != "forecast" || second["name"] != "lookup" {
		t.Fatalf("results out of order got names %v, %v", first["name"], second["name"])
	}
}

func TestRenderContentsKeepsASignatureOnAPlainTextPart(t *testing.T) {
	sealed := ir.ContentBlock{Type: ir.BlockText, Text: "No."}
	sealed.SetExtraString(ir.ExtraThoughtSignature, "sig-t")
	body, _ := builtFor(t, "gemini-2.5-flash", &ir.Request{Messages: []ir.Message{
		{Role: ir.RoleUser, Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "Is 91 prime?"}}},
		{Role: ir.RoleAssistant, Content: []ir.ContentBlock{sealed}},
		{Role: ir.RoleUser, Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "Factors?"}}},
	}})
	model := body["contents"].([]any)[1].(map[string]any)["parts"].([]any)[0].(map[string]any)
	if model["text"] != "No." || model["thoughtSignature"] != "sig-t" {
		t.Fatalf("text part = %v", model)
	}
}

func TestParseResponseCarriesSignaturesOnCallsAndText(t *testing.T) {
	resp, err := parseBody(t, `{"candidates":[{"content":{"parts":[
	  {"text":"thinking","thought":true,"thoughtSignature":"sig-th"},
	  {"functionCall":{"name":"lookup","args":{"city":"Oslo"}},"thoughtSignature":"sig-fc"},
	  {"text":"done","thoughtSignature":"sig-tx"}]},"finishReason":"STOP"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	c := resp.Content
	if len(c) != 3 || c[0].Thinking.Signature != "sig-th" || c[0].Thinking.Text != "thinking" {
		t.Fatalf("content = %+v", c)
	}
	if c[1].ToolUse == nil || c[1].ToolUse.Signature != "sig-fc" {
		t.Fatalf("call = %+v", c[1])
	}
	if c[2].Text != "done" || c[2].ExtraString(ir.ExtraThoughtSignature) != "sig-tx" {
		t.Fatalf("text = %+v", c[2])
	}
	if resp.StopReason != ir.StopToolUse {
		t.Fatalf("stop = %s", resp.StopReason)
	}
}

func TestParseResponseReportsAnErrorBody(t *testing.T) {
	_, err := parseBody(t, `{"error":{"code":429,"message":"quota","status":"RESOURCE_EXHAUSTED"}}`)
	var e *ir.Error
	if !errors.As(err, &e) || e.Type != ir.ErrRateLimit || e.Code != "RESOURCE_EXHAUSTED" {
		t.Fatalf("err = %v", err)
	}
}

func TestParseStreamRemembersACallAcrossChunks(t *testing.T) {
	body := data(`{"candidates":[{"content":{"parts":[{"functionCall":{"name":"lookup","args":{}},"thoughtSignature":"sig-fc"}]}}]}`) +
		data(`{"candidates":[{"content":{"parts":[]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1,"totalTokenCount":2}}`)
	evs, err := collect(t, body)
	if err != nil {
		t.Fatal(err)
	}
	var stop ir.StopReason
	var sig string
	for _, ev := range evs {
		if ev.Type == ir.EventMessageStop {
			stop = ev.StopReason
		}
		if ev.Type == ir.EventContentDelta && ev.Delta.Type == ir.BlockToolUse {
			sig = ev.Delta.Signature
		}
	}
	if stop != ir.StopToolUse {
		t.Fatalf("stop = %s; the call was in an earlier chunk than the finish reason", stop)
	}
	if sig != "sig-fc" {
		t.Fatalf("signature = %q", sig)
	}
}

// Anthropic's event model has no delta carrying thinking text and a signature
// together, so its writer keeps only one of the two from a combined delta.
// Gemini puts both on one part; the parser splits them the way Anthropic
// streams them, text first.
func TestParseStreamKeepsThoughtTextNextToItsSignature(t *testing.T) {
	body := data(`{"candidates":[{"content":{"parts":[{"text":"weighing","thought":true,"thoughtSignature":"sig-1"},{"text":"No.","thoughtSignature":"sig-2"}]},"finishReason":"STOP"}]}`)
	evs, err := collect(t, body)
	if err != nil {
		t.Fatal(err)
	}
	var thoughts []ir.Delta
	var text ir.Delta
	for _, ev := range evs {
		if ev.Type != ir.EventContentDelta {
			continue
		}
		switch ev.Delta.Type {
		case ir.BlockThinking:
			thoughts = append(thoughts, *ev.Delta)
		case ir.BlockText:
			text = *ev.Delta
		}
	}
	if len(thoughts) != 2 || thoughts[0].Thinking != "weighing" || thoughts[0].Signature != "" ||
		thoughts[1].Thinking != "" || thoughts[1].Signature != "sig-1" {
		t.Fatalf("thought deltas = %+v; want the text, then the signature alone", thoughts)
	}
	if text.Text != "No." || text.Signature != "sig-2" {
		t.Fatalf("text delta = %+v", text)
	}

	rec := httptest.NewRecorder()
	if err := anthropicedge.WriteStream(rec, ParseStream(strings.NewReader(body), 1<<20)); err != nil {
		t.Fatal(err)
	}
	out := rec.Body.String()
	if !strings.Contains(out, `"thinking":"weighing"`) || !strings.Contains(out, `"signature":"sig-1"`) {
		t.Errorf("an Anthropic client lost the thought text or its signature:\n%s", out)
	}
}

func TestParseStreamSurfacesAnErrorChunk(t *testing.T) {
	body := data(`{"candidates":[{"content":{"parts":[{"text":"partial"}]}}]}`) +
		data(`{"error":{"code":429,"message":"Resource has been exhausted","status":"RESOURCE_EXHAUSTED"}}`)
	var got *ir.Error
	for _, err := range ParseStream(strings.NewReader(body), 1<<16) {
		if err != nil {
			got, _ = err.(*ir.Error)
		}
	}
	if got == nil || got.Type != ir.ErrRateLimit {
		t.Fatalf("err = %+v", got)
	}
	unstatused := data(`{"error":{"code":503,"message":"try later"}}`)
	for _, err := range ParseStream(strings.NewReader(unstatused), 1<<16) {
		if err != nil {
			got, _ = err.(*ir.Error)
		}
	}
	if got == nil || got.Type != ir.ErrOverloaded || got.Code != "503" {
		t.Fatalf("err = %+v", got)
	}
}

func TestRecognizeEventReportsAnErrorChunk(t *testing.T) {
	re := New().RecognizeEvent(sse.Event{Data: `{"error":{"code":429,"message":"quota","status":"RESOURCE_EXHAUSTED"}}`})
	if re.ErrPayload == "" || re.Content {
		t.Fatalf("raw event = %+v", re)
	}
	if re := New().RecognizeEvent(sse.Event{Data: `{"error":null,"candidates":[{"content":{"parts":[{"text":"x"}]}}]}`}); re.ErrPayload != "" || !re.Content {
		t.Fatalf("raw event = %+v; a null error is no error", re)
	}
}

func TestBuildCountRequestWrapsTheWholeRequest(t *testing.T) {
	hr, err := New().BuildCountRequest(context.Background(),
		&adapter.Target{BaseURL: "https://generativelanguage.googleapis.com/v1beta", Model: "gemini-2.5-flash"},
		&ir.Request{
			Stream:   true,
			System:   []ir.ContentBlock{{Type: ir.BlockText, Text: "be brief"}},
			Messages: []ir.Message{userMsg("hi")},
			Tools:    []ir.Tool{{Name: "lookup", Schema: json.RawMessage(`{"type":"object"}`)}},
		})
	if err != nil {
		t.Fatal(err)
	}
	if got := hr.URL.String(); !strings.HasSuffix(got, "/models/gemini-2.5-flash:countTokens") || strings.Contains(got, "alt=sse") {
		t.Fatalf("url = %s", got)
	}
	raw, _ := io.ReadAll(hr.Body)
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if len(body) != 1 {
		t.Fatalf("body = %v; countTokens takes contents or generateContentRequest and nothing beside", body)
	}
	inner := body["generateContentRequest"].(map[string]any)
	if inner["model"] != "models/gemini-2.5-flash" {
		t.Fatalf("generateContentRequest.model = %v", inner["model"])
	}
	for _, k := range []string{"contents", "systemInstruction", "tools"} {
		if _, ok := inner[k]; !ok {
			t.Errorf("generateContentRequest lacks %s: %v", k, inner)
		}
	}
}

func TestRenderContentsPositionalResultSkipsAClaimedCall(t *testing.T) {
	body, _ := builtFor(t, "gemini-2.5-flash", &ir.Request{Messages: []ir.Message{
		{Role: ir.RoleUser, Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "?"}}},
		{Role: ir.RoleAssistant, Content: []ir.ContentBlock{
			{Type: ir.BlockToolUse, ToolUse: &ir.ToolUse{ID: "call_a", Name: "lookup", Input: json.RawMessage(`{}`)}},
			{Type: ir.BlockToolUse, ToolUse: &ir.ToolUse{ID: "call_b", Name: "forecast", Input: json.RawMessage(`{}`)}},
		}},
		{Role: ir.RoleTool, Content: []ir.ContentBlock{
			{Type: ir.BlockToolResult, ToolResult: &ir.ToolResult{ToolUseID: "unknown",
				Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "rain"}}}},
			{Type: ir.BlockToolResult, ToolResult: &ir.ToolResult{ToolUseID: "call_a",
				Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "clear"}}}},
		}},
	}})
	results := body["contents"].([]any)[2].(map[string]any)["parts"].([]any)
	first := results[0].(map[string]any)["functionResponse"].(map[string]any)
	if first["name"] != "forecast" {
		t.Fatalf("unmatched result took %v; call_a was claimed by name", first["name"])
	}
}

func TestBuildRequestDropsTypedServerTools(t *testing.T) {
	body, warns := builtFor(t, "gemini-2.5-flash", &ir.Request{Tools: []ir.Tool{
		{Name: "web_search", Extra: map[string]json.RawMessage{"type": json.RawMessage(`"web_search_20250305"`)}},
	}})
	if _, ok := body["tools"]; ok {
		t.Fatalf("tools = %v; a server tool must not become a function declaration", body["tools"])
	}
	if len(warns) != 1 || warns[0].Field != "tools[].type" {
		t.Fatalf("warnings = %v", warns)
	}
}
