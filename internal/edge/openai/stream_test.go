package openai

import (
	"encoding/json"
	"iter"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/ir"
)

func seq(events ...ir.StreamEvent) iter.Seq2[ir.StreamEvent, error] {
	return func(yield func(ir.StreamEvent, error) bool) {
		for _, e := range events {
			if !yield(e, nil) {
				return
			}
		}
	}
}

func TestWriteStreamEmitsDeltasAndDone(t *testing.T) {
	rec := httptest.NewRecorder()
	err := WriteStream(rec, seq(
		ir.StreamEvent{Type: ir.EventMessageStart},
		ir.StreamEvent{Type: ir.EventContentDelta, Delta: &ir.Delta{Type: ir.BlockText, Text: "he"}},
		ir.StreamEvent{Type: ir.EventContentDelta, Delta: &ir.Delta{Type: ir.BlockText, Text: "llo"}},
		ir.StreamEvent{Type: ir.EventMessageStop, StopReason: ir.StopEndTurn},
	))
	if err != nil {
		t.Fatal(err)
	}
	body := rec.Body.String()
	if strings.Count(body, `"delta"`) < 2 {
		t.Fatalf("expected two delta chunks, got:\n%s", body)
	}
	if !strings.HasSuffix(body, "data: [DONE]\n\n") {
		t.Fatalf("stream must end with the DONE sentinel, got:\n%s", body)
	}
}

func TestWriteStreamSkipsPings(t *testing.T) {
	rec := httptest.NewRecorder()
	if err := WriteStream(rec, seq(ir.StreamEvent{Type: ir.EventPing})); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rec.Body.String(), `"delta"`) {
		t.Fatal("a ping must not become a client-visible chunk")
	}
}

func TestWriteStreamEmitsUsageWhenPresent(t *testing.T) {
	rec := httptest.NewRecorder()
	err := WriteStream(rec, seq(
		ir.StreamEvent{Type: ir.EventMessageDelta, Usage: &ir.Usage{InputTokens: 2, OutputTokens: 4}},
		ir.StreamEvent{Type: ir.EventMessageStop},
	))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rec.Body.String(), `"total_tokens":6`) {
		t.Fatalf("expected a usage chunk, got:\n%s", rec.Body.String())
	}
}

func TestWriteStreamEmitsInStreamError(t *testing.T) {
	rec := httptest.NewRecorder()
	events := func(yield func(ir.StreamEvent, error) bool) {
		if !yield(ir.StreamEvent{Type: ir.EventContentDelta, Delta: &ir.Delta{Type: ir.BlockText, Text: "x"}}, nil) {
			return
		}
		yield(ir.StreamEvent{}, &ir.Error{Type: ir.ErrOverloaded, Message: "upstream died"})
	}
	if err := WriteStream(rec, events); err != nil {
		t.Fatal(err)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "upstream died") {
		t.Fatalf("expected an in-stream error, got:\n%s", body)
	}
	if !strings.HasSuffix(body, "data: [DONE]\n\n") {
		t.Fatal("an errored stream still terminates with DONE")
	}
}

// chunks collects the JSON payload of every SSE data line except the sentinel.
func chunks(t *testing.T, events []ir.StreamEvent) []map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	err := WriteStream(rec, func(yield func(ir.StreamEvent, error) bool) {
		for _, ev := range events {
			if !yield(ev, nil) {
				return
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	for _, line := range strings.Split(rec.Body.String(), "\n") {
		data, ok := strings.CutPrefix(line, "data: ")
		if !ok || data == "[DONE]" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(data), &m); err != nil {
			t.Fatalf("chunk %q: %v", data, err)
		}
		out = append(out, m)
	}
	return out
}

func delta(t *testing.T, chunk map[string]any) map[string]any {
	t.Helper()
	return chunk["choices"].([]any)[0].(map[string]any)["delta"].(map[string]any)
}

func TestWriteStreamEmitsDenseToolCallIndices(t *testing.T) {
	got := chunks(t, []ir.StreamEvent{
		{Type: ir.EventMessageStart, ID: "r", Model: "m"},
		{Type: ir.EventBlockStart, Index: 1000, Delta: &ir.Delta{
			Type: ir.BlockToolUse, ToolID: "call_a", ToolName: "f"}},
		{Type: ir.EventContentDelta, Index: 1000, Delta: &ir.Delta{
			Type: ir.BlockToolUse, ToolInput: `{"x":`}},
		{Type: ir.EventBlockStart, Index: 1001, Delta: &ir.Delta{
			Type: ir.BlockToolUse, ToolID: "call_b", ToolName: "f"}},
		{Type: ir.EventContentDelta, Index: 1000, Delta: &ir.Delta{
			Type: ir.BlockToolUse, ToolInput: `1}`}},
		{Type: ir.EventMessageStop, StopReason: ir.StopToolUse},
	})
	if len(got) != 6 {
		t.Fatalf("chunks = %d: %v", len(got), got)
	}
	first := delta(t, got[1])["tool_calls"].([]any)[0].(map[string]any)
	if first["index"].(float64) != 0 || first["id"] != "call_a" {
		t.Errorf("first tool call = %v", first)
	}
	if first["function"].(map[string]any)["name"] != "f" {
		t.Errorf("first tool call = %v", first)
	}
	second := delta(t, got[3])["tool_calls"].([]any)[0].(map[string]any)
	if second["index"].(float64) != 1 {
		t.Errorf("second tool call index = %v; the wire index is dense, not the IR block index",
			second["index"])
	}
	frag := delta(t, got[4])["tool_calls"].([]any)[0].(map[string]any)
	if frag["index"].(float64) != 0 {
		t.Errorf("continuation index = %v; it must return to the first call", frag["index"])
	}
	if frag["function"].(map[string]any)["arguments"] != "1}" {
		t.Errorf("continuation = %v", frag)
	}
	if _, ok := frag["id"]; ok {
		t.Error("a continuation must not repeat the id")
	}
}

func TestWriteStreamEmitsReasoningDeltas(t *testing.T) {
	got := chunks(t, []ir.StreamEvent{
		{Type: ir.EventMessageStart, ID: "r", Model: "m"},
		{Type: ir.EventContentDelta, Delta: &ir.Delta{Type: ir.BlockThinking, Thinking: "hmm"}},
		{Type: ir.EventContentDelta, Delta: &ir.Delta{Type: ir.BlockText, Text: "42"}},
		{Type: ir.EventMessageStop, StopReason: ir.StopEndTurn},
	})
	if delta(t, got[1])["reasoning_content"] != "hmm" {
		t.Errorf("chunk 1 = %v", got[1])
	}
	if delta(t, got[2])["content"] != "42" {
		t.Errorf("chunk 2 = %v", got[2])
	}
}

func TestWriteStreamUsageChunkCarriesNoChoicesAndTheDetails(t *testing.T) {
	rec := httptest.NewRecorder()
	err := WriteStream(rec, seq(
		ir.StreamEvent{Type: ir.EventMessageDelta, Usage: &ir.Usage{
			InputTokens: 2, OutputTokens: 4, CacheReadTokens: 8, ReasoningTokens: 3}},
		ir.StreamEvent{Type: ir.EventMessageStop},
	))
	if err != nil {
		t.Fatal(err)
	}
	var usageChunk map[string]any
	for _, line := range strings.Split(rec.Body.String(), "\n") {
		if !strings.HasPrefix(line, "data: {") || !strings.Contains(line, `"usage"`) {
			continue
		}
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &usageChunk); err != nil {
			t.Fatal(err)
		}
	}
	if usageChunk == nil {
		t.Fatalf("no usage chunk in:\n%s", rec.Body.String())
	}
	if choices, ok := usageChunk["choices"].([]any); !ok || len(choices) != 0 {
		t.Fatalf("choices = %v, want an empty array", usageChunk["choices"])
	}
	u := usageChunk["usage"].(map[string]any)
	if u["prompt_tokens"] != float64(10) || u["total_tokens"] != float64(14) {
		t.Fatalf("usage = %v", u)
	}
	if u["prompt_tokens_details"].(map[string]any)["cached_tokens"] != float64(8) ||
		u["completion_tokens_details"].(map[string]any)["reasoning_tokens"] != float64(3) {
		t.Fatalf("usage details = %v", u)
	}
}
