package golden

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/ir"
)

// roundTrip renders an IR request for one kind and parses the result back with
// the edge dialect that speaks the same wire format.
func roundTrip(t *testing.T, kind string, req *ir.Request) (*ir.Request, []string) {
	t.Helper()
	hr, warns, err := adapters()[kind].BuildRequest(context.Background(), target(), req)
	if err != nil {
		t.Fatalf("%s build: %v", kind, err)
	}
	body, err := io.ReadAll(hr.Body)
	if err != nil {
		t.Fatal(err)
	}

	dialect := kind
	if kind == "openaicompat" {
		dialect = "openai"
	}
	m := meta{}
	if dialect == "gemini" {
		m.Path = "models/target-model:generateContent"
	}
	back, _, err := dialects()[dialect].ParseRequest(requestFor(t, dialect, m, body), 1<<20)
	if err != nil {
		t.Fatalf("%s reparse: %v\n%s", kind, err, body)
	}
	return back, warningStrings(warns)
}

func firstBlock(t *testing.T, req *ir.Request, kind ir.BlockType) ir.ContentBlock {
	t.Helper()
	for _, m := range req.Messages {
		for _, b := range m.Content {
			if b.Type == kind {
				return b
			}
		}
	}
	t.Fatalf("no %s block in %+v", kind, req.Messages)
	return ir.ContentBlock{}
}

func thinkingRequest() *ir.Request {
	n := 1024
	return &ir.Request{
		Model:     "target-model",
		MaxTokens: &n,
		Messages: []ir.Message{
			{Role: ir.RoleUser, Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "Is 91 prime?"}}},
			{Role: ir.RoleAssistant, Content: []ir.ContentBlock{
				{Type: ir.BlockThinking, Thinking: &ir.Thinking{
					Text: "91 = 7 * 13", Signature: "ErUBCkYIBRgCIkA=",
				}},
				{Type: ir.BlockText, Text: "No."},
			}},
			{Role: ir.RoleUser, Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "Factors?"}}},
		},
	}
}

func cacheRequest() *ir.Request {
	n := 1024
	return &ir.Request{
		Model:     "target-model",
		MaxTokens: &n,
		Messages: []ir.Message{{Role: ir.RoleUser, Content: []ir.ContentBlock{{
			Type: ir.BlockText, Text: "a long shared prefix",
			CacheControl: &ir.CacheControl{Type: "ephemeral", TTL: "1h"},
		}}}},
	}
}

func TestThinkingSignatureRoundTripsOrWarns(t *testing.T) {
	for _, kind := range []string{"openaicompat", "anthropic", "gemini"} {
		t.Run(kind, func(t *testing.T) {
			back, warns := roundTrip(t, kind, thinkingRequest())

			supported := kind == "anthropic" || kind == "gemini"
			if !supported {
				if !hasPrefixIn(warns, "messages[].assistant.thinking") {
					t.Errorf("thinking was dropped with no warning: %v", warns)
				}
				return
			}
			b := firstBlock(t, back, ir.BlockThinking)
			if b.Thinking == nil || b.Thinking.Signature != "ErUBCkYIBRgCIkA=" {
				t.Errorf("signature = %+v; it must return byte for byte", b.Thinking)
			}
			if b.Thinking.Text != "91 = 7 * 13" {
				t.Errorf("thinking text = %q", b.Thinking.Text)
			}
		})
	}
}

func TestCacheControlTTLRoundTripsOrWarns(t *testing.T) {
	for _, kind := range []string{"openaicompat", "anthropic", "gemini"} {
		t.Run(kind, func(t *testing.T) {
			back, warns := roundTrip(t, kind, cacheRequest())

			if kind != "anthropic" {
				if !hasPrefixIn(warns, "cache_control") {
					t.Errorf("a paid feature vanished with no warning: %v", warns)
				}
				return
			}
			b := firstBlock(t, back, ir.BlockText)
			if b.CacheControl == nil || b.CacheControl.TTL != "1h" {
				t.Errorf("cache control = %+v", b.CacheControl)
			}
		})
	}
}

func TestToolCallIdentityRoundTrips(t *testing.T) {
	n := 512
	req := &ir.Request{
		Model: "target-model", MaxTokens: &n,
		Tools: []ir.Tool{{Name: "lookup", Schema: json.RawMessage(`{"type":"object"}`)}},
		Messages: []ir.Message{
			{Role: ir.RoleUser, Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "two cities"}}},
			{Role: ir.RoleAssistant, Content: []ir.ContentBlock{
				{Type: ir.BlockToolUse, ToolUse: &ir.ToolUse{
					ID: "call_a", Name: "lookup", Input: json.RawMessage(`{"city":"Oslo"}`)}},
				{Type: ir.BlockToolUse, ToolUse: &ir.ToolUse{
					ID: "call_b", Name: "lookup", Input: json.RawMessage(`{"city":"Bergen"}`)}},
			}},
			{Role: ir.RoleTool, Content: []ir.ContentBlock{
				{Type: ir.BlockToolResult, ToolResult: &ir.ToolResult{
					ToolUseID: "call_a",
					Content:   []ir.ContentBlock{{Type: ir.BlockText, Text: "clear"}}}},
				{Type: ir.BlockToolResult, ToolResult: &ir.ToolResult{
					ToolUseID: "call_b",
					Content:   []ir.ContentBlock{{Type: ir.BlockText, Text: "rain"}}}},
			}},
		},
	}

	for _, kind := range []string{"openaicompat", "anthropic", "gemini"} {
		t.Run(kind, func(t *testing.T) {
			back, _ := roundTrip(t, kind, req)

			var ids []string
			for _, m := range back.Messages {
				for _, b := range m.Content {
					if b.Type == ir.BlockToolResult && b.ToolResult != nil {
						ids = append(ids, b.ToolResult.ToolUseID)
					}
				}
			}
			if len(ids) != 2 {
				t.Fatalf("tool results = %v", ids)
			}
			if ids[0] == ids[1] {
				t.Errorf("both results carry %q; parallel calls to one function are only "+
					"distinguishable by id", ids[0])
			}
		})
	}
}

func boolPtr(b bool) *bool { return &b }

func TestEveryDroppedFieldProducesAWarning(t *testing.T) {
	k := 40
	n := 512
	req := &ir.Request{
		Model: "target-model", MaxTokens: &n, TopK: &k,
		Safety:            []ir.SafetySetting{{Category: "HARM_CATEGORY_HARASSMENT", Threshold: "BLOCK_NONE"}},
		ResponseFormat:    &ir.ResponseFormat{Type: "json_schema", Schema: json.RawMessage(`{"type":"object"}`)},
		ParallelToolCalls: boolPtr(false),
		Messages: []ir.Message{{Role: ir.RoleUser, Content: []ir.ContentBlock{
			{Type: ir.BlockText, Text: "hi"},
			{Type: ir.BlockAudio, Media: &ir.Media{MIME: "audio/wav", Data: "UklGR"}},
		}}},
	}

	// Each kind's unsupported set, from the spec's mapping tables.
	unsupported := map[string][]string{
		"openaicompat": {"top_k", "safety"},
		// Not response_format: Anthropic's structured output is GA and the
		// adapter emits it under output_config.format.
		"anthropic": {"safety", "messages[].audio"},
		"gemini":    {"parallel_tool_calls"},
	}
	for kind, fields := range unsupported {
		t.Run(kind, func(t *testing.T) {
			_, warns := roundTrip(t, kind, req)
			for _, f := range fields {
				if !hasPrefixIn(warns, f) {
					t.Errorf("%s dropped %s with no warning; warnings = %v", kind, f, warns)
				}
			}
		})
	}
}

// systemTexts gathers system content from both places it can live: the
// top-level field the Anthropic and Gemini edges fill, and the inline
// RoleSystem messages the OpenAI edge keeps in place.
func systemTexts(req *ir.Request) []string {
	var out []string
	for _, b := range req.System {
		out = append(out, b.Text)
	}
	for _, m := range req.Messages {
		if m.Role != ir.RoleSystem {
			continue
		}
		for _, b := range m.Content {
			out = append(out, b.Text)
		}
	}
	return out
}

func TestSystemContentSurvivesEveryTarget(t *testing.T) {
	n := 128
	req := &ir.Request{
		Model: "target-model", MaxTokens: &n,
		System:   []ir.ContentBlock{{Type: ir.BlockText, Text: "be terse"}},
		Messages: []ir.Message{{Role: ir.RoleUser, Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "hi"}}}},
	}
	for _, kind := range []string{"openaicompat", "anthropic", "gemini"} {
		t.Run(kind, func(t *testing.T) {
			back, _ := roundTrip(t, kind, req)
			text := strings.Join(systemTexts(back), " ")
			if !strings.Contains(text, "be terse") {
				t.Errorf("system content was lost: system=%+v messages=%+v", back.System, back.Messages)
			}
		})
	}
}
