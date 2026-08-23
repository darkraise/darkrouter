package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/darkraise/darkrouter/internal/catalog"
	"github.com/darkraise/darkrouter/internal/config"
	"github.com/darkraise/darkrouter/internal/crypto"
	"github.com/darkraise/darkrouter/internal/health"
	"github.com/darkraise/darkrouter/internal/provider"
	"github.com/darkraise/darkrouter/internal/store"
)

// sessionTTL is spec §3's default, sliding on every authenticated request.
const sessionTTL = 30 * 24 * time.Hour

// sessionCookie is the cookie name. It is not "session" so it cannot collide
// with anything an operator runs on the same host.
const sessionCookie = "darkrouter_session"

// csrfHeader is where the SPA puts the token. A header rather than a form field
// because every request the SPA makes is JSON or SSE, and a header cannot be
// set by a cross-site form post at all.
const csrfHeader = "X-CSRF-Token"

// Deps are the admin server's collaborators. Every field except DB and
// PasswordHash is optional, so a handler test can build a server without
// standing up a router, a catalog and a breaker.
type Deps struct {
	DB           *store.DB
	PasswordHash string

	Config   *config.Store
	Src      *provider.SQLSource
	Key      *crypto.Key
	Catalog  *catalog.Store
	Disc     *catalog.Discoverer
	Breaker  *health.Breaker
	Presets  catalog.Presets
	Warnings []string

	// Dev, when non-empty, is the Vite dev server to reverse-proxy unmatched
	// paths to. It is empty in production.
	Dev string
}

type Server struct {
	deps   Deps
	csrf   *CSRF
	mux    *http.ServeMux
	probes probeLocks
}

// New builds the admin server and sweeps expired sessions.
//
// The sweep runs here rather than on a timer because sessions outlive the
// process: nothing else would ever remove them, and a thirty-day TTL means a
// long-lived deployment accumulates a row per login forever.
func New(deps Deps) (*Server, error) {
	if deps.DB == nil {
		return nil, fmt.Errorf("admin: DB is required")
	}
	csrf, err := NewCSRF(context.Background(), deps.DB)
	if err != nil {
		return nil, err
	}
	if _, err := deps.DB.SweepSessions(context.Background()); err != nil {
		return nil, fmt.Errorf("admin: sweep sessions: %w", err)
	}
	s := &Server{deps: deps, csrf: csrf}
	s.routes()
	return s, nil
}

func (s *Server) Handler() http.Handler { return s.mux }

// routes registers every endpoint. Read handlers are wrapped in requireSession;
// mutating ones in requireCSRF, which wraps requireSession itself, so a route
// cannot accidentally get one check and not the other.
func (s *Server) routes() {
	s.mux = http.NewServeMux()

	// The one endpoint reachable without a session: the SPA calls it to decide
	// whether to render the login screen.
	s.mux.HandleFunc("GET /api/auth/status", s.handleAuthStatus)
	s.mux.HandleFunc("POST /api/auth/login", s.handleLogin)
	s.mux.HandleFunc("POST /api/auth/logout", s.requireCSRF(s.handleLogout))

	s.mux.HandleFunc("GET /api/presets", s.requireSession(s.handlePresets))
	s.mux.HandleFunc("GET /api/providers", s.requireSession(s.handleListProviders))
	s.mux.HandleFunc("POST /api/providers", s.requireCSRF(s.handleCreateProvider))
	s.mux.HandleFunc("PATCH /api/providers/{id}", s.requireCSRF(s.handlePatchProvider))
	s.mux.HandleFunc("DELETE /api/providers/{id}", s.requireCSRF(s.handleDeleteProvider))
	s.mux.HandleFunc("POST /api/providers/{id}/test", s.requireCSRF(s.handleProbe))
	s.mux.HandleFunc("POST /api/providers/{id}/keys", s.requireCSRF(s.handleAddCredential))
	s.mux.HandleFunc("DELETE /api/providers/{id}/keys/{keyId}", s.requireCSRF(s.handleDeleteCredential))

	s.mux.HandleFunc("GET /api/models", s.requireSession(s.handleModels))
	s.mux.HandleFunc("GET /api/requests", s.requireSession(s.handleListRequests))
	s.mux.HandleFunc("GET /api/requests/{id}", s.requireSession(s.handleRequestTrace))

	s.mux.HandleFunc("GET /api/config", s.requireSession(s.handleConfig))
	s.mux.HandleFunc("POST /api/config/reload", s.requireCSRF(s.handleConfigReload))
}

// writeJSON is the single response path, so no handler invents its own header
// order or forgets the content type.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// writeError is the single error path. The shape is fixed here so the SPA can
// read every failure the same way.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": msg})
}

// probeLocks serializes probes per provider. Declared here so Server can embed
// it; the probe handler that uses it arrives with its own task.
type probeLocks struct {
	mu sync.Mutex
	m  map[string]*sync.Mutex
}

func (l *probeLocks) get(providerID string) *sync.Mutex {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.m == nil {
		l.m = map[string]*sync.Mutex{}
	}
	if _, ok := l.m[providerID]; !ok {
		l.m[providerID] = &sync.Mutex{}
	}
	return l.m[providerID]
}
