package openai

import (
	"encoding/json"
	"errors"
	"iter"
	"net/http"
	"sort"
	"strings"

	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/sse"
)

// responsesItem is one open output item. It holds what the closing events need
// and what the accumulated response needs, which are the same text.
type responsesItem struct {
	index  int // the output index, which is also its position in acc.Content
	kind   string
	itemID string
	text   strings.Builder
}

// responsesStream is the state a semantic stream needs that a chat stream does
// not: every event carries a sequence number and an output index, every item is
// opened and closed explicitly, and the terminal event carries the whole
// response object.
type responsesStream struct {
	s   *sse.Writer
	seq int
	id  string

	// acc is the response as it accumulates. response.completed returns the
	// same object the unary path does, and building it here as an ir.Response
	// is what lets both call responsesBody rather than assembling twice.
	acc ir.Response

	// open maps an IR block index to its item. The IR block index is not the
	// output index — openaicompat offsets tool blocks by 1000 to keep them
	// clear of the text block — so the two are mapped, exactly as chat's
	// writer maps toolIndex.
	open map[int]*responsesItem
	next int

	echo *responsesEcho

	started   bool
	completed bool
	// stopped records that the provider sent its finish reason. The terminal
	// event is not emitted there: OpenAI-compatible upstreams send the usage
	// chunk AFTER the finish chunk, and Darkrouter always asks for it, so
	// completing on message_stop would report zero usage on every streamed
	// response. The terminal event goes out when the sequence ends.
	stopped bool
}

// WriteResponsesStream converts canonical stream events into Responses semantic
// events. There is no DONE sentinel: the Responses stream ends at
// response.completed, and chat's sentinel would put an unparseable line in
// front of a client that reads every data: line as JSON.
func WriteResponsesStream(w http.ResponseWriter, events iter.Seq2[ir.StreamEvent, error],
	echo *responsesEcho) error {

	rs := &responsesStream{s: sse.NewWriter(w), open: map[int]*responsesItem{}, echo: echo}
	for ev, err := range events {
		if err != nil {
			return rs.fail(err)
		}
		if serr := rs.handle(ev); serr != nil {
			return serr
		}
	}
	// A provider that ends without a message_stop still owes the client a
	// terminal event, or it waits forever.
	return rs.complete()
}

func (rs *responsesStream) send(kind string, obj map[string]any) error {
	obj["type"] = kind
	obj["sequence_number"] = rs.seq
	rs.seq++
	b, err := json.Marshal(obj)
	if err != nil {
		return err
	}
	return rs.s.Send(kind, string(b))
}

func (rs *responsesStream) handle(ev ir.StreamEvent) error {
	switch ev.Type {
	case ir.EventMessageStart:
		rs.acc.ID, rs.acc.Model = ev.ID, ev.Model
		rs.id = responsesID(ev.ID)
		return rs.ensureStarted()
	case ir.EventBlockStart:
		if ev.Delta == nil || ev.Delta.Type != ir.BlockToolUse {
			return nil
		}
		_, err := rs.openTool(ev.Index, ev.Delta.ToolID, ev.Delta.ToolName)
		return err
	case ir.EventContentDelta:
		return rs.delta(ev)
	case ir.EventBlockStop:
		return rs.closeItem(ev.Index)
	case ir.EventMessageDelta:
		if ev.Usage != nil {
			rs.acc.Usage = *ev.Usage
		}
		return nil
	case ir.EventMessageStop:
		// Close the items but do not finish: the usage chunk arrives after the
		// finish chunk on every OpenAI-compatible upstream, and Darkrouter
		// always requests it. Completing here would report zero usage.
		rs.acc.StopReason = ev.StopReason
		rs.stopped = true
		return rs.closeAll()
	default:
		return nil
	}
}

func (rs *responsesStream) ensureStarted() error {
	if rs.started {
		return nil
	}
	rs.started = true
	if rs.id == "" {
		rs.id = responsesID(rs.acc.ID)
	}
	if err := rs.send("response.created", map[string]any{
		"response": responsesBody(rs.id, &rs.acc, "in_progress", "", rs.echo),
	}); err != nil {
		return err
	}
	return rs.send("response.in_progress", map[string]any{
		"response": responsesBody(rs.id, &rs.acc, "in_progress", "", rs.echo),
	})
}

func (rs *responsesStream) delta(ev ir.StreamEvent) error {
	if ev.Delta == nil {
		return nil
	}
	switch ev.Delta.Type {
	case ir.BlockText:
		if ev.Delta.Text == "" {
			return nil
		}
		it, err := rs.openMessage(ev.Index)
		if err != nil {
			return err
		}
		it.text.WriteString(ev.Delta.Text)
		rs.acc.Content[it.index].Text = it.text.String()
		return rs.send("response.output_text.delta", map[string]any{
			"item_id": it.itemID, "output_index": it.index, "content_index": 0,
			"delta": ev.Delta.Text, "logprobs": []any{},
		})
	case ir.BlockThinking:
		if ev.Delta.Thinking == "" {
			return nil
		}
		it, err := rs.openReasoning(ev.Index)
		if err != nil {
			return err
		}
		it.text.WriteString(ev.Delta.Thinking)
		rs.acc.Content[it.index].Thinking.Text = it.text.String()
		return rs.send("response.reasoning_summary_text.delta", map[string]any{
			"item_id": it.itemID, "output_index": it.index, "summary_index": 0,
			"delta": ev.Delta.Thinking,
		})
	case ir.BlockToolUse:
		if ev.Delta.ToolInput == "" {
			return nil
		}
		// A provider that streams arguments without ever opening the block
		// still has to reach the client, so the item is opened here rather
		// than dropping the call.
		it, err := rs.openTool(ev.Index, ev.Delta.ToolID, ev.Delta.ToolName)
		if err != nil {
			return err
		}
		it.text.WriteString(ev.Delta.ToolInput)
		rs.acc.Content[it.index].ToolUse.Input = json.RawMessage(it.text.String())
		return rs.send("response.function_call_arguments.delta", map[string]any{
			"item_id": it.itemID, "output_index": it.index, "delta": ev.Delta.ToolInput,
		})
	default:
		return nil
	}
}

// claim allocates an output index and appends the matching accumulator block.
// The two must stay in step: responsesBody derives an item's id from its
// position in the final output array, and a delta's item_id has to match it or
// the client drops the text it assembled.
func (rs *responsesStream) claim(kind, prefix string, at int, blk ir.ContentBlock) *responsesItem {
	it := &responsesItem{index: rs.next, kind: kind}
	it.itemID = responsesItemID(rs.id, prefix, it.index)
	rs.next++
	rs.open[at] = it
	rs.acc.Content = append(rs.acc.Content, blk)
	return it
}

func (rs *responsesStream) openMessage(block int) (*responsesItem, error) {
	if it, ok := rs.open[block]; ok {
		return it, nil
	}
	if err := rs.ensureStarted(); err != nil {
		return nil, err
	}
	it := rs.claim("message", "msg", block, ir.ContentBlock{Type: ir.BlockText})
	if err := rs.send("response.output_item.added", map[string]any{
		"output_index": it.index,
		"item": map[string]any{
			"type": "message", "id": it.itemID, "status": "in_progress",
			"role": "assistant", "content": []any{},
		},
	}); err != nil {
		return nil, err
	}
	return it, rs.send("response.content_part.added", map[string]any{
		"item_id": it.itemID, "output_index": it.index, "content_index": 0,
		"part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}},
	})
}

func (rs *responsesStream) openReasoning(block int) (*responsesItem, error) {
	if it, ok := rs.open[block]; ok {
		return it, nil
	}
	if err := rs.ensureStarted(); err != nil {
		return nil, err
	}
	it := rs.claim("reasoning", "rs", block,
		ir.ContentBlock{Type: ir.BlockThinking, Thinking: &ir.Thinking{}})
	if err := rs.send("response.output_item.added", map[string]any{
		"output_index": it.index,
		"item": map[string]any{
			"type": "reasoning", "id": it.itemID, "summary": []any{},
		},
	}); err != nil {
		return nil, err
	}
	return it, rs.send("response.reasoning_summary_part.added", map[string]any{
		"item_id": it.itemID, "output_index": it.index, "summary_index": 0,
		"part": map[string]any{"type": "summary_text", "text": ""},
	})
}

func (rs *responsesStream) openTool(block int, callID, name string) (*responsesItem, error) {
	if it, ok := rs.open[block]; ok {
		return it, nil
	}
	if err := rs.ensureStarted(); err != nil {
		return nil, err
	}
	it := rs.claim("function_call", "fc", block, ir.ContentBlock{
		Type: ir.BlockToolUse, ToolUse: &ir.ToolUse{ID: callID, Name: name},
	})
	return it, rs.send("response.output_item.added", map[string]any{
		"output_index": it.index,
		"item": map[string]any{
			"type": "function_call", "id": it.itemID, "call_id": callID,
			"name": name, "arguments": "", "status": "in_progress",
		},
	})
}

func (rs *responsesStream) closeItem(block int) error {
	it, ok := rs.open[block]
	if !ok {
		return nil
	}
	delete(rs.open, block)
	text := it.text.String()

	switch it.kind {
	case "message":
		part := map[string]any{"type": "output_text", "text": text, "annotations": []any{}}
		if err := rs.send("response.output_text.done", map[string]any{
			"item_id": it.itemID, "output_index": it.index, "content_index": 0,
			"text": text, "logprobs": []any{},
		}); err != nil {
			return err
		}
		if err := rs.send("response.content_part.done", map[string]any{
			"item_id": it.itemID, "output_index": it.index, "content_index": 0,
			"part": part,
		}); err != nil {
			return err
		}
		return rs.send("response.output_item.done", map[string]any{
			"output_index": it.index,
			"item": map[string]any{
				"type": "message", "id": it.itemID, "status": "completed",
				"role": "assistant", "content": []any{part},
			},
		})
	case "reasoning":
		summary := map[string]any{"type": "summary_text", "text": text}
		if err := rs.send("response.reasoning_summary_text.done", map[string]any{
			"item_id": it.itemID, "output_index": it.index, "summary_index": 0, "text": text,
		}); err != nil {
			return err
		}
		if err := rs.send("response.reasoning_summary_part.done", map[string]any{
			"item_id": it.itemID, "output_index": it.index, "summary_index": 0, "part": summary,
		}); err != nil {
			return err
		}
		return rs.send("response.output_item.done", map[string]any{
			"output_index": it.index,
			"item": map[string]any{
				"type": "reasoning", "id": it.itemID, "summary": []any{summary},
			},
		})
	default:
		args := text
		if args == "" {
			args = "{}"
		}
		tu := rs.acc.Content[it.index].ToolUse
		if err := rs.send("response.function_call_arguments.done", map[string]any{
			"item_id": it.itemID, "output_index": it.index, "arguments": args,
		}); err != nil {
			return err
		}
		return rs.send("response.output_item.done", map[string]any{
			"output_index": it.index,
			"item": map[string]any{
				"type": "function_call", "id": it.itemID, "call_id": tu.ID,
				"name": tu.Name, "arguments": args, "status": "completed",
			},
		})
	}
}

// closeAll closes whatever the provider left open, in output order so the
// events are deterministic.
func (rs *responsesStream) closeAll() error {
	blocks := make([]int, 0, len(rs.open))
	for b := range rs.open {
		blocks = append(blocks, b)
	}
	sort.Ints(blocks)
	for _, b := range blocks {
		if err := rs.closeItem(b); err != nil {
			return err
		}
	}
	return nil
}

func (rs *responsesStream) complete() error {
	if rs.completed {
		return nil
	}
	if err := rs.ensureStarted(); err != nil {
		return err
	}
	// A client does not treat an item as final until output_item.done, so an
	// item still open here would leave it waiting forever.
	if err := rs.closeAll(); err != nil {
		return err
	}
	rs.completed = true
	status, incomplete := responsesStatus(rs.acc.StopReason)
	// The terminal event name follows the status. response.completed always
	// carries status "completed"; a truncated or filtered answer ends with
	// response.incomplete, and a client switching on the event type would
	// otherwise treat a half answer as a whole one.
	name := "response.completed"
	if status == "incomplete" {
		name = "response.incomplete"
	}
	return rs.send(name, map[string]any{
		"response": responsesBody(rs.id, &rs.acc, status, incomplete, rs.echo),
	})
}

// fail ends a stream the provider could not finish. The client has already
// received content and cannot be given a different response, so the least
// wrong thing is to tell it plainly that this one did not complete.
func (rs *responsesStream) fail(err error) error {
	// A reader error after the terminal event — trailing bytes, a reset after
	// the last event went out — must not produce a second terminal event.
	if rs.completed {
		return nil
	}
	var e *ir.Error
	if !errors.As(err, &e) {
		e = &ir.Error{Type: ir.ErrAPI, Message: err.Error()}
	}
	if serr := rs.ensureStarted(); serr != nil {
		return serr
	}
	if serr := rs.closeAll(); serr != nil {
		return serr
	}
	rs.completed = true
	body := responsesBody(rs.id, &rs.acc, "failed", "", rs.echo)
	body["error"] = map[string]any{"code": string(e.Type), "message": e.Message}
	return rs.send("response.failed", map[string]any{"response": body})
}
