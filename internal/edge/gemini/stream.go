package gemini

import (
	"encoding/json"
	"errors"
	"io"
	"iter"
	"net/http"
	"sort"

	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/sse"
)

// chunkWriter frames chunks in whichever form the client asked for. Both flush
// per chunk: buffering the array form turns time-to-first-token into
// time-to-completion.
type chunkWriter struct {
	sse   *sse.Writer
	w     http.ResponseWriter
	rc    *http.ResponseController
	first bool
}

func newChunkWriter(w http.ResponseWriter, asSSE bool) *chunkWriter {
	if asSSE {
		return &chunkWriter{sse: sse.NewWriter(w), first: true}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Accel-Buffering", "no")
	return &chunkWriter{w: w, rc: http.NewResponseController(w), first: true}
}

func (c *chunkWriter) send(v map[string]any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if c.sse != nil {
		c.first = false
		return c.sse.Send("", string(b))
	}
	prefix := ","
	if c.first {
		prefix = "["
		c.first = false
	}
	if _, err := io.WriteString(c.w, prefix); err != nil {
		return err
	}
	if _, err := c.w.Write(b); err != nil {
		return err
	}
	_ = c.rc.Flush()
	return nil
}

// close finishes the array form. Gemini's SSE has no terminator at all — no
// [DONE], no final event — so the SSE path closes with nothing.
func (c *chunkWriter) close() error {
	if c.sse != nil {
		return nil
	}
	body := "]"
	if c.first {
		body = "[]"
	}
	_, err := io.WriteString(c.w, body)
	_ = c.rc.Flush()
	return err
}

// pendingCall accumulates a tool call. Gemini sends functionCall whole in one
// chunk, while the IR carries its arguments as fragments whenever the upstream
// was OpenAI-compatible.
type pendingCall struct {
	id   string
	name string
	args string
	sig  string
}

func writeStream(w http.ResponseWriter, events iter.Seq2[ir.StreamEvent, error], asSSE bool) error {
	cw := newChunkWriter(w, asSSE)

	var (
		model   string
		usage   ir.Usage
		stop    = ir.StopEndTurn
		calls   = map[int]*pendingCall{}
		sendErr error
	)

	partChunk := func(parts []any) error {
		return cw.send(map[string]any{
			"candidates": []any{map[string]any{
				"content": map[string]any{"role": "model", "parts": parts},
				"index":   0,
			}},
			"modelVersion": model,
		})
	}

	flushCall := func(idx int) error {
		pc, ok := calls[idx]
		if !ok {
			return nil
		}
		delete(calls, idx)
		args := json.RawMessage(pc.args)
		if len(args) == 0 {
			args = json.RawMessage(`{}`)
		}
		call := map[string]any{"name": pc.name, "args": args}
		if pc.id != "" {
			call["id"] = pc.id
		}
		p := map[string]any{"functionCall": call}
		if pc.sig != "" {
			p["thoughtSignature"] = pc.sig
		}
		return partChunk([]any{p})
	}

	flushAllCalls := func() error {
		idxs := make([]int, 0, len(calls))
		for idx := range calls {
			idxs = append(idxs, idx)
		}
		sort.Ints(idxs)
		for _, idx := range idxs {
			if err := flushCall(idx); err != nil {
				return err
			}
		}
		return nil
	}

	terminal := func(reason string) error {
		return cw.send(map[string]any{
			"candidates": []any{map[string]any{
				"content":      map[string]any{"role": "model", "parts": []any{}},
				"finishReason": reason,
				"index":        0,
			}},
			"usageMetadata": usageBody(usage),
			"modelVersion":  model,
		})
	}

	for ev, err := range events {
		if err != nil {
			var e *ir.Error
			if !errors.As(err, &e) {
				e = &ir.Error{Type: ir.ErrAPI, Message: err.Error()}
			}
			// Spec §4.9: Gemini's SSE defines no error event, so a post-commit
			// failure becomes a terminal chunk carrying a promptFeedback-shaped
			// object. Only a real content filter reports SAFETY.
			reason := "OTHER"
			if e.Type == ir.ErrContentFilter {
				reason = "SAFETY"
			}
			if serr := cw.send(map[string]any{
				"candidates": []any{map[string]any{
					"content":      map[string]any{"role": "model", "parts": []any{}},
					"finishReason": reason,
					"index":        0,
				}},
				"promptFeedback": map[string]any{
					"blockReason":        reason,
					"blockReasonMessage": e.Message,
				},
				"usageMetadata": usageBody(usage),
				"modelVersion":  model,
			}); serr != nil {
				return serr
			}
			return cw.close()
		}

		switch ev.Type {
		case ir.EventMessageStart:
			model = ev.Model

		case ir.EventBlockStart:
			if ev.Delta != nil && ev.Delta.Type == ir.BlockToolUse {
				calls[ev.Index] = &pendingCall{id: ev.Delta.ToolID, name: ev.Delta.ToolName}
			}

		case ir.EventContentDelta:
			if ev.Delta == nil {
				continue
			}
			switch ev.Delta.Type {
			case ir.BlockText:
				if ev.Delta.Text == "" && ev.Delta.Signature == "" {
					continue
				}
				p := map[string]any{"text": ev.Delta.Text}
				if ev.Delta.Signature != "" {
					p["thoughtSignature"] = ev.Delta.Signature
				}
				sendErr = partChunk([]any{p})
			case ir.BlockThinking:
				p := map[string]any{"text": ev.Delta.Thinking, "thought": true}
				if ev.Delta.Signature != "" {
					p["thoughtSignature"] = ev.Delta.Signature
				}
				sendErr = partChunk([]any{p})
			case ir.BlockToolUse:
				pc, ok := calls[ev.Index]
				if !ok {
					// A provider that streams arguments without opening a block
					// still has to reach the client.
					pc = &pendingCall{id: ev.Delta.ToolID, name: ev.Delta.ToolName}
					calls[ev.Index] = pc
				}
				if pc.name == "" {
					pc.name = ev.Delta.ToolName
				}
				if pc.id == "" {
					pc.id = ev.Delta.ToolID
				}
				if ev.Delta.Signature != "" {
					pc.sig = ev.Delta.Signature
				}
				pc.args += ev.Delta.ToolInput
			}

		case ir.EventBlockStop:
			sendErr = flushCall(ev.Index)

		case ir.EventMessageDelta:
			if ev.Usage != nil {
				usage = *ev.Usage
			}
			if ev.StopReason != "" {
				stop = ev.StopReason
			}

		case ir.EventMessageStop:
			if ev.StopReason != "" {
				stop = ev.StopReason
			}
			if err := flushAllCalls(); err != nil {
				return err
			}
			if err := terminal(finishReasonWire(stop)); err != nil {
				return err
			}
			return cw.close()
		}

		if sendErr != nil {
			return sendErr
		}
	}

	// The sequence ended without a message_stop. Flush and terminate anyway, or
	// the array form is never closed and the client sees truncated JSON.
	if err := flushAllCalls(); err != nil {
		return err
	}
	if err := terminal(finishReasonWire(stop)); err != nil {
		return err
	}
	return cw.close()
}
