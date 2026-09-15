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
		// malformed records a call dropped for unparseable arguments.
		malformed bool
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
		// Arguments cut off mid-object, typically by an output limit, cannot
		// be rendered as a functionCall; failing the marshal would leave the
		// array form unterminated, so the call is dropped and the finish
		// reason says why.
		if !json.Valid(args) {
			malformed = true
			return nil
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

	// The IR carries a thought's signature on a delta after its text, while
	// Gemini requires the signature back on the exact part it came with. The
	// latest thought part is held for one event so a signature that follows
	// can rejoin it.
	var (
		held    map[string]any
		heldIdx int
	)
	flushThought := func() error {
		if held == nil {
			return nil
		}
		p := held
		held = nil
		return partChunk([]any{p})
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
			if ferr := flushThought(); ferr != nil {
				return ferr
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

		if d := ev.Delta; held != nil && ev.Type == ir.EventContentDelta && d != nil &&
			d.Type == ir.BlockThinking && d.Thinking == "" && d.Signature != "" && ev.Index == heldIdx {
			held["thoughtSignature"] = d.Signature
			if err := flushThought(); err != nil {
				return err
			}
			continue
		}
		if err := flushThought(); err != nil {
			return err
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
					sendErr = partChunk([]any{p})
				} else {
					held, heldIdx = p, ev.Index
				}
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
			// The terminal chunk waits for the sequence to end: OpenAI-compatible
			// and Bedrock upstreams report usage after their stop, and the
			// terminal chunk has to carry it.
			if ev.StopReason != "" {
				stop = ev.StopReason
			}
			sendErr = flushAllCalls()
		}

		if sendErr != nil {
			return sendErr
		}
	}

	// Terminate once the sequence ends, whether or not a message_stop arrived:
	// without it the array form is never closed and the client sees truncated
	// JSON.
	if err := flushThought(); err != nil {
		return err
	}
	if err := flushAllCalls(); err != nil {
		return err
	}
	reason := finishReasonWire(stop)
	// MAX_TOKENS stays: it names the cause, and a client acts on it by
	// raising the limit.
	if malformed && stop != ir.StopMaxTokens {
		reason = "MALFORMED_FUNCTION_CALL"
	}
	if err := terminal(reason); err != nil {
		return err
	}
	return cw.close()
}
