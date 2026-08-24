package exec

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// A provider that commits and then fails must not produce a second response.
func TestPostCommitFailureBecomesAnInStreamError(t *testing.T) {
	sc := &scripted{by: map[string]http.HandlerFunc{
		"g1": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n"))
			w.(http.Flusher).Flush()
			// An in-stream error after committing.
			_, _ = w.Write([]byte("data: {\"error\":{\"message\":\"died\",\"type\":\"api_error\"}}\n\n"))
		},
		"g2": sseOK,
		"c1": sseOK,
	}}
	up := httptest.NewServer(sc)
	defer up.Close()

	logger := &captureLogger{}
	e, _ := loopExecutor(t, up, twoProviderFleet(), logger, "")
	rec := post(t, e, `{"model":"m","stream":true,"messages":[{"role":"user","content":"ping"}]}`)

	body := rec.Body.String()
	if !strings.Contains(body, "partial") {
		t.Fatalf("committed content is missing: %s", body)
	}
	// Failover is impossible once the client has bytes.
	if got := sc.order(); len(got) != 1 {
		t.Errorf("order = %v, want only g1 — a committed stream cannot fail over", got)
	}
	if !strings.Contains(body, "died") {
		t.Errorf("a post-commit failure must surface as an in-stream error: %s", body)
	}
	// This request is passthrough-eligible, so the post-commit bytes are
	// forwarded verbatim (spec §9) rather than re-rendered through the
	// dialect writer — there is no synthesized [DONE] to expect, only exactly
	// what the upstream sent.
	if !strings.HasSuffix(body, "data: {\"error\":{\"message\":\"died\",\"type\":\"api_error\"}}\n\n") {
		t.Errorf("the error event must reach the client verbatim: %q", body)
	}
	// The request is recorded as served, not as a failure to route.
	if r := logger.only(t); r.Status != "success" {
		t.Errorf("Status = %q, want success — the client got its bytes", r.Status)
	}
}

// A committed stream survives past policy.timeout.total.
func TestCommittedStreamOutlivesTheTotalBudget(t *testing.T) {
	sc := &scripted{by: map[string]http.HandlerFunc{
		"g1": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"a\"}}]}\n\n"))
			w.(http.Flusher).Flush()
			// Past the 400ms total, but well inside the 5s idle gap.
			time.Sleep(600 * time.Millisecond)
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"b\"}}]}\n\n"))
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
		},
	}}
	up := httptest.NewServer(sc)
	defer up.Close()

	e, _ := loopExecutor(t, up, twoKeyFleet(), &captureLogger{},
		"policy:\n  timeout:\n    connect: 5ms\n    first_byte: 300ms\n    total: 400ms\n    idle: 5s\n")

	rec := post(t, e, `{"model":"m","stream":true,"messages":[{"role":"user","content":"ping"}]}`)
	body := rec.Body.String()
	if !strings.Contains(body, `"content":"b"`) {
		t.Fatalf("a committed stream was killed by the total budget: %s", body)
	}
}

// A committed stream that goes silent is cut at idle.
func TestCommittedStreamIsCutAtIdle(t *testing.T) {
	sc := &scripted{by: map[string]http.HandlerFunc{
		"g1": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"a\"}}]}\n\n"))
			w.(http.Flusher).Flush()
			time.Sleep(3 * time.Second) // far past idle
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
		},
	}}
	up := httptest.NewServer(sc)
	defer up.Close()

	e, _ := loopExecutor(t, up, twoKeyFleet(), &captureLogger{},
		"policy:\n  timeout:\n    connect: 5ms\n    first_byte: 1s\n    total: 10s\n    idle: 200ms\n")

	done := make(chan string, 1)
	go func() {
		done <- post(t, e, `{"model":"m","stream":true,"messages":[{"role":"user","content":"ping"}]}`).Body.String()
	}()
	select {
	case body := <-done:
		if !strings.Contains(body, `"content":"a"`) {
			t.Errorf("committed content missing: %s", body)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a silent committed stream was not cut at idle")
	}
}
