package anthropic

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/ir"
)

type wireEvent struct {
	name string
	body map[string]any
}

func streamed(t *testing.T, events []ir.StreamEvent, final error) []wireEvent {
	t.Helper()
	rec := httptest.NewRecorder()
	err := WriteStream(rec, func(yield func(ir.StreamEvent, error) bool) {
		for _, ev := range events {
			if !yield(ev, nil) {
				return
			}
		}
		if final != nil {
			yield(ir.StreamEvent{}, final)
		}
	})
	if err != nil {
		t.Fatal(err)
	}

	var out []wireEvent
	for _, block := range strings.Split(rec.Body.String(), "\n\n") {
		if strings.TrimSpace(block) == "" {
			continue
		}
		var ev wireEvent
		for _, line := range strings.Split(block, "\n") {
			if name, ok := strings.CutPrefix(line, "event: "); ok {
				ev.name = name
			}
			if data, ok := strings.CutPrefix(line, "data: "); ok {
				if err := json.Unmarshal([]byte(data), &ev.body); err != nil {
					t.Fatalf("data %q: %v", data, err)
				}
			}
		}
		out = append(out, ev)
	}
	return out
}

func names(evs []wireEvent) []string {
	out := make([]string, 0, len(evs))
	for _, e := range evs {
		out = append(out, e.name)
	}
	return out
}

func TestWriteStreamEmitsTheAnthropicSequence(t *testing.T) {
	got := streamed(t, []ir.StreamEvent{
		{Type: ir.EventMessageStart, ID: "msg_1", Model: "claude-x"},
		{Type: ir.EventMessageDelta, Usage: &ir.Usage{InputTokens: 10, CacheReadTokens: 3}},
		{Type: ir.EventBlockStart, Index: 0, Delta: &ir.Delta{Type: ir.BlockText}},
		{Type: ir.EventContentDelta, Index: 0, Delta: &ir.Delta{Type: ir.BlockText, Text: "Hi"}},
		{Type: ir.EventBlockStop, Index: 0},
		{Type: ir.EventMessageDelta, Usage: &ir.Usage{InputTokens: 10, OutputTokens: 4}},
		{Type: ir.EventMessageStop, StopReason: ir.StopEndTurn},
	}, nil)

	want := []string{"message_start", "content_block_start", "content_block_delta",
		"content_block_stop", "message_delta", "message_stop"}
	if strings.Join(names(got), ",") != strings.Join(want, ",") {
		t.Fatalf("events = %v, want %v", names(got), want)
	}
	msg := got[0].body["message"].(map[string]any)
	if msg["id"] != "msg_1" || msg["model"] != "claude-x" {
		t.Errorf("message_start = %v", msg)
	}
	if msg["usage"].(map[string]any)["input_tokens"].(float64) != 10 {
		t.Errorf("message_start usage = %v; it must carry the input count", msg["usage"])
	}
	if got[4].body["delta"].(map[string]any)["stop_reason"] != "end_turn" {
		t.Errorf("message_delta = %v", got[4].body)
	}
	if got[4].body["usage"].(map[string]any)["output_tokens"].(float64) != 4 {
		t.Errorf("message_delta usage = %v", got[4].body["usage"])
	}
}

func TestWriteStreamRenumbersToolBlocksDensely(t *testing.T) {
	got := streamed(t, []ir.StreamEvent{
		{Type: ir.EventMessageStart, ID: "m", Model: "c"},
		{Type: ir.EventBlockStart, Index: 0, Delta: &ir.Delta{Type: ir.BlockText}},
		{Type: ir.EventContentDelta, Index: 0, Delta: &ir.Delta{Type: ir.BlockText, Text: "hi"}},
		{Type: ir.EventBlockStart, Index: 1000, Delta: &ir.Delta{
			Type: ir.BlockToolUse, ToolID: "call_a", ToolName: "f"}},
		{Type: ir.EventContentDelta, Index: 1000, Delta: &ir.Delta{
			Type: ir.BlockToolUse, ToolInput: `{"x":1}`}},
		{Type: ir.EventMessageStop, StopReason: ir.StopToolUse},
	}, nil)

	var toolStart, toolDelta map[string]any
	for _, e := range got {
		if e.name == "content_block_start" &&
			e.body["content_block"].(map[string]any)["type"] == "tool_use" {
			toolStart = e.body
		}
		if e.name == "content_block_delta" &&
			e.body["delta"].(map[string]any)["type"] == "input_json_delta" {
			toolDelta = e.body
		}
	}
	if toolStart == nil || toolDelta == nil {
		t.Fatalf("events = %v", names(got))
	}
	if toolStart["index"].(float64) != 1 {
		t.Errorf("tool block index = %v; the wire index is dense, not the IR's 1000",
			toolStart["index"])
	}
	if toolDelta["index"].(float64) != 1 {
		t.Errorf("tool delta index = %v", toolDelta["index"])
	}
	cb := toolStart["content_block"].(map[string]any)
	if cb["id"] != "call_a" || cb["name"] != "f" {
		t.Errorf("content_block = %v", cb)
	}
}

func TestWriteStreamOpensABlockForAnOrphanDelta(t *testing.T) {
	got := streamed(t, []ir.StreamEvent{
		{Type: ir.EventMessageStart, ID: "m", Model: "c"},
		{Type: ir.EventContentDelta, Delta: &ir.Delta{Type: ir.BlockThinking, Thinking: "hmm"}},
		{Type: ir.EventMessageStop, StopReason: ir.StopEndTurn},
	}, nil)
	want := []string{"message_start", "content_block_start", "content_block_delta",
		"content_block_stop", "message_delta", "message_stop"}
	if strings.Join(names(got), ",") != strings.Join(want, ",") {
		t.Fatalf("events = %v, want %v; a delta with no start is invalid to a client",
			names(got), want)
	}
	if got[1].body["content_block"].(map[string]any)["type"] != "thinking" {
		t.Errorf("synthesized block = %v", got[1].body)
	}
	if got[2].body["delta"].(map[string]any)["type"] != "thinking_delta" {
		t.Errorf("delta = %v", got[2].body)
	}
}

func TestWriteStreamClosesOpenBlocksBeforeTheEnd(t *testing.T) {
	got := streamed(t, []ir.StreamEvent{
		{Type: ir.EventMessageStart, ID: "m", Model: "c"},
		{Type: ir.EventBlockStart, Index: 0, Delta: &ir.Delta{Type: ir.BlockText}},
		{Type: ir.EventContentDelta, Index: 0, Delta: &ir.Delta{Type: ir.BlockText, Text: "hi"}},
		{Type: ir.EventMessageStop, StopReason: ir.StopEndTurn},
	}, nil)
	if names(got)[3] != "content_block_stop" {
		t.Fatalf("events = %v; an unclosed block leaves the client waiting", names(got))
	}
}

func TestWriteStreamEmitsARealErrorEvent(t *testing.T) {
	got := streamed(t, []ir.StreamEvent{
		{Type: ir.EventMessageStart, ID: "m", Model: "c"},
		{Type: ir.EventContentDelta, Index: 0, Delta: &ir.Delta{Type: ir.BlockText, Text: "partial"}},
	}, &ir.Error{Type: ir.ErrOverloaded, Message: "upstream gave up"})

	last := got[len(got)-1]
	if last.name != "error" {
		t.Fatalf("events = %v; spec §4.9 gives Anthropic a real error event", names(got))
	}
	e := last.body["error"].(map[string]any)
	if e["type"] != "overloaded_error" || e["message"] != "upstream gave up" {
		t.Errorf("error = %v", e)
	}
	if strings.Contains(strings.Join(names(got), ","), "message_stop") {
		t.Error("a stream that errored must not also claim to have stopped normally")
	}
}

func TestWriteStreamReEmitsACarriedBlockStart(t *testing.T) {
	// A server-tool block the Anthropic adapter carried through Extra is
	// re-emitted as it arrived. Rendering it as an empty text block would
	// hide the search from a client that asked for it.
	got := streamed(t, []ir.StreamEvent{
		{Type: ir.EventMessageStart, ID: "m", Model: "c"},
		{Type: ir.EventBlockStart, Index: 0, Delta: &ir.Delta{
			Type: "web_search_tool_result", Extra: map[string]json.RawMessage{
				"type":        json.RawMessage(`"web_search_tool_result"`),
				"tool_use_id": json.RawMessage(`"srvtoolu_1"`),
				"content":     json.RawMessage(`[{"type":"web_search_result","url":"https://a"}]`),
			}}},
		{Type: ir.EventBlockStop, Index: 0},
		{Type: ir.EventMessageStop, StopReason: ir.StopEndTurn},
	}, nil)
	cb := got[1].body["content_block"].(map[string]any)
	if cb["type"] != "web_search_tool_result" || cb["tool_use_id"] != "srvtoolu_1" {
		t.Errorf("content_block = %v", cb)
	}
	if _, ok := cb["content"].([]any); !ok {
		t.Errorf("content_block = %v; the carried payload was lost", cb)
	}
}
