package anthropic

import (
	"encoding/json"
	"errors"
	"iter"
	"net/http"
	"sort"

	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/sse"
)

// blockStartBody describes an opening block. Anthropic requires the shape to
// match the kind: a tool_use block carries id, name, and an empty input object.
func blockStartBody(d *ir.Delta) map[string]any {
	if d == nil {
		return map[string]any{"type": "text", "text": ""}
	}
	// A block the Anthropic adapter carried through untouched goes back as
	// it arrived.
	if len(d.Extra) > 0 {
		m := make(map[string]any, len(d.Extra))
		for k, v := range d.Extra {
			m[k] = v
		}
		return m
	}
	switch d.Type {
	case ir.BlockThinking:
		return map[string]any{"type": "thinking", "thinking": "", "signature": ""}
	case ir.BlockRedactedThinking:
		return map[string]any{"type": "redacted_thinking", "data": ""}
	case ir.BlockToolUse:
		return map[string]any{
			"type": "tool_use", "id": d.ToolID, "name": d.ToolName, "input": map[string]any{},
		}
	default:
		return map[string]any{"type": "text", "text": ""}
	}
}

// deltaBody names the delta kind Anthropic uses for each block kind. A
// signature arrives on its own event and carries no text.
func deltaBody(d *ir.Delta) map[string]any {
	switch d.Type {
	case ir.BlockThinking:
		if d.Signature != "" {
			return map[string]any{"type": "signature_delta", "signature": d.Signature}
		}
		return map[string]any{"type": "thinking_delta", "thinking": d.Thinking}
	case ir.BlockToolUse:
		return map[string]any{"type": "input_json_delta", "partial_json": d.ToolInput}
	default:
		return map[string]any{"type": "text_delta", "text": d.Text}
	}
}

// WriteStream renders IR events as Anthropic's SSE event model. Anthropic
// sends no [DONE] sentinel: message_stop ends the stream.
func WriteStream(w http.ResponseWriter, events iter.Seq2[ir.StreamEvent, error]) error {
	s := sse.NewWriter(w)
	send := func(typ string, body map[string]any) error {
		body["type"] = typ
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		return s.Send(typ, string(b))
	}

	var (
		id, model string
		usage     ir.Usage
		started   bool
		wireOf    = map[int]int{}
		stop      = ir.StopEndTurn
	)

	// start is held back until the first usage update or the first content,
	// whichever comes first: an Anthropic-served route reports real input
	// tokens inside message_start, and a route whose dialect reports usage
	// last is not delayed waiting for it.
	start := func() error {
		if started {
			return nil
		}
		started = true
		return send("message_start", map[string]any{"message": map[string]any{
			"id": messageID(id), "type": "message", "role": "assistant", "model": model,
			"content": []any{}, "stop_reason": nil, "stop_sequence": nil,
			"usage": usageBody(usage),
		}})
	}

	openBlock := func(irIdx int, d *ir.Delta) (int, error) {
		if wire, ok := wireOf[irIdx]; ok {
			return wire, nil
		}
		if err := start(); err != nil {
			return 0, err
		}
		wire := len(wireOf)
		wireOf[irIdx] = wire
		body := blockStartBody(d)
		return wire, send("content_block_start", map[string]any{
			"index": wire, "content_block": body,
		})
	}

	closeAll := func() error {
		// Ascending wire order, so the sequence is deterministic; map iteration
		// order is not.
		wires := make([]int, 0, len(wireOf))
		for _, wire := range wireOf {
			wires = append(wires, wire)
		}
		sort.Ints(wires)
		for _, wire := range wires {
			if err := send("content_block_stop", map[string]any{"index": wire}); err != nil {
				return err
			}
		}
		wireOf = map[int]int{}
		return nil
	}

	for ev, err := range events {
		if err != nil {
			if serr := start(); serr != nil {
				return serr
			}
			var e *ir.Error
			if !errors.As(err, &e) {
				e = &ir.Error{Type: ir.ErrAPI, Message: err.Error()}
			}
			name, _ := errorShape(e.Type)
			// Spec §4.9: Anthropic's shape is a real error event, and the
			// stream ends there — no message_stop, which would claim success.
			return send("error", map[string]any{
				"error": map[string]any{"type": name, "message": e.Message},
			})
		}

		switch ev.Type {
		case ir.EventMessageStart:
			id, model = ev.ID, ev.Model

		case ir.EventPing:
			if err := start(); err != nil {
				return err
			}
			if err := send("ping", map[string]any{}); err != nil {
				return err
			}

		case ir.EventBlockStart:
			if _, err := openBlock(ev.Index, ev.Delta); err != nil {
				return err
			}

		case ir.EventContentDelta:
			if ev.Delta == nil {
				continue
			}
			// An orphan delta opens its own block: OpenAI's flat model has
			// nothing to open, and a client rejects a delta for an index it
			// never saw start.
			wire, err := openBlock(ev.Index, ev.Delta)
			if err != nil {
				return err
			}
			// Anthropic has no delta form for a redacted payload — it ships
			// whole inside the block start — so there is nothing valid to
			// send for one that arrived incrementally.
			if ev.Delta.Type == ir.BlockRedactedThinking {
				continue
			}
			if err := send("content_block_delta", map[string]any{
				"index": wire, "delta": deltaBody(ev.Delta),
			}); err != nil {
				return err
			}

		case ir.EventBlockStop:
			wire, ok := wireOf[ev.Index]
			if !ok {
				continue
			}
			delete(wireOf, ev.Index)
			if err := send("content_block_stop", map[string]any{"index": wire}); err != nil {
				return err
			}

		case ir.EventMessageDelta:
			if ev.Usage != nil {
				usage = *ev.Usage
				if err := start(); err != nil {
					return err
				}
			}
			if ev.StopReason != "" {
				stop = ev.StopReason
			}

		case ir.EventMessageStop:
			if ev.StopReason != "" {
				stop = ev.StopReason
			}
			if err := start(); err != nil {
				return err
			}
			if err := closeAll(); err != nil {
				return err
			}
			if err := send("message_delta", map[string]any{
				"delta": map[string]any{"stop_reason": stopReasonWire(stop), "stop_sequence": nil},
				"usage": usageBody(usage),
			}); err != nil {
				return err
			}
			return send("message_stop", map[string]any{})
		}
	}

	// The sequence ended without a message_stop. Close what is open and end the
	// message anyway, or the client waits forever.
	if err := start(); err != nil {
		return err
	}
	if err := closeAll(); err != nil {
		return err
	}
	if err := send("message_delta", map[string]any{
		"delta": map[string]any{"stop_reason": stopReasonWire(stop), "stop_sequence": nil},
		"usage": usageBody(usage),
	}); err != nil {
		return err
	}
	return send("message_stop", map[string]any{})
}
