package sse

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWriterSetsAntiBufferingHeaders(t *testing.T) {
	rec := httptest.NewRecorder()
	NewWriter(rec)
	h := rec.Header()
	for k, want := range map[string]string{
		"Content-Type":      "text/event-stream",
		"Cache-Control":     "no-cache",
		"Connection":        "keep-alive",
		"X-Accel-Buffering": "no", // nginx in front of a homelab box is normal
	} {
		if got := h.Get(k); got != want {
			t.Errorf("header %s = %q, want %q", k, got, want)
		}
	}
}

func TestWriterEmitsDataAndBlankLine(t *testing.T) {
	rec := httptest.NewRecorder()
	w := NewWriter(rec)
	if err := w.Send("", `{"a":1}`); err != nil {
		t.Fatal(err)
	}
	if got := rec.Body.String(); got != "data: {\"a\":1}\n\n" {
		t.Fatalf("got %q", got)
	}
}

func TestWriterEmitsNamedEvent(t *testing.T) {
	rec := httptest.NewRecorder()
	w := NewWriter(rec)
	if err := w.Send("ping", "{}"); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(rec.Body.String(), "event: ping\ndata: {}") {
		t.Fatalf("got %q", rec.Body.String())
	}
}

func TestWriterSendDoneEmitsSentinel(t *testing.T) {
	rec := httptest.NewRecorder()
	w := NewWriter(rec)
	if err := w.SendDone(); err != nil {
		t.Fatal(err)
	}
	if got := rec.Body.String(); got != "data: [DONE]\n\n" {
		t.Fatalf("got %q", got)
	}
}

func TestWriterSplitsMultilineData(t *testing.T) {
	rec := httptest.NewRecorder()
	w := NewWriter(rec)
	if err := w.Send("", "one\ntwo"); err != nil {
		t.Fatal(err)
	}
	if got := rec.Body.String(); got != "data: one\ndata: two\n\n" {
		t.Fatalf("got %q", got)
	}
}
