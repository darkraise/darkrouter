package openaicompat

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

type wireUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	PromptDetails    struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
	CompletionDetails struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
}

func (u *wireUsage) toIR() ir.Usage {
	return ir.Usage{
		InputTokens:     u.PromptTokens,
		OutputTokens:    u.CompletionTokens,
		CacheReadTokens: u.PromptDetails.CachedTokens,
		ReasoningTokens: u.CompletionDetails.ReasoningTokens,
	}
}

func stopReason(s string) ir.StopReason {
	switch s {
	case "length":
		return ir.StopMaxTokens
	case "tool_calls", "function_call":
		return ir.StopToolUse
	case "content_filter":
		return ir.StopContentFilter
	default:
		return ir.StopEndTurn
	}
}

func ParseResponse(resp *http.Response) (*ir.Response, error) {
	defer resp.Body.Close()
	var w struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content   string `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Function struct {
						Name      string          `json:"name"`
						Arguments json.RawMessage `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage wireUsage `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&w); err != nil {
		return nil, err
	}
	out := &ir.Response{ID: w.ID, Model: w.Model, Usage: w.Usage.toIR()}
	if len(w.Choices) > 0 {
		c := w.Choices[0]
		out.StopReason = stopReason(c.FinishReason)
		if c.Message.Content != "" {
			out.Content = append(out.Content, ir.ContentBlock{Type: ir.BlockText, Text: c.Message.Content})
		}
		for _, tc := range c.Message.ToolCalls {
			out.Content = append(out.Content, ir.ContentBlock{
				Type: ir.BlockToolUse,
				ToolUse: &ir.ToolUse{
					ID: tc.ID, Name: tc.Function.Name, Input: tc.Function.Arguments,
				},
			})
		}
	}
	return out, nil
}

type wireChunk struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Delta struct {
			Role      string `json:"role"`
			Content   string `json:"content"`
			Reasoning string `json:"reasoning_content"`
			ToolCalls []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *wireUsage `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error"`
}

// toolBlockBase keeps tool blocks in an index space that cannot collide with
// the text block's.
const toolBlockBase = 1000

// reasoningBlockBase does for reasoning what toolBlockBase does for tool calls.
// A block index identifies a block, and a consumer that keys items by index —
// the responses stream writer is the first — cannot tell a thinking delta from
// a text delta when both arrive at zero.
const reasoningBlockBase = 2000

// ParseStream reconstructs block structure from OpenAI's flat deltas. The state
// machine opens a block when a delta first carries a given kind and closes it
// when the stream ends or a finish reason arrives. Tool calls are indexed, so
// each index accumulates into its own block.
func ParseStream(r io.Reader, maxLine int) iter.Seq2[ir.StreamEvent, error] {
	return func(yield func(ir.StreamEvent, error) bool) {
		reader := sse.NewReader(r, maxLine)
		open := make(map[int]bool)
		textIdx := -1
		reasoningIdx := -1
		started := false

		// closeAll emits stops in ascending index order so the event sequence is
		// deterministic; map iteration order is not.
		closeAll := func() bool {
			idxs := make([]int, 0, len(open))
			for idx := range open {
				idxs = append(idxs, idx)
			}
			sort.Ints(idxs)
			for _, idx := range idxs {
				if !yield(ir.StreamEvent{Type: ir.EventBlockStop, Index: idx}, nil) {
					return false
				}
			}
			open = map[int]bool{}
			textIdx = -1
			reasoningIdx = -1
			return true
		}

		for {
			ev, err := reader.Next()
			if errors.Is(err, io.EOF) {
				closeAll()
				return
			}
			if err != nil {
				yield(ir.StreamEvent{}, err)
				return
			}
			if ev.Data == sse.Done {
				closeAll()
				return
			}
			var c wireChunk
			if err := json.Unmarshal([]byte(ev.Data), &c); err != nil {
				continue // a chunk we cannot parse is not a reason to kill the stream
			}
			if c.Error != nil {
				yield(ir.StreamEvent{}, &ir.Error{
					Type: ir.ErrAPI, Message: c.Error.Message, Code: c.Error.Code,
				})
				return
			}
			if !started {
				started = true
				if !yield(ir.StreamEvent{
					Type: ir.EventMessageStart, ID: c.ID, Model: c.Model,
				}, nil) {
					return
				}
			}
			if c.Usage != nil {
				u := c.Usage.toIR()
				if !yield(ir.StreamEvent{Type: ir.EventMessageDelta, Usage: &u}, nil) {
					return
				}
			}
			for _, ch := range c.Choices {
				d := ch.Delta
				if d.Content != "" {
					if textIdx < 0 {
						textIdx = 0
						open[textIdx] = true
						if !yield(ir.StreamEvent{Type: ir.EventBlockStart, Index: textIdx,
							Delta: &ir.Delta{Type: ir.BlockText}}, nil) {
							return
						}
					}
					if !yield(ir.StreamEvent{Type: ir.EventContentDelta, Index: textIdx,
						Delta: &ir.Delta{Type: ir.BlockText, Text: d.Content}}, nil) {
						return
					}
				}
				if d.Reasoning != "" {
					if reasoningIdx < 0 {
						reasoningIdx = reasoningBlockBase
						open[reasoningIdx] = true
						if !yield(ir.StreamEvent{Type: ir.EventBlockStart, Index: reasoningIdx,
							Delta: &ir.Delta{Type: ir.BlockThinking}}, nil) {
							return
						}
					}
					if !yield(ir.StreamEvent{Type: ir.EventContentDelta, Index: reasoningIdx,
						Delta: &ir.Delta{Type: ir.BlockThinking, Thinking: d.Reasoning}}, nil) {
						return
					}
				}
				for _, tc := range d.ToolCalls {
					idx := toolBlockBase + tc.Index
					if !open[idx] {
						open[idx] = true
						if !yield(ir.StreamEvent{Type: ir.EventBlockStart, Index: idx,
							Delta: &ir.Delta{Type: ir.BlockToolUse, ToolID: tc.ID, ToolName: tc.Function.Name}}, nil) {
							return
						}
					}
					if !yield(ir.StreamEvent{Type: ir.EventContentDelta, Index: idx,
						Delta: &ir.Delta{
							Type: ir.BlockToolUse, ToolID: tc.ID,
							ToolName: tc.Function.Name, ToolInput: tc.Function.Arguments,
						}}, nil) {
						return
					}
				}
				if ch.FinishReason != nil {
					if !closeAll() {
						return
					}
					if !yield(ir.StreamEvent{Type: ir.EventMessageStop,
						StopReason: stopReason(*ch.FinishReason)}, nil) {
						return
					}
				}
			}
		}
	}
}
