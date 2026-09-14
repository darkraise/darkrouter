package exec

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/adapter/openaicompat"
	"github.com/darkraise/darkrouter/internal/config"
	openaiedge "github.com/darkraise/darkrouter/internal/edge/openai"
	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/provider/providertest"
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
	e, _ := loopExecutor(t, up, twoProviderFleet(), logger, nil)
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

	e, _ := loopExecutor(t, up, twoKeyFleet(), &captureLogger{}, func(c *config.Config) {
		c.Policy.Timeout.Connect = 5 * time.Millisecond
		c.Policy.Timeout.FirstByte = 300 * time.Millisecond
		c.Policy.Timeout.Total = 400 * time.Millisecond
		c.Policy.Timeout.Idle = 5 * time.Second
	})

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

	e, _ := loopExecutor(t, up, twoKeyFleet(), &captureLogger{}, func(c *config.Config) {
		c.Policy.Timeout.Connect = 5 * time.Millisecond
		c.Policy.Timeout.FirstByte = time.Second
		c.Policy.Timeout.Total = 10 * time.Second
		c.Policy.Timeout.Idle = 200 * time.Millisecond
	})

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

// idle bounds a gap in the transfer, not the transfer. A body whose bytes keep
// arriving for longer than idle — long audio, a slow link — must not be cut
// once idle has passed since its headers.
func TestABodyThatKeepsArrivingOutlivesIdle(t *testing.T) {
	const pieces = 8
	trickle := func(contentType string, piece func(i int) string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", contentType)
			for i := range pieces {
				_, _ = w.Write([]byte(piece(i)))
				w.(http.Flusher).Flush()
				time.Sleep(60 * time.Millisecond)
			}
		}
	}
	jsonPiece := func(i int) string {
		parts := []string{`{"id":"x",`, `"model":"m",`, `"choices":[{"message":`, `{"content":"po`,
			`ng"},`, `"finish_reason":"stop"}],`, `"usage":{"prompt_tokens":1,`, `"completion_tokens":1}}`}
		return parts[i]
	}
	cfg := func(c *config.Config) {
		c.Policy.Timeout.Connect = 50 * time.Millisecond
		c.Policy.Timeout.FirstByte = time.Second
		c.Policy.Timeout.Total = 10 * time.Second
		c.Policy.Timeout.Idle = 200 * time.Millisecond
	}

	t.Run("speech", func(t *testing.T) {
		up := httptest.NewServer(trickle("audio/mpeg", func(int) string { return "A" }))
		defer up.Close()
		src := providertest.NewSource(providertest.Keyed("p", "probe", up.URL, "sk", "tts-1"))
		e := executorFor(t, cfg, src, map[string]adapter.Adapter{"probe": openaicompat.New()},
			Deps{Catalog: catalogWith("p", "tts-1", ir.SurfaceTTS)})
		w := httptest.NewRecorder()
		e.HandleSpeech(w, speechRequest(), openaiedge.New())
		if got := w.Body.String(); got != strings.Repeat("A", pieces) {
			t.Errorf("body = %q; the audio was cut while it was still arriving", got)
		}
	})
	for _, tc := range []struct {
		name string
		post func(*testing.T, *Executor, string) *httptest.ResponseRecorder
		body string
	}{
		{"forwarded unary", post, `{"model":"m","messages":[{"role":"user","content":"ping"}]}`},
		{"translated unary", postAnthropic, anthropicPing},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sc := &scripted{by: map[string]http.HandlerFunc{"g1": trickle("application/json", jsonPiece)}}
			up := httptest.NewServer(sc)
			defer up.Close()
			e, _ := breakerExecutor(t, up, oneKeyFleet(), Deps{}, cfg)
			if w := tc.post(t, e, tc.body); w.Code != 200 || !strings.Contains(w.Body.String(), "pong") {
				t.Errorf("status = %d body = %s; the body was cut while it was still arriving",
					w.Code, w.Body.String())
			}
		})
	}
}

// A unary body that arrives slowly is bounded by idle once its headers are in,
// not by the connect+first_byte budget that was only ever meant to cover the
// wait for those headers.
func TestASlowUnaryBodyIsBoundedByIdleNotFirstByte(t *testing.T) {
	slow := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"x","model":"m","choices":[{"message":`))
		w.(http.Flusher).Flush()
		// Past the 300ms first_byte, well inside the 5s idle.
		time.Sleep(600 * time.Millisecond)
		_, _ = w.Write([]byte(`{"content":"pong"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}
	cfg := func(c *config.Config) {
		c.Policy.Timeout.Connect = 5 * time.Millisecond
		c.Policy.Timeout.FirstByte = 300 * time.Millisecond
		c.Policy.Timeout.Total = 5 * time.Second
		c.Policy.Timeout.Idle = 5 * time.Second
	}
	for _, tc := range []struct {
		name string
		post func(*testing.T, *Executor, string) *httptest.ResponseRecorder
		body string
	}{
		{"passthrough", post, `{"model":"m","messages":[{"role":"user","content":"ping"}]}`},
		{"ir", postAnthropic, anthropicPing},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sc := &scripted{by: map[string]http.HandlerFunc{"g1": slow}}
			up := httptest.NewServer(sc)
			defer up.Close()
			e, _ := breakerExecutor(t, up, oneKeyFleet(), Deps{Log: &captureLogger{}}, cfg)
			rec := tc.post(t, e, tc.body)
			if rec.Code != 200 || !strings.Contains(rec.Body.String(), "pong") {
				t.Fatalf("code = %d body = %s; the body read was cut by the pre-commit deadline",
					rec.Code, rec.Body.String())
			}
		})
	}
}
