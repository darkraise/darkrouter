// Package server wires the components into two listeners and owns shutdown.
package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"time"

	"github.com/darkraise/darkrouter/internal/adapter/openaicompat"
	"github.com/darkraise/darkrouter/internal/config"
	openaiedge "github.com/darkraise/darkrouter/internal/edge/openai"
	"github.com/darkraise/darkrouter/internal/exec"
	"github.com/darkraise/darkrouter/internal/provider"
)

type Server struct {
	store *config.Store
	src   provider.Source
	ex    *exec.Executor
}

func New(store *config.Store) *Server {
	src := provider.NewYAMLSource(store)
	return &Server{store: store, src: src, ex: exec.New(store, src, openaicompat.New())}
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

// withProxyAuth enforces the optional bearer token. Comparison is constant-time.
func (s *Server) withProxyAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := s.store.Current().Server.ProxyToken
		if token == "" {
			next.ServeHTTP(w, r)
			return
		}
		got, _ := parseBearer(r.Header.Get("Authorization"))
		if subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{
				"message": "invalid proxy token", "type": "authentication",
			}})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func parseBearer(h string) (string, bool) {
	const p = "Bearer "
	if len(h) > len(p) && h[:len(p)] == p {
		return h[len(p):], true
	}
	return "", false
}

// handleModels lists configured models. Aliases would be listed first, but
// Phase 1 has none; Phase 6 replaces the backing with the catalog.
func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	ps, err := s.src.Providers(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
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
		body := map[string]any{
			"config_valid": s.store.LastError() == nil,
			"warnings":     cfg.Warnings,
		}
		if err := s.store.LastError(); err != nil {
			body["config_error"] = err.Error()
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
	cfg := s.store.Current()

	proxy := &http.Server{
		Addr:    cfg.Server.ProxyListen,
		Handler: s.ProxyHandler(),
		// No WriteTimeout: it would kill long streams at a fixed age. Slowloris
		// protection comes from ReadHeaderTimeout instead.
		ReadHeaderTimeout: 10 * time.Second,
		// BaseContext must not be tied to ctx, or in-flight streams die the
		// instant shutdown begins rather than draining.
		BaseContext: func(net.Listener) context.Context { return context.Background() },
	}
	admin := &http.Server{
		Addr:              cfg.Server.AdminListen,
		Handler:           s.AdminHandler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 2)
	go func() { errCh <- ignoreClosed(proxy.ListenAndServe()) }()
	go func() { errCh <- ignoreClosed(admin.ListenAndServe()) }()
	go func() { _ = s.store.Watch(ctx) }()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	drain, cancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownGrace)
	defer cancel()
	// Stop accepting proxy connections first, then the admin port.
	_ = proxy.Shutdown(drain)
	_ = admin.Shutdown(drain)
	return nil
}

func ignoreClosed(err error) error {
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
