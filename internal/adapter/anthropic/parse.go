package anthropic

import (
	"encoding/json"
	"net/http"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/ir"
)

type wireUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
}

func (u *wireUsage) toIR() ir.Usage {
	return ir.Usage{
		InputTokens:      u.InputTokens,
		OutputTokens:     u.OutputTokens,
		CacheWriteTokens: u.CacheCreationInputTokens,
		CacheReadTokens:  u.CacheReadInputTokens,
	}
}

type wireBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	Signature string          `json:"signature"`
	Data      string          `json:"data"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
}

// stopReason maps Anthropic's set. The bool reports whether the value was
// recognized; an unknown one degrades to end_turn and is warned about rather
// than failing the response, because Anthropic adds values without notice.
func stopReason(s string) (ir.StopReason, bool) {
	switch s {
	case "", "end_turn":
		return ir.StopEndTurn, true
	case "max_tokens", "model_context_window_exceeded":
		return ir.StopMaxTokens, true
	case "stop_sequence":
		return ir.StopStopSequence, true
	case "tool_use":
		return ir.StopToolUse, true
	case "refusal":
		return ir.StopContentFilter, true
	case "pause_turn":
		return ir.StopPauseTurn, true
	default:
		return ir.StopEndTurn, false
	}
}

// carriedBlock wraps a block kind the IR does not model, keeping every wire
// field so the Anthropic writers can re-emit it verbatim. Server-tool blocks
// are the case: web search runs upstream and its results arrive as content.
func carriedBlock(typ string, raw json.RawMessage) (ir.ContentBlock, bool) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || len(fields) == 0 {
		return ir.ContentBlock{}, false
	}
	return ir.ContentBlock{Type: ir.BlockType(typ), Extra: fields}, true
}

func blockToIR(b wireBlock) (ir.ContentBlock, bool) {
	switch b.Type {
	case "text":
		return ir.ContentBlock{Type: ir.BlockText, Text: b.Text}, true
	case "thinking":
		return ir.ContentBlock{Type: ir.BlockThinking, Thinking: &ir.Thinking{
			Text: b.Thinking, Signature: b.Signature,
		}}, true
	case "redacted_thinking":
		return ir.ContentBlock{Type: ir.BlockRedactedThinking, Thinking: &ir.Thinking{Data: b.Data}}, true
	case "tool_use":
		return ir.ContentBlock{Type: ir.BlockToolUse, ToolUse: &ir.ToolUse{
			ID: b.ID, Name: b.Name, Input: b.Input,
		}}, true
	default:
		return ir.ContentBlock{}, false
	}
}

func ParseResponse(resp *http.Response) (*ir.Response, error) {
	defer resp.Body.Close()
	var w struct {
		ID         string            `json:"id"`
		Model      string            `json:"model"`
		Content    []json.RawMessage `json:"content"`
		StopReason string            `json:"stop_reason"`
		Usage      wireUsage         `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&w); err != nil {
		return nil, err
	}

	out := &ir.Response{ID: w.ID, Model: w.Model, Usage: w.Usage.toIR()}
	sr, known := stopReason(w.StopReason)
	out.StopReason = sr
	if !known {
		out.Warnings = append(out.Warnings, ir.Warning{
			Field: "stop_reason", Target: targetName,
			Reason: "unrecognized value " + w.StopReason + "; reported as end_turn",
		})
	}
	for _, raw := range w.Content {
		var b wireBlock
		if err := json.Unmarshal(raw, &b); err != nil {
			return nil, err
		}
		blk, ok := blockToIR(b)
		if !ok {
			blk, ok = carriedBlock(b.Type, raw)
		}
		if !ok {
			out.Warnings = append(out.Warnings, ir.Warning{
				Field: "content[]." + b.Type, Target: targetName,
				Reason: "unrecognized content block",
			})
			continue
		}
		out.Content = append(out.Content, blk)
	}
	return out, nil
}

func Classify(resp *http.Response, err error) adapter.Outcome {
	return adapter.ClassifyStatus(resp, err)
}
