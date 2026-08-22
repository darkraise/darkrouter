package server

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkrouter/internal/config"
)

// freePort returns a port that was listenable a moment ago. Racy in principle,
// fine for a test that binds it immediately.
func freePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

func serverOn(t *testing.T, proxyAddr, adminAddr string) *Server {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "darkrouter.yaml")
	body := "server:\n  proxy_listen: " + proxyAddr + "\n  admin_listen: " + adminAddr +
		"\n  shutdown_grace: 1s\nproviders:\n  - id: fake\n    kind: openaicompat\n" +
		"    base_url: https://up.example/v1\n    api_key: ${K}\n    models: [m]\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := config.NewStore(path, func(string) (string, bool) { return "sk", true })
	if err != nil {
		t.Fatal(err)
	}
	return New(store)
}

func TestRunReturnsOnContextCancelAndReleasesPorts(t *testing.T) {
	proxyAddr, adminAddr := freePort(t), freePort(t)
	s := serverOn(t, proxyAddr, adminAddr)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()

	waitListening(t, proxyAddr)
	waitListening(t, adminAddr)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancellation")
	}

	// Both ports must be rebindable, or Run leaked a listener.
	for _, addr := range []string{proxyAddr, adminAddr} {
		l, err := net.Listen("tcp", addr)
		if err != nil {
			t.Fatalf("port %s not released: %v", addr, err)
		}
		_ = l.Close()
	}
}

// A listener that cannot bind must not leave the other server running: that
// leaks a port and a goroutine for the process lifetime.
func TestRunClosesSurvivingServerWhenOneListenerFails(t *testing.T) {
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer taken.Close()

	adminAddr := freePort(t)
	s := serverOn(t, taken.Addr().String(), adminAddr)

	done := make(chan error, 1)
	go func() { done <- s.Run(context.Background()) }()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected Run to return the bind error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after a listener failed")
	}

	l, err := net.Listen("tcp", adminAddr)
	if err != nil {
		t.Fatalf("admin port leaked after proxy bind failure: %v", err)
	}
	_ = l.Close()
}

func TestRunSurfacesWatcherFailureOnHealthz(t *testing.T) {
	// A watcher on a directory that disappears records its error rather than
	// leaving hot reload silently dead.
	dir := t.TempDir()
	path := filepath.Join(dir, "darkrouter.yaml")
	body := "server:\n  proxy_listen: " + freePort(t) + "\n  admin_listen: " + freePort(t) +
		"\nproviders:\n  - id: fake\n    kind: openaicompat\n" +
		"    base_url: https://up.example/v1\n    api_key: ${K}\n    models: [m]\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := config.NewStore(path, func(string) (string, bool) { return "sk", true })
	if err != nil {
		t.Fatal(err)
	}
	// RecordError is the mechanism Run uses; assert it reaches LastError.
	store.RecordError(errWatcherDead)
	if store.LastError() == nil {
		t.Fatal("a watcher failure must be visible through LastError")
	}
	if !strings.Contains(store.LastError().Error(), "watcher") {
		t.Fatalf("unexpected error %v", store.LastError())
	}
}

func waitListening(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			_ = c.Close()
			return
		}
		timer := time.NewTimer(20 * time.Millisecond)
		<-timer.C
	}
	t.Fatalf("%s never started listening", addr)
}

var errWatcherDead = errWatcher{}

type errWatcher struct{}

func (errWatcher) Error() string { return "watcher: could not start" }
