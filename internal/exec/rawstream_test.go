package exec

import (
	"testing"

	"github.com/darkraise/darkrouter/internal/ir"
)

func TestSplitterCutsAtEventBoundaries(t *testing.T) {
	s := &eventSplitter{max: 1 << 16}
	got, err := s.push([]byte("event: a\ndata: 1\n\nevent: b\ndata: 2\n\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d events, want 2: %q", len(got), got)
	}
	if string(got[0]) != "event: a\ndata: 1\n\n" {
		t.Errorf("event 0 = %q", got[0])
	}
	if string(got[1]) != "event: b\ndata: 2\n\n" {
		t.Errorf("event 1 = %q", got[1])
	}
}

func TestSplitterCarriesAPartialEventAcrossReads(t *testing.T) {
	// The failure this prevents: a provider writing "data: {" and "...}\n\n" in
	// two TCP segments, and a recognizer that saw neither as an event.
	s := &eventSplitter{max: 1 << 16}
	for _, chunk := range []string{"data: {\"a\"", ":1}\n"} {
		got, err := s.push([]byte(chunk))
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 0 {
			t.Fatalf("premature event from %q: %q", chunk, got)
		}
	}
	got, err := s.push([]byte("\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || string(got[0]) != "data: {\"a\":1}\n\n" {
		t.Errorf("got %q", got)
	}
}

func TestSplitterAcceptsCRLFAndLoneCR(t *testing.T) {
	// The SSE grammar permits all three terminators and providers differ.
	for _, tc := range []struct{ name, in string }{
		{"crlf", "data: 1\r\n\r\n"},
		{"lone cr", "data: 1\r\r"},
		{"lf", "data: 1\n\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &eventSplitter{max: 1 << 16}
			got, err := s.push([]byte(tc.in))
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 || string(got[0]) != tc.in {
				t.Errorf("got %q, want %q", got, tc.in)
			}
		})
	}
}

func TestSplitterRefusesAnUnboundedCarry(t *testing.T) {
	s := &eventSplitter{max: 64}
	_, err := s.push(make([]byte, 65))
	if err == nil {
		t.Fatal("want an error when the carry exceeds the cap")
	}
}

func TestFlushReturnsAnUnterminatedTail(t *testing.T) {
	// A provider that ends its stream without a trailing blank line still owes
	// the client those bytes.
	s := &eventSplitter{max: 1 << 16}
	if _, err := s.push([]byte("data: last\n")); err != nil {
		t.Fatal(err)
	}
	if got := string(s.flush()); got != "data: last\n" {
		t.Errorf("flush = %q", got)
	}
	if got := s.flush(); len(got) != 0 {
		t.Errorf("a second flush returned %q", got)
	}
}

func TestParseEventReadsTheFieldsBack(t *testing.T) {
	ev, ok := parseEvent([]byte("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"), 1<<16)
	if !ok {
		t.Fatal("parseEvent reported failure")
	}
	if ev.Type != "message_stop" || ev.Data != `{"type":"message_stop"}` {
		t.Errorf("ev = %+v", ev)
	}
}

func TestParseEventReportsAComment(t *testing.T) {
	// OpenRouter sends ": OPENROUTER PROCESSING" as a keepalive. It dispatches
	// nothing, and treating it as an unrecognized event would be the same
	// answer by a longer route.
	if _, ok := parseEvent([]byte(": OPENROUTER PROCESSING\n\n"), 1<<16); ok {
		t.Error("a comment dispatched an event")
	}
}

func TestMergeUsageKeepsBothHalvesOfAnAnthropicStream(t *testing.T) {
	// spec §7: assigning message_delta's usage would erase the input count and
	// compute a wrong cost on every cached or long-prompt request.
	var got ir.Usage
	mergeUsage(&got, &ir.Usage{InputTokens: 100, CacheReadTokens: 40, CacheWriteTokens: 10})
	mergeUsage(&got, &ir.Usage{OutputTokens: 25})

	want := ir.Usage{InputTokens: 100, OutputTokens: 25, CacheReadTokens: 40, CacheWriteTokens: 10}
	if got != want {
		t.Errorf("usage = %+v, want %+v", got, want)
	}
}

func TestMergeUsageDoesNotZeroAKnownCount(t *testing.T) {
	got := ir.Usage{InputTokens: 100, OutputTokens: 25}
	mergeUsage(&got, &ir.Usage{OutputTokens: 0})
	if got.OutputTokens != 25 {
		t.Errorf("a later zero erased a known count: %+v", got)
	}
}
