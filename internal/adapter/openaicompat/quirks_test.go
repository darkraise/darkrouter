package openaicompat

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/ir"
)

func buildQuirked(t *testing.T, q quirkSet, req *ir.Request) (map[string]any, []ir.Warning) {
	t.Helper()
	tgt := &adapter.Target{BaseURL: "https://up.example/v1", APIKey: "sk-x", Model: "up-model"}
	hr, warns, err := buildRequest(context.Background(), tgt, req, q)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(hr.Body)
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	return got, warns
}

func TestQuirksShapeTheRequest(t *testing.T) {
	n, temp, topP, par := 256, 0.5, 0.9, true
	withTools := func() *ir.Request {
		return &ir.Request{
			Stream: true,
			Messages: []ir.Message{{Role: ir.RoleUser,
				Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "hi"}}}},
			Tools: []ir.Tool{{Name: "lookup", Schema: json.RawMessage(`{"type":"object"}`)}},
		}
	}
	cases := []struct {
		name    string
		quirk   string
		req     *ir.Request
		present []string
		absent  []string
		warned  []string
		check   func(t *testing.T, body map[string]any)
	}{
		{
			name: "max-completion-tokens-name renames the cap", quirk: "max-completion-tokens-name",
			req:     &ir.Request{MaxTokens: &n},
			present: []string{"max_completion_tokens"}, absent: []string{"max_tokens"},
		},
		{
			name: "requires-max-tokens substitutes a default", quirk: "requires-max-tokens",
			req:     &ir.Request{},
			present: []string{"max_tokens"}, warned: []string{"max_tokens"},
			check: func(t *testing.T, body map[string]any) {
				if body["max_tokens"] != float64(4096) {
					t.Fatalf("max_tokens = %v, want 4096", body["max_tokens"])
				}
			},
		},
		{
			name: "requires-max-tokens honors the request", quirk: "requires-max-tokens",
			req: &ir.Request{MaxTokens: &n},
			check: func(t *testing.T, body map[string]any) {
				if body["max_tokens"] != float64(256) {
					t.Fatalf("max_tokens = %v, want 256", body["max_tokens"])
				}
			},
		},
		{
			name: "no-system-role folds system into the first user turn", quirk: "no-system-role",
			req: &ir.Request{
				System: []ir.ContentBlock{{Type: ir.BlockText, Text: "Be terse."}},
				Messages: []ir.Message{
					{Role: ir.RoleSystem, Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "Answer in French."}}},
					{Role: ir.RoleUser, Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "Hello"}}},
				},
			},
			warned: []string{"system"},
			check: func(t *testing.T, body map[string]any) {
				msgs := body["messages"].([]any)
				if len(msgs) != 1 {
					t.Fatalf("messages = %v, want one folded user turn", msgs)
				}
				m := msgs[0].(map[string]any)
				content, _ := m["content"].(string)
				if m["role"] != "user" || !strings.Contains(content, "Be terse.") ||
					!strings.Contains(content, "Answer in French.") || !strings.HasSuffix(content, "Hello") {
					t.Fatalf("folded turn = %v", m)
				}
			},
		},
		{
			name: "no-parallel-tool-calls omits the field", quirk: "no-parallel-tool-calls",
			req:    &ir.Request{ParallelToolCalls: &par},
			absent: []string{"parallel_tool_calls"}, warned: []string{"parallel_tool_calls"},
		},
		{
			name: "temperature-top-p-exclusive keeps temperature", quirk: "temperature-top-p-exclusive",
			req:     &ir.Request{Temperature: &temp, TopP: &topP},
			present: []string{"temperature"}, absent: []string{"top_p"}, warned: []string{"top_p"},
		},
		{
			name: "temperature-top-p-exclusive passes a lone top_p", quirk: "temperature-top-p-exclusive",
			req:     &ir.Request{TopP: &topP},
			present: []string{"top_p"},
		},
		{
			name: "strict-unknown-fields withholds the optional fields", quirk: "strict-unknown-fields",
			req: &ir.Request{
				Stream: true, ParallelToolCalls: &par,
				Reasoning: &ir.Reasoning{Effort: "high"},
				Metadata:  map[string]string{"trace": "x"},
			},
			present: []string{"stream"},
			absent:  []string{"stream_options", "reasoning_effort", "metadata", "parallel_tool_calls"},
			warned:  []string{"reasoning.effort", "metadata", "parallel_tool_calls"},
		},
		{
			name: "no-tool-streaming forces unary when tools are present", quirk: "no-tool-streaming",
			req:    withTools(),
			absent: []string{"stream", "stream_options"}, warned: []string{"stream"},
		},
		{
			name: "no-tool-streaming leaves a tool-free stream alone", quirk: "no-tool-streaming",
			req:     &ir.Request{Stream: true},
			present: []string{"stream", "stream_options"},
		},
		{
			name: "usage-final-chunk-only changes nothing", quirk: "usage-final-chunk-only",
			req:     &ir.Request{Stream: true},
			present: []string{"stream", "stream_options"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, warns := buildQuirked(t, quirkSet{tc.quirk: true}, tc.req)
			for _, k := range tc.present {
				if _, ok := body[k]; !ok {
					t.Errorf("%s missing from %v", k, body)
				}
			}
			for _, k := range tc.absent {
				if _, ok := body[k]; ok {
					t.Errorf("%s present in %v", k, body)
				}
			}
			for _, f := range tc.warned {
				if !hasWarning(warns, f) {
					t.Errorf("no warning for %s in %v", f, warns)
				}
			}
			if tc.check != nil {
				tc.check(t, body)
			}
		})
	}
}

func TestNoQuirksLeavesTheRequestUnchanged(t *testing.T) {
	n, par := 256, true
	body, warns := buildQuirked(t, nil, &ir.Request{
		Stream: true, MaxTokens: &n, ParallelToolCalls: &par,
	})
	for _, k := range []string{"max_tokens", "stream_options", "parallel_tool_calls"} {
		if _, ok := body[k]; !ok {
			t.Errorf("%s missing from %v", k, body)
		}
	}
	if len(warns) != 0 {
		t.Errorf("warnings = %v", warns)
	}
}

func TestQuirksResolveFromTheTargetBaseURL(t *testing.T) {
	cases := []struct {
		base string
		want []string
	}{
		{"https://api.openai.com/v1", []string{"max-completion-tokens-name"}},
		{"https://api.openai.com/v1/", []string{"max-completion-tokens-name"}},
		{"https://api.mistral.ai/v1", []string{"strict-unknown-fields", "temperature-top-p-exclusive"}},
		{"https://api.deepseek.com/v1", []string{"echo-reasoning-content"}},
		{"https://openrouter.ai/api/v1", []string{"echo-reasoning-content"}},
		{"https://upstream.example/v1", nil},
	}
	for _, tc := range cases {
		got := quirksForTarget(&adapter.Target{BaseURL: tc.base})
		if len(got) != len(tc.want) {
			t.Errorf("%s: quirks = %v, want %v", tc.base, got, tc.want)
			continue
		}
		for _, q := range tc.want {
			if !got.has(q) {
				t.Errorf("%s: missing %s in %v", tc.base, q, got)
			}
		}
	}
	if got := QuirksFor("openai"); !got.has("max-completion-tokens-name") {
		t.Errorf("QuirksFor(openai) = %v", got)
	}
	if got := QuirksFor(""); got != nil {
		t.Errorf("QuirksFor(\"\") = %v, want nil", got)
	}
}

func TestBuildAppliesTheTargetPresetQuirks(t *testing.T) {
	n := 32
	tgt := &adapter.Target{BaseURL: "https://api.openai.com/v1", APIKey: "sk-x", Model: "gpt"}
	hr, _, err := BuildRequest(context.Background(), tgt, &ir.Request{MaxTokens: &n})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(hr.Body)
	if !strings.Contains(string(body), `"max_completion_tokens":32`) {
		t.Fatalf("body = %s", body)
	}
}
