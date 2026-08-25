package openai

import (
	"encoding/json"
	"errors"
	"iter"
	"net/http"

	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/sse"
)

func chunk(id, model string, delta map[string]any, finish any) map[string]any {
	if id == "" {
		id = "chatcmpl-darkrouter"
	}
	return map[string]any{
		"id":      id,
		"object":  "chat.completion.chunk",
		"created": now().Unix(),
		"model":   model,
		"choices": []any{map[string]any{
			"index":         0,
			"delta":         delta,
			"finish_reason": finish,
		}},
	}
}

// WriteStream converts canonical stream events into OpenAI chunks. An error
// yielded by the sequence is terminal: it becomes an in-stream error and the
// stream still ends with the DONE sentinel, because a client that has already
// received content cannot be given a different response.
func WriteStream(w http.ResponseWriter, events iter.Seq2[ir.StreamEvent, error]) error {
	s := sse.NewWriter(w)
	send := func(v any) error {
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		return s.Send("", string(b))
	}

	// id and model arrive on message_start; without capturing them every chunk
	// would report an empty model, which clients surface to users.
	var id, model string
	// OpenAI numbers tool calls densely from zero in the order they open. The
	// IR block index is not that number — the openaicompat adapter offsets tool
	// blocks by 1000 to keep them clear of the text block — so the two are
	// mapped rather than passed through.
	toolIndex := map[int]int{}
	var sendErr error
	for ev, err := range events {
		if err != nil {
			var e *ir.Error
			if !errors.As(err, &e) {
				e = &ir.Error{Type: ir.ErrAPI, Message: err.Error()}
			}
			sendErr = send(map[string]any{"error": map[string]any{
				"message": e.Message, "type": string(e.Type), "code": e.Code,
			}})
			break
		}
		switch ev.Type {
		case ir.EventPing, ir.EventBlockStop:
			continue
		case ir.EventMessageStart:
			id, model = ev.ID, ev.Model
			sendErr = send(chunk(id, model, map[string]any{"role": "assistant"}, nil))
		case ir.EventBlockStart:
			if ev.Delta == nil || ev.Delta.Type != ir.BlockToolUse {
				continue
			}
			idx := len(toolIndex)
			toolIndex[ev.Index] = idx
			sendErr = send(chunk(id, model, map[string]any{
				"tool_calls": []any{map[string]any{
					"index": idx, "id": ev.Delta.ToolID, "type": "function",
					"function": map[string]any{"name": ev.Delta.ToolName, "arguments": ""},
				}},
			}, nil))
		case ir.EventContentDelta:
			if ev.Delta == nil {
				continue
			}
			switch ev.Delta.Type {
			case ir.BlockText:
				sendErr = send(chunk(id, model, map[string]any{"content": ev.Delta.Text}, nil))
			case ir.BlockThinking:
				if ev.Delta.Thinking == "" {
					continue
				}
				sendErr = send(chunk(id, model, map[string]any{"reasoning_content": ev.Delta.Thinking}, nil))
			case ir.BlockToolUse:
				if ev.Delta.ToolInput == "" {
					continue
				}
				idx, ok := toolIndex[ev.Index]
				if !ok {
					// A provider that streams arguments without ever opening the
					// block still has to reach the client, so the block is
					// opened here rather than dropping the call.
					idx = len(toolIndex)
					toolIndex[ev.Index] = idx
					if err := send(chunk(id, model, map[string]any{
						"tool_calls": []any{map[string]any{
							"index": idx, "id": ev.Delta.ToolID, "type": "function",
							"function": map[string]any{"name": ev.Delta.ToolName, "arguments": ""},
						}},
					}, nil)); err != nil {
						return err
					}
				}
				// No id on a continuation: repeating it makes some clients open
				// a second call.
				sendErr = send(chunk(id, model, map[string]any{
					"tool_calls": []any{map[string]any{
						"index":    idx,
						"function": map[string]any{"arguments": ev.Delta.ToolInput},
					}},
				}, nil))
			default:
				continue
			}
		case ir.EventMessageDelta:
			if ev.Usage == nil {
				continue
			}
			c := chunk(id, model, map[string]any{}, nil)
			c["usage"] = map[string]any{
				"prompt_tokens":     ev.Usage.PromptTokens(),
				"completion_tokens": ev.Usage.OutputTokens,
				"total_tokens":      ev.Usage.PromptTokens() + ev.Usage.OutputTokens,
			}
			sendErr = send(c)
		case ir.EventMessageStop:
			sendErr = send(chunk(id, model, map[string]any{}, finishReason(ev.StopReason)))
		}
		if sendErr != nil {
			return sendErr
		}
	}
	if sendErr != nil {
		return sendErr
	}
	return s.SendDone()
}
