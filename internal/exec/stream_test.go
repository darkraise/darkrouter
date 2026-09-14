package exec

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/config"
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
	e, _ := loopExecutor(t, up, twoProviderFleet(), logger, nil)
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

func sseErrorOf(typ string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(
			"data: {\"error\":{\"message\":\"refused\",\"type\":\"" + typ + "\"}}\n\n"))
	}
}

// An in-stream error the adapter typed keeps its type. A rejected credential
// or a per-key rate limit says nothing about the provider's other keys, and a
// content filter is the provider answering: the same outcomes a status line
// or a unary body would have produced. Both renderings must agree.
func TestATypedInStreamErrorAdvancesLikeItsStatusWould(t *testing.T) {
	const (
		openaiStream    = `{"model":"m","stream":true,"messages":[{"role":"user","content":"ping"}]}`
		anthropicStream = `{"model":"m","max_tokens":16,"stream":true,"messages":[{"role":"user","content":"ping"}]}`
	)
	for _, render := range []struct {
		name string
		post func(*testing.T, *Executor, string) *httptest.ResponseRecorder
		body string
	}{
		{"forwarded", post, openaiStream},
		{"translated", postAnthropic, anthropicStream},
	} {
		for _, tc := range []struct {
			typ  string
			want []string
		}{
			{"authentication_error", []string{"g1", "g2"}},
			{"rate_limit_exceeded", []string{"g1", "g2"}},
			{"content_filter", []string{"g1"}},
		} {
			t.Run(render.name+"/"+tc.typ, func(t *testing.T) {
				sc := &scripted{by: map[string]http.HandlerFunc{
					"g1": sseErrorOf(tc.typ), "g2": sseOK, "c1": sseOK,
				}}
				up := httptest.NewServer(sc)
				defer up.Close()

				e, _ := loopExecutor(t, up, twoProviderFleet(), &captureLogger{}, nil)
				render.post(t, e, render.body)
				if got := sc.order(); strings.Join(got, ",") != strings.Join(tc.want, ",") {
					t.Errorf("order = %v, want %v", got, tc.want)
				}
			})
		}
	}
}

// The breaker cools a 429 at once rather than counting it toward trip_after,
// and an in-stream rate limit is a 429 in everything but its status line.
func TestAnInStreamRateLimitReachesTheBreakerAsA429(t *testing.T) {
	up := httptest.NewServer(sseErrorOf("rate_limit_exceeded"))
	defer up.Close()
	h := &captureHealth{}
	e := newExecutorWith(t, up.URL, Deps{Health: h}, 0)

	post(t, e, `{"model":"m","stream":true,"messages":[{"role":"user","content":"ping"}]}`)

	if _, s := h.only(t); s.Outcome != adapter.OutcomeRetryableProvider || s.StatusCode != 429 {
		t.Errorf("signal = %+v, want retryable_provider with 429", s)
	}
}

// A forwarded unary body goes out as it arrived, so a 200 carrying the
// provider's error envelope, or a body that is not JSON at all, must be caught
// before commit: served, it is recorded as a success and resets the breaker.
func TestAForwardedUnaryErrorBodyUnder200FailsOver(t *testing.T) {
	for name, body := range map[string]string{
		"error envelope": `{"error":{"message":"overloaded","type":"server_error"}}`,
		"malformed":      `{"id":"x","choices":[`,
	} {
		t.Run(name, func(t *testing.T) {
			sc := &scripted{by: map[string]http.HandlerFunc{
				"g1": func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(body))
				},
				"c1": ok200,
			}}
			up := httptest.NewServer(sc)
			defer up.Close()

			logger := &captureLogger{}
			e, _ := loopExecutor(t, up, twoProviderFleet(), logger, nil)
			w := post(t, e, `{"model":"m","messages":[{"role":"user","content":"ping"}]}`)
			if w.Code != 200 || !strings.Contains(w.Body.String(), "pong") {
				t.Fatalf("status = %d body = %s", w.Code, w.Body.String())
			}
			if got := sc.order(); strings.Join(got, ",") != "g1,c1" {
				t.Errorf("order = %v, want [g1 c1]", got)
			}
			if r := logger.only(t); r.Attempts[0].Path != PathPassthrough || r.Attempts[0].Outcome == "success" {
				t.Errorf("first attempt = %+v, want a failed passthrough", r.Attempts[0])
			}
		})
	}
}

// The client must see exactly one coherent stream, not two spliced together.
func TestStreamReplaysPreCommitEventsExactlyOnce(t *testing.T) {
	sc := &scripted{by: map[string]http.HandlerFunc{"g1": sseOK}}
	up := httptest.NewServer(sc)
	defer up.Close()

	e, _ := loopExecutor(t, up, twoKeyFleet(), &captureLogger{}, nil)
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
		func(c *config.Config) { c.Server.SSE.MaxPrecommitBytes = 4096 })

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
	e, _ := loopExecutor(t, up, twoKeyFleet(), logger, nil)
	post(t, e, `{"model":"m","stream":true,"messages":[{"role":"user","content":"ping"}]}`)

	r := logger.only(t)
	if r.TTFTMs == nil {
		t.Fatal("TTFT was not recorded")
	}
	if r.TokensOut != 5 {
		t.Errorf("TokensOut = %d, want 5", r.TokensOut)
	}
}
