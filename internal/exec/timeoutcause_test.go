package exec

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkrouter/internal/config"
)

// stall holds a handler until the client leaves or the test's upstream closes,
// whichever is first, so a timed-out request does not keep the server open.
func stall(r *http.Request) {
	// The server notices a departed client only once the body has been read.
	_, _ = io.Copy(io.Discard, r.Body)
	select {
	case <-r.Context().Done():
	case <-time.After(3 * time.Second):
	}
}

// The attempt row names the bound that fired. Every one of them used to read
// "total timeout exceeded", which sent an operator whose provider went silent
// mid-body to raise policy.timeout.total, a setting that had nothing to do
// with it.
func TestATimeoutNamesTheBoundThatFired(t *testing.T) {
	partialBody := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":`))
		w.(http.Flusher).Flush()
		stall(r)
	}
	unary := `{"model":"m","messages":[{"role":"user","content":"ping"}]}`

	cases := []struct {
		name    string
		handler http.HandlerFunc
		tune    func(*config.Config)
		prepare func(*Executor, <-chan struct{})
		body    string
		want    string
		outcome string
	}{{
		name:    "connect",
		handler: func(w http.ResponseWriter, r *http.Request) {},
		tune: func(c *config.Config) {
			c.Policy.Timeout.Connect = 50 * time.Millisecond
			c.Policy.Timeout.FirstByte = 100 * time.Millisecond
		},
		// A dial that never completes, standing in for a slow DNS lookup or
		// TLS handshake the dialer's own timeout does not cover.
		prepare: func(e *Executor, done <-chan struct{}) {
			e.client.Transport.(*http.Transport).DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
				select {
				case <-ctx.Done():
				case <-done:
				}
				return nil, errors.New("dial abandoned")
			}
		},
		body: unary, want: "darkrouter: connect timeout exceeded", outcome: "retryable_provider",
	}, {
		name: "first_byte",
		handler: func(w http.ResponseWriter, r *http.Request) {
			stall(r)
		},
		tune: func(c *config.Config) {
			c.Policy.Timeout.Connect = 5 * time.Millisecond
			c.Policy.Timeout.FirstByte = 150 * time.Millisecond
		},
		// The transport's own header timeout is fixed at startup, so after a
		// reload lowers first_byte the attempt timer is the one that fires.
		prepare: func(e *Executor, _ <-chan struct{}) {
			e.client.Transport.(*http.Transport).ResponseHeaderTimeout = 0
		},
		body: unary, want: "darkrouter: first_byte timeout exceeded", outcome: "retryable_provider",
	}, {
		name:    "idle",
		handler: partialBody,
		tune: func(c *config.Config) {
			c.Policy.Timeout.Connect = 5 * time.Millisecond
			c.Policy.Timeout.FirstByte = time.Second
			c.Policy.Timeout.Idle = 150 * time.Millisecond
		},
		body: unary, want: "darkrouter: idle timeout exceeded", outcome: "retryable_provider",
	}, {
		// Idle is capped by what remains of total until the first write to the
		// client, and a cap that fires is total's.
		name:    "total",
		handler: partialBody,
		tune: func(c *config.Config) {
			c.Policy.Timeout.Connect = 5 * time.Millisecond
			c.Policy.Timeout.FirstByte = 250 * time.Millisecond
			c.Policy.Timeout.Total = 300 * time.Millisecond
			c.Policy.Timeout.Idle = 5 * time.Second
		},
		body: unary, want: "darkrouter: total timeout exceeded", outcome: "retryable_provider",
	}, {
		name: "idle after commit",
		handler: func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"a\"}}]}\n\n"))
			w.(http.Flusher).Flush()
			stall(r)
		},
		tune: func(c *config.Config) {
			c.Policy.Timeout.Connect = 5 * time.Millisecond
			c.Policy.Timeout.FirstByte = time.Second
			c.Policy.Timeout.Idle = 150 * time.Millisecond
		},
		body: `{"model":"m","stream":true,"messages":[{"role":"user","content":"ping"}]}`,
		want: "darkrouter: idle timeout exceeded", outcome: "success",
	}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			up := httptest.NewServer(tc.handler)
			defer up.Close()
			done := make(chan struct{})
			defer close(done)

			logger := &captureLogger{}
			e, _ := loopExecutor(t, up, twoKeyFleet(), logger, tc.tune)
			if tc.prepare != nil {
				tc.prepare(e, done)
			}
			post(t, e, tc.body)

			a := logger.only(t).Attempts
			if len(a) == 0 {
				t.Fatal("no attempt recorded")
			}
			if !strings.Contains(a[0].Error, tc.want) || a[0].Outcome != tc.outcome {
				t.Errorf("attempt = outcome %q error %q; want outcome %q and an error naming %q",
					a[0].Outcome, a[0].Error, tc.outcome, tc.want)
			}
		})
	}
}

// Classification matches on identity, not text: every bound's cause is still
// the one timeout.
func TestEveryTimeoutCauseIsTheDarkrouterTimeout(t *testing.T) {
	for _, b := range []timeoutBound{boundConnect, boundFirstByte, boundIdle, boundTotal} {
		if err := timeoutCause(b); !errors.Is(err, errDarkrouterTimeout) {
			t.Errorf("%v is not errDarkrouterTimeout", err)
		}
	}
}
