package exec

import (
	"errors"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/ir"
)

func TestContentBearingEvents(t *testing.T) {
	cases := []struct {
		name string
		ev   ir.StreamEvent
		want bool
	}{
		{"message start does not commit", ir.StreamEvent{Type: ir.EventMessageStart}, false},
		{"ping does not commit", ir.StreamEvent{Type: ir.EventPing}, false},
		{"block start does not commit", ir.StreamEvent{Type: ir.EventBlockStart}, false},
		{"empty delta does not commit", ir.StreamEvent{
			Type: ir.EventContentDelta, Delta: &ir.Delta{Type: ir.BlockText, Text: ""}}, false},
		{"text delta commits", ir.StreamEvent{
			Type: ir.EventContentDelta, Delta: &ir.Delta{Type: ir.BlockText, Text: "hi"}}, true},
		// Thinking commits: a reasoning model can think for a minute before its
		// first text token, and holding the response open that long would blow
		// the pre-commit budget.
		{"thinking delta commits", ir.StreamEvent{
			Type: ir.EventContentDelta, Delta: &ir.Delta{Type: ir.BlockThinking, Thinking: "hmm"}}, true},
		{"tool input commits", ir.StreamEvent{
			Type: ir.EventContentDelta, Delta: &ir.Delta{Type: ir.BlockToolUse, ToolInput: `{"a`}}, true},
		{"nil delta does not commit", ir.StreamEvent{Type: ir.EventContentDelta}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsContentBearing(tc.ev); got != tc.want {
				t.Errorf("IsContentBearing = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestBufferReplaysEventsExactlyOnceInOrder(t *testing.T) {
	b := newPreCommitBuffer(1 << 20)
	for _, ev := range []ir.StreamEvent{
		{Type: ir.EventMessageStart, ID: "x", Model: "m"},
		{Type: ir.EventPing},
		{Type: ir.EventContentDelta, Delta: &ir.Delta{Type: ir.BlockText, Text: "hi"}},
	} {
		if err := b.add(ev); err != nil {
			t.Fatal(err)
		}
	}
	got := b.events()
	if len(got) != 3 {
		t.Fatalf("got %d events, want 3", len(got))
	}
	if got[0].Type != ir.EventMessageStart || got[2].Delta.Text != "hi" {
		t.Errorf("events = %+v", got)
	}
	if len(b.events()) != 3 {
		t.Error("events() is not repeatable")
	}
}

// A payload flood must breach the byte cap rather than waiting out first_byte.
func TestBufferBreachesTheByteCapOnAPayloadFlood(t *testing.T) {
	b := newPreCommitBuffer(256)
	var err error
	for i := 0; i < 10000 && err == nil; i++ {
		err = b.add(ir.StreamEvent{
			Type:  ir.EventContentDelta,
			Delta: &ir.Delta{Type: ir.BlockText, Text: strings.Repeat("x", 64)},
		})
	}
	if !errors.Is(err, ErrPreCommitBufferFull) {
		t.Fatalf("err = %v, want ErrPreCommitBufferFull", err)
	}
}

func TestBufferCountsOnlyPayloadBytes(t *testing.T) {
	b := newPreCommitBuffer(100)
	// Fifty pings carry no payload and must not exhaust a byte cap on their own;
	// the time bound is what stops an infinite ping stream.
	for i := 0; i < 50; i++ {
		if err := b.add(ir.StreamEvent{Type: ir.EventPing}); err != nil {
			t.Fatalf("ping %d exhausted the byte cap: %v", i, err)
		}
	}
}

func TestBufferWithNoCapIsUnbounded(t *testing.T) {
	b := newPreCommitBuffer(0)
	for i := 0; i < 1000; i++ {
		if err := b.add(ir.StreamEvent{
			Type:  ir.EventContentDelta,
			Delta: &ir.Delta{Type: ir.BlockText, Text: strings.Repeat("y", 100)},
		}); err != nil {
			t.Fatalf("a zero cap must mean unbounded, got %v", err)
		}
	}
}
