package openaicompat

import (
	"context"
	"encoding/json"
	"io"
	"testing"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/ir"
)

func build(t *testing.T, req *ir.Request) map[string]any {
	t.Helper()
	tgt := &adapter.Target{BaseURL: "https://up.example/v1", APIKey: "sk-x", Model: "up-model"}
	hr, _, err := BuildRequest(context.Background(), tgt, req)
	if err != nil {
		t.Fatal(err)
	}
	if hr.URL.String() != "https://up.example/v1/chat/completions" {
		t.Fatalf("url = %s", hr.URL)
	}
	if hr.Header.Get("Authorization") != "Bearer sk-x" {
		t.Fatalf("auth = %q", hr.Header.Get("Authorization"))
	}
	body, _ := io.ReadAll(hr.Body)
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestBuildUsesTargetModelNotRequestModel(t *testing.T) {
	got := build(t, &ir.Request{Model: "alias-name"})
	if got["model"] != "up-model" {
		t.Fatalf("model = %v", got["model"])
	}
}

func TestBuildInjectsStreamOptionsOnStreamingRequests(t *testing.T) {
	// Without this, OpenAI-compatible providers emit no usage on streams and
	// Phase 2's accounting is blind on the dominant path.
	got := build(t, &ir.Request{Stream: true})
	so, ok := got["stream_options"].(map[string]any)
	if !ok || so["include_usage"] != true {
		t.Fatalf("stream_options = %v", got["stream_options"])
	}
}

func TestBuildOmitsStreamOptionsOnUnaryRequests(t *testing.T) {
	got := build(t, &ir.Request{Stream: false})
	if _, present := got["stream_options"]; present {
		t.Fatal("stream_options must not appear on a unary request")
	}
}

func TestBuildFlattensTextBlocksToStringContent(t *testing.T) {
	got := build(t, &ir.Request{Messages: []ir.Message{
		{Role: ir.RoleUser, Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "hi"}}},
	}})
	msgs := got["messages"].([]any)
	if msgs[0].(map[string]any)["content"] != "hi" {
		t.Fatalf("content = %v", msgs[0])
	}
}

func TestBuildEmitsMultiPartContentForImages(t *testing.T) {
	got := build(t, &ir.Request{Messages: []ir.Message{
		{Role: ir.RoleUser, Content: []ir.ContentBlock{
			{Type: ir.BlockText, Text: "what"},
			{Type: ir.BlockImage, Media: &ir.Media{URL: "https://x/y.png"}},
		}},
	}})
	parts := got["messages"].([]any)[0].(map[string]any)["content"].([]any)
	if len(parts) != 2 || parts[1].(map[string]any)["type"] != "image_url" {
		t.Fatalf("parts = %v", parts)
	}
}

func TestBuildEmitsTools(t *testing.T) {
	got := build(t, &ir.Request{Tools: []ir.Tool{
		{Name: "f", Description: "d", Schema: json.RawMessage(`{"type":"object"}`)},
	}})
	tools := got["tools"].([]any)
	fn := tools[0].(map[string]any)["function"].(map[string]any)
	if fn["name"] != "f" {
		t.Fatalf("tools = %v", tools)
	}
}

func TestBuildRequestReturnsNoWarningsForAPlainRequest(t *testing.T) {
	_, warns, err := BuildRequest(context.Background(),
		&adapter.Target{BaseURL: "https://x.example/v1", Model: "m"},
		&ir.Request{Messages: []ir.Message{{
			Role:    ir.RoleUser,
			Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "hi"}},
		}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 0 {
		t.Errorf("warnings = %v, want none", warns)
	}
}

func built(t *testing.T, req *ir.Request) (map[string]any, []ir.Warning) {
	t.Helper()
	hr, warns, err := BuildRequest(context.Background(),
		&adapter.Target{BaseURL: "https://x.example/v1", Model: "m"}, req)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(hr.Body)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	return out, warns
}

func TestBuildRequestRendersADocumentPart(t *testing.T) {
	got, warns := built(t, &ir.Request{Messages: []ir.Message{{
		Role: ir.RoleUser,
		Content: []ir.ContentBlock{
			{Type: ir.BlockText, Text: "summarize"},
			{Type: ir.BlockDocument, Media: &ir.Media{MIME: "application/pdf", Data: "JVBER"}},
		},
	}}})
	if len(warns) != 0 {
		t.Fatalf("warnings = %+v, want none", warns)
	}
	parts := got["messages"].([]any)[0].(map[string]any)["content"].([]any)
	file := parts[1].(map[string]any)
	if file["type"] != "file" {
		t.Fatalf("part = %v", file)
	}
	f := file["file"].(map[string]any)
	if f["filename"] != "document.pdf" {
		t.Errorf("filename = %v", f["filename"])
	}
	if f["file_data"] != "data:application/pdf;base64,JVBER" {
		t.Errorf("file_data = %v", f["file_data"])
	}
}

func TestBuildRequestRendersADocumentByFileID(t *testing.T) {
	got, _ := built(t, &ir.Request{Messages: []ir.Message{{
		Role: ir.RoleUser,
		Content: []ir.ContentBlock{
			{Type: ir.BlockText, Text: "read it"},
			{Type: ir.BlockDocument, Media: &ir.Media{MIME: "application/pdf", FileID: "file-123"}},
		},
	}}})
	parts := got["messages"].([]any)[0].(map[string]any)["content"].([]any)
	f := parts[1].(map[string]any)["file"].(map[string]any)
	if f["file_id"] != "file-123" {
		t.Errorf("file = %v", f)
	}
	if _, ok := f["file_data"]; ok {
		t.Error("a file id and inline data must not both be sent")
	}
}

func TestBuildRequestRendersInputAudio(t *testing.T) {
	got, warns := built(t, &ir.Request{Messages: []ir.Message{{
		Role: ir.RoleUser,
		Content: []ir.ContentBlock{
			{Type: ir.BlockText, Text: "transcribe"},
			{Type: ir.BlockAudio, Media: &ir.Media{MIME: "audio/wav", Data: "UklGR"}},
		},
	}}})
	if len(warns) != 0 {
		t.Fatalf("warnings = %+v", warns)
	}
	parts := got["messages"].([]any)[0].(map[string]any)["content"].([]any)
	a := parts[1].(map[string]any)
	if a["type"] != "input_audio" {
		t.Fatalf("part = %v", a)
	}
	in := a["input_audio"].(map[string]any)
	if in["format"] != "wav" || in["data"] != "UklGR" {
		t.Errorf("input_audio = %v", in)
	}
}

func TestBuildRequestWarnsOnAnUnsupportedAudioFormat(t *testing.T) {
	_, warns := built(t, &ir.Request{Messages: []ir.Message{{
		Role: ir.RoleUser,
		Content: []ir.ContentBlock{
			{Type: ir.BlockText, Text: "hi"},
			{Type: ir.BlockAudio, Media: &ir.Media{MIME: "audio/ogg", Data: "T2dn"}},
		},
	}}})
	if !hasWarning(warns, "messages[].audio") {
		t.Errorf("warnings = %+v; OpenAI accepts wav and mp3 only", warns)
	}
}

func TestBuildRequestRendersAJSONSchemaResponseFormat(t *testing.T) {
	got, warns := built(t, &ir.Request{
		Messages: []ir.Message{{Role: ir.RoleUser, Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "hi"}}}},
		ResponseFormat: &ir.ResponseFormat{
			Type:   "json_schema",
			Schema: json.RawMessage(`{"type":"object","properties":{"a":{"type":"string"}}}`),
		},
	})
	if len(warns) != 0 {
		t.Fatalf("warnings = %+v", warns)
	}
	rf := got["response_format"].(map[string]any)
	if rf["type"] != "json_schema" {
		t.Fatalf("response_format = %v", rf)
	}
	js := rf["json_schema"].(map[string]any)
	if js["name"] != "response" {
		t.Errorf("json_schema = %v; OpenAI requires a name", js)
	}
	if _, ok := js["schema"]; !ok {
		t.Errorf("json_schema = %v; the schema must be nested under `schema`", js)
	}
}

func TestBuildRequestBandsAReasoningBudgetToAnEffort(t *testing.T) {
	got, warns := built(t, &ir.Request{
		Messages:  []ir.Message{{Role: ir.RoleUser, Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "hi"}}}},
		Reasoning: &ir.Reasoning{Budget: 30000},
	})
	if got["reasoning_effort"] != "high" {
		t.Errorf("reasoning_effort = %v, want high", got["reasoning_effort"])
	}
	if !hasWarning(warns, "reasoning.budget") {
		t.Errorf("warnings = %+v; banding a budget loses precision and must say so", warns)
	}
}

func TestBuildRequestWarnsOnTopKAndSafety(t *testing.T) {
	k := 40
	_, warns := built(t, &ir.Request{
		Messages: []ir.Message{{Role: ir.RoleUser, Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "hi"}}}},
		TopK:     &k,
		Safety:   []ir.SafetySetting{{Category: "HARM_CATEGORY_HARASSMENT", Threshold: "BLOCK_NONE"}},
	})
	if !hasWarning(warns, "top_k") || !hasWarning(warns, "safety") {
		t.Errorf("warnings = %+v", warns)
	}
}

func TestBuildRequestPassesParallelToolCallsThrough(t *testing.T) {
	no := false
	got, _ := built(t, &ir.Request{
		Messages:          []ir.Message{{Role: ir.RoleUser, Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "hi"}}}},
		Tools:             []ir.Tool{{Name: "f", Schema: json.RawMessage(`{"type":"object"}`)}},
		ParallelToolCalls: &no,
	})
	if got["parallel_tool_calls"] != false {
		t.Errorf("parallel_tool_calls = %v", got["parallel_tool_calls"])
	}
}

func TestBuildRequestDoesNotLeakAnthropicTransportMetadata(t *testing.T) {
	got, _ := built(t, &ir.Request{
		Messages: []ir.Message{{Role: ir.RoleUser, Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "hi"}}}},
		Metadata: map[string]string{
			"anthropic_version":       "2023-06-01",
			"anthropic_thinking_type": "adaptive",
			"user_id":                 "u1",
		},
	})
	md, ok := got["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("metadata = %v", got["metadata"])
	}
	for _, k := range []string{"anthropic_version", "anthropic_thinking_type"} {
		if _, leaked := md[k]; leaked {
			t.Errorf("%s must not reach an OpenAI upstream as metadata", k)
		}
	}
	if md["user_id"] != "u1" {
		t.Errorf("metadata = %v", md)
	}
}

func TestBuildEmitsStrictAndNameOnlyAsAsked(t *testing.T) {
	yes, no := true, false
	schema := json.RawMessage(`{"type":"object"}`)
	strict := build(t, &ir.Request{ResponseFormat: &ir.ResponseFormat{
		Type: "json_schema", Schema: schema, Name: "answer", Strict: &yes}})
	js := strict["response_format"].(map[string]any)["json_schema"].(map[string]any)
	if js["name"] != "answer" || js["strict"] != true {
		t.Fatalf("json_schema = %v", js)
	}
	for _, rf := range []*ir.ResponseFormat{
		{Type: "json_schema", Schema: schema},
		{Type: "json_schema", Schema: schema, Strict: &no},
	} {
		js := build(t, &ir.Request{ResponseFormat: rf})["response_format"].(map[string]any)["json_schema"].(map[string]any)
		if _, present := js["strict"]; present {
			t.Errorf("strict = %v for %+v; strict mode changes which schemas are accepted", js["strict"], rf)
		}
		if js["name"] != "response" {
			t.Errorf("name = %v, want the default", js["name"])
		}
	}
}

func TestBuildEmitsJSONObjectMode(t *testing.T) {
	got := build(t, &ir.Request{ResponseFormat: &ir.ResponseFormat{Type: "json_object"}})
	rf, _ := got["response_format"].(map[string]any)
	if rf["type"] != "json_object" || len(rf) != 1 {
		t.Fatalf("response_format = %v", rf)
	}
}

func TestBuildReEmitsExtraWithoutOverridingTheIR(t *testing.T) {
	temp := 0.2
	got := build(t, &ir.Request{
		Temperature: &temp,
		Extra: map[string]json.RawMessage{
			"seed":        json.RawMessage(`7`),
			"logit_bias":  json.RawMessage(`{"50256":-100}`),
			"temperature": json.RawMessage(`0.9`),
		},
	})
	if got["seed"] != float64(7) || got["logit_bias"].(map[string]any)["50256"] != float64(-100) {
		t.Fatalf("extras = %v", got)
	}
	if got["temperature"] != 0.2 {
		t.Fatalf("temperature = %v; the IR wins over an extra with the same key", got["temperature"])
	}
}

func TestBuildRendersToolStrictAndDropsBuiltInTools(t *testing.T) {
	tgt := &adapter.Target{BaseURL: "https://up.example/v1", Model: "up-model"}
	hr, warns, err := BuildRequest(context.Background(), tgt, &ir.Request{Tools: []ir.Tool{
		{Name: "lookup", Schema: json.RawMessage(`{"type":"object"}`),
			Extra: map[string]json.RawMessage{"strict": json.RawMessage(`true`)}},
		{Extra: map[string]json.RawMessage{"googleSearch": json.RawMessage(`{}`)}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(hr.Body)
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	tools := got["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools = %v; a built-in tool cannot become a nameless function", tools)
	}
	fn := tools[0].(map[string]any)["function"].(map[string]any)
	if fn["strict"] != true || fn["name"] != "lookup" {
		t.Fatalf("function = %v", fn)
	}
	if !hasWarning(warns, "tools[].googleSearch") {
		t.Fatalf("warnings = %v", warns)
	}
}
