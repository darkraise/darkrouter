package gemini

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"

	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/sse"
)

var errStreamTruncated = fmt.Errorf("upstream stream ended before a finish reason: %w", io.ErrUnexpectedEOF)

// ParseStream reads Gemini's alt=sse stream.
//
// Chunks are incremental: every chunk carries new content to append. Text parts
// are fragments and structural parts arrive whole, so the parts are walked once
// and emitted as deltas. Diffing successive chunks — which an earlier draft of
// the spec described — produces garbage against these semantics.
func ParseStream(r io.Reader, maxLine int) iter.Seq2[ir.StreamEvent, error] {
	return func(yield func(ir.StreamEvent, error) bool) {
		reader := sse.NewReader(r, maxLine)
		var (
			started    bool
			ids        *callIDs
			textIdx    = -1
			thoughtIdx = -1
			nextIdx    int
			open       ir.OpenBlocks
			// hasCall spans the whole candidate: the call arrives in one
			// chunk and the finish reason in a later one, and STOP on that
			// later chunk means tool_use only if this remembers the call.
			hasCall      bool
			droppedMedia bool
		)

		closeAll := func() bool {
			textIdx, thoughtIdx = -1, -1
			return open.CloseAll(yield)
		}

		// openBlock returns the index for a persistent kind, opening it once.
		openBlock := func(slot *int, kind ir.BlockType) (int, bool) {
			if *slot >= 0 {
				return *slot, true
			}
			*slot = nextIdx
			nextIdx++
			open.Open(*slot)
			return *slot, yield(ir.StreamEvent{
				Type: ir.EventBlockStart, Index: *slot, Delta: &ir.Delta{Type: kind},
			}, nil)
		}

		for {
			raw, err := reader.Next()
			if errors.Is(err, io.EOF) {
				// A finish reason returns from the loop, so reaching the end
				// of the body means the candidate never finished.
				yield(ir.StreamEvent{}, errStreamTruncated)
				return
			}
			if err != nil {
				yield(ir.StreamEvent{}, err)
				return
			}
			if raw.Data == "" || raw.Data == sse.Done {
				continue
			}

			var chunk wireResponse
			if json.Unmarshal([]byte(raw.Data), &chunk) != nil {
				continue // a chunk we cannot parse is not a reason to kill the stream
			}
			if chunk.Error != nil {
				// Quota exhaustion arrives this way under a 200 once the
				// stream has opened; the status line is no help by then.
				yield(ir.StreamEvent{}, chunk.Error.toIR())
				return
			}

			if len(chunk.Candidates) == 0 && chunk.PromptFeedback != nil &&
				chunk.PromptFeedback.BlockReason != "" {
				yield(ir.StreamEvent{}, &ir.Error{
					Type:    ir.ErrContentFilter,
					Message: "the prompt was blocked: " + chunk.PromptFeedback.BlockReason,
					Code:    chunk.PromptFeedback.BlockReason,
				})
				return
			}

			if !started {
				started = true
				ids = newCallIDs(chunk.ResponseID)
				if !yield(ir.StreamEvent{
					Type: ir.EventMessageStart, ID: chunk.ResponseID, Model: chunk.ModelVersion,
				}, nil) {
					return
				}
			}

			if chunk.UsageMetadata.TotalTokenCount > 0 || chunk.UsageMetadata.PromptTokenCount > 0 {
				u := chunk.UsageMetadata.toIR()
				if !yield(ir.StreamEvent{Type: ir.EventMessageDelta, Usage: &u}, nil) {
					return
				}
			}
			if len(chunk.Candidates) == 0 {
				continue
			}

			c := chunk.Candidates[0]
			for _, p := range c.Content.Parts {
				switch {
				case p.FunctionCall != nil:
					hasCall = true
					args := p.FunctionCall.Args
					if len(args) == 0 {
						args = json.RawMessage(`{}`)
					}
					idx := nextIdx
					nextIdx++
					d := &ir.Delta{
						Type:   ir.BlockToolUse,
						ToolID: ids.next(p.FunctionCall.ID), ToolName: p.FunctionCall.Name,
					}
					if !yield(ir.StreamEvent{Type: ir.EventBlockStart, Index: idx, Delta: d}, nil) {
						return
					}
					full := &ir.Delta{
						Type: ir.BlockToolUse, ToolID: d.ToolID, ToolName: d.ToolName,
						ToolInput: string(args), Signature: p.ThoughtSignature,
					}
					if !yield(ir.StreamEvent{Type: ir.EventContentDelta, Index: idx, Delta: full}, nil) {
						return
					}
					if !yield(ir.StreamEvent{Type: ir.EventBlockStop, Index: idx}, nil) {
						return
					}

				case p.InlineData != nil:
					// The IR delta has no slot for media and no client writer
					// renders it, so generated media cannot reach a translated
					// client; it is dropped, but never silently.
					droppedMedia = true

				case p.Thought:
					idx, ok := openBlock(&thoughtIdx, ir.BlockThinking)
					if !ok {
						return
					}
					// Text and signature go out as two deltas, text first, as
					// Anthropic streams them: its writer has no event carrying
					// both and would keep only the signature. A signature-only
					// delta is not content-bearing, so a signature alone never
					// commits the response.
					if p.Text != "" {
						d := &ir.Delta{Type: ir.BlockThinking, Thinking: p.Text}
						if !yield(ir.StreamEvent{Type: ir.EventContentDelta, Index: idx, Delta: d}, nil) {
							return
						}
					}
					if p.ThoughtSignature != "" || p.Text == "" {
						d := &ir.Delta{Type: ir.BlockThinking, Signature: p.ThoughtSignature}
						if !yield(ir.StreamEvent{Type: ir.EventContentDelta, Index: idx, Delta: d}, nil) {
							return
						}
					}

				case p.Text != "" || p.ThoughtSignature != "":
					idx, ok := openBlock(&textIdx, ir.BlockText)
					if !ok {
						return
					}
					if !yield(ir.StreamEvent{
						Type: ir.EventContentDelta, Index: idx,
						Delta: &ir.Delta{Type: ir.BlockText, Text: p.Text, Signature: p.ThoughtSignature},
					}, nil) {
						return
					}
				}
			}

			if c.FinishReason != "" {
				if !closeAll() {
					return
				}
				sr, known := finishReason(c.FinishReason, hasCall)
				stop := ir.StreamEvent{Type: ir.EventMessageStop, StopReason: sr}
				if !known {
					// Spec §4.5: degrade, but never silently. The unary path
					// puts this on ir.Response.Warnings; streaming has its own
					// channel for exactly this reason.
					stop.Warnings = append(stop.Warnings, ir.Warning{
						Field: "finishReason", Target: targetName,
						Reason: "unrecognized value " + c.FinishReason + "; reported as end_turn",
					})
				}
				if droppedMedia {
					stop.Warnings = append(stop.Warnings, ir.Warning{
						Field: "candidates[].content.parts[].inlineData", Target: targetName,
						Reason: "generated media cannot be streamed to this client; it was dropped",
					})
				}
				if !yield(stop, nil) {
					return
				}
				return
			}
		}
	}
}
