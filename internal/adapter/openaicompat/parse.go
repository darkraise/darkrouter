package openaicompat

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"iter"
	"net/http"
	"sort"
	"strconv"
	"strings"

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
	// prompt_tokens includes the cached subset. Subtracting it here is what
	// lets every provider's InputTokens mean the same thing downstream.
	in := u.PromptTokens - u.PromptDetails.CachedTokens
	if in < 0 {
		in = 0
	}
	return ir.Usage{
		InputTokens:     in,
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
	return decodeResponse(resp.Body)
}

func decodeResponse(r io.Reader) (*ir.Response, error) {
	var w struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content          json.RawMessage `json:"content"`
				ReasoningContent string          `json:"reasoning_content"`
				// Reasoning is OpenRouter's spelling of the same field.
				Reasoning string `json:"reasoning"`
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
	if err := json.NewDecoder(r).Decode(&w); err != nil {
		return nil, err
	}
	out := &ir.Response{ID: w.ID, Model: w.Model, Usage: w.Usage.toIR()}
	if len(w.Choices) > 1 {
		out.Warnings = append(out.Warnings, ir.Warning{
			Field: "choices", Target: targetName,
			Reason: "the upstream returned " + strconv.Itoa(len(w.Choices)) + " choices; only the first is carried",
		})
	}
	if len(w.Choices) > 0 {
		c := w.Choices[0]
		out.StopReason = stopReason(c.FinishReason)
		if thought := c.Message.ReasoningContent + c.Message.Reasoning; thought != "" {
			out.Content = append(out.Content, ir.ContentBlock{
				Type: ir.BlockThinking, Thinking: &ir.Thinking{Text: thought},
			})
		}
		if text := contentText(c.Message.Content); text != "" {
			out.Content = append(out.Content, ir.ContentBlock{Type: ir.BlockText, Text: text})
		}
		for _, tc := range c.Message.ToolCalls {
			out.Content = append(out.Content, ir.ContentBlock{
				Type: ir.BlockToolUse,
				ToolUse: &ir.ToolUse{
					ID: tc.ID, Name: tc.Function.Name, Input: toolArguments(tc.Function.Arguments),
				},
			})
		}
	}
	return out, nil
}

// contentText reads a message's content in either of its wire forms. A string
// is the common case; some compatible upstreams answer with the multi-part
// array the request form allows, whose text parts are concatenated.
func contentText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) != nil {
		return ""
	}
	var b strings.Builder
	for _, p := range parts {
		if p.Type == "" || p.Type == "text" {
			b.WriteString(p.Text)
		}
	}
	return b.String()
}

// toolArguments unwraps the JSON-encoded string OpenAI puts a call's
// arguments in. Some compatible upstreams send the object itself, which is
// taken as it is; an empty value becomes an empty object so the IR never
// carries an input no target would accept.
func toolArguments(raw json.RawMessage) json.RawMessage {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		raw = json.RawMessage(s)
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return json.RawMessage(`{}`)
	}
	return raw
}

type wireChunk struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Delta struct {
			Role      string `json:"role"`
			Content   string `json:"content"`
			Reasoning string `json:"reasoning_content"`
			// ReasoningAlt is OpenRouter's spelling of reasoning_content.
			ReasoningAlt string `json:"reasoning"`
			ToolCalls    []struct {
				// Index is a pointer because some compatible upstreams omit
				// it; a zero would then merge every call into the first.
				Index    *int   `json:"index"`
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
	Error *wireError `json:"error"`
}

// wireError is the in-stream error object. code is a number on some upstreams
// and a string on others, so it is read as raw JSON.
type wireError struct {
	Message string          `json:"message"`
	Type    string          `json:"type"`
	Code    json.RawMessage `json:"code"`
}

// toIR classifies an in-stream error from what the upstream said about it. A
// status line is no help here: the stream arrived under a 200.
func (e *wireError) toIR() *ir.Error {
	code := strings.Trim(string(e.Code), `"`)
	if code == "null" {
		code = ""
	}
	return &ir.Error{Type: classifyError(e.Type, code), Message: e.Message, Code: code}
}

// classifyError maps the type and code vocabularies OpenAI-compatible
// upstreams use onto the IR's taxonomy. Unknown values stay api_error, which
// is the honest default rather than a guess at retryability.
func classifyError(typ, code string) ir.ErrorType {
	for _, v := range []string{strings.ToLower(code), strings.ToLower(typ)} {
		switch {
		case v == "":
			continue
		case v == "429" || strings.Contains(v, "rate_limit") || strings.Contains(v, "insufficient_quota") ||
			strings.Contains(v, "quota"):
			return ir.ErrRateLimit
		case v == "503" || v == "529" || strings.Contains(v, "overloaded") || strings.Contains(v, "server_error") ||
			strings.Contains(v, "unavailable"):
			return ir.ErrOverloaded
		case v == "401" || strings.Contains(v, "authentication") || strings.Contains(v, "invalid_api_key"):
			return ir.ErrAuthentication
		case v == "403" || strings.Contains(v, "permission"):
			return ir.ErrPermission
		case v == "404" || strings.Contains(v, "not_found"):
			return ir.ErrNotFound
		case strings.Contains(v, "content_filter") || strings.Contains(v, "content_policy"):
			return ir.ErrContentFilter
		case v == "400" || strings.Contains(v, "invalid_request") || strings.Contains(v, "invalid_argument"):
			return ir.ErrInvalidRequest
		}
	}
	return ir.ErrAPI
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
		br := bufio.NewReader(r)
		if isUnaryBody(br) {
			// A request the no-tool-streaming quirk sent unary, or an
			// upstream that answered a stream request with a plain
			// completion: either way the client asked for a stream and gets
			// one, replayed from the whole body.
			replayResponse(br, yield)
			return
		}
		reader := sse.NewReader(br, maxLine)
		open := make(map[int]bool)
		textIdx := -1
		reasoningIdx := -1
		started := false
		// callByID and nextCall serve upstreams that omit tool_calls[].index:
		// a continuation carrying the id rejoins its block, and a new call
		// takes the next number.
		callByID := map[string]int{}
		nextCall := 0

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
			callByID = map[string]int{}
			nextCall = 0
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
				yield(ir.StreamEvent{}, c.Error.toIR())
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
				if d.Reasoning == "" {
					d.Reasoning = d.ReasoningAlt
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
					var idx int
					switch {
					case tc.Index != nil:
						idx = toolBlockBase + *tc.Index
					case tc.ID != "" && callByID[tc.ID] != 0:
						idx = callByID[tc.ID]
					default:
						// No index and no known id: a new call, numbered by
						// arrival so parallel calls stay separate blocks.
						idx = toolBlockBase + nextCall
					}
					if !open[idx] {
						open[idx] = true
						nextCall++
						if tc.ID != "" {
							callByID[tc.ID] = idx
						}
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

// isUnaryBody reports whether the body opens as a JSON object rather than an
// SSE frame. An SSE stream begins with a field name or a comment, never with a
// brace, so the first non-blank byte settles it without consuming anything.
func isUnaryBody(br *bufio.Reader) bool {
	for {
		b, err := br.Peek(1)
		if err != nil {
			return false
		}
		switch b[0] {
		case ' ', '\t', '\r', '\n':
			br.ReadByte()
		case '{':
			return true
		default:
			return false
		}
	}
}

// replayResponse turns one complete completion into the event sequence a
// streamed one would have produced, block by block.
func replayResponse(r io.Reader, yield func(ir.StreamEvent, error) bool) {
	resp, err := decodeResponse(r)
	if err != nil {
		yield(ir.StreamEvent{}, err)
		return
	}
	if !yield(ir.StreamEvent{Type: ir.EventMessageStart, ID: resp.ID, Model: resp.Model,
		Warnings: resp.Warnings}, nil) {
		return
	}
	tools := 0
	for _, b := range resp.Content {
		var idx int
		var start, delta ir.Delta
		switch b.Type {
		case ir.BlockText:
			idx = 0
			start = ir.Delta{Type: ir.BlockText}
			delta = ir.Delta{Type: ir.BlockText, Text: b.Text}
		case ir.BlockThinking:
			if b.Thinking == nil {
				continue
			}
			idx = reasoningBlockBase
			start = ir.Delta{Type: ir.BlockThinking}
			delta = ir.Delta{Type: ir.BlockThinking, Thinking: b.Thinking.Text}
		case ir.BlockToolUse:
			if b.ToolUse == nil {
				continue
			}
			idx = toolBlockBase + tools
			tools++
			start = ir.Delta{Type: ir.BlockToolUse, ToolID: b.ToolUse.ID, ToolName: b.ToolUse.Name}
			delta = ir.Delta{Type: ir.BlockToolUse, ToolID: b.ToolUse.ID, ToolName: b.ToolUse.Name,
				ToolInput: string(b.ToolUse.Input)}
		default:
			continue
		}
		if !yield(ir.StreamEvent{Type: ir.EventBlockStart, Index: idx, Delta: &start}, nil) ||
			!yield(ir.StreamEvent{Type: ir.EventContentDelta, Index: idx, Delta: &delta}, nil) ||
			!yield(ir.StreamEvent{Type: ir.EventBlockStop, Index: idx}, nil) {
			return
		}
	}
	usage := resp.Usage
	if !yield(ir.StreamEvent{Type: ir.EventMessageDelta, Usage: &usage}, nil) {
		return
	}
	yield(ir.StreamEvent{Type: ir.EventMessageStop, StopReason: resp.StopReason}, nil)
}
