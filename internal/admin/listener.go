package admin

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/darkraise/darkrouter/internal/auth"
)

// newRedirectListener binds the vendor's registered loopback port.
//
// Loopback only: the registered URI is http://localhost:{port}/callback, and
// binding 0.0.0.0 would put a code-accepting endpoint on the LAN for as long as
// the flow lasts.
func newRedirectListener(port int) (net.Listener, error) {
	return net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
}

// redirectListener receives exactly one callback and stops.
type redirectListener struct {
	srv  *http.Server
	once sync.Once
	done chan struct{}
}

func (r *redirectListener) stop() {
	r.once.Do(func() {
		close(r.done)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = r.srv.Shutdown(ctx)
	})
}

// startListener runs a temporary server on the vendor's registered port and
// completes the first callback it receives.
//
// It calls completeOAuth rather than duplicating the exchange, which is how
// spec §7's "the manual-paste path validating identically to the listener path"
// is true by construction rather than by inspection.
func (s *Server) startListener(flow auth.Flow, port int, sessionID string, ttl time.Duration) error {
	ln, err := newRedirectListener(port)
	if err != nil {
		// The port is taken — often by the vendor's own CLI, which is exactly
		// the situation the paste path exists for.
		return fmt.Errorf("cannot listen on the registered redirect port %d: %w", port, err)
	}

	rl := &redirectListener{done: make(chan struct{})}
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		defer rl.stop()
		code, state, perr := parseRedirected(r.URL.String())
		if perr != nil {
			// Plain text, and never the query string: the browser shows this
			// page and the operator may screenshot it.
			http.Error(w, perr.Error(), http.StatusBadRequest)
			return
		}
		if _, _, _, cerr := s.completeOAuth(r.Context(), code, state, sessionID); cerr != nil {
			http.Error(w, cerr.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("Connected. You can close this tab and return to Darkrouter."))
	})
	rl.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}

	go func() { _ = rl.srv.Serve(ln) }()
	// Torn down on expiry as well as on completion. A listener that outlived
	// its flow would hold a port the operator's own tooling may want, and would
	// accept a code with no flow to match it.
	go func() {
		select {
		case <-rl.done:
		case <-time.After(ttl):
			rl.stop()
		}
	}()
	s.trackListener(flow.ProviderID, rl)
	return nil
}

// trackListener keeps the live listeners so shutdown can stop them, and so a
// second flow for the same provider replaces the first rather than failing to
// bind a port the first still holds.
func (s *Server) trackListener(providerID string, rl *redirectListener) {
	s.listenerMu.Lock()
	prev := s.listeners[providerID]
	if s.listeners == nil {
		s.listeners = map[string]*redirectListener{}
	}
	s.listeners[providerID] = rl
	s.listenerMu.Unlock()
	if prev != nil {
		prev.stop()
	}
}

// Close stops every listener this server started. Process hygiene: a listener
// left bound holds a port after the gateway is gone.
func (s *Server) Close() {
	s.listenerMu.Lock()
	live := make([]*redirectListener, 0, len(s.listeners))
	for _, rl := range s.listeners {
		live = append(live, rl)
	}
	s.listeners = map[string]*redirectListener{}
	s.listenerMu.Unlock()
	for _, rl := range live {
		rl.stop()
	}
}
