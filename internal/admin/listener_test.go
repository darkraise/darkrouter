package admin

import (
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkrouter/internal/catalog"
)

// freePort finds a port the listener can bind, so the test does not depend on
// the vendor's real registered one being free on this machine.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}

func serverWithListener(t *testing.T) (*Server, *http.Cookie, string, *fakeAuthServer, int) {
	t.Helper()
	port := freePort(t)
	s, cookie, token, fake := serverWithRedirectStyle(t,
		catalog.Redirect{Style: "localhost", Port: port})
	t.Cleanup(s.Close)
	return s, cookie, token, fake, port
}

func TestCallbackCompletesAListenerFlow(t *testing.T) {
	s, cookie, token, fake, port := serverWithListener(t)
	id := oauthProvider(t, s, cookie, token)
	start := startFlow(t, s, cookie, token, id, `{}`)
	if start.Style != "localhost" {
		t.Fatalf("style = %q, listener_error = %q", start.Style, start.ListenerError)
	}

	// The browser follows the vendor's redirect to the loopback listener.
	// Driving it directly is the same request the browser would make.
	resp, err := http.Get(start.RedirectURI + "?code=the-code&state=" + url.QueryEscape(start.State))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("listener returned %d: %s", resp.StatusCode, body)
	}
	fake.mu.Lock()
	code := fake.code
	fake.mu.Unlock()
	if code != "the-code" {
		t.Errorf("the exchange sent code %q", code)
	}
	_ = port
}

func TestTheListenerBindsLoopbackOnly(t *testing.T) {
	// Binding 0.0.0.0 would put a code-accepting endpoint on the LAN for the
	// duration of the flow.
	l, err := newRedirectListener(0)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	host, _, err := net.SplitHostPort(l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		t.Errorf("listening on %s, want loopback", host)
	}
}

func TestTheListenerStopsAfterOneCallback(t *testing.T) {
	// A listener that outlived its flow holds a port the operator's own tooling
	// may want, and accepts a code with no flow to match it.
	s, cookie, token, _, port := serverWithListener(t)
	id := oauthProvider(t, s, cookie, token)
	start := startFlow(t, s, cookie, token, id, `{}`)

	resp, err := http.Get(start.RedirectURI + "?code=c&state=" + url.QueryEscape(start.State))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	addr := "127.0.0.1:" + strconv.Itoa(port)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err != nil {
			return
		}
		_ = c.Close()
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf("the listener on %s is still accepting", addr)
}

func TestCloseStopsEveryListener(t *testing.T) {
	// Process hygiene: a listener left bound holds a port after the gateway is
	// gone.
	s, cookie, token, _, port := serverWithListener(t)
	id := oauthProvider(t, s, cookie, token)
	startFlow(t, s, cookie, token, id, `{}`)
	s.Close()

	addr := "127.0.0.1:" + strconv.Itoa(port)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err != nil {
			return
		}
		_ = c.Close()
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf("a listener on %s survived Close", addr)
}

func TestCallbackRefusesAForeignState(t *testing.T) {
	// The forced-binding attack, at the endpoint where it applies. SameSite=Lax
	// means the victim's cookie travels with a cross-site top-level redirect,
	// so state is the whole defense.
	s, cookie, _, _ := serverWithFakeAuthServer(t)
	r := httptest.NewRequest("GET", "/api/oauth/callback?code=attacker-code&state=forged", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code == http.StatusCreated {
		t.Fatal("a forged state was accepted")
	}
}

func TestCallbackRefusesWithoutASession(t *testing.T) {
	s, _, _, _ := serverWithFakeAuthServer(t)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/api/oauth/callback?code=c&state=s", nil))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestCallbackRefusesAStateFromAnotherSession(t *testing.T) {
	// One session starts the flow; another presents the callback. That is the
	// attack the session binding exists for.
	s, cookie, token, _ := serverWithFakeAuthServer(t)
	id := oauthProvider(t, s, cookie, token)
	start := startFlow(t, s, cookie, token, id, `{}`)

	other, _ := login(t, s)
	r := httptest.NewRequest("GET",
		"/api/oauth/callback?code=c&state="+url.QueryEscape(start.State), nil)
	r.AddCookie(other)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code == http.StatusCreated {
		t.Fatal("a state from another session was accepted")
	}

	// And the original session's callback must still work: letting a blocked
	// attack invalidate it would turn the block into a denial of service.
	r = httptest.NewRequest("GET",
		"/api/oauth/callback?code=c&state="+url.QueryEscape(start.State), nil)
	r.AddCookie(cookie)
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusCreated {
		t.Errorf("the legitimate callback was lost: %d %s", w.Code, w.Body.String())
	}
}

func TestNoQueryStringReachesALog(t *testing.T) {
	// Spec §5.1. There is no admin access logging today; this is what stops one
	// being added that logs the authorization code.
	s, cookie, token, _ := serverWithFakeAuthServer(t)
	id := oauthProvider(t, s, cookie, token)
	start := startFlow(t, s, cookie, token, id, `{}`)

	var logged strings.Builder
	prevOut, prevFlags := log.Writer(), log.Flags()
	log.SetOutput(&logged)
	defer func() {
		log.SetOutput(prevOut)
		log.SetFlags(prevFlags)
	}()

	r := httptest.NewRequest("GET",
		"/api/oauth/callback?code=SECRET-CODE&state="+url.QueryEscape(start.State), nil)
	r.AddCookie(cookie)
	s.Handler().ServeHTTP(httptest.NewRecorder(), r)

	if strings.Contains(logged.String(), "SECRET-CODE") {
		t.Fatalf("the authorization code reached a log line:\n%s", logged.String())
	}
}
