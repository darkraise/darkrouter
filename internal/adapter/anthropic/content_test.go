package anthropic

import (
	"encoding/json"
	"testing"

	"github.com/darkraise/darkrouter/internal/ir"
)

func blocks(t *testing.T, in []ir.ContentBlock, cb *cacheBudget) ([]map[string]any, []ir.Warning) {
	t.Helper()
	if cb == nil {
		cb = &cacheBudget{}
	}
	raw, warns := renderBlocks(in, cb)
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

func hasWarning(warns []ir.Warning, field string) bool {
	for _, w := range warns {
		if w.Field == field {
			return true
		}
	}
	return false
}

func TestRenderBlocksPreservesThinkingVerbatim(t *testing.T) {
	got, warns := blocks(t, []ir.ContentBlock{
		{Type: ir.BlockThinking, Thinking: &ir.Thinking{Text: "step one", Signature: "sig-abc"}},
		{Type: ir.BlockText, Text: "42"},
	}, nil)
	if len(warns) != 0 {
		t.Fatalf("warnings = %+v; nothing is lost round-tripping to Anthropic", warns)
	}
	if got[0]["type"] != "thinking" || got[0]["thinking"] != "step one" || got[0]["signature"] != "sig-abc" {
		t.Fatalf("thinking block = %v", got[0])
	}
	if got[1]["type"] != "text" {
		t.Errorf("order was not preserved: %v", got)
	}
}

func TestRenderBlocksEmitsRedactedThinking(t *testing.T) {
	got, _ := blocks(t, []ir.ContentBlock{
		{Type: ir.BlockRedactedThinking, Thinking: &ir.Thinking{Data: "encrypted"}},
	}, nil)
	if got[0]["type"] != "redacted_thinking" || got[0]["data"] != "encrypted" {
		t.Errorf("block = %v", got[0])
	}
}

func TestRenderBlocksEmitsImageSources(t *testing.T) {
	got, _ := blocks(t, []ir.ContentBlock{
		{Type: ir.BlockImage, Media: &ir.Media{MIME: "image/png", Data: "AAAA"}},
		{Type: ir.BlockImage, Media: &ir.Media{URL: "https://x.example/a.png"}},
		{Type: ir.BlockImage, Media: &ir.Media{FileID: "file_1"}},
	}, nil)
	if s := got[0]["source"].(map[string]any); s["type"] != "base64" ||
		s["media_type"] != "image/png" || s["data"] != "AAAA" {
		t.Errorf("base64 source = %v", s)
	}
	if s := got[1]["source"].(map[string]any); s["type"] != "url" || s["url"] != "https://x.example/a.png" {
		t.Errorf("url source = %v", s)
	}
	if s := got[2]["source"].(map[string]any); s["type"] != "file" || s["file_id"] != "file_1" {
		t.Errorf("file source = %v", s)
	}
}

func TestRenderBlocksDropsAudioWithAWarning(t *testing.T) {
	got, warns := blocks(t, []ir.ContentBlock{
		{Type: ir.BlockText, Text: "listen"},
		{Type: ir.BlockAudio, Media: &ir.Media{MIME: "audio/wav", Data: "UklGR"}},
	}, nil)
	if len(got) != 1 {
		t.Fatalf("blocks = %v; Anthropic has no audio content block", got)
	}
	if !hasWarning(warns, "messages[].audio") {
		t.Errorf("warnings = %+v", warns)
	}
}

func TestRenderBlocksNestsToolResultContent(t *testing.T) {
	got, warns := blocks(t, []ir.ContentBlock{{
		Type: ir.BlockToolResult,
		ToolResult: &ir.ToolResult{
			ToolUseID: "call_a", IsError: true,
			Content: []ir.ContentBlock{
				{Type: ir.BlockText, Text: "boom"},
				{Type: ir.BlockImage, Media: &ir.Media{MIME: "image/png", Data: "AAAA"}},
			},
		},
	}}, nil)
	if len(warns) != 0 {
		t.Fatalf("warnings = %+v; Anthropic tool results carry images natively", warns)
	}
	if got[0]["type"] != "tool_result" || got[0]["tool_use_id"] != "call_a" || got[0]["is_error"] != true {
		t.Fatalf("tool_result = %v", got[0])
	}
	inner := got[0]["content"].([]any)
	if len(inner) != 2 || inner[1].(map[string]any)["type"] != "image" {
		t.Errorf("nested content = %v", inner)
	}
}

func TestRenderBlocksEmitsToolUseInputAsAnObject(t *testing.T) {
	got, _ := blocks(t, []ir.ContentBlock{{
		Type:    ir.BlockToolUse,
		ToolUse: &ir.ToolUse{ID: "call_a", Name: "f", Input: json.RawMessage(`{"x":1}`)},
	}}, nil)
	in, ok := got[0]["input"].(map[string]any)
	if !ok {
		t.Fatalf("input = %#v; Anthropic takes an object, not a JSON string", got[0]["input"])
	}
	if in["x"].(float64) != 1 {
		t.Errorf("input = %v", in)
	}
}

func TestRenderBlocksEmitsToolUseEmptyInputAsAnEmptyObject(t *testing.T) {
	got, _ := blocks(t, []ir.ContentBlock{{
		Type:    ir.BlockToolUse,
		ToolUse: &ir.ToolUse{ID: "call_a", Name: "f"},
	}}, nil)
	if in, ok := got[0]["input"].(map[string]any); !ok || len(in) != 0 {
		t.Errorf("input = %#v, want {}", got[0]["input"])
	}
}

func TestRenderBlocksCarriesCacheControlWithTTL(t *testing.T) {
	got, warns := blocks(t, []ir.ContentBlock{{
		Type: ir.BlockText, Text: "long",
		CacheControl: &ir.CacheControl{Type: "ephemeral", TTL: "1h"},
	}}, nil)
	if len(warns) != 0 {
		t.Fatalf("warnings = %+v", warns)
	}
	cc := got[0]["cache_control"].(map[string]any)
	if cc["type"] != "ephemeral" || cc["ttl"] != "1h" {
		t.Errorf("cache_control = %v; the TTL is a paid feature and must survive", cc)
	}
}

func TestRenderBlocksOmitsAnEmptyTTL(t *testing.T) {
	got, _ := blocks(t, []ir.ContentBlock{{
		Type: ir.BlockText, Text: "long",
		CacheControl: &ir.CacheControl{Type: "ephemeral"},
	}}, nil)
	cc := got[0]["cache_control"].(map[string]any)
	if _, ok := cc["ttl"]; ok {
		t.Errorf("cache_control = %v; an absent TTL means the default, not \"\"", cc)
	}
}

func TestRenderBlocksDropsTheFifthCacheBreakpoint(t *testing.T) {
	cb := &cacheBudget{}
	marked := func(text string) ir.ContentBlock {
		return ir.ContentBlock{
			Type: ir.BlockText, Text: text,
			CacheControl: &ir.CacheControl{Type: "ephemeral", TTL: "5m"},
		}
	}
	got, warns := blocks(t, []ir.ContentBlock{
		marked("a"), marked("b"), marked("c"), marked("d"), marked("e"),
	}, cb)
	if len(got) != 5 {
		t.Fatalf("blocks = %d; the content stays, only the marker is dropped", len(got))
	}
	for i := 0; i < 4; i++ {
		if _, ok := got[i]["cache_control"]; !ok {
			t.Errorf("block %d lost its marker", i)
		}
	}
	if _, ok := got[4]["cache_control"]; ok {
		t.Error("the fifth marker must be dropped; a fifth is a 400")
	}
	if !hasWarning(warns, "cache_control") {
		t.Errorf("warnings = %+v", warns)
	}
}

func TestRenderBlocksDropsAnUnsignedThinkingBlock(t *testing.T) {
	// Anthropic verifies the signature of every replayed thinking block. One
	// without a signature — synthesized by another dialect, or hand-written —
	// is a 400 on the whole request, so it is dropped and the drop recorded.
	got, warns := blocks(t, []ir.ContentBlock{
		{Type: ir.BlockThinking, Thinking: &ir.Thinking{Text: "unsigned"}},
		{Type: ir.BlockThinking, Thinking: &ir.Thinking{Text: "signed", Signature: "sig"}},
		{Type: ir.BlockText, Text: "answer"},
	}, nil)
	if len(got) != 2 || got[0]["type"] != "thinking" || got[1]["type"] != "text" {
		t.Fatalf("blocks = %v; the unsigned block must go and the signed one stay", got)
	}
	if !hasWarning(warns, "messages[].thinking") {
		t.Errorf("warnings = %+v", warns)
	}
}
