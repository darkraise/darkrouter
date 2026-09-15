package exec

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/adapter/openaicompat"
	"github.com/darkraise/darkrouter/internal/config"
	anthropicedge "github.com/darkraise/darkrouter/internal/edge/anthropic"
	openaiedge "github.com/darkraise/darkrouter/internal/edge/openai"
	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/writedeadline"
)

// The attempt's timer can fire in the same instant a write to the client
// fails. The copy stopped at the write, so the failure is the client's.
func TestAFailedClientWriteOutranksTheAttemptTimer(t *testing.T) {
	upstream, cancel := context.WithCancelCause(context.Background())
	cancel(errDarkrouterTimeout)
	ac := &AttemptCtx{inbound: context.Background(), upstream: upstream}
	err := fmt.Errorf("%w: %w", errClientWrite, os.ErrDeadlineExceeded)
	if got := ac.readOutcome(err); got != adapter.OutcomeClientCancelled {
		t.Errorf("readOutcome = %q, want %q", got, adapter.OutcomeClientCancelled)
	}
}

// endless answers with a body that never ends while the gateway keeps taking
// it, so the only thing that can stop the response is the client.
func endless(contentType string, chunk []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", contentType)
		for r.Context().Err() == nil {
			if _, err := w.Write(chunk); err != nil {
				return
			}
			w.(http.Flusher).Flush()
		}
	}
}

// A client that stops reading is the client's failure. Its blocked write
// fails at the write deadline, and the provider behind it — which was sending
// as fast as it could — must hear neither a failure nor a success. The write
// deadline is tried both at idle, as the listener sets it, and past idle, where
// the attempt's own idle timer would otherwise expire first on every run.
func TestAClientThatStopsReadingIsNotAProviderFailure(t *testing.T) {
	const idle = 100 * time.Millisecond
	sseChunk := []byte("data: {\"choices\":[{\"delta\":{\"content\":\"" +
		strings.Repeat("x", 1024) + "\"}}]}\n\n")
	chat := `{"model":"m","stream":true,"messages":[{"role":"user","content":"ping"}]}`

	for _, tc := range []struct {
		name     string
		path     string
		body     string
		upstream http.HandlerFunc
		serve    func(e *Executor) http.HandlerFunc
		surface  ir.Surface
		tune     func(*testing.T)
	}{
		{name: "forwarded stream", path: "/v1/chat/completions", body: chat,
			upstream: endless("text/event-stream", sseChunk),
			serve: func(e *Executor) http.HandlerFunc {
				return func(w http.ResponseWriter, r *http.Request) { e.Handle(w, r, openaiedge.New()) }
			}},
		{name: "translated stream", path: "/v1/messages",
			body:     `{"model":"m","max_tokens":16,"stream":true,"messages":[{"role":"user","content":"ping"}]}`,
			upstream: endless("text/event-stream", sseChunk),
			serve: func(e *Executor) http.HandlerFunc {
				return func(w http.ResponseWriter, r *http.Request) { e.Handle(w, r, anthropicedge.New()) }
			}},
		{name: "speech", path: "/v1/audio/speech", body: `{"model":"m","input":"hello","voice":"alloy"}`,
			upstream: endless("audio/mpeg", bytes.Repeat([]byte("A"), 32<<10)),
			serve: func(e *Executor) http.HandlerFunc {
				return func(w http.ResponseWriter, r *http.Request) { e.HandleSpeech(w, r, openaiedge.New()) }
			},
			surface: ir.SurfaceTTS},
		{name: "oversized forwarded unary", path: "/v1/chat/completions",
			body:     `{"model":"m","messages":[{"role":"user","content":"ping"}]}`,
			upstream: endless("application/json", bytes.Repeat([]byte(" "), 32<<10)),
			serve: func(e *Executor) http.HandlerFunc {
				return func(w http.ResponseWriter, r *http.Request) { e.Handle(w, r, openaiedge.New()) }
			},
			tune: func(t *testing.T) {
				old := maxForwardedUnaryBytes
				maxForwardedUnaryBytes = 1 << 10
				t.Cleanup(func() { maxForwardedUnaryBytes = old })
			}},
	} {
		for _, writeDeadline := range []time.Duration{idle, 3 * idle} {
			t.Run(fmt.Sprintf("%s/write deadline %s", tc.name, writeDeadline), func(t *testing.T) {
				if tc.tune != nil {
					tc.tune(t)
				}
				up := httptest.NewServer(&scripted{by: map[string]http.HandlerFunc{"g1": tc.upstream}})
				defer up.Close()

				fleet := oneKeyFleet()
				fleet[0].BaseURL, fleet[0].Kind = up.URL, "openaicompat"
				cfg := testConfig(t, func(c *config.Config) {
					c.Policy.Timeout.Connect = time.Second
					c.Policy.Timeout.FirstByte = 2 * time.Second
					c.Policy.Timeout.Total = 10 * time.Second
					c.Policy.Timeout.Idle = idle
				})
				h, logger := &captureHealth{}, &captureLogger{}
				deps := Deps{Health: h, Log: logger}
				if tc.surface != "" {
					deps.Catalog = catalogWith("groq", "m", tc.surface)
				}
				e := New(config.NewStoreOf(cfg), &fleetSource{ps: fleet},
					map[string]adapter.Adapter{"openaicompat": openaicompat.New()}, deps)

				returned := make(chan struct{})
				serve := tc.serve(e)
				gw := httptest.NewServer(writedeadline.Handler(http.HandlerFunc(
					func(w http.ResponseWriter, r *http.Request) {
						defer close(returned)
						serve(w, r)
					}), func() time.Duration { return writeDeadline }))
				defer gw.Close()

				conn, err := net.Dial("tcp", gw.Listener.Addr().String())
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				_ = conn.(*net.TCPConn).SetReadBuffer(4 << 10)
				if _, err := fmt.Fprintf(conn, "POST %s HTTP/1.1\r\nHost: test\r\n"+
					"Content-Type: application/json\r\nContent-Length: %d\r\n\r\n%s",
					tc.path, len(tc.body), tc.body); err != nil {
					t.Fatal(err)
				}

				select {
				case <-returned:
				case <-time.After(10 * time.Second):
					t.Fatal("the handler was still writing to a client that stopped reading")
				}

				_, sig := h.only(t)
				if sig.Outcome != adapter.OutcomeClientCancelled {
					t.Errorf("breaker heard %q, want %q — the provider was sending the whole time",
						sig.Outcome, adapter.OutcomeClientCancelled)
				}
				if r := logger.only(t); r.ErrorCode != "" {
					t.Errorf("ErrorCode = %q, want none — the response failed at the client", r.ErrorCode)
				}
			})
		}
	}
}
