package openai

import (
	"encoding/json"
	"iter"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/ir"
)

// respEvents runs the writer over a fixed sequence and returns the parsed
// event objects in order.
func respEvents(t *testing.T, evs []ir.StreamEvent, final error) []map[string]any {
	t.Helper()
	seq := func(yield func(ir.StreamEvent, error) bool) {
		for _, e := range evs {
			if !yield(e, nil) {
				return
			}
		}
		if final != nil {
			yield(ir.StreamEvent{}, final)
		}
	}
	w := httptest.NewRecorder()
	// A nil echo is the no-request case and must produce a valid object: the
	// required fields fall back to their SDK defaults.
	if err := WriteResponsesStream(w, iter.Seq2[ir.StreamEvent, error](seq), nil); err != nil {
		t.Fatalf("WriteResponsesStream: %v", err)
	}
	var out []map[string]any
	for _, block := range strings.Split(w.Body.String(), "\n\n") {
		var data string
		for _, line := range strings.Split(block, "\n") {
			if rest, ok := strings.CutPrefix(line, "data: "); ok {
				data += rest
			}
		}
		if data == "" {
			continue
		}
		var obj map[string]any
		if err := json.Unmarshal([]byte(data), &obj); err != nil {
			t.Fatalf("event data is not JSON: %q", data)
		}
		out = append(out, obj)
	}
	return out
}

func types(evs []map[string]any) []string {
	out := make([]string, 0, len(evs))
	for _, e := range evs {
		s, _ := e["type"].(string)
		out = append(out, s)
	}
	return out
}

// textTurn puts the usage event AFTER message_stop, which is the real order:
// OpenAI-compatible upstreams send the usage chunk after the finish chunk and
// Darkrouter always asks for it.
func textTurn() []ir.StreamEvent {
	return []ir.StreamEvent{
		{Type: ir.EventMessageStart, ID: "chatcmpl-1", Model: "gpt-4o"},
		{Type: ir.EventContentDelta, Index: 0, Delta: &ir.Delta{Type: ir.BlockText, Text: "he"}},
		{Type: ir.EventContentDelta, Index: 0, Delta: &ir.Delta{Type: ir.BlockText, Text: "llo"}},
		{Type: ir.EventBlockStop, Index: 0},
		{Type: ir.EventMessageStop, StopReason: ir.StopEndTurn},
		{Type: ir.EventMessageDelta, Usage: &ir.Usage{InputTokens: 3, OutputTokens: 2}},
	}
}

func TestResponsesStreamEmitsTheTextLifecycle(t *testing.T) {
	got := types(respEvents(t, textTurn(), nil))
	want := []string{
		"response.created", "response.in_progress",
		"response.output_item.added", "response.content_part.added",
		"response.output_text.delta", "response.output_text.delta",
		"response.output_text.done", "response.content_part.done",
		"response.output_item.done", "response.completed",
	}
	if len(got) != len(want) {
		t.Fatalf("events = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("event %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestResponsesStreamNumbersEveryEvent(t *testing.T) {
	// Clients detect drops with this. A repeated or missing number is
	// indistinguishable from a lost event.
	evs := respEvents(t, textTurn(), nil)
	for i, e := range evs {
		n, ok := e["sequence_number"].(float64)
		if !ok {
			t.Fatalf("event %d (%v) has no sequence_number", i, e["type"])
		}
		if int(n) != i {
			t.Errorf("event %d has sequence_number %d", i, int(n))
		}
	}
}

func TestResponsesStreamNeverSendsTheChatSentinel(t *testing.T) {
	// The Responses stream ends at response.completed. [DONE] would put an
	// unparseable line in front of a client that reads every data: as JSON.
	w := httptest.NewRecorder()
	seq := func(yield func(ir.StreamEvent, error) bool) {
		for _, e := range textTurn() {
			if !yield(e, nil) {
				return
			}
		}
	}
	if err := WriteResponsesStream(w, iter.Seq2[ir.StreamEvent, error](seq), nil); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(w.Body.String(), "[DONE]") {
		t.Errorf("body carried the chat sentinel:\n%s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "event: response.completed") {
		t.Errorf("no named completed event:\n%s", w.Body.String())
	}
}

func TestResponsesStreamCompletedCarriesTheWholeResponse(t *testing.T) {
	evs := respEvents(t, textTurn(), nil)
	last := evs[len(evs)-1]
	resp, ok := last["response"].(map[string]any)
	if !ok {
		t.Fatalf("completed event = %v", last)
	}
	if resp["status"] != "completed" || resp["output_text"] != "hello" {
		t.Errorf("response = %v", resp)
	}
	if resp["store"] != false {
		t.Errorf("store = %v; the streamed object must say the id is not resumable", resp["store"])
	}
	usage, _ := resp["usage"].(map[string]any)
	if usage == nil || usage["input_tokens"].(float64) != 3 || usage["output_tokens"].(float64) != 2 {
		t.Errorf("usage = %v", usage)
	}
	out, _ := resp["output"].([]any)
	if len(out) != 1 {
		t.Fatalf("output = %v", out)
	}
	item, _ := out[0].(map[string]any)
	if item["type"] != "message" || item["status"] != "completed" {
		t.Errorf("item = %v", item)
	}
}

func TestResponsesStreamItemIDsMatchTheFinalObject(t *testing.T) {
	// A client correlates deltas to items by item_id. If the streamed id and
	// the one in the final object differ, the assembled answer is dropped.
	evs := respEvents(t, textTurn(), nil)
	var deltaID string
	for _, e := range evs {
		if e["type"] == "response.output_text.delta" {
			deltaID, _ = e["item_id"].(string)
		}
	}
	resp := evs[len(evs)-1]["response"].(map[string]any)
	item := resp["output"].([]any)[0].(map[string]any)
	if deltaID == "" || deltaID != item["id"] {
		t.Errorf("delta item_id = %q, final item id = %v", deltaID, item["id"])
	}
}

func TestResponsesStreamEmitsAToolCallLifecycle(t *testing.T) {
	got := types(respEvents(t, []ir.StreamEvent{
		{Type: ir.EventMessageStart, ID: "c1", Model: "m"},
		{Type: ir.EventBlockStart, Index: 1000, Delta: &ir.Delta{
			Type: ir.BlockToolUse, ToolID: "call_1", ToolName: "f"}},
		{Type: ir.EventContentDelta, Index: 1000, Delta: &ir.Delta{
			Type: ir.BlockToolUse, ToolInput: `{"a":`}},
		{Type: ir.EventContentDelta, Index: 1000, Delta: &ir.Delta{
			Type: ir.BlockToolUse, ToolInput: `1}`}},
		{Type: ir.EventBlockStop, Index: 1000},
		{Type: ir.EventMessageStop, StopReason: ir.StopToolUse},
	}, nil))
	want := []string{
		"response.created", "response.in_progress", "response.output_item.added",
		"response.function_call_arguments.delta", "response.function_call_arguments.delta",
		"response.function_call_arguments.done", "response.output_item.done",
		"response.completed",
	}
	if len(got) != len(want) {
		t.Fatalf("events = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("event %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestResponsesStreamCarriesTheAssembledArguments(t *testing.T) {
	evs := respEvents(t, []ir.StreamEvent{
		{Type: ir.EventMessageStart, ID: "c1", Model: "m"},
		{Type: ir.EventBlockStart, Index: 1000, Delta: &ir.Delta{
			Type: ir.BlockToolUse, ToolID: "call_1", ToolName: "f"}},
		{Type: ir.EventContentDelta, Index: 1000, Delta: &ir.Delta{
			Type: ir.BlockToolUse, ToolInput: `{"a":1}`}},
		{Type: ir.EventBlockStop, Index: 1000},
		{Type: ir.EventMessageStop, StopReason: ir.StopToolUse},
	}, nil)
	resp := evs[len(evs)-1]["response"].(map[string]any)
	item := resp["output"].([]any)[0].(map[string]any)
	if item["type"] != "function_call" || item["call_id"] != "call_1" ||
		item["name"] != "f" || item["arguments"] != `{"a":1}` {
		t.Errorf("item = %v", item)
	}
}

func TestResponsesStreamEmitsReasoningSummaries(t *testing.T) {
	got := types(respEvents(t, []ir.StreamEvent{
		{Type: ir.EventMessageStart, ID: "c1", Model: "m"},
		{Type: ir.EventContentDelta, Index: 0, Delta: &ir.Delta{
			Type: ir.BlockThinking, Thinking: "pondering"}},
		{Type: ir.EventBlockStop, Index: 0},
		{Type: ir.EventMessageStop, StopReason: ir.StopEndTurn},
	}, nil))
	want := []string{
		"response.created", "response.in_progress", "response.output_item.added",
		"response.reasoning_summary_part.added", "response.reasoning_summary_text.delta",
		"response.reasoning_summary_text.done", "response.reasoning_summary_part.done",
		"response.output_item.done", "response.completed",
	}
	if len(got) != len(want) {
		t.Fatalf("events = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("event %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestResponsesStreamClosesAnItemTheProviderLeftOpen(t *testing.T) {
	// A client does not treat an item as final until output_item.done. A
	// stream that ends mid-item would leave it waiting forever.
	got := types(respEvents(t, []ir.StreamEvent{
		{Type: ir.EventMessageStart, ID: "c1", Model: "m"},
		{Type: ir.EventContentDelta, Index: 0, Delta: &ir.Delta{Type: ir.BlockText, Text: "hi"}},
		{Type: ir.EventMessageStop, StopReason: ir.StopEndTurn},
	}, nil))
	var sawDone, sawCompleted bool
	for i, ty := range got {
		if ty == "response.output_item.done" {
			sawDone = true
			for _, later := range got[i+1:] {
				if later == "response.completed" {
					sawCompleted = true
				}
			}
		}
	}
	if !sawDone || !sawCompleted {
		t.Errorf("events = %v; the open item was not closed before completion", got)
	}
}

func TestResponsesStreamReportsTruncationAsIncomplete(t *testing.T) {
	evs := respEvents(t, []ir.StreamEvent{
		{Type: ir.EventMessageStart, ID: "c1", Model: "m"},
		{Type: ir.EventContentDelta, Index: 0, Delta: &ir.Delta{Type: ir.BlockText, Text: "half"}},
		{Type: ir.EventBlockStop, Index: 0},
		{Type: ir.EventMessageStop, StopReason: ir.StopMaxTokens},
	}, nil)
	last := evs[len(evs)-1]
	// The event NAME, not only the status inside it. A client switching on the
	// terminal event type would treat response.completed as a whole answer.
	if last["type"] != "response.incomplete" {
		t.Errorf("terminal event = %v, want response.incomplete", last["type"])
	}
	resp := last["response"].(map[string]any)
	if resp["status"] != "incomplete" {
		t.Errorf("status = %v", resp["status"])
	}
	det, _ := resp["incomplete_details"].(map[string]any)
	if det == nil || det["reason"] != "max_output_tokens" {
		t.Errorf("incomplete_details = %v", det)
	}
}

func TestResponsesStreamReportsUsageThatArrivesAfterTheStop(t *testing.T) {
	// This is the real order on every OpenAI-compatible upstream. Completing on
	// message_stop would report zero usage on every streamed response.
	evs := respEvents(t, textTurn(), nil)
	resp := evs[len(evs)-1]["response"].(map[string]any)
	usage, _ := resp["usage"].(map[string]any)
	if usage == nil || usage["input_tokens"].(float64) != 3 || usage["output_tokens"].(float64) != 2 {
		t.Errorf("usage = %v; the post-stop usage event was not waited for", usage)
	}
}

func TestResponsesStreamEmitsExactlyOneTerminalEvent(t *testing.T) {
	// A reader error after the finish chunk must not append a second one.
	evs := respEvents(t, textTurn(), &ir.Error{Type: ir.ErrAPI, Message: "trailing garbage"})
	terminal := 0
	for _, e := range evs {
		switch e["type"] {
		case "response.completed", "response.incomplete", "response.failed":
			terminal++
		}
	}
	if terminal != 1 {
		t.Errorf("%d terminal events in %v", terminal, types(evs))
	}
}

func TestResponsesStreamEndsAFailedStreamWithResponseFailed(t *testing.T) {
	// The client has already received content, so it cannot be given a
	// different response. It must at least be told this one did not finish.
	evs := respEvents(t, []ir.StreamEvent{
		{Type: ir.EventMessageStart, ID: "c1", Model: "m"},
		{Type: ir.EventContentDelta, Index: 0, Delta: &ir.Delta{Type: ir.BlockText, Text: "hi"}},
	}, &ir.Error{Type: ir.ErrOverloaded, Message: "upstream went away"})

	last := evs[len(evs)-1]
	if last["type"] != "response.failed" {
		t.Fatalf("last event = %v", last["type"])
	}
	resp, _ := last["response"].(map[string]any)
	e, _ := resp["error"].(map[string]any)
	if e == nil || e["code"] != string(ir.ErrOverloaded) ||
		!strings.Contains(e["message"].(string), "went away") {
		t.Errorf("error = %v", e)
	}
	if resp["status"] != "failed" {
		t.Errorf("status = %v", resp["status"])
	}
}
