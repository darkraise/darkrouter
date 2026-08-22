// Package server wires the components into two listeners and owns shutdown.
package server

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/darkraise/darkrouter/internal/adapter/openaicompat"
	"github.com/darkraise/darkrouter/internal/config"
	openaiedge "github.com/darkraise/darkrouter/internal/edge/openai"
	"github.com/darkraise/darkrouter/internal/exec"
	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/provider"
)

// Version is stamped at build time with -ldflags "-X ...Version=v1.2.3".
var Version = "dev"

// terminalGrace is how long handlers get to emit a final in-stream event after
// the drain deadline expires, before their connections are forced closed.
const terminalGrace = 500 * time.Millisecond

type Server struct {
	store   *config.Store
	src     provider.Source
	ex      *exec.Executor
	started time.Time
}

func New(store *config.Store) *Server {
	src := provider.NewYAMLSource(store)
	return &Server{
		store:   store,
		src:     src,
		ex:      exec.New(store, src, openaicompat.New()),
		started: time.Now(),
	}
}

func (s *Server) ProxyHandler() http.Handler {
	mux := http.NewServeMux()
	d := openaiedge.New()
	mux.HandleFunc("POST /v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		s.ex.Handle(w, r, d)
	})
	mux.HandleFunc("GET /v1/models", s.handleModels)
	return s.withProxyAuth(mux)
}

// withProxyAuth enforces the optional bearer token. The token is read live
// because proxy_token is hot-reloadable, unlike the listen addresses.
func (s *Server) withProxyAuth(next http.Handler) http.Handler {
	d := openaiedge.New()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := s.store.Current().Server.ProxyToken
		if token == "" {
			next.ServeHTTP(w, r)
			return
		}
		got := parseBearer(r.Header.Get("Authorization"))
		if !constantTimeEqual(got, token) {
			_ = d.WriteError(w, &ir.Error{
				Type: ir.ErrAuthentication, Message: "invalid proxy token",
			})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// constantTimeEqual compares two secrets without leaking their lengths.
// subtle.ConstantTimeCompare returns early when lengths differ, so hashing both
// sides first is what makes the comparison genuinely constant-time.
func constantTimeEqual(got, want string) bool {
	g := sha256.Sum256([]byte(got))
	w := sha256.Sum256([]byte(want))
	return subtle.ConstantTimeCompare(g[:], w[:]) == 1
}

// parseBearer extracts the token. RFC 7235 auth schemes are case-insensitive.
func parseBearer(h string) string {
	scheme, token, found := strings.Cut(h, " ")
	if !found || !strings.EqualFold(scheme, "Bearer") {
		return ""
	}
	return strings.TrimSpace(token)
}

// handleModels lists configured models. Aliases would be listed first, but
// Phase 1 has none; Phase 6 replaces the backing with the catalog.
func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	ps, err := s.src.Providers(r.Context())
	if err != nil {
		// Errors are normalized into the inbound dialect's shape, per design §14.
		_ = openaiedge.New().WriteError(w, &ir.Error{
			Type: ir.ErrDarkrouter, Message: "could not list providers",
		})
		return
	}
	seen := map[string]bool{}
	data := []any{}
	for _, p := range ps {
		for _, m := range p.Models {
			if seen[m] {
				continue
			}
			seen[m] = true
			data = append(data, map[string]any{
				"id": m, "object": "model", "owned_by": p.ID,
			})
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": data})
}

func (s *Server) AdminHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		cfg := s.store.Current()
		// Read once: two calls could straddle a reload and report a valid config
		// with an error attached, or an invalid one with none.
		cfgErr := s.store.LastError()
		body := map[string]any{
			"config_valid": cfgErr == nil,
			"warnings":     cfg.Warnings,
			"uptime":       time.Since(s.started).Round(time.Second).String(),
			"version":      Version,
		}
		if cfgErr != nil {
			body["config_error"] = cfgErr.Error()
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_, _ = w.Write([]byte("# Phase 2 populates this endpoint.\n"))
	})
	return mux
}

// Run starts both listeners and blocks until ctx is cancelled, then drains.
func (s *Server) Run(ctx context.Context) error {
	// Derived so every goroutine this function starts is cancelled on any exit
	// path, not only on the caller's cancellation.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	cfg := s.store.Current()

	// The lifecycle context is handed to every request. It is deliberately not
	// derived from ctx: tying it there would kill in-flight streams the instant
	// SIGTERM arrives instead of letting them drain. It is cancelled only when
	// the drain deadline expires, which is the signal handlers need to emit a
	// terminal event.
	lc, cancelLC := context.WithCancel(context.Background())
	defer cancelLC()

	proxy := &http.Server{
		Addr:    cfg.Server.ProxyListen,
		Handler: s.ProxyHandler(),
		// No WriteTimeout: it would kill long streams at a fixed age. Slowloris
		// protection comes from ReadHeaderTimeout instead.
		ReadHeaderTimeout: 10 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return lc },
	}
	admin := &http.Server{
		Addr:              cfg.Server.AdminListen,
		Handler:           s.AdminHandler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 2)
	go func() { errCh <- ignoreClosed(proxy.ListenAndServe()) }()
	go func() { errCh <- ignoreClosed(admin.ListenAndServe()) }()
	go func() {
		// A watcher that cannot start leaves hot reload silently dead, so the
		// failure has to reach /healthz rather than being discarded.
		s.store.RecordError(s.store.Watch(ctx))
	}()

	select {
	case err := <-errCh:
		// One listener died. Close the other rather than leaking its port and
		// its goroutine.
		_ = proxy.Close()
		_ = admin.Close()
		return err
	case <-ctx.Done():
	}

	// Read live: shutdown_grace is hot-reloadable, unlike the listen addresses.
	grace := s.store.Current().Server.ShutdownGrace
	drain, cancelDrain := context.WithTimeout(context.Background(), grace)
	defer cancelDrain()

	// Shutdown closes listeners and waits, but on deadline it returns and leaves
	// active connections running. Cancelling the lifecycle context propagates to
	// each handler's request context, which aborts its upstream read and lets
	// the stream path emit a final error event; Close then forces the sockets
	// down so the process can actually exit.
	shutdownErr := proxy.Shutdown(drain)
	if shutdownErr != nil {
		cancelLC()
		timer := time.NewTimer(terminalGrace)
		<-timer.C
		_ = proxy.Close()
	}
	_ = admin.Shutdown(drain)
	_ = admin.Close()
	return shutdownErr
}

func ignoreClosed(err error) error {
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
