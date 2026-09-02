package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
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
// store.SessionMaxAge caps the whole life regardless of use.
const sessionTTL = 30 * 24 * time.Hour

// sessionCookie is the cookie name. It is not "session" so it cannot collide
// with anything an operator runs on the same host.
const sessionCookie = "darkrouter_session"

// csrfHeader is where the SPA puts the token. A header rather than a form field
// because every request the SPA makes is JSON or SSE, and a header cannot be
// set by a cross-site form post at all.
const csrfHeader = "X-CSRF-Token"

// sweepInterval is how often expired sessions and abandoned OAuth flows are
// dropped while the process runs.
const sweepInterval = time.Hour

// DiscoveryTrigger asks for one provider's models to be listed now, rather
// than at the next sweep. Satisfied by *catalog.Discoverer.
type DiscoveryTrigger interface {
	Trigger(providerID string)
}

// Deps are the admin server's collaborators. Every field except DB and
// PasswordHash is optional, so a handler test can build a server without
// standing up a router, a catalog and a breaker.
type Deps struct {
	DB           *store.DB
	PasswordHash string

	Config  *config.Store
	Src     *provider.SQLSource
	Key     *crypto.Key
	Catalog *catalog.Store
	// Disc asks for one provider to be swept now. An interface rather than
	// *catalog.Discoverer because Trigger is the whole of what this package
	// wants from it, and a narrow dependency is one a test can stand in for.
	Disc     DiscoveryTrigger
	Sync     *catalog.Syncer
	Breaker  *health.Breaker
	Presets  catalog.Presets
	Warnings []string

	// Kinds names the adapter kinds this build can serve, so a provider
	// cannot be created for one no adapter exists for. The registry lives
	// with the executor's construction; nil skips the check.
	Kinds []string

	// Exec is the same executor the proxy port uses. The playground runs real
	// requests through it so what it verifies is the gateway rather than
	// itself.
	Exec *exec.Executor

	// Flows holds in-progress OAuth connect attempts. Nil disables the OAuth
	// routes, which is what every test that does not exercise them wants.
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
	logins *loginLimiter

	// listeners are the temporary loopback servers receiving OAuth redirects,
	// keyed by provider so a second flow replaces the first rather than failing
	// to bind a port the first still holds.
	listenerMu sync.Mutex
	listeners  map[string]*redirectListener

	// stopSweep ends the background sweeper; closeOnce makes Close idempotent
	// because both the server's shutdown path and a test's cleanup reach it.
	stopSweep chan struct{}
	closeOnce sync.Once
}

// New builds the admin server, reconciles the password hash with the
// environment, sweeps expired sessions once, and starts the periodic sweeper.
func New(deps Deps) (*Server, error) {
	if deps.DB == nil {
		return nil, fmt.Errorf("admin: DB is required")
	}
	ctx := context.Background()
	csrf, err := NewCSRF(ctx, deps.DB)
	if err != nil {
		return nil, err
	}
	s := &Server{
		deps: deps, csrf: csrf,
		logins:    newLoginLimiter(loginRate, loginBurst, loginConcurrency),
		stopSweep: make(chan struct{}),
	}
	if err := s.reconcilePasswordHash(ctx); err != nil {
		return nil, fmt.Errorf("admin: %w", err)
	}
	if _, err := deps.DB.SweepSessions(ctx); err != nil {
		return nil, fmt.Errorf("admin: sweep sessions: %w", err)
	}
	s.routes()
	go s.sweep()
	return s, nil
}

// sweep drops expired sessions and abandoned OAuth flows while the process
// runs. Sessions outlive the process, so a startup-only sweep leaves a
// long-lived deployment accumulating a row per login; a flow store swept only
// on Claim keeps every abandoned verifier until the next connect.
func (s *Server) sweep() {
	t := time.NewTicker(sweepInterval)
	defer t.Stop()
	for {
		select {
		case <-s.stopSweep:
			return
		case now := <-t.C:
			s.sweepOnce(now)
		}
	}
}

func (s *Server) sweepOnce(now time.Time) {
	if _, err := s.deps.DB.SweepSessions(context.Background()); err != nil {
		log.Printf("admin: sweep sessions: %v", err)
	}
	if s.deps.Flows != nil {
		s.deps.Flows.Sweep(now)
	}
}

// Handler is the mux behind the security headers every response carries.
func (s *Server) Handler() http.Handler { return securityHeaders(s.mux) }

// routeAuth is what a route requires of its caller.
type routeAuth int

const (
	// routePublic is reachable without a session.
	routePublic routeAuth = iota
	// routeSession needs a valid cookie.
	routeSession
	// routeCSRF needs the cookie, a same-origin request and the CSRF token.
	routeCSRF
)

type route struct {
	method  string
	pattern string
	auth    routeAuth
	handler http.HandlerFunc
}

// routeTable is every endpoint with the guard it needs. One table rather
// than a list of registrations so a test can walk it and check that every
// route really carries its guard, including the one nobody has written yet.
func (s *Server) routeTable() []route {
	rs := []route{
		// The one endpoint reachable without a session: the SPA calls it to
		// decide whether to render the login screen.
		{"GET", "/api/auth/status", routePublic, s.handleAuthStatus},
		{"POST", "/api/auth/login", routePublic, s.handleLogin},
		{"POST", "/api/auth/logout", routeCSRF, s.handleLogout},

		{"GET", "/api/presets", routeSession, s.handlePresets},
		{"GET", "/api/providers", routeSession, s.handleListProviders},
		{"POST", "/api/providers", routeCSRF, s.handleCreateProvider},
		{"PATCH", "/api/providers/{id}", routeCSRF, s.handlePatchProvider},
		{"DELETE", "/api/providers/{id}", routeCSRF, s.handleDeleteProvider},
		{"POST", "/api/providers/{id}/test", routeCSRF, s.handleProbe},
		{"POST", "/api/providers/{id}/keys", routeCSRF, s.handleAddCredential},
		{"PATCH", "/api/providers/{id}/keys/{keyId}", routeCSRF, s.handlePatchCredential},
		{"DELETE", "/api/providers/{id}/keys/{keyId}", routeCSRF, s.handleDeleteCredential},

		{"POST", "/api/playground", routeCSRF, s.handlePlayground},
		{"POST", "/api/playground/count", routeCSRF, s.handlePlaygroundCount},
		{"POST", "/api/playground/aux", routeCSRF, s.handlePlaygroundAux},

		{"GET", "/api/playground/presets", routeSession, s.handleListPlaygroundPresets},
		{"POST", "/api/playground/presets", routeCSRF, s.handleCreatePlaygroundPreset},
		{"PATCH", "/api/playground/presets/{id}", routeCSRF, s.handleUpdatePlaygroundPreset},
		{"DELETE", "/api/playground/presets/{id}", routeCSRF, s.handleDeletePlaygroundPreset},

		{"GET", "/api/playground/conversations", routeSession, s.handleListPlaygroundConversations},
		{"POST", "/api/playground/conversations", routeCSRF, s.requireConversationSaving(s.handleCreatePlaygroundConversation)},
		// The exact literal beside the wildcard below it: ServeMux prefers the
		// literal, so the purge and the single delete coexist.
		{"DELETE", "/api/playground/conversations", routeCSRF, s.handlePurgePlaygroundConversations},
		{"GET", "/api/playground/conversations/{id}", routeSession, s.handleGetPlaygroundConversation},
		{"PATCH", "/api/playground/conversations/{id}", routeCSRF, s.requireConversationSaving(s.handleUpdatePlaygroundConversation)},
		{"DELETE", "/api/playground/conversations/{id}", routeCSRF, s.handleDeletePlaygroundConversation},
		{"POST", "/api/playground/conversations/{id}/messages", routeCSRF, s.requireConversationSaving(s.handleAppendPlaygroundTurn)},

		{"GET", "/api/overview", routeSession, s.handleOverview},
		{"GET", "/api/usage", routeSession, s.handleUsage},
		{"GET", "/api/models", routeSession, s.handleModels},
		{"GET", "/api/requests", routeSession, s.handleListRequests},
		{"GET", "/api/requests/{id}", routeSession, s.handleRequestTrace},

		{"GET", "/api/config", routeSession, s.handleConfig},
		{"PUT", "/api/config", routeCSRF, s.handleConfigPut},
		{"POST", "/api/config/reload", routeCSRF, s.handleConfigReload},

		{"GET", "/api/health/providers", routeSession, s.handleHealthProviders},
		{"GET", "/api/health/discovery", routeSession, s.handleDiscoveryHealth},
		{"POST", "/api/providers/{id}/breaker/reset", routeCSRF, s.handleBreakerReset},
		{"POST", "/api/providers/{id}/discover", routeCSRF, s.handleForceDiscover},
		{"POST", "/api/catalog/sync", routeCSRF, s.handleForceCatalogSync},
		{"POST", "/api/route/preview", routeCSRF, s.handleRoutePreview},

		{"GET", "/api/aliases", routeSession, s.handleAliases},
		{"PUT", "/api/aliases", routeCSRF, s.handlePutAliases},
		{"GET", "/api/policy", routeSession, s.handlePolicy},
		{"PUT", "/api/policy", routeCSRF, s.handlePutPolicy},
		{"GET", "/api/models/{provider}/{model}/override", routeSession, s.handleGetOverride},
		{"PUT", "/api/models/{provider}/{model}/override", routeCSRF, s.handlePutOverride},
		{"DELETE", "/api/models/{provider}/{model}/override", routeCSRF, s.handleDeleteOverride},

		{"GET", "/api/proxy-tokens", routeSession, s.handleListProxyTokens},
		{"POST", "/api/proxy-tokens", routeCSRF, s.handleCreateProxyToken},
		{"DELETE", "/api/proxy-tokens/{id}", routeCSRF, s.handleDeleteProxyToken},

		{"GET", "/api/sessions", routeSession, s.handleListSessions},
		{"DELETE", "/api/sessions/{id}", routeCSRF, s.handleDeleteSession},
		{"POST", "/api/auth/password", routeCSRF, s.handleChangePassword},
	}
	// The OAuth routes need somewhere to keep a flow between start and
	// callback. Without a store every call would fail on a nil map, so they
	// are absent rather than broken.
	if s.deps.Flows != nil {
		rs = append(rs,
			route{"POST", "/api/providers/{id}/oauth/start", routeCSRF, s.handleOAuthStart},
			route{"POST", "/api/providers/{id}/oauth/complete", routeCSRF, s.handleOAuthComplete},
			// requireSession rather than requireCSRF: a top-level navigation
			// carries no header to check. State does that work — see
			// handleOAuthCallback.
			route{"GET", "/api/oauth/callback", routeSession, s.handleOAuthCallback},
		)
	}
	return rs
}

// routes registers the table. Read handlers are wrapped in requireSession;
// mutating ones in requireCSRF, which wraps requireSession itself, so a route
// cannot accidentally get one check and not the other.
func (s *Server) routes() {
	s.mux = http.NewServeMux()
	for _, r := range s.routeTable() {
		h := r.handler
		switch r.auth {
		case routeSession:
			h = s.requireSession(h)
		case routeCSRF:
			h = s.requireCSRF(h)
		}
		s.mux.HandleFunc(r.method+" "+r.pattern, h)
	}

	// A mistyped API path must answer as an API path. Without these an
	// unknown /api/… would fall through to the SPA and return HTML, and the
	// client would report a JSON parse error instead of the missing route.
	// Every verb, not only the two that are common: a mistyped DELETE
	// answering 200 with index.html is exactly the failure this prevents.
	for _, method := range []string{"GET", "POST", "PATCH", "PUT", "DELETE"} {
		s.mux.HandleFunc(method+" /api/", func(w http.ResponseWriter, r *http.Request) {
			writeError(w, http.StatusNotFound, "no such endpoint")
		})
	}

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

// probeLocks serializes probes per provider.
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

// drop forgets a provider's lock once the provider is gone. A probe already
// holding it finishes on its own reference.
func (l *probeLocks) drop(providerID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.m, providerID)
}
