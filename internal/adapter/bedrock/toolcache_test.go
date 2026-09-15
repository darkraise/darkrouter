package bedrock

import (
	"encoding/json"
	"testing"

	"github.com/darkraise/darkrouter/internal/ir"
)

// Converse admits a cachePoint in toolConfig.tools, after the tools it covers.
// It shares the four-marker budget and comes first in the order the TTL rule
// is checked in: tools, system, messages.
func TestToolCacheControlBecomesAToolCachePoint(t *testing.T) {
	const hourModel = "us.anthropic.claude-sonnet-4-5-20250929-v1:0"
	plain := &ir.CacheControl{Type: "ephemeral"}
	req := simple()
	req.Model = hourModel
	req.Tools = []ir.Tool{
		{Name: "f", Extra: map[string]json.RawMessage{"cache_control": json.RawMessage(`{"type":"ephemeral","ttl":"1h"}`)}},
		{Name: "g"},
	}
	req.System = []ir.ContentBlock{{Type: ir.BlockText, Text: "preamble", CacheControl: plain}}
	req.Messages = []ir.Message{{Role: ir.RoleUser, Content: []ir.ContentBlock{
		{Type: ir.BlockText, Text: "a", CacheControl: plain},
		{Type: ir.BlockText, Text: "b", CacheControl: plain},
		{Type: ir.BlockText, Text: "c", CacheControl: plain},
	}}}
	body, _, warns := build(t, anthropicTarget(req.Model), req)

	tools := body["toolConfig"].(map[string]any)["tools"].([]any)
	if len(tools) != 3 {
		t.Fatalf("tools = %#v, want f, a cachePoint, g", tools)
	}
	if _, ok := tools[0].(map[string]any)["toolSpec"]; !ok {
		t.Errorf("tools[0] = %#v, want f's toolSpec", tools[0])
	}
	cp, _ := tools[1].(map[string]any)["cachePoint"].(map[string]any)
	if cp["type"] != "default" || cp["ttl"] != "1h" {
		t.Errorf("tools[1] = %#v, want a one-hour cachePoint", tools[1])
	}
	if spec, _ := tools[2].(map[string]any)["toolSpec"].(map[string]any); spec["name"] != "g" {
		t.Errorf("tools[2] = %#v, want g's toolSpec", tools[2])
	}
	for _, s := range body["system"].([]any) {
		if cp, ok := s.(map[string]any)["cachePoint"].(map[string]any); ok {
			if _, set := cp["ttl"]; set {
				t.Errorf("system cachePoint = %#v, want the default five minutes", cp)
			}
		}
	}
	var msg int
	for _, b := range body["messages"].([]any)[0].(map[string]any)["content"].([]any) {
		if _, ok := b.(map[string]any)["cachePoint"]; ok {
			msg++
		}
	}
	if msg != 2 {
		t.Errorf("message cachePoints = %d, want 2: the tool's marker is one of four", msg)
	}
	if !hasWarning(warns, "cache_control") {
		t.Errorf("surplus marker dropped without a warning: %+v", warns)
	}
}

// A one-hour marker after a five-minute tool marker is downgraded: the tool's
// is checked first.
func TestToolCachePointPrecedesSystemForTheTTLOrderingRule(t *testing.T) {
	req := simple()
	req.Model = "us.anthropic.claude-sonnet-4-5-20250929-v1:0"
	req.Tools = []ir.Tool{{Name: "f", Extra: map[string]json.RawMessage{"cache_control": json.RawMessage(`{"type":"ephemeral"}`)}}}
	req.System = []ir.ContentBlock{{Type: ir.BlockText, Text: "preamble", CacheControl: &ir.CacheControl{Type: "ephemeral", TTL: "1h"}}}
	body, _, warns := build(t, anthropicTarget(req.Model), req)
	for _, s := range body["system"].([]any) {
		if cp, ok := s.(map[string]any)["cachePoint"].(map[string]any); ok && cp["ttl"] != nil {
			t.Errorf("system cachePoint = %#v; a one-hour entry after a five-minute tool entry is rejected", cp)
		}
	}
	if !hasWarning(warns, "cache_control") {
		t.Errorf("downgrade without a warning: %+v", warns)
	}
}

// AWS documents tools as a cache checkpoint field for Claude models only, so
// another publisher's model is sent the tool without one.
func TestToolCacheControlIsDroppedForANonClaudeModel(t *testing.T) {
	req := simple()
	req.Model = "us.amazon.nova-pro-v1:0"
	req.Tools = []ir.Tool{{Name: "f", Extra: map[string]json.RawMessage{"cache_control": json.RawMessage(`{"type":"ephemeral"}`)}}}
	body, _, warns := build(t, anthropicTarget(req.Model), req)
	tools := body["toolConfig"].(map[string]any)["tools"].([]any)
	if len(tools) != 1 {
		t.Errorf("tools = %#v, want the toolSpec alone", tools)
	}
	if !hasWarning(warns, "tools[].cache_control") {
		t.Errorf("tool marker dropped without a warning: %+v", warns)
	}
}

// AWS's explicit caching table is the list of models that take a tool
// cachePoint. A Claude model missing from it is sent the tool without one.
func TestToolCachePointFollowsAWSsCachingTable(t *testing.T) {
	for model, want := range map[string]bool{
		"us.anthropic.claude-3-7-sonnet-20250219-v1:0": true,
		"anthropic.claude-3-5-sonnet-20241022-v2:0":    true,
		"global.anthropic.claude-opus-4-6-v1":          true,
		"us.anthropic.claude-sonnet-4-20250514-v1:0":   false,
		"us.anthropic.claude-opus-4-20250514-v1:0":     false,
		"us.anthropic.claude-opus-4-1-20250805-v1:0":   false,
		"anthropic.claude-3-5-haiku-20241022-v1:0":     false,
		"anthropic.claude-3-haiku-20240307-v1:0":       false,
		"anthropic.claude-3-5-sonnet-20240620-v1:0":    false,
	} {
		req := simple()
		req.Model = model
		req.Tools = []ir.Tool{{Name: "f", Extra: map[string]json.RawMessage{"cache_control": json.RawMessage(`{"type":"ephemeral"}`)}}}
		body, _, warns := build(t, anthropicTarget(model), req)
		tools := body["toolConfig"].(map[string]any)["tools"].([]any)
		if got := len(tools) == 2; got != want {
			t.Errorf("%s: tools = %#v, want a tool cachePoint: %v", model, tools, want)
		}
		if dropped := hasWarning(warns, "tools[].cache_control"); dropped == want {
			t.Errorf("%s: drop warning = %v, want %v: %+v", model, dropped, !want, warns)
		}
	}
}
