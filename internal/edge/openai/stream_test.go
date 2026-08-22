package openai

import (
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
