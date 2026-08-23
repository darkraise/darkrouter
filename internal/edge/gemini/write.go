package gemini

import (
	"encoding/json"
	"net/http"

	"github.com/darkraise/darkrouter/internal/edge"

	"github.com/darkraise/darkrouter/internal/ir"
)

// finishReasonWire maps the IR onto Gemini's enum. Gemini has no tool-use
// reason: a turn that called a tool finishes with STOP, and the functionCall
// part is the only signal. That asymmetry is why the adapter has to infer it
// coming back the other way.
func finishReasonWire(s ir.StopReason) string {
	switch s {
	case ir.StopMaxTokens:
		return "MAX_TOKENS"
	case ir.StopContentFilter:
		return "SAFETY"
	case ir.StopError:
		return "OTHER"
	default:
		return "STOP"
	}
}

// responseParts renders assistant content. A response never carries tool
// results, images, or cache markers, so the set is text, thoughts, and calls.
func responseParts(blocks []ir.ContentBlock) []any {
	out := []any{}
	for _, b := range blocks {
		switch b.Type {
		case ir.BlockText:
			out = append(out, map[string]any{"text": b.Text})
		case ir.BlockThinking:
			if b.Thinking == nil {
				continue
			}
			p := map[string]any{"text": b.Thinking.Text, "thought": true}
			if b.Thinking.Signature != "" {
				p["thoughtSignature"] = b.Thinking.Signature
			}
			out = append(out, p)
		case ir.BlockToolUse:
			if b.ToolUse == nil {
				continue
			}
			args := b.ToolUse.Input
			if len(args) == 0 {
				args = json.RawMessage(`{}`)
			}
			call := map[string]any{"name": b.ToolUse.Name, "args": args}
			if b.ToolUse.ID != "" {
				call["id"] = b.ToolUse.ID
			}
			out = append(out, map[string]any{"functionCall": call})
		}
	}
	return out
}

func usageBody(u ir.Usage) map[string]any {
	return map[string]any{
		"promptTokenCount":        u.InputTokens,
		"candidatesTokenCount":    u.OutputTokens,
		"cachedContentTokenCount": u.CacheReadTokens,
		"thoughtsTokenCount":      u.ReasoningTokens,
		"totalTokenCount":         u.InputTokens + u.OutputTokens,
	}
}

// candidate builds the single candidate Darkrouter ever returns. Routing picks
// one target and one completion; there is no n>1 concept to surface.
func candidate(blocks []ir.ContentBlock, stop ir.StopReason) map[string]any {
	return map[string]any{
		"content":      map[string]any{"role": "model", "parts": responseParts(blocks)},
		"finishReason": finishReasonWire(stop),
		"index":        0,
	}
}

func WriteResponse(w http.ResponseWriter, resp *ir.Response) error {
	out := map[string]any{
		"candidates":    []any{candidate(resp.Content, resp.StopReason)},
		"usageMetadata": usageBody(resp.Usage),
		"modelVersion":  resp.Model,
		"responseId":    resp.ID,
	}
	w.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(out)
}

// errorShape maps a canonical error onto Google's status string and code.
// Gemini CLI switches on the string, and UNAVAILABLE and RESOURCE_EXHAUSTED are
// the two it retries.
func errorShape(t ir.ErrorType) (string, int) {
	switch t {
	case ir.ErrInvalidRequest, ir.ErrContentFilter:
		return "INVALID_ARGUMENT", http.StatusBadRequest
	case ir.ErrAuthentication:
		return "UNAUTHENTICATED", http.StatusUnauthorized
	case ir.ErrPermission:
		return "PERMISSION_DENIED", http.StatusForbidden
	case ir.ErrNotFound:
		return "NOT_FOUND", http.StatusNotFound
	case ir.ErrPayloadTooLarge:
		// Same reasoning as Anthropic: the google.rpc.Code vocabulary is fixed,
		// so the status carries what the code cannot.
		return "INVALID_ARGUMENT", http.StatusRequestEntityTooLarge
	case ir.ErrRateLimit:
		return "RESOURCE_EXHAUSTED", http.StatusTooManyRequests
	case ir.ErrOverloaded:
		return "UNAVAILABLE", http.StatusServiceUnavailable
	default:
		// 500 rather than the 502 the other dialects use: Google's vocabulary
		// has no gateway status, and a Gemini client would not recognize one.
		return "INTERNAL", http.StatusInternalServerError
	}
}

func WriteError(w http.ResponseWriter, e *ir.Error) error {
	name, status := errorShape(e.Type)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	return json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{"code": status, "message": e.Message, "status": name},
	})
}

// WriteCount answers :countTokens. totalBillableCharacters is omitted: it is
// deprecated for text models and Darkrouter has no honest value for it.
func (d *Dialect) WriteCount(w http.ResponseWriter, tokens int) error {
	w.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(map[string]any{"totalTokens": tokens})
}

var _ edge.CountWriter = (*Dialect)(nil)
