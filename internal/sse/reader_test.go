package sse

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func readAll(t *testing.T, body string, maxLine int) []Event {
	t.Helper()
	r := NewReader(strings.NewReader(body), maxLine)
	var got []Event
	for {
		ev, err := r.Next()
		if errors.Is(err, io.EOF) {
			return got
		}
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		got = append(got, ev)
	}
}

func TestReaderParsesSimpleEvent(t *testing.T) {
	got := readAll(t, "data: hello\n\n", 1024)
	if len(got) != 1 || got[0].Data != "hello" {
		t.Fatalf("got %+v", got)
	}
}

func TestReaderIgnoresCommentLines(t *testing.T) {
	// OpenRouter emits ": OPENROUTER PROCESSING" keepalives. A parser that
	// treats every line as data breaks on the most popular aggregator.
	got := readAll(t, ": OPENROUTER PROCESSING\n\ndata: hello\n\n", 1024)
	if len(got) != 1 || got[0].Data != "hello" {
		t.Fatalf("comment line was not ignored: %+v", got)
	}
}

func TestReaderJoinsMultipleDataLines(t *testing.T) {
	got := readAll(t, "data: one\ndata: two\n\n", 1024)
	if len(got) != 1 || got[0].Data != "one\ntwo" {
		t.Fatalf("got %q", got[0].Data)
	}
}

func TestReaderHandlesCarriageReturnTerminators(t *testing.T) {
	got := readAll(t, "data: a\r\rdata: b\r\r", 1024)
	if len(got) != 2 || got[0].Data != "a" || got[1].Data != "b" {
		t.Fatalf("lone CR not handled: %+v", got)
	}
}

func TestReaderReadsEventAndIDFields(t *testing.T) {
	got := readAll(t, "event: ping\nid: 7\ndata: x\n\n", 1024)
	if len(got) != 1 || got[0].Type != "ping" || got[0].ID != "7" {
		t.Fatalf("got %+v", got)
	}
}

func TestReaderStripsOneOptionalSpaceOnly(t *testing.T) {
	got := readAll(t, "data:  padded\n\n", 1024)
	if got[0].Data != " padded" {
		t.Fatalf("expected exactly one space stripped, got %q", got[0].Data)
	}
}

func TestReaderSurfacesDoneSentinel(t *testing.T) {
	got := readAll(t, "data: [DONE]\n\n", 1024)
	if len(got) != 1 || got[0].Data != Done {
		t.Fatalf("got %+v", got)
	}
}

func TestReaderTreatsEOFWithoutDoneAsNormalEnd(t *testing.T) {
	got := readAll(t, "data: partial\n\n", 1024)
	if len(got) != 1 {
		t.Fatalf("got %+v", got)
	}
}

func TestReaderRejectsOversizedLine(t *testing.T) {
	body := "data: " + strings.Repeat("x", 200) + "\n\n"
	r := NewReader(strings.NewReader(body), 64)
	if _, err := r.Next(); !errors.Is(err, ErrLineTooLong) {
		t.Fatalf("expected ErrLineTooLong, got %v", err)
	}
}

func TestReaderIgnoresUnknownFields(t *testing.T) {
	got := readAll(t, "foo: bar\ndata: x\n\n", 1024)
	if len(got) != 1 || got[0].Data != "x" {
		t.Fatalf("got %+v", got)
	}
}
