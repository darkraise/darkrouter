package openai

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/darkraise/darkrouter/internal/ir"
)

func finishReason(s ir.StopReason) string {
	switch s {
	case ir.StopMaxTokens:
		return "length"
	case ir.StopToolUse:
		return "tool_calls"
	case ir.StopContentFilter:
		return "content_filter"
	default:
		return "stop"
	}
}

// now is a seam so the golden suite can pin `created`, which is the only
// non-deterministic field in an OpenAI response.
var now = time.Now

func WriteResponse(w http.ResponseWriter, resp *ir.Response) error {
	var text, reasoning strings.Builder
	var calls []any
	for _, b := range resp.Content {
		switch b.Type {
		case ir.BlockText:
			text.WriteString(b.Text)
		case ir.BlockThinking:
			if b.Thinking != nil {
				reasoning.WriteString(b.Thinking.Text)
			}
		case ir.BlockToolUse:
			if b.ToolUse == nil {
				continue
			}
			args := string(b.ToolUse.Input)
			if args == "" {
				args = "{}"
			}
			calls = append(calls, map[string]any{
				"id": b.ToolUse.ID, "type": "function",
				"function": map[string]any{"name": b.ToolUse.Name, "arguments": args},
			})
		}
	}

	msg := map[string]any{"role": "assistant"}
	// null rather than "" when there is no text: an OpenAI client reads an
	// empty string as a real empty answer and stops its loop.
	if text.Len() > 0 {
		msg["content"] = text.String()
	} else {
		msg["content"] = nil
	}
	if reasoning.Len() > 0 {
		msg["reasoning_content"] = reasoning.String()
	}
	if len(calls) > 0 {
		msg["tool_calls"] = calls
	}

	out := map[string]any{
		"id":      resp.ID,
		"object":  "chat.completion",
		"created": now().Unix(),
		"model":   resp.Model,
		"choices": []any{map[string]any{
			"index":         0,
			"message":       msg,
			"finish_reason": finishReason(resp.StopReason),
		}},
		"usage": usageBody(resp.Usage),
	}
	w.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(out)
}

// usageBody reports cache and reasoning tokens under OpenAI's nested details
// objects, and omits each object when its counter is zero — sending zeros would
// claim the provider reported them.
func usageBody(u ir.Usage) map[string]any {
	body := map[string]any{
		"prompt_tokens":     u.InputTokens,
		"completion_tokens": u.OutputTokens,
		"total_tokens":      u.InputTokens + u.OutputTokens,
	}
	if u.CacheReadTokens > 0 {
		body["prompt_tokens_details"] = map[string]any{"cached_tokens": u.CacheReadTokens}
	}
	if u.ReasoningTokens > 0 {
		body["completion_tokens_details"] = map[string]any{"reasoning_tokens": u.ReasoningTokens}
	}
	return body
}

// statusFor maps a canonical error type to the HTTP status an OpenAI client
// expects, so clients handle gateway failures with their existing code.
func statusFor(t ir.ErrorType) int {
	switch t {
	case ir.ErrInvalidRequest, ir.ErrContentFilter:
		return http.StatusBadRequest
	case ir.ErrAuthentication:
		return http.StatusUnauthorized
	case ir.ErrPermission:
		return http.StatusForbidden
	case ir.ErrNotFound:
		return http.StatusNotFound
	case ir.ErrPayloadTooLarge:
		return http.StatusRequestEntityTooLarge
	case ir.ErrUnsupportedMedia:
		return http.StatusUnsupportedMediaType
	case ir.ErrRateLimit:
		return http.StatusTooManyRequests
	case ir.ErrOverloaded:
		return http.StatusServiceUnavailable
	default:
		return http.StatusBadGateway
	}
}

func WriteError(w http.ResponseWriter, e *ir.Error) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusFor(e.Type))
	return json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"message": e.Message,
			"type":    string(e.Type),
			"code":    e.Code,
		},
	})
}
