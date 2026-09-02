package anthropic

import (
	"encoding/json"
	"net/http"

	"github.com/darkraise/darkrouter/internal/edge"

	"github.com/darkraise/darkrouter/internal/ir"
)

// stopReasonWire maps the IR onto Anthropic's set. `error` has no Anthropic
// spelling and reports as end_turn, which is what a client can act on.
func stopReasonWire(s ir.StopReason) any {
	switch s {
	case ir.StopMaxTokens:
		return "max_tokens"
	case ir.StopToolUse:
		return "tool_use"
	case ir.StopStopSequence:
		return "stop_sequence"
	case ir.StopContentFilter:
		return "refusal"
	case ir.StopPauseTurn:
		return "pause_turn"
	default:
		return "end_turn"
	}
}

// responseBlocks renders assistant content. A response carries text, thinking,
// and tool uses only, so this is deliberately narrower than the adapter's
// request-side renderer.
func responseBlocks(blocks []ir.ContentBlock) []any {
	// Never nil: Anthropic clients index content without checking.
	out := []any{}
	for _, b := range blocks {
		switch b.Type {
		case ir.BlockText:
			out = append(out, map[string]any{"type": "text", "text": b.Text})
		case ir.BlockThinking:
			if b.Thinking == nil {
				continue
			}
			m := map[string]any{"type": "thinking", "thinking": b.Thinking.Text}
			if b.Thinking.Signature != "" {
				m["signature"] = b.Thinking.Signature
			}
			out = append(out, m)
		case ir.BlockRedactedThinking:
			if b.Thinking == nil {
				continue
			}
			out = append(out, map[string]any{"type": "redacted_thinking", "data": b.Thinking.Data})
		case ir.BlockToolUse:
			if b.ToolUse == nil {
				continue
			}
			input := b.ToolUse.Input
			if len(input) == 0 {
				input = json.RawMessage(`{}`)
			}
			out = append(out, map[string]any{
				"type": "tool_use", "id": b.ToolUse.ID, "name": b.ToolUse.Name, "input": input,
			})
		default:
			// A block the Anthropic adapter carried through untouched — a
			// server-tool block — goes back as it arrived.
			if len(b.Extra) > 0 {
				m := make(map[string]any, len(b.Extra))
				for k, v := range b.Extra {
					m[k] = v
				}
				out = append(out, m)
			}
		}
	}
	return out
}

func usageBody(u ir.Usage) map[string]any {
	body := map[string]any{
		"input_tokens":                u.InputTokens,
		"output_tokens":               u.OutputTokens,
		"cache_read_input_tokens":     u.CacheReadTokens,
		"cache_creation_input_tokens": u.CacheWriteTokens,
	}
	// Only when the upstream broke the writes out by TTL: claiming a split
	// that was not reported would price every write as a 5-minute one.
	if u.CacheWrite5mTokens > 0 || u.CacheWrite1hTokens > 0 {
		body["cache_creation"] = map[string]any{
			"ephemeral_5m_input_tokens": u.CacheWrite5mTokens,
			"ephemeral_1h_input_tokens": u.CacheWrite1hTokens,
		}
	}
	return body
}

func messageID(id string) string {
	if id == "" {
		return "msg_darkrouter"
	}
	return id
}

func WriteResponse(w http.ResponseWriter, resp *ir.Response) error {
	out := map[string]any{
		"id":            messageID(resp.ID),
		"type":          "message",
		"role":          "assistant",
		"model":         resp.Model,
		"content":       responseBlocks(resp.Content),
		"stop_reason":   stopReasonWire(resp.StopReason),
		"stop_sequence": nil,
		"usage":         usageBody(resp.Usage),
	}
	w.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(out)
}

// errorShape maps a canonical error onto Anthropic's type name and status.
// The status is what Claude Code's retry logic reads: 529 is retried, 400 is not.
func errorShape(t ir.ErrorType) (string, int) {
	switch t {
	case ir.ErrInvalidRequest, ir.ErrContentFilter:
		return "invalid_request_error", http.StatusBadRequest
	case ir.ErrAuthentication:
		return "authentication_error", http.StatusUnauthorized
	case ir.ErrPermission:
		return "permission_error", http.StatusForbidden
	case ir.ErrNotFound:
		return "not_found_error", http.StatusNotFound
	case ir.ErrPayloadTooLarge:
		// Anthropic documents no size-specific error type, and inventing one
		// would break clients matching on the documented set. The status is
		// what carries the distinction.
		return "invalid_request_error", http.StatusRequestEntityTooLarge
	case ir.ErrUnsupportedMedia:
		return "invalid_request_error", http.StatusUnsupportedMediaType
	case ir.ErrRateLimit:
		return "rate_limit_error", http.StatusTooManyRequests
	case ir.ErrOverloaded:
		return "overloaded_error", 529
	default:
		return "api_error", http.StatusBadGateway
	}
}

func WriteError(w http.ResponseWriter, e *ir.Error) error {
	name, status := errorShape(e.Type)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	return json.NewEncoder(w).Encode(map[string]any{
		"type":  "error",
		"error": map[string]any{"type": name, "message": e.Message},
	})
}

// WriteCount answers /v1/messages/count_tokens. The body carries no marker for
// an estimate — clients parse it strictly — so the X-Darkrouter-Estimated
// header is where that goes, and exec sets it.
func (d *Dialect) WriteCount(w http.ResponseWriter, tokens int) error {
	w.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(map[string]any{"input_tokens": tokens})
}

var _ edge.CountWriter = (*Dialect)(nil)
