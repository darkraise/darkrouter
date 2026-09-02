package anthropic

import (
	"encoding/json"
	"errors"
	"io"
	"iter"

	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/sse"
)

type wireStreamEvent struct {
	Type    string `json:"type"`
	Index   int    `json:"index"`
	Message *struct {
		ID    string    `json:"id"`
		Model string    `json:"model"`
		Usage wireUsage `json:"usage"`
	} `json:"message"`
	ContentBlock json.RawMessage `json:"content_block"`
	Delta        *struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		Thinking    string `json:"thinking"`
		Signature   string `json:"signature"`
		PartialJSON string `json:"partial_json"`
		StopReason  string `json:"stop_reason"`
	} `json:"delta"`
	Usage *wireUsage `json:"usage"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// blockStart maps a content_block onto the IR's opening delta. A kind the IR
// does not model keeps its wire type and every wire field, so the Anthropic
// writer can re-emit it and its deltas still reach the client rather than
// arriving inside an empty text block.
func blockStart(raw json.RawMessage) *ir.Delta {
	d := &ir.Delta{Type: ir.BlockText}
	if len(raw) == 0 {
		return d
	}
	var b wireBlock
	if json.Unmarshal(raw, &b) != nil {
		return d
	}
	d.ToolID, d.ToolName = b.ID, b.Name
	switch b.Type {
	case "text", "":
	case "thinking":
		d.Type = ir.BlockThinking
	case "redacted_thinking":
		d.Type = ir.BlockRedactedThinking
	case "tool_use":
		d.Type = ir.BlockToolUse
	default:
		d.Type = ir.BlockType(b.Type)
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) == nil {
			d.Extra = fields
		}
	}
	return d
}

// errorType maps Anthropic's error taxonomy. It matters most for
// overloaded_error, which arrives in-stream under a 200 and would otherwise be
// reported as a generic API failure the exec loop cannot reason about.
func errorType(t string) ir.ErrorType {
	switch t {
	case "invalid_request_error":
		return ir.ErrInvalidRequest
	case "authentication_error":
		return ir.ErrAuthentication
	case "permission_error":
		return ir.ErrPermission
	case "not_found_error":
		return ir.ErrNotFound
	case "rate_limit_error":
		return ir.ErrRateLimit
	case "overloaded_error":
		return ir.ErrOverloaded
	default:
		return ir.ErrAPI
	}
}

// ParseStream converts Anthropic's SSE events to the IR's. The event model is
// the same, so the interesting parts are the two that are not: usage arrives
// split across message_start and message_delta and has to be accumulated, and
// unknown event types are ignored rather than treated as failures.
func ParseStream(r io.Reader, maxLine int) iter.Seq2[ir.StreamEvent, error] {
	return func(yield func(ir.StreamEvent, error) bool) {
		reader := sse.NewReader(r, maxLine)
		var (
			usage ir.Usage
			stop  = ir.StopEndTurn
		)
		for {
			raw, err := reader.Next()
			if errors.Is(err, io.EOF) {
				return
			}
			if err != nil {
				yield(ir.StreamEvent{}, err)
				return
			}
			if raw.Data == "" {
				continue
			}
			var ev wireStreamEvent
			if json.Unmarshal([]byte(raw.Data), &ev) != nil {
				continue // an unparseable event is not a reason to kill the stream
			}

			switch ev.Type {
			case "message_start":
				if ev.Message == nil {
					continue
				}
				usage = ev.Message.Usage.toIR()
				if !yield(ir.StreamEvent{
					Type: ir.EventMessageStart, ID: ev.Message.ID, Model: ev.Message.Model,
				}, nil) {
					return
				}
				u := usage
				if !yield(ir.StreamEvent{Type: ir.EventMessageDelta, Usage: &u}, nil) {
					return
				}

			case "content_block_start":
				if !yield(ir.StreamEvent{Type: ir.EventBlockStart, Index: ev.Index, Delta: blockStart(ev.ContentBlock)}, nil) {
					return
				}

			case "content_block_delta":
				if ev.Delta == nil {
					continue
				}
				d := &ir.Delta{}
				switch ev.Delta.Type {
				case "text_delta":
					d.Type, d.Text = ir.BlockText, ev.Delta.Text
				case "thinking_delta":
					d.Type, d.Thinking = ir.BlockThinking, ev.Delta.Thinking
				case "signature_delta":
					// Carries no text: a signature must never look like content,
					// or an empty thinking block would commit the response.
					d.Type, d.Signature = ir.BlockThinking, ev.Delta.Signature
				case "input_json_delta":
					d.Type, d.ToolInput = ir.BlockToolUse, ev.Delta.PartialJSON
				default:
					continue
				}
				if !yield(ir.StreamEvent{Type: ir.EventContentDelta, Index: ev.Index, Delta: d}, nil) {
					return
				}

			case "content_block_stop":
				if !yield(ir.StreamEvent{Type: ir.EventBlockStop, Index: ev.Index}, nil) {
					return
				}

			case "message_delta":
				var warns []ir.Warning
				if ev.Delta != nil && ev.Delta.StopReason != "" {
					var known bool
					stop, known = stopReason(ev.Delta.StopReason)
					if !known {
						warns = append(warns, ir.Warning{
							Field: "stop_reason", Target: targetName,
							Reason: "unrecognized value " + ev.Delta.StopReason + "; reported as end_turn",
						})
					}
				}
				if ev.Usage != nil {
					// Merged, not replaced: message_delta reports output tokens
					// only, and assigning it would erase the input count.
					u := ev.Usage.toIR()
					if u.OutputTokens > 0 {
						usage.OutputTokens = u.OutputTokens
					}
					if u.InputTokens > 0 {
						usage.InputTokens = u.InputTokens
					}
					if u.CacheReadTokens > 0 {
						usage.CacheReadTokens = u.CacheReadTokens
					}
					if u.CacheWriteTokens > 0 {
						usage.CacheWriteTokens = u.CacheWriteTokens
					}
					if u.CacheWrite5mTokens > 0 {
						usage.CacheWrite5mTokens = u.CacheWrite5mTokens
					}
					if u.CacheWrite1hTokens > 0 {
						usage.CacheWrite1hTokens = u.CacheWrite1hTokens
					}
				}
				merged := usage
				if !yield(ir.StreamEvent{Type: ir.EventMessageDelta, Usage: &merged, Warnings: warns}, nil) {
					return
				}

			case "message_stop":
				if !yield(ir.StreamEvent{Type: ir.EventMessageStop, StopReason: stop}, nil) {
					return
				}

			case "ping":
				if !yield(ir.StreamEvent{Type: ir.EventPing}, nil) {
					return
				}

			case "error":
				e := &ir.Error{Type: ir.ErrAPI, Message: "upstream stream error"}
				if ev.Error != nil {
					e = &ir.Error{Type: errorType(ev.Error.Type), Message: ev.Error.Message, Code: ev.Error.Type}
				}
				yield(ir.StreamEvent{}, e)
				return

			default:
				// Anthropic warns that new event types will appear. Ignoring
				// them is the documented client behavior.
				continue
			}
		}
	}
}
