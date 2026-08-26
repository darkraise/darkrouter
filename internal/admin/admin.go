package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/darkraise/darkrouter/internal/auth"
	"github.com/darkraise/darkrouter/internal/catalog"
	"github.com/darkraise/darkrouter/internal/config"
	"github.com/darkraise/darkrouter/internal/crypto"
	"github.com/darkraise/darkrouter/internal/exec"
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
	Sync     *catalog.Syncer
	Breaker  *health.Breaker
	Presets  catalog.Presets
	Warnings []string

	// Exec is the same executor the proxy port uses. The playground runs real
	// requests through it so what it verifies is the gateway rather than
	// itself.
	Exec *exec.Executor

	// Flows holds in-progress OAuth connect attempts. Nil disables the two
	// OAuth routes, which is what every test that does not exercise them wants.
	Flows *auth.FlowStore

	// HTTP is the client used for token exchange and credential probes. Nil
	// uses http.DefaultClient.
	HTTP *http.Client

	// Auth resolves a non-static credential into an authorizer, so the probe
	// can exercise a signed or subscription credential the way a request does.
	Auth AuthResolver

	// Dev, when non-empty, is the Vite dev server to reverse-proxy unmatched
	// paths to. It is empty in production.
	Dev string
}

// AuthResolver mirrors exec.AuthResolver, declared here so a test can hand over
// a fixed authorizer without constructing a signer.
type AuthResolver interface {
	For(ctx context.Context, t auth.Target, c auth.Credential) (auth.Authorizer, error)
}

type Server struct {
	deps   Deps
	csrf   *CSRF
	mux    *http.ServeMux
	probes probeLocks

	// listeners are the temporary loopback servers receiving OAuth redirects,
	// keyed by provider so a second flow replaces the first rather than failing
	// to bind a port the first still holds.
	listenerMu sync.Mutex
	listeners  map[string]*redirectListener
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

	s.mux.HandleFunc("POST /api/providers/{id}/oauth/start", s.requireCSRF(s.handleOAuthStart))
	s.mux.HandleFunc("POST /api/providers/{id}/oauth/complete", s.requireCSRF(s.handleOAuthComplete))
	// requireSession rather than requireCSRF: a top-level navigation carries no
	// header to check. State does that work — see handleOAuthCallback.
	s.mux.HandleFunc("GET /api/oauth/callback", s.requireSession(s.handleOAuthCallback))

	s.mux.HandleFunc("POST /api/playground", s.requireCSRF(s.handlePlayground))

	s.mux.HandleFunc("GET /api/overview", s.requireSession(s.handleOverview))
	s.mux.HandleFunc("GET /api/usage", s.requireSession(s.handleUsage))
	s.mux.HandleFunc("GET /api/models", s.requireSession(s.handleModels))
	s.mux.HandleFunc("GET /api/requests", s.requireSession(s.handleListRequests))
	s.mux.HandleFunc("GET /api/requests/{id}", s.requireSession(s.handleRequestTrace))

	s.mux.HandleFunc("GET /api/config", s.requireSession(s.handleConfig))
	s.mux.HandleFunc("PUT /api/config", s.requireCSRF(s.handleConfigPut))

	s.mux.HandleFunc("GET /api/health/providers", s.requireSession(s.handleHealthProviders))
	s.mux.HandleFunc("POST /api/providers/{id}/breaker/reset", s.requireCSRF(s.handleBreakerReset))
	s.mux.HandleFunc("POST /api/providers/{id}/discover", s.requireCSRF(s.handleForceDiscover))
	s.mux.HandleFunc("POST /api/catalog/sync", s.requireCSRF(s.handleForceCatalogSync))
	s.mux.HandleFunc("POST /api/route/preview", s.requireCSRF(s.handleRoutePreview))

	s.mux.HandleFunc("GET /api/aliases", s.requireSession(s.handleAliases))
	s.mux.HandleFunc("PUT /api/aliases", s.requireCSRF(s.handlePutAliases))
	s.mux.HandleFunc("GET /api/policy", s.requireSession(s.handlePolicy))
	s.mux.HandleFunc("PUT /api/policy", s.requireCSRF(s.handlePutPolicy))
	s.mux.HandleFunc("GET /api/models/{provider}/{model}/override", s.requireSession(s.handleGetOverride))
	s.mux.HandleFunc("PUT /api/models/{provider}/{model}/override", s.requireCSRF(s.handlePutOverride))
	s.mux.HandleFunc("DELETE /api/models/{provider}/{model}/override", s.requireCSRF(s.handleDeleteOverride))

	s.mux.HandleFunc("PATCH /api/providers/{id}/keys/{keyId}", s.requireCSRF(s.handlePatchCredential))
	s.mux.HandleFunc("GET /api/proxy-tokens", s.requireSession(s.handleListProxyTokens))
	s.mux.HandleFunc("POST /api/proxy-tokens", s.requireCSRF(s.handleCreateProxyToken))
	s.mux.HandleFunc("DELETE /api/proxy-tokens/{id}", s.requireCSRF(s.handleDeleteProxyToken))

	s.mux.HandleFunc("GET /api/sessions", s.requireSession(s.handleListSessions))
	s.mux.HandleFunc("DELETE /api/sessions/{id}", s.requireCSRF(s.handleDeleteSession))
	s.mux.HandleFunc("POST /api/auth/password", s.requireCSRF(s.handleChangePassword))
	s.mux.HandleFunc("POST /api/config/reload", s.requireCSRF(s.handleConfigReload))

	// A mistyped API path must answer as an API path. Without these two an
	// unknown /api/… would fall through to the SPA and return HTML, and the
	// client would report a JSON parse error instead of the missing route.
	s.mux.HandleFunc("GET /api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "no such endpoint")
	})
	s.mux.HandleFunc("POST /api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "no such endpoint")
	})

	// Registered last and at the root so every exact API path above wins:
	// http.ServeMux prefers the longest matching pattern.
	if s.deps.Dev != "" {
		if proxy, err := devProxy(s.deps.Dev); err == nil {
			s.mux.Handle("/", proxy)
			return
		}
	}
	s.mux.Handle("/", s.spaHandler())
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
