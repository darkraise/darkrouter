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
