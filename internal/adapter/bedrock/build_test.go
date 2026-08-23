package bedrock

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/ir"
)

func build(t *testing.T, tgt *adapter.Target, req *ir.Request) (map[string]any, string, []ir.Warning) {
	t.Helper()
	hr, warns, err := New().BuildRequest(context.Background(), tgt, req)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(hr.Body)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("body is not JSON: %v\n%s", err, raw)
	}
	return body, hr.URL.String(), warns
}

func simple() *ir.Request {
	return &ir.Request{
		Model:    "anthropic.claude-3-5-sonnet-20241022-v2:0",
		Messages: []ir.Message{{Role: ir.RoleUser, Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "hi"}}}},
	}
}

func TestEndpointComesFromTheRegion(t *testing.T) {
	// The preset declares base_url: "" because there is no single host.
	_, url, _ := build(t, &adapter.Target{Region: "eu-west-1", Model: simple().Model}, simple())
	want := "https://bedrock-runtime.eu-west-1.amazonaws.com/model/" +
		"anthropic.claude-3-5-sonnet-20241022-v2%3A0/converse"
	if url != want {
		t.Errorf("url = %s\nwant %s", url, want)
	}
}

func TestAnExplicitBaseURLWins(t *testing.T) {
	// A VPC endpoint, or a test server.
	_, url, _ := build(t, &adapter.Target{
		BaseURL: "https://vpce-x.bedrock-runtime.eu-west-1.vpce.amazonaws.com",
		Region:  "eu-west-1", Model: "m",
	}, simple())
	if url != "https://vpce-x.bedrock-runtime.eu-west-1.vpce.amazonaws.com/model/m/converse" {
		t.Errorf("url = %s", url)
	}
}

func TestNeitherBaseURLNorRegionIsAnError(t *testing.T) {
	if _, _, err := New().BuildRequest(context.Background(),
		&adapter.Target{Model: "m"}, simple()); err == nil {
		t.Fatal("a bedrock target with no endpoint must be refused")
	}
}

func TestStreamingUsesTheStreamRoute(t *testing.T) {
	req := simple()
	req.Stream = true
	_, url, _ := build(t, &adapter.Target{Region: "us-east-1", Model: req.Model}, req)
	if !strings.HasSuffix(url, "/converse-stream") {
		t.Errorf("streaming url = %s", url)
	}
}

func TestModelIdIsPathEscaped(t *testing.T) {
	// The colon in a model id is part of the canonical URI the signature
	// covers. Task 3's known-answer vector was generated against %3A.
	_, url, _ := build(t, &adapter.Target{Region: "us-east-1", Model: "us.anthropic.claude-x-v1:0"}, simple())
	if !strings.Contains(url, "us.anthropic.claude-x-v1%3A0") {
		t.Errorf("url = %s; the model id is not escaped", url)
	}
}

func TestSystemBecomesItsOwnField(t *testing.T) {
	req := simple()
	req.System = []ir.ContentBlock{{Type: ir.BlockText, Text: "be terse"}}
	body, _, _ := build(t, &adapter.Target{Region: "us-east-1", Model: req.Model}, req)

	sys, ok := body["system"].([]any)
	if !ok || len(sys) != 1 {
		t.Fatalf("system = %#v", body["system"])
	}
	if sys[0].(map[string]any)["text"] != "be terse" {
		t.Errorf("system block = %#v", sys[0])
	}
	// Converse takes system separately; a system turn folded into messages is
	// a 400 from the API, not a degraded answer.
	msgs := body["messages"].([]any)
	if len(msgs) != 1 {
		t.Errorf("system leaked into messages: %#v", msgs)
	}
}

func TestInferenceConfigCarriesSampling(t *testing.T) {
	req := simple()
	max, temp, topP := 256, 0.5, 0.9
	req.MaxTokens, req.Temperature, req.TopP = &max, &temp, &topP
	req.StopSequences = []string{"END"}
	body, _, _ := build(t, &adapter.Target{Region: "us-east-1", Model: req.Model}, req)

	cfg, ok := body["inferenceConfig"].(map[string]any)
	if !ok {
		t.Fatalf("inferenceConfig = %#v", body["inferenceConfig"])
	}
	if cfg["maxTokens"] != float64(256) || cfg["temperature"] != 0.5 || cfg["topP"] != 0.9 {
		t.Errorf("inferenceConfig = %#v", cfg)
	}
	if seqs, _ := cfg["stopSequences"].([]any); len(seqs) != 1 || seqs[0] != "END" {
		t.Errorf("stopSequences = %#v", cfg["stopSequences"])
	}
}

func TestTopKIsWarnedNotDropped(t *testing.T) {
	// Converse has no topK in inferenceConfig; it belongs in
	// additionalModelRequestFields, which is per-family. Master design §5
	// requires the loss to be recorded rather than silent.
	req := simple()
	k := 40
	req.TopK = &k
	_, _, warns := build(t, &adapter.Target{Region: "us-east-1", Model: req.Model}, req)
	found := false
	for _, w := range warns {
		if w.Field == "top_k" {
			found = true
		}
	}
	if !found {
		t.Errorf("no warning named top_k: %+v", warns)
	}
}

func TestToolsBecomeAToolConfig(t *testing.T) {
	req := simple()
	req.Tools = []ir.Tool{{
		Name: "get_weather", Description: "look it up",
		Schema: json.RawMessage(`{"type":"object","properties":{}}`),
	}}
	req.ToolChoice = &ir.ToolChoice{Mode: "tool", Name: "get_weather"}
	body, _, _ := build(t, &adapter.Target{Region: "us-east-1", Model: req.Model}, req)

	tc, ok := body["toolConfig"].(map[string]any)
	if !ok {
		t.Fatalf("toolConfig = %#v", body["toolConfig"])
	}
	tools := tc["tools"].([]any)
	spec := tools[0].(map[string]any)["toolSpec"].(map[string]any)
	if spec["name"] != "get_weather" {
		t.Errorf("toolSpec = %#v", spec)
	}
	// inputSchema wraps the JSON Schema in a json key. Sending the schema bare
	// is a validation error from the API.
	if _, ok := spec["inputSchema"].(map[string]any)["json"]; !ok {
		t.Errorf("inputSchema = %#v", spec["inputSchema"])
	}
	choice := tc["toolChoice"].(map[string]any)
	if _, ok := choice["tool"]; !ok {
		t.Errorf("toolChoice = %#v", choice)
	}
}

func TestImagesBecomeImageBlocks(t *testing.T) {
	req := simple()
	req.Messages[0].Content = append(req.Messages[0].Content, ir.ContentBlock{
		Type:  ir.BlockImage,
		Media: &ir.Media{MIME: "image/png", Data: "aGk="},
	})
	body, _, _ := build(t, &adapter.Target{Region: "us-east-1", Model: req.Model}, req)

	content := body["messages"].([]any)[0].(map[string]any)["content"].([]any)
	var img map[string]any
	for _, b := range content {
		if m, ok := b.(map[string]any)["image"].(map[string]any); ok {
			img = m
		}
	}
	if img == nil {
		t.Fatalf("no image block: %#v", content)
	}
	if img["format"] != "png" {
		t.Errorf("format = %v, want png (Converse takes a bare format, not a mime type)", img["format"])
	}
	if _, ok := img["source"].(map[string]any)["bytes"]; !ok {
		t.Errorf("source = %#v", img["source"])
	}
}

func TestAURLImageIsWarnedNotFetched(t *testing.T) {
	// Converse takes bytes only. Fetching the URL here would make an outbound
	// request from a request builder, which no other adapter does.
	req := simple()
	req.Messages[0].Content = append(req.Messages[0].Content, ir.ContentBlock{
		Type: ir.BlockImage, Media: &ir.Media{URL: "https://example.invalid/x.png"},
	})
	_, _, warns := build(t, &adapter.Target{Region: "us-east-1", Model: req.Model}, req)
	if len(warns) == 0 {
		t.Fatal("a URL image must produce a warning")
	}
}

func TestToolResultsBecomeUserContent(t *testing.T) {
	req := simple()
	req.Messages = append(req.Messages, ir.Message{
		Role: ir.RoleTool,
		Content: []ir.ContentBlock{{
			Type: ir.BlockToolResult,
			ToolResult: &ir.ToolResult{
				ToolUseID: "tu_1",
				Content:   []ir.ContentBlock{{Type: ir.BlockText, Text: "17C"}},
			},
		}},
	})
	body, _, _ := build(t, &adapter.Target{Region: "us-east-1", Model: req.Model}, req)

	msgs := body["messages"].([]any)
	last := msgs[len(msgs)-1].(map[string]any)
	// Converse has no tool role. A tool result is user content carrying a
	// toolResult block, and getting this wrong is a 400 on every tool loop.
	if last["role"] != "user" {
		t.Errorf("tool result role = %v, want user", last["role"])
	}
	res := last["content"].([]any)[0].(map[string]any)["toolResult"].(map[string]any)
	if res["toolUseId"] != "tu_1" {
		t.Errorf("toolResult = %#v", res)
	}
}

func TestNoCredentialHeaderIsWritten(t *testing.T) {
	// Signing is the authorizer's job, Task 1. An adapter that also wrote a
	// header would put a key in a request the signature does not cover.
	hr, _, err := New().BuildRequest(context.Background(),
		&adapter.Target{Region: "us-east-1", Model: "m", APIKey: "should-be-ignored"}, simple())
	if err != nil {
		t.Fatal(err)
	}
	if hr.Header.Get("Authorization") != "" || hr.Header.Get("x-api-key") != "" {
		t.Errorf("the builder wrote a credential header: %v", hr.Header)
	}
}

func TestSurfacesIsLLMOnly(t *testing.T) {
	// Bedrock's embedding API is a different shape; claiming the surface would
	// route embeddings to a Converse endpoint that answers 400.
	s := New().Surfaces()
	if !s.Has(ir.SurfaceLLM) {
		t.Error("llm is not served")
	}
	if s.Has(ir.SurfaceEmbedding) {
		t.Error("embedding must not be claimed")
	}
}

func TestEscapePathSegmentMatchesTheAWSRule(t *testing.T) {
	// url.PathEscape leaves ':' alone, which is legal in a path segment and is
	// not what AWS signs. Every inference-profile id contains one, so this is
	// the difference between working and a 403 on every request.
	for in, want := range map[string]string{
		"anthropic.claude-3-5-sonnet-20241022-v2:0": "anthropic.claude-3-5-sonnet-20241022-v2%3A0",
		"us.anthropic.claude-x-v1:0":                "us.anthropic.claude-x-v1%3A0",
		"plain-model_1.0~x":                         "plain-model_1.0~x",
		"a/b":                                       "a%2Fb",
	} {
		if got := escapePathSegment(in); got != want {
			t.Errorf("escapePathSegment(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestASystemTurnBecomesTheSystemField(t *testing.T) {
	// The OpenAI edge leaves a developer or system turn as an ir.RoleSystem
	// message rather than moving it to Request.System. Rendering that as a user
	// turn silently strips its status, and Converse's messages array admits
	// only user and assistant anyway.
	req := simple()
	req.Messages = append([]ir.Message{{
		Role: ir.RoleSystem, Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "be terse"}},
	}}, req.Messages...)

	body, _, _ := build(t, &adapter.Target{Region: "us-east-1", Model: req.Model}, req)
	sys, ok := body["system"].([]any)
	if !ok || len(sys) != 1 || sys[0].(map[string]any)["text"] != "be terse" {
		t.Fatalf("system = %#v", body["system"])
	}
	for _, m := range body["messages"].([]any) {
		if role := m.(map[string]any)["role"]; role == "system" {
			t.Errorf("a system role reached the messages array")
		}
	}
	if n := len(body["messages"].([]any)); n != 1 {
		t.Errorf("messages = %d, want 1: the system turn was not removed", n)
	}
}
