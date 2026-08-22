package exec

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func sseOK(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = w.Write([]byte(
		"data: {\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\n" +
			"data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n" +
			"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
			"data: [DONE]\n\n"))
}

// A 200 whose stream carries an error before any content must fail over.
func sseErrorUnder200(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = w.Write([]byte(
		"data: {\"error\":{\"message\":\"overloaded\",\"type\":\"overloaded_error\"}}\n\n"))
}

func TestStreamFailsOverOnAnInStreamErrorBeforeCommit(t *testing.T) {
	// A two-provider fleet: an in-stream error is provider-level, so per spec
	// §7.2 it skips groq's remaining credentials rather than rotating keys.
	sc := &scripted{by: map[string]http.HandlerFunc{
		"g1": sseErrorUnder200,
		"g2": sseErrorUnder200,
		"c1": sseOK,
	}}
	up := httptest.NewServer(sc)
	defer up.Close()

	logger := &captureLogger{}
	e, _ := loopExecutor(t, up, twoProviderFleet(), logger, "")
	rec := post(t, e, `{"model":"m","stream":true,"messages":[{"role":"user","content":"ping"}]}`)

	body := rec.Body.String()
	if strings.Contains(body, "overloaded") {
		t.Fatal("the failed attempt's error reached the client; it must be discarded")
	}
	if !strings.Contains(body, `"content":"hi"`) {
		t.Fatalf("the second attempt's content is missing: %s", body)
	}
	if got := sc.order(); len(got) != 2 || got[0] != "g1" || got[1] != "c1" {
		t.Errorf("order = %v, want [g1 c1]", got)
	}
}

// The client must see exactly one coherent stream, not two spliced together.
func TestStreamReplaysPreCommitEventsExactlyOnce(t *testing.T) {
	sc := &scripted{by: map[string]http.HandlerFunc{"g1": sseOK}}
	up := httptest.NewServer(sc)
	defer up.Close()

	e, _ := loopExecutor(t, up, twoKeyFleet(), &captureLogger{}, "")
	rec := post(t, e, `{"model":"m","stream":true,"messages":[{"role":"user","content":"ping"}]}`)

	body := rec.Body.String()
	if n := strings.Count(body, `"role":"assistant"`); n != 1 {
		t.Errorf("role delta appears %d times, want exactly 1", n)
	}
	if n := strings.Count(body, `"content":"hi"`); n != 1 {
		t.Errorf("content appears %d times, want exactly 1", n)
	}
	if !strings.HasSuffix(body, "data: [DONE]\n\n") {
		t.Errorf("stream did not terminate: %q", body)
	}
}

// A payload flood must breach the byte cap and be treated as an attempt failure.
func TestStreamPayloadFloodFailsTheAttempt(t *testing.T) {
	flood := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		// Usage-only chunks buffer without ever committing, which is the shape
		// the byte cap exists for.
		for i := 0; i < 2000; i++ {
			fmt.Fprintf(w, "data: {\"id\":%q,\"usage\":{\"prompt_tokens\":1}}\n\n",
				strings.Repeat("p", 100))
		}
	}
	sc := &scripted{by: map[string]http.HandlerFunc{"g1": flood, "g2": flood, "c1": sseOK}}
	up := httptest.NewServer(sc)
	defer up.Close()

	logger := &captureLogger{}
	e, _ := loopExecutor(t, up, twoProviderFleet(), logger,
		"  sse:\n    max_precommit_bytes: 4096\n")

	rec := post(t, e, `{"model":"m","stream":true,"messages":[{"role":"user","content":"ping"}]}`)
	if !strings.Contains(rec.Body.String(), `"content":"hi"`) {
		t.Fatalf("the flood should have failed over to g2: %s", rec.Body.String())
	}
	r := logger.only(t)
	if len(r.Attempts) != 2 {
		t.Errorf("attempts = %d, want 2", len(r.Attempts))
	}
}

func TestStreamRecordsTTFTAndUsage(t *testing.T) {
	sc := &scripted{by: map[string]http.HandlerFunc{"g1": func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(
			"data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n" +
				"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
				"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":5}}\n\n" +
				"data: [DONE]\n\n"))
	}}}
	up := httptest.NewServer(sc)
	defer up.Close()

	logger := &captureLogger{}
	e, _ := loopExecutor(t, up, twoKeyFleet(), logger, "")
	post(t, e, `{"model":"m","stream":true,"messages":[{"role":"user","content":"ping"}]}`)

	r := logger.only(t)
	if r.TTFTMs == nil {
		t.Fatal("TTFT was not recorded")
	}
	if r.TokensOut != 5 {
		t.Errorf("TokensOut = %d, want 5", r.TokensOut)
	}
}
