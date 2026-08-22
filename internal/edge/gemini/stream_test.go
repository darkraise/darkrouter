package gemini

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/ir"
)

func seq(events []ir.StreamEvent, final error) func(func(ir.StreamEvent, error) bool) {
	return func(yield func(ir.StreamEvent, error) bool) {
		for _, ev := range events {
			if !yield(ev, nil) {
				return
			}
		}
		if final != nil {
			yield(ir.StreamEvent{}, final)
		}
	}
}

func sseChunks(t *testing.T, events []ir.StreamEvent, final error) []map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	if err := writeStream(rec, seq(events, final), true); err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	for _, line := range strings.Split(rec.Body.String(), "\n") {
		payload, ok := strings.CutPrefix(line, "data: ")
		if !ok {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(payload), &m); err != nil {
			t.Fatalf("chunk %q: %v", payload, err)
		}
		out = append(out, m)
	}
	return out
}

func arrayChunks(t *testing.T, events []ir.StreamEvent, final error) []map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	if err := writeStream(rec, seq(events, final), false); err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("body %q: %v", rec.Body.String(), err)
	}
	return out
}

func textEvents() []ir.StreamEvent {
	return []ir.StreamEvent{
		{Type: ir.EventMessageStart, ID: "r1", Model: "gemini-2.0-flash"},
		{Type: ir.EventBlockStart, Index: 0, Delta: &ir.Delta{Type: ir.BlockText}},
		{Type: ir.EventContentDelta, Index: 0, Delta: &ir.Delta{Type: ir.BlockText, Text: "Hel"}},
		{Type: ir.EventContentDelta, Index: 0, Delta: &ir.Delta{Type: ir.BlockText, Text: "lo"}},
		{Type: ir.EventBlockStop, Index: 0},
		{Type: ir.EventMessageDelta, Usage: &ir.Usage{InputTokens: 3, OutputTokens: 2}},
		{Type: ir.EventMessageStop, StopReason: ir.StopEndTurn},
	}
}

// sseBody re-runs the SSE writer and returns the raw body, for assertions about
// framing rather than payload.
func sseBody(t *testing.T, events []ir.StreamEvent) string {
	t.Helper()
	rec := httptest.NewRecorder()
	if err := writeStream(rec, seq(events, nil), true); err != nil {
		t.Fatal(err)
	}
	return rec.Body.String()
}

func TestWriteStreamSSEEmitsOneChunkPerFragment(t *testing.T) {
	got := sseChunks(t, textEvents(), nil)
	if len(got) != 3 {
		t.Fatalf("chunks = %d, want two fragments and a terminal chunk: %v", len(got), got)
	}
	first := got[0]["candidates"].([]any)[0].(map[string]any)
	part := first["content"].(map[string]any)["parts"].([]any)[0].(map[string]any)
	if part["text"] != "Hel" {
		t.Errorf("first chunk = %v; chunks are incremental", got[0])
	}
	if _, ok := first["finishReason"]; ok {
		t.Errorf("first chunk = %v; only the terminal chunk finishes", first)
	}
	last := got[2]["candidates"].([]any)[0].(map[string]any)
	if last["finishReason"] != "STOP" {
		t.Errorf("terminal chunk = %v", last)
	}
	u := got[2]["usageMetadata"].(map[string]any)
	if u["candidatesTokenCount"].(float64) != 2 {
		t.Errorf("usage = %v; the last chunk is authoritative", u)
	}
	if strings.Contains(sseBody(t, textEvents()), "[DONE]") {
		t.Error("Gemini sends no DONE sentinel")
	}
}

func TestWriteStreamArrayFormIsValidJSON(t *testing.T) {
	got := arrayChunks(t, textEvents(), nil)
	if len(got) != 3 {
		t.Fatalf("chunks = %d: %v", len(got), got)
	}
	rec := httptest.NewRecorder()
	if err := writeStream(rec, seq(textEvents(), nil), false); err != nil {
		t.Fatal(err)
	}
	body := strings.TrimSpace(rec.Body.String())
	if !strings.HasPrefix(body, "[") || !strings.HasSuffix(body, "]") {
		t.Errorf("body = %q; the array form must be bracketed", body)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("content-type = %q", ct)
	}
}

func TestWriteStreamEmptyStreamIsStillAValidArray(t *testing.T) {
	got := arrayChunks(t, nil, nil)
	if got == nil {
		t.Fatal("an empty stream must still produce []")
	}
}

func TestWriteStreamReassemblesAFunctionCall(t *testing.T) {
	got := sseChunks(t, []ir.StreamEvent{
		{Type: ir.EventMessageStart, ID: "r", Model: "m"},
		{Type: ir.EventBlockStart, Index: 1000, Delta: &ir.Delta{
			Type: ir.BlockToolUse, ToolID: "call_a", ToolName: "f"}},
		{Type: ir.EventContentDelta, Index: 1000, Delta: &ir.Delta{
			Type: ir.BlockToolUse, ToolInput: `{"x":`}},
		{Type: ir.EventContentDelta, Index: 1000, Delta: &ir.Delta{
			Type: ir.BlockToolUse, ToolInput: `1}`}},
		{Type: ir.EventBlockStop, Index: 1000},
		{Type: ir.EventMessageStop, StopReason: ir.StopToolUse},
	}, nil)

	var call map[string]any
	for _, c := range got {
		cands, ok := c["candidates"].([]any)
		if !ok || len(cands) == 0 {
			continue
		}
		parts, ok := cands[0].(map[string]any)["content"].(map[string]any)["parts"].([]any)
		if !ok {
			continue
		}
		for _, p := range parts {
			if fc, ok := p.(map[string]any)["functionCall"]; ok {
				call = fc.(map[string]any)
			}
		}
	}
	if call == nil {
		t.Fatalf("no functionCall part: %v", got)
	}
	if call["name"] != "f" || call["id"] != "call_a" {
		t.Errorf("functionCall = %v", call)
	}
	args, ok := call["args"].(map[string]any)
	if !ok || args["x"].(float64) != 1 {
		t.Errorf("args = %#v; fragments must be reassembled into one object", call["args"])
	}
}

func TestWriteStreamCarriesThoughtSignatures(t *testing.T) {
	got := sseChunks(t, []ir.StreamEvent{
		{Type: ir.EventMessageStart, ID: "r", Model: "m"},
		{Type: ir.EventContentDelta, Delta: &ir.Delta{Type: ir.BlockThinking, Thinking: "weighing"}},
		{Type: ir.EventContentDelta, Delta: &ir.Delta{Type: ir.BlockThinking, Signature: "sig-1"}},
		{Type: ir.EventMessageStop, StopReason: ir.StopEndTurn},
	}, nil)

	var sawThought, sawSig bool
	for _, c := range got {
		cands, _ := c["candidates"].([]any)
		if len(cands) == 0 {
			continue
		}
		parts, _ := cands[0].(map[string]any)["content"].(map[string]any)["parts"].([]any)
		for _, p := range parts {
			m := p.(map[string]any)
			if m["thought"] == true && m["text"] == "weighing" {
				sawThought = true
			}
			if m["thoughtSignature"] == "sig-1" {
				sawSig = true
			}
		}
	}
	if !sawThought || !sawSig {
		t.Fatalf("chunks = %v", got)
	}
}

func TestWriteStreamEndsAnErroredStreamWithAFeedbackChunk(t *testing.T) {
	got := sseChunks(t, []ir.StreamEvent{
		{Type: ir.EventMessageStart, ID: "r", Model: "m"},
		{Type: ir.EventContentDelta, Index: 0, Delta: &ir.Delta{Type: ir.BlockText, Text: "partial"}},
	}, &ir.Error{Type: ir.ErrOverloaded, Message: "upstream gave up"})

	last := got[len(got)-1]
	fb, ok := last["promptFeedback"].(map[string]any)
	if !ok {
		t.Fatalf("last chunk = %v; Gemini SSE has no error event, so the shape is a chunk", last)
	}
	if fb["blockReason"] != "OTHER" {
		t.Errorf("blockReason = %v; SAFETY would send the client chasing its own prompt", fb["blockReason"])
	}
	if !strings.Contains(fb["blockReasonMessage"].(string), "upstream gave up") {
		t.Errorf("blockReasonMessage = %v", fb["blockReasonMessage"])
	}
	c := last["candidates"].([]any)[0].(map[string]any)
	if c["finishReason"] != "OTHER" {
		t.Errorf("finishReason = %v", c["finishReason"])
	}
}

func TestWriteStreamContentFilterErrorReportsSafety(t *testing.T) {
	got := sseChunks(t, []ir.StreamEvent{
		{Type: ir.EventMessageStart, ID: "r", Model: "m"},
		{Type: ir.EventContentDelta, Index: 0, Delta: &ir.Delta{Type: ir.BlockText, Text: "partial"}},
	}, &ir.Error{Type: ir.ErrContentFilter, Message: "blocked"})

	last := got[len(got)-1]
	if last["promptFeedback"].(map[string]any)["blockReason"] != "SAFETY" {
		t.Errorf("last chunk = %v", last)
	}
}
