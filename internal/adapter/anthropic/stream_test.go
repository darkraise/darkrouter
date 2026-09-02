package anthropic

import (
	"errors"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/ir"
)

func errorsAs(err error, t **ir.Error) bool { return errors.As(err, t) }

func collect(t *testing.T, body string) ([]ir.StreamEvent, error) {
	t.Helper()
	var (
		evs  []ir.StreamEvent
		last error
	)
	for ev, err := range ParseStream(strings.NewReader(body), 1<<20) {
		if err != nil {
			last = err
			break
		}
		evs = append(evs, ev)
	}
	return evs, last
}

func sseEvent(name, data string) string { return "event: " + name + "\ndata: " + data + "\n\n" }

func TestParseStreamMapsTheEventModel(t *testing.T) {
	body := sseEvent("message_start", `{"type":"message_start","message":{"id":"msg_1","model":"claude-x","usage":{"input_tokens":10,"cache_read_input_tokens":3}}}`) +
		sseEvent("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`) +
		sseEvent("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hi"}}`) +
		sseEvent("content_block_stop", `{"type":"content_block_stop","index":0}`) +
		sseEvent("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":4}}`) +
		sseEvent("message_stop", `{"type":"message_stop"}`)

	evs, err := collect(t, body)
	if err != nil {
		t.Fatal(err)
	}
	want := []ir.EventType{
		ir.EventMessageStart, ir.EventMessageDelta, ir.EventBlockStart,
		ir.EventContentDelta, ir.EventBlockStop, ir.EventMessageDelta, ir.EventMessageStop,
	}
	if len(evs) != len(want) {
		t.Fatalf("events = %d, want %d: %+v", len(evs), len(want), evs)
	}
	for i, w := range want {
		if evs[i].Type != w {
			t.Fatalf("event %d = %q, want %q", i, evs[i].Type, w)
		}
	}
	if evs[0].ID != "msg_1" || evs[0].Model != "claude-x" {
		t.Errorf("message_start = %+v", evs[0])
	}
	if evs[len(evs)-1].StopReason != ir.StopEndTurn {
		t.Errorf("message_stop = %+v", evs[len(evs)-1])
	}
}

func TestParseStreamAccumulatesUsageAcrossBothEvents(t *testing.T) {
	body := sseEvent("message_start", `{"type":"message_start","message":{"id":"m","model":"c","usage":{"input_tokens":10,"cache_read_input_tokens":3,"cache_creation_input_tokens":7}}}`) +
		sseEvent("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":4}}`)

	evs, err := collect(t, body)
	if err != nil {
		t.Fatal(err)
	}
	last := evs[len(evs)-1]
	if last.Usage == nil {
		t.Fatal("message_delta carried no usage")
	}
	if last.Usage.InputTokens != 10 || last.Usage.OutputTokens != 4 {
		t.Errorf("usage = %+v; input arrives in message_start and must not be overwritten", last.Usage)
	}
	if last.Usage.CacheReadTokens != 3 || last.Usage.CacheWriteTokens != 7 {
		t.Errorf("usage = %+v", last.Usage)
	}
}

func TestParseStreamReadsToolAndThinkingDeltas(t *testing.T) {
	body := sseEvent("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`) +
		sseEvent("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"weighing"}}`) +
		sseEvent("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig-1"}}`) +
		sseEvent("content_block_start", `{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"call_a","name":"f","input":{}}}`) +
		sseEvent("content_block_delta", `{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"x\":1}"}}`)

	evs, err := collect(t, body)
	if err != nil {
		t.Fatal(err)
	}
	if evs[0].Delta.Type != ir.BlockThinking {
		t.Errorf("block start = %+v", evs[0].Delta)
	}
	if evs[1].Delta.Thinking != "weighing" {
		t.Errorf("thinking delta = %+v", evs[1].Delta)
	}
	if evs[2].Delta.Signature != "sig-1" || evs[2].Delta.Thinking != "" {
		t.Errorf("signature delta = %+v; a signature is not thinking text", evs[2].Delta)
	}
	if evs[3].Delta.ToolID != "call_a" || evs[3].Delta.ToolName != "f" {
		t.Errorf("tool block start = %+v", evs[3].Delta)
	}
	if evs[4].Delta.ToolInput != `{"x":1}` || evs[4].Index != 1 {
		t.Errorf("tool delta = %+v", evs[4])
	}
}

func TestParseStreamIgnoresUnknownEventTypes(t *testing.T) {
	body := sseEvent("ping", `{"type":"ping"}`) +
		sseEvent("future_event", `{"type":"future_event","whatever":1}`) +
		sseEvent("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`)

	evs, err := collect(t, body)
	if err != nil {
		t.Fatalf("an unknown event type must not fail the stream: %v", err)
	}
	if len(evs) != 2 || evs[0].Type != ir.EventPing || evs[1].Type != ir.EventContentDelta {
		t.Errorf("events = %+v", evs)
	}
}

func TestParseStreamYieldsAnInStreamError(t *testing.T) {
	body := sseEvent("error", `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`)
	_, err := collect(t, body)
	var e *ir.Error
	if !errorsAs(err, &e) {
		t.Fatalf("err = %v, want *ir.Error", err)
	}
	if e.Type != ir.ErrOverloaded || e.Message != "Overloaded" {
		t.Errorf("error = %+v; Anthropic sends overloaded_error under a 200", e)
	}
}

func TestParseStreamWarnsOnAnUnknownStopReason(t *testing.T) {
	body := sseEvent("message_delta", `{"type":"message_delta","delta":{"stop_reason":"brand_new"},"usage":{"output_tokens":1}}`) +
		sseEvent("message_stop", `{"type":"message_stop"}`)
	evs, err := collect(t, body)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs[0].Warnings) != 1 || evs[0].Warnings[0].Field != "stop_reason" {
		t.Errorf("message_delta warnings = %+v; an unrecognized value degrades to end_turn and must say so", evs[0].Warnings)
	}
	if evs[1].StopReason != ir.StopEndTurn {
		t.Errorf("stop = %q", evs[1].StopReason)
	}
}

func TestParseStreamCarriesServerToolBlocks(t *testing.T) {
	// A server_tool_use block streams its input like a tool_use, and a
	// web_search_tool_result arrives whole in content_block_start. Neither
	// is text; both used to become an empty text block that hid the search.
	body := sseEvent("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"server_tool_use","id":"srvtoolu_1","name":"web_search","input":{}}}`) +
		sseEvent("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"query\":\"x\"}"}}`) +
		sseEvent("content_block_start", `{"type":"content_block_start","index":1,"content_block":{"type":"web_search_tool_result","tool_use_id":"srvtoolu_1","content":[{"type":"web_search_result","url":"https://a"}]}}`)
	evs, err := collect(t, body)
	if err != nil {
		t.Fatal(err)
	}
	if d := evs[0].Delta; d.Type != "server_tool_use" || d.ToolID != "srvtoolu_1" || d.ToolName != "web_search" ||
		string(d.Extra["id"]) != `"srvtoolu_1"` {
		t.Errorf("server_tool_use start = %+v", d)
	}
	if d := evs[1].Delta; d.Type != ir.BlockToolUse || d.ToolInput != `{"query":"x"}` {
		t.Errorf("input delta = %+v", d)
	}
	if d := evs[2].Delta; d.Type != "web_search_tool_result" || string(d.Extra["tool_use_id"]) != `"srvtoolu_1"` ||
		len(d.Extra["content"]) == 0 {
		t.Errorf("web_search_tool_result start = %+v", d)
	}
}
