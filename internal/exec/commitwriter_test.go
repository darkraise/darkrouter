package exec

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCommitWriterStartsUncommitted(t *testing.T) {
	cw := NewCommitWriter(httptest.NewRecorder())
	if cw.Committed() {
		t.Error("a writer nobody has written to reports committed")
	}
	if cw.Bytes() != 0 {
		t.Errorf("bytes = %d", cw.Bytes())
	}
}

func TestWriteHeaderCommits(t *testing.T) {
	// A status line is as irrevocable as a body byte: the client has been told
	// this attempt is the answer.
	rec := httptest.NewRecorder()
	cw := NewCommitWriter(rec)
	cw.WriteHeader(http.StatusOK)
	if !cw.Committed() {
		t.Error("WriteHeader did not commit")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d", rec.Code)
	}
}

func TestWriteCommitsAndCounts(t *testing.T) {
	rec := httptest.NewRecorder()
	cw := NewCommitWriter(rec)
	n, err := cw.Write([]byte("hello"))
	if err != nil || n != 5 {
		t.Fatalf("Write = (%d, %v)", n, err)
	}
	if _, err := cw.Write([]byte(" world")); err != nil {
		t.Fatal(err)
	}
	if !cw.Committed() {
		t.Error("Write did not commit")
	}
	if cw.Bytes() != 11 {
		t.Errorf("bytes = %d, want 11", cw.Bytes())
	}
	if rec.Body.String() != "hello world" {
		t.Errorf("body = %q", rec.Body.String())
	}
}

func TestAnEmptyWriteDoesNotCommit(t *testing.T) {
	// A zero-length write reaches no client and must not end the chain. A
	// surface that probes with one would otherwise lose its failover.
	cw := NewCommitWriter(httptest.NewRecorder())
	if _, err := cw.Write(nil); err != nil {
		t.Fatal(err)
	}
	if cw.Committed() {
		t.Error("an empty write committed")
	}
}

func TestOnCommitFiresExactlyOnce(t *testing.T) {
	// The loop hangs the total-to-idle timeout switch and the diagnostics
	// headers off this hook. Firing it twice would restart the idle clock
	// mid-stream.
	var fired int
	cw := NewCommitWriter(httptest.NewRecorder())
	cw.OnCommit(func() { fired++ })

	cw.WriteHeader(http.StatusOK)
	_, _ = cw.Write([]byte("a"))
	_, _ = cw.Write([]byte("b"))

	if fired != 1 {
		t.Errorf("OnCommit fired %d times, want 1", fired)
	}
}

func TestOnCommitRegisteredAfterCommitFiresImmediately(t *testing.T) {
	// Registration order must not decide whether the hook runs, or a surface
	// that writes before the loop finishes wiring would skip the timer switch.
	var fired int
	cw := NewCommitWriter(httptest.NewRecorder())
	_, _ = cw.Write([]byte("a"))
	cw.OnCommit(func() { fired++ })
	if fired != 1 {
		t.Errorf("OnCommit fired %d times, want 1", fired)
	}
}

func TestFlushPassesThroughAndCommits(t *testing.T) {
	// SSE surfaces flush per event. A recorder implements http.Flusher, and a
	// wrapper that swallowed it would buffer every stream to completion.
	rec := httptest.NewRecorder()
	cw := NewCommitWriter(rec)
	f, ok := any(cw).(http.Flusher)
	if !ok {
		t.Fatal("CommitWriter does not implement http.Flusher")
	}
	f.Flush()
	if !cw.Committed() {
		t.Error("a flush did not commit; the client has seen the headers")
	}
	if !rec.Flushed {
		t.Error("the flush did not reach the underlying writer")
	}
}

func TestHeaderIsTheUnderlyingHeader(t *testing.T) {
	rec := httptest.NewRecorder()
	cw := NewCommitWriter(rec)
	cw.Header().Set("X-Test", "1")
	if rec.Header().Get("X-Test") != "1" {
		t.Error("Header() did not reach the underlying writer")
	}
	if cw.Committed() {
		t.Error("setting a header committed; nothing has been sent yet")
	}
}
