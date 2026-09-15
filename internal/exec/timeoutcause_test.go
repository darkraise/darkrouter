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

	"github.com/darkraise/darkrouter/internal/auth"
	"github.com/darkraise/darkrouter/internal/config"
	"github.com/darkraise/darkrouter/internal/provider"
)

// refreshingAuth reaches a token endpoint before it authorizes, through the
// context it is handed, as an OAuth credential due for refresh does.
type refreshingAuth struct{ tokenURL string }

func (a refreshingAuth) For(ctx context.Context, _ auth.Target, _ auth.Credential) (auth.Authorizer, error) {
	tr := &http.Transport{}
	defer tr.CloseIdleConnections()
	req, err := http.NewRequestWithContext(ctx, "GET", a.tokenURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := (&http.Client{Transport: tr}).Do(req)
	if err != nil {
		return nil, err
	}
	_ = resp.Body.Close()
	return func(context.Context, *http.Request) error { return nil }, nil
}

// hangingDial is a dial that never completes until the attempt gives up.
func hangingDial(done <-chan struct{}) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, _, _ string) (net.Conn, error) {
		select {
		case <-ctx.Done():
		case <-done:
		}
		return nil, errors.New("dial abandoned")
	}
}

// A connection the credential made on its way to a token endpoint is not a
// connection for the send. A send that then never connects is still waiting
// to connect.
func TestAConnectionMadeForTheCredentialIsNotTheSends(t *testing.T) {
	token := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer token.Close()
	up := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer up.Close()
	done := make(chan struct{})
	defer close(done)

	fleet := []provider.Provider{{
		ID: "groq", Models: []string{"m"}, AuthStyle: auth.StyleOAuth,
		Credentials: []provider.Credential{{ID: "g1", Secret: "g1", Enabled: true}},
	}}
	logger := &captureLogger{}
	e, _ := loopExecutor(t, up, fleet, logger, func(c *config.Config) {
		c.Policy.Timeout.Connect = 50 * time.Millisecond
		c.Policy.Timeout.FirstByte = 100 * time.Millisecond
	})
	e.deps.Auth = refreshingAuth{tokenURL: token.URL}
	e.client.Transport.(*http.Transport).DialContext = hangingDial(done)
	post(t, e, `{"model":"m","messages":[{"role":"user","content":"ping"}]}`)

	a := logger.only(t).Attempts
	if len(a) == 0 || !strings.Contains(a[0].Error, "darkrouter: connect timeout exceeded") {
		t.Errorf("attempts = %+v, want an error naming connect", a)
	}
}

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
		// The transport's dialer carries connect itself, fixed at startup, and
		// fires before the attempt timer whenever a reload has not lowered it.
		name:    "connect by the transport",
		handler: func(w http.ResponseWriter, r *http.Request) {},
		tune: func(c *config.Config) {
			c.Policy.Timeout.Connect = time.Second
			c.Policy.Timeout.FirstByte = time.Second
		},
		prepare: func(e *Executor, _ <-chan struct{}) {
			e.client.Transport.(*http.Transport).DialContext =
				(&net.Dialer{Deadline: time.Now().Add(-time.Second)}).DialContext
		},
		body: unary, want: "darkrouter: connect timeout exceeded", outcome: "retryable_provider",
	}, {
		name: "first_byte by the transport",
		handler: func(w http.ResponseWriter, r *http.Request) {
			stall(r)
		},
		tune: func(c *config.Config) {
			c.Policy.Timeout.Connect = time.Second
			c.Policy.Timeout.FirstByte = 2 * time.Second
		},
		prepare: func(e *Executor, _ <-chan struct{}) {
			e.client.Transport.(*http.Transport).ResponseHeaderTimeout = 100 * time.Millisecond
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

// The timer can fire in the instant the attempt moves it to another bound.
// That firing is the old bound expiring, and it is what cancels the attempt,
// so the cause it records must name the old bound rather than the new one.
func TestAFiringUnderwayKeepsTheNameOfTheBoundThatFired(t *testing.T) {
	for _, tc := range []struct {
		name  string
		fired timeoutBound
		rearm func(*AttemptCtx)
		want  string
	}{
		{name: "moved to idle", fired: boundFirstByte, want: "first_byte",
			rearm: func(ac *AttemptCtx) { ac.resetIdle() }},
		{name: "moved to a new send", fired: boundIdle, want: "idle",
			rearm: func(ac *AttemptCtx) { ac.resetSend() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{}
			cfg.Policy.Timeout.Idle = time.Hour
			ac := &AttemptCtx{Cfg: cfg, bud: budget{deadline: time.Now().Add(10 * time.Hour), perTry: time.Hour}}
			ac.bound.Store(int32(tc.fired))
			ac.connected.Store(true)
			ac.idleArmed = tc.fired == boundIdle

			started, release := make(chan struct{}), make(chan struct{})
			causes := make(chan error, 1)
			ac.Timer = time.AfterFunc(0, func() {
				close(started)
				<-release
				causes <- ac.firedCause()
			})
			defer ac.Timer.Stop()
			<-started

			tc.rearm(ac)
			close(release)
			if got := <-causes; got.Error() != "darkrouter: "+tc.want+" timeout exceeded" {
				t.Errorf("cause = %q, want the %s bound that fired", got, tc.want)
			}
		})
	}
}

// A timer held off during a write to the client is renamed when it is armed
// again: before the first write idle may be capped by total, after it only
// idle applies.
func TestATimerHeldForAWriteTakesItsNewBound(t *testing.T) {
	cfg := &config.Config{}
	cfg.Policy.Timeout.Idle = time.Hour
	ac := &AttemptCtx{Cfg: cfg, bud: budget{deadline: time.Now().Add(30 * time.Minute)}}
	ac.Timer = time.AfterFunc(time.Hour, func() {})
	defer ac.Timer.Stop()

	ac.resetIdle()
	if got := timeoutBound(ac.bound.Load()); got != boundTotal {
		t.Fatalf("bound = %v before the first write, want total", got)
	}
	ac.beginWrite()
	ac.endWrite()
	if got := timeoutBound(ac.bound.Load()); got != boundIdle {
		t.Errorf("bound = %v after a write, want idle", got)
	}
}
