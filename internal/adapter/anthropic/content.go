// Package anthropic speaks the Anthropic Messages wire format to an upstream.
package anthropic

import (
	"encoding/json"

	"github.com/darkraise/darkrouter/internal/adapter/xlate"
	"github.com/darkraise/darkrouter/internal/ir"
)

// targetName labels the warnings this kind produces.
const targetName = "anthropic"

// cacheBudget tracks Anthropic's four-breakpoint limit across a whole request.
// A fifth marker is a 400 whose message does not name the surplus one, so
// Darkrouter drops it here and says which rather than letting the upstream fail.
type cacheBudget struct{ used int }

func (c *cacheBudget) take() bool {
	if c.used >= xlate.MaxCacheBreakpoints {
		return false
	}
	c.used++
	return true
}

// renderBlocks converts IR content to Anthropic blocks.
//
// Thinking and redacted-thinking blocks pass through with their payloads and
// their order intact. Anthropic requires them returned byte-identical; a
// re-encoded signature silently loses the model's reasoning state on the turn
// after next, which is far harder to diagnose than an error would be.
func renderBlocks(blocks []ir.ContentBlock, cb *cacheBudget) ([]any, []ir.Warning) {
	var (
		out   []any
		warns []ir.Warning
	)
	for _, b := range blocks {
		rendered, w := renderBlock(b, cb)
		warns = append(warns, w...)
		if rendered == nil {
			continue
		}
		if b.CacheControl != nil {
			if cb.take() {
				rendered["cache_control"] = cacheControl(b.CacheControl)
			} else {
				warns = append(warns, ir.Warning{
					Field: "cache_control", Target: targetName,
					Reason: "more than four breakpoints; the surplus marker was dropped",
				})
			}
		}
		out = append(out, rendered)
	}
	return out, warns
}

func cacheControl(cc *ir.CacheControl) map[string]any {
	t := cc.Type
	if t == "" {
		t = "ephemeral"
	}
	m := map[string]any{"type": t}
	// An absent TTL means Anthropic's default. Sending "" is a validation error.
	if cc.TTL != "" {
		m["ttl"] = cc.TTL
	}
	return m
}

func renderBlock(b ir.ContentBlock, cb *cacheBudget) (map[string]any, []ir.Warning) {
	switch b.Type {
	case ir.BlockText:
		return map[string]any{"type": "text", "text": b.Text}, nil

	case ir.BlockThinking:
		if b.Thinking == nil {
			return nil, nil
		}
		m := map[string]any{"type": "thinking", "thinking": b.Thinking.Text}
		if b.Thinking.Signature != "" {
			m["signature"] = b.Thinking.Signature
		}
		return m, nil

	case ir.BlockRedactedThinking:
		if b.Thinking == nil {
			return nil, nil
		}
		return map[string]any{"type": "redacted_thinking", "data": b.Thinking.Data}, nil

	case ir.BlockImage:
		src, w := mediaSource(b.Media, "image")
		if src == nil {
			return nil, w
		}
		return map[string]any{"type": "image", "source": src}, w

	case ir.BlockDocument:
		src, w := mediaSource(b.Media, "document")
		if src == nil {
			return nil, w
		}
		return map[string]any{"type": "document", "source": src}, w

	case ir.BlockAudio:
		return nil, []ir.Warning{{
			Field: "messages[].audio", Target: targetName,
			Reason: "Anthropic has no audio content block",
		}}

	case ir.BlockToolUse:
		if b.ToolUse == nil {
			return nil, nil
		}
		// Anthropic takes the arguments as an object, unlike OpenAI's JSON
		// string. An empty input must still be {} or the call is rejected.
		input := b.ToolUse.Input
		if len(input) == 0 {
			input = json.RawMessage(`{}`)
		}
		return map[string]any{
			"type": "tool_use", "id": b.ToolUse.ID, "name": b.ToolUse.Name, "input": input,
		}, nil

	case ir.BlockToolResult:
		if b.ToolResult == nil {
			return nil, nil
		}
		inner, w := renderBlocks(b.ToolResult.Content, cb)
		m := map[string]any{
			"type": "tool_result", "tool_use_id": b.ToolResult.ToolUseID, "content": inner,
		}
		if b.ToolResult.IsError {
			m["is_error"] = true
		}
		return m, w

	default:
		return nil, []ir.Warning{{
			Field: "messages[]." + string(b.Type), Target: targetName,
			Reason: "unsupported content block",
		}}
	}
}

// mediaSource builds Anthropic's source object. The three source types are not
// interchangeable: a file id is a workspace handle, a URL is fetched by
// Anthropic, and base64 is inline.
func mediaSource(m *ir.Media, field string) (map[string]any, []ir.Warning) {
	if m == nil {
		return nil, nil
	}
	switch {
	case m.FileID != "":
		return map[string]any{"type": "file", "file_id": m.FileID}, nil
	case m.Data != "":
		return map[string]any{"type": "base64", "media_type": m.MIME, "data": m.Data}, nil
	case m.URL != "":
		return map[string]any{"type": "url", "url": m.URL}, nil
	}
	return nil, []ir.Warning{{
		Field: "messages[]." + field, Target: targetName,
		Reason: "carried neither data, a URL, nor a file id",
	}}
}
