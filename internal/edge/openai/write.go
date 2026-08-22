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

func WriteResponse(w http.ResponseWriter, resp *ir.Response) error {
	var text strings.Builder
	for _, b := range resp.Content {
		if b.Type == ir.BlockText {
			text.WriteString(b.Text)
		}
	}
	out := map[string]any{
		"id":      resp.ID,
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   resp.Model,
		"choices": []any{map[string]any{
			"index":         0,
			"message":       map[string]any{"role": "assistant", "content": text.String()},
			"finish_reason": finishReason(resp.StopReason),
		}},
		"usage": map[string]any{
			"prompt_tokens":     resp.Usage.InputTokens,
			"completion_tokens": resp.Usage.OutputTokens,
			"total_tokens":      resp.Usage.InputTokens + resp.Usage.OutputTokens,
		},
	}
	w.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(out)
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
