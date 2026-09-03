// Package server wires the components into two listeners and owns shutdown.
package server

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"runtime/debug"
	"sort"
	"sync"
	"time"

	"github.com/darkraise/darkrouter/internal/adapter"
	anthropicadapter "github.com/darkraise/darkrouter/internal/adapter/anthropic"
	bedrockadapter "github.com/darkraise/darkrouter/internal/adapter/bedrock"
	geminiadapter "github.com/darkraise/darkrouter/internal/adapter/gemini"
	"github.com/darkraise/darkrouter/internal/adapter/openaicompat"
	vertexadapter "github.com/darkraise/darkrouter/internal/adapter/vertex"
	"github.com/darkraise/darkrouter/internal/admin"
	"github.com/darkraise/darkrouter/internal/auth"
	"github.com/darkraise/darkrouter/internal/catalog"
	"github.com/darkraise/darkrouter/internal/config"
	"github.com/darkraise/darkrouter/internal/crypto"
	"github.com/darkraise/darkrouter/internal/edge"
	anthropicedge "github.com/darkraise/darkrouter/internal/edge/anthropic"
	geminiedge "github.com/darkraise/darkrouter/internal/edge/gemini"
	openaiedge "github.com/darkraise/darkrouter/internal/edge/openai"
	"github.com/darkraise/darkrouter/internal/exec"
	"github.com/darkraise/darkrouter/internal/health"
	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/localcli"
	"github.com/darkraise/darkrouter/internal/provider"
	"github.com/darkraise/darkrouter/internal/store"
)

// Version is stamped at build time with -ldflags "-X ...Version=v1.2.3".
var Version = "dev"

// terminalGrace is how long handlers get to emit a final in-stream event after
// the drain deadline expires, before their connections are forced closed.
const terminalGrace = 500 * time.Millisecond

type Server struct {
	store   *config.Store
	db      *store.DB
	src     *provider.SQLSource
	ex      *exec.Executor
	logw    *store.LogWriter
	metrics *metrics
	tokens  *tokenAuth
	breaker *health.Breaker
	persist *health.Persister

	cat  *catalog.Store
	disc *catalog.Discoverer
	// freeSync is nil when the daily refresh is turned off; the catalogue the
	// release shipped with is then what the import filter reads.
	freeSync *catalog.FreeSyncer
	// litellmSync is nil when the daily price refresh is turned off; the
	// catalogue then prices from models.dev and the row alone.
	litellmSync *catalog.LiteLLMSyncer
	sync        *catalog.Syncer

	adm *admin.Server

	// refresher renews OAuth tokens ahead of expiry. It drives the same
	// authorizer a request would, under the same per-account mutex.
	refresher *auth.RefreshWorker

	started  time.Time
	warnings []string
}

// Catalog exposes the live snapshot holder. The listing handlers read it, and
// phase 7's admin API will too.
func (s *Server) Catalog() *catalog.Store { return s.cat }

// Discoverer exposes the worker so a provider change can trigger an immediate
// probe. It is nil when discovery is disabled.
func (s *Server) Discoverer() *catalog.Discoverer { return s.disc }

// Option adjusts what New builds. There is exactly one, and it exists because
// the shipped preset set is embedded: without a seam, nothing above this package
// can point an OAuth token endpoint at a fake, and the assembled refresh path
// would be untestable.
type Option func(*options)

type options struct{ presets catalog.Presets }

// WithPresets replaces the shipped preset set.
func WithPresets(p catalog.Presets) Option {
	return func(o *options) { o.presets = p }
}

// New wires the gateway. It loads the provider set eagerly so a bad credential
// fails startup rather than every request.
func New(cfgStore *config.Store, db *store.DB, key *crypto.Key, startupWarnings []string,
	opts ...Option) (*Server, error) {

	o := options{presets: catalog.Embedded()}
	for _, fn := range opts {
		fn(&o)
	}
	cfg := cfgStore.Current()

	src := provider.NewSQLSource(db, key)
	if err := src.Reload(context.Background()); err != nil {
		return nil, fmt.Errorf("load providers: %w", err)
	}

	logw := store.NewLogWriter(db, store.LogOptions{})
	breaker := health.New(*cfg.Policy.Cooldown.TripAfter, cfg.Policy.Cooldown.Max)
	met := newMetrics(breaker, logw)
	// policy.cooldown is edited through the admin API and lands in the next
	// config snapshot, so the breaker reads it from there rather than from the
	// values it was built with.
	breaker.Configure(func() (int, time.Duration) {
		p := cfgStore.Current().Policy.Cooldown
		return *p.TripAfter, p.Max
	})

	// Built above both the discoverer and the executor because each resolves
	// credentials through it. Static styles need none of these collaborators:
	// the manager serves them by returning a nil authorizer.
	tokens := tokenStore{db: db, key: key}
	authManager := auth.NewManager(auth.Deps{
		Tokens: tokens,
		OAuth:  presetOAuth(o),
	})
	refresher := auth.NewRefreshWorker(authManager, tokens, auth.RefreshOptions{})

	// In-progress OAuth connect attempts. In memory deliberately: a flow lives
	// for the minute or two an operator spends in a consent screen, and
	// persisting it would put a PKCE verifier on disk for no benefit.
	flows := auth.NewFlowStore(10 * time.Minute)

	// A provider whose preset this build no longer ships degrades to its
	// stored kind and base url. The degradation is free — provider rows carry
	// both — but losing the preset's quirks and surfaces silently on upgrade
	// is not, so it reaches /healthz.
	if ps, err := src.Providers(context.Background()); err == nil {
		startupWarnings = append(startupWarnings, catalog.OrphanedPresets(ps, catalog.Embedded())...)
	}

	cat := catalog.NewStore(db, src)
	// Rebuilt synchronously, before anything binds a listener. A request
	// arriving in the first second must route against what the database
	// already knows rather than against an empty snapshot.
	if err := cat.Rebuild(context.Background()); err != nil {
		// Not fatal: an unreadable catalog costs routing precision, and
		// refusing to serve over it would be worse. It reaches /healthz
		// through the same channel a bad config edit does.
		startupWarnings = append(startupWarnings, fmt.Sprintf("catalog: %v", err))
	}

	syncer := catalog.NewSyncer(db, src, cat, catalog.SyncOptions{
		URL:      cfg.Catalog.ModelsDevURL,
		Interval: cfg.Catalog.SyncInterval,
		Timeout:  cfg.Catalog.SyncTimeout,
	})

	// Constructed whether or not its worker runs: with the refresh turned off
	// it simply serves the catalogue embedded at build time, which keeps one
	// path into the import filter rather than two.
	freeSync := catalog.NewFreeSyncer(catalog.FreeSyncOptions{
		URL:      cfg.Catalog.FreeCatalogURL,
		Interval: cfg.Catalog.FreeCatalogInterval,
		Timeout:  cfg.Catalog.SyncTimeout,
	})

	// The price index has no embedded fallback, so the store is wired to the
	// syncer whether or not the worker runs: with the refresh off it simply
	// serves nothing and another source prices the model.
	litellmSync := catalog.NewLiteLLMSyncer(catalog.LiteLLMSyncOptions{
		URL:      cfg.Catalog.LiteLLMURL,
		Interval: cfg.Catalog.LiteLLMInterval,
		Timeout:  cfg.Catalog.SyncTimeout,
		OnUpdate: func(c context.Context) {
			if err := cat.Rebuild(c); err != nil {
				slog.Warn("catalog rebuild after litellm price sync failed", "err", err)
			}
		},
	})
	cat.SetLiteLLM(litellmSync.Doc)

	// A provider whose base URL names a local program rather than a host is
	// served by a transport of its own, registered on every client that
	// reaches providers: the executor's, the discovery sweep's, and the
	// console's probe. Registering in one place would have left the other two
	// reporting "unsupported protocol scheme" for a provider the operator can
	// see in the catalogue.
	protocols := map[string]http.RoundTripper{
		localcli.AuggieScheme: localcli.NewTransport(localcli.NewAuggie()),
	}

	var disc *catalog.Discoverer
	if e := cfg.Catalog.Discovery.Enabled; e == nil || *e {
		disc = catalog.NewDiscoverer(db, src, cat, breaker, catalog.DiscoveryOptions{
			Interval:    cfg.Catalog.Discovery.Interval,
			Concurrency: cfg.Catalog.Discovery.Concurrency,
			Timeout:     cfg.Catalog.Discovery.Timeout,
			// Bedrock's model list comes from two signed control-plane calls
			// rather than one GET. Registered here because the server already
			// imports both halves and catalog must not import an adapter.
			Listers: map[string]catalog.KindLister{"bedrock": bedrockadapter.NewLister(nil)},
			Auth:    authManager,
			// The syncer holds the newest models.dev document; the import
			// filter prices against that rather than against the snapshot
			// frozen into this binary. The free-tier catalogue is refreshed
			// the same way, for the same reason.
			Metadata:  syncer.Doc,
			FreeTiers: freeSync.Catalog,
			Protocols: protocols,
		})
	}

	var freeSyncWorker *catalog.FreeSyncer
	if cfg.Catalog.FreeCatalogSyncEnabled() {
		freeSyncWorker = freeSync
	}

	var litellmSyncWorker *catalog.LiteLLMSyncer
	if cfg.Catalog.LiteLLMSyncEnabled() {
		litellmSyncWorker = litellmSync
	}

	mediaFetcher := geminiadapter.NewFetcher()
	mediaFetcher.Inline = cfg.MediaInline()

	adapters := map[string]adapter.Adapter{
		"openaicompat": openaicompat.New(),
		"anthropic":    anthropicadapter.New(),
		"gemini":       geminiadapter.NewWithFetcher(mediaFetcher),
		"bedrock":      bedrockadapter.New(),
		// The same fetcher as the direct Gemini route: media.inline is the
		// operator's answer to "may the gateway fetch a client's image URL",
		// and it has to mean the same thing on both.
		"vertex": vertexadapter.NewWithFetcher(mediaFetcher),
	}
	kinds := make([]string, 0, len(adapters))
	for k := range adapters {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	ex := exec.New(cfgStore, src, adapters, exec.Deps{
		Log: met, Health: breaker, Fleet: breaker, Catalog: cat,
		Auth:      authManager,
		Protocols: protocols,
	})

	// The dashboard is always mounted. A missing password hash closes it — the
	// API refuses every login — rather than making startup fail: the gateway's
	// job is proxying, and refusing to start over an optional dashboard would
	// take a working proxy down for a feature the operator may not use.
	//
	// The warning is appended before admin.New because startupWarnings is
	// passed by value, and the same slice is what /healthz reads.
	passwordHash := os.Getenv("DARKROUTER_ADMIN_PASSWORD_HASH")
	if passwordHash == "" {
		startupWarnings = append(startupWarnings,
			"DARKROUTER_ADMIN_PASSWORD_HASH is not set; the admin dashboard will refuse "+
				"every login. Generate one with: darkrouter hash-password")
	}
	// A typed nil is not a nil interface. Assigning a disabled discoverer
	// straight into admin.Deps.Disc would satisfy every `Disc != nil` guard in
	// that package and then dereference on the first call.
	var discTrigger admin.DiscoveryTrigger
	if disc != nil {
		discTrigger = disc
	}

	adm, err := admin.New(admin.Deps{
		DB: db, PasswordHash: passwordHash,
		Config: cfgStore, Src: src, Key: key,
		Catalog: cat, Disc: discTrigger, Sync: syncer, Breaker: breaker,
		Presets: o.presets, Exec: ex, Kinds: kinds,
		Warnings: startupWarnings,
		Flows:    flows,
		Auth:     authManager,
		HTTP:     &http.Client{Transport: protocolTransport(protocols)},
	})
	if err != nil {
		return nil, fmt.Errorf("admin: %w", err)
	}

	return &Server{
		store: cfgStore, db: db, src: src, logw: logw, breaker: breaker,
		metrics: met, tokens: newTokenAuth(db),
		persist: health.NewPersister(breaker, db, 5*time.Second),
		cat:     cat, disc: disc, sync: syncer, adm: adm,
		freeSync:    freeSyncWorker,
		litellmSync: litellmSyncWorker,
		refresher:   refresher,
		ex:          ex,
		started:     time.Now(),
		warnings:    startupWarnings,
	}, nil
}

// protocolTransport is the console's outbound transport: the shared default
// plus the non-network schemes, so the Test button reaches a local CLI provider
// the same way the sweep does.
func protocolTransport(protocols map[string]http.RoundTripper) http.RoundTripper {
	t := http.DefaultTransport.(*http.Transport).Clone()
	for scheme, rt := range protocols {
		t.RegisterProtocol(scheme, rt)
	}
	return t
}

func (s *Server) ProxyHandler() http.Handler {
	mux := http.NewServeMux()

	oa := openaiedge.New()
	mux.HandleFunc("POST /v1/chat/completions", s.authed(oa, func(w http.ResponseWriter, r *http.Request) {
		s.ex.Handle(w, r, oa)
	}))
	mux.HandleFunc("POST /v1/embeddings", s.authed(oa, func(w http.ResponseWriter, r *http.Request) {
		s.ex.HandleEmbeddings(w, r, oa)
	}))
	mux.HandleFunc("POST /v1/moderations", s.authed(oa, func(w http.ResponseWriter, r *http.Request) {
		s.ex.HandleModerations(w, r, oa)
	}))
	mux.HandleFunc("POST /v1/rerank", s.authed(oa, func(w http.ResponseWriter, r *http.Request) {
		s.ex.HandleRerank(w, r, oa)
	}))
	mux.HandleFunc("POST /v1/images/generations", s.authed(oa, func(w http.ResponseWriter, r *http.Request) {
		s.ex.HandleImages(w, r, oa)
	}))
	mux.HandleFunc("POST /v1/audio/transcriptions", s.authed(oa, func(w http.ResponseWriter, r *http.Request) {
		s.ex.HandleTranscriptions(w, r, oa)
	}))
	mux.HandleFunc("POST /v1/audio/speech", s.authed(oa, func(w http.ResponseWriter, r *http.Request) {
		s.ex.HandleSpeech(w, r, oa)
	}))
	// One shared instance for the auth check, which is stateless, and a fresh
	// one per request for the handler, which holds the response echo. Sharing
	// the handler's instance across requests would race on that field.
	rdAuth := openaiedge.NewResponses()
	mux.HandleFunc("POST /v1/responses", s.authed(rdAuth, func(w http.ResponseWriter, r *http.Request) {
		s.ex.Handle(w, r, openaiedge.NewResponses())
	}))
	mux.HandleFunc("GET /v1/models", s.authed(oa, s.handleModels))

	an := anthropicedge.New()
	mux.HandleFunc("POST /v1/messages", s.authed(an, func(w http.ResponseWriter, r *http.Request) {
		s.ex.Handle(w, r, an)
	}))
	// More specific than "POST /v1/messages", so net/http's precedence rules
	// pick it without any ordering concern.
	mux.HandleFunc("POST /v1/messages/count_tokens", s.authed(an, func(w http.ResponseWriter, r *http.Request) {
		s.ex.HandleCount(w, r, an, "anthropic")
	}))

	// One pattern for every Gemini method. A net/http wildcard occupies a whole
	// path segment, so "{model}:generateContent" is not a legal pattern and the
	// suffix is dispatched here instead.
	gm := geminiedge.New()
	mux.HandleFunc("POST /v1beta/models/{model}", s.authed(gm, s.handleGemini))
	mux.HandleFunc("GET /v1beta/models", s.authed(gm, s.handleGeminiModels))

	return mux
}

// handleGemini dispatches on the method suffix the path segment carries.
func (s *Server) handleGemini(w http.ResponseWriter, r *http.Request) {
	_, method := geminiedge.ExtractModel(r.PathValue("model"))
	switch method {
	case "countTokens":
		s.ex.HandleCount(w, r, geminiedge.New(), "gemini")
	case "generateContent", "streamGenerateContent":
		// NewFor rather than New: ?alt=sse selects the streaming wire form, and
		// WriteStream cannot see the request that chose it.
		s.ex.Handle(w, r, geminiedge.NewFor(r))
	default:
		_ = geminiedge.New().WriteError(w, &ir.Error{
			Type: ir.ErrNotFound, Message: "unsupported method: " + method,
		})
	}
}

func (s *Server) handleGeminiModels(w http.ResponseWriter, r *http.Request) {
	entries := make([]geminiedge.ModelEntry, 0)
	for _, m := range s.listedModels() {
		entries = append(entries, geminiedge.ModelEntry{
			ID: m.ID, ContextWindow: m.ContextWindow, MaxOutputTokens: m.MaxOutputTokens,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(geminiedge.ListModels(entries))
}

// authed enforces the optional proxy token in the route's own dialect. The
// token is read live because proxy_token is hot-reloadable, unlike the listen
// addresses, and a rejection is written in the dialect the client speaks so its
// existing error handling applies.
func (s *Server) authed(d edge.Dialect, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		presented := d.ProxyToken(r)
		shared := s.store.Current().Server.ProxyToken

		// The shared secret stays valid alongside per-client tokens. Removing
		// it in the release that adds them would stop every existing client
		// the moment an operator upgrades.
		if shared != "" && constantTimeEqual(presented, shared) {
			h(w, r)
			return
		}
		if s.tokens.accept(r.Context(), presented) {
			h(w, r)
			return
		}
		// Authentication is off only when neither mechanism is configured: a
		// gateway with proxy tokens issued must not accept an empty header
		// just because the shared secret is unset.
		if shared == "" && !s.tokens.configured(r.Context()) {
			h(w, r)
			return
		}
		_ = d.WriteError(w, &ir.Error{
			Type: ir.ErrAuthentication, Message: "invalid proxy token",
		})
	}
}

// constantTimeEqual compares two secrets without leaking their lengths.
// subtle.ConstantTimeCompare returns early when lengths differ, so hashing both
// sides first is what makes the comparison genuinely constant-time.
func constantTimeEqual(got, want string) bool {
	g := sha256.Sum256([]byte(got))
	w := sha256.Sum256([]byte(want))
	return subtle.ConstantTimeCompare(g[:], w[:]) == 1
}

// listedModel is one row of a client-facing listing.
type listedModel struct {
	ID              string
	OwnedBy         string
	ContextWindow   int
	MaxOutputTokens int
}

// listedModels returns the models a client should see: the configured aliases
// first, then everything routable in the catalog.
//
// Aliases first is phase 1's behavior and spec §9 preserves it deliberately.
// They are the names the operator chose for this gateway; burying them under
// two hundred discovered ids makes it look like somebody else's catalog.
//
// Search excludes removed_upstream by default and keeps stale, which is the
// asymmetry spec §5.1 exists for.
func (s *Server) listedModels() []listedModel {
	seen := map[string]bool{}
	out := []listedModel{}

	cfg := s.store.Current()
	aliases := make([]string, 0, len(cfg.Aliases))
	for name := range cfg.Aliases {
		aliases = append(aliases, name)
	}
	// Map iteration is random; a listing that reorders itself between two
	// requests looks broken in a client's model picker.
	sort.Strings(aliases)
	for _, name := range aliases {
		seen[name] = true
		out = append(out, listedModel{ID: name, OwnedBy: "darkrouter"})
	}

	for _, m := range s.cat.Snapshot().Search(catalog.Filter{Surface: ir.SurfaceLLM}) {
		if seen[m.ModelID] {
			continue
		}
		seen[m.ModelID] = true
		out = append(out, listedModel{
			ID: m.ModelID, OwnedBy: m.ProviderID,
			ContextWindow: m.ContextWindow, MaxOutputTokens: m.MaxOutputTokens,
		})
	}
	return out
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	data := []any{}
	for _, m := range s.listedModels() {
		data = append(data, map[string]any{
			"id": m.ID, "object": "model", "owned_by": m.OwnedBy,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": data})
}

// CloseAdmin stops any temporary OAuth redirect listener. Run() does this on
// its own shutdown path; it is exported for a caller that builds a Server
// without running it, which a test does.
func (s *Server) CloseAdmin() { s.adm.Close() }

func (s *Server) AdminHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		cfg := s.store.Current()
		// Read once: two calls could straddle a reload and report a valid config
		// with an error attached, or an invalid one with none.
		cfgErr := s.store.LastError()

		// Startup warnings first: they explain state the config file cannot,
		// such as a providers block that is no longer the source of truth.
		warnings := append(append([]string{}, s.warnings...), cfg.Warnings...)

		body := map[string]any{
			"config_valid": cfgErr == nil,
			"warnings":     warnings,
			"uptime":       time.Since(s.started).Round(time.Second).String(),
			"version":      Version,
			// A non-zero count means usage_daily is a lower bound. It counts
			// records, not tokens or dollars.
			"log_records_dropped": s.logw.Dropped(),
			"log_records_written": s.logw.Written(),
		}
		if cfgErr != nil {
			body["config_error"] = cfgErr.Error()
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	})
	// Ready means able to serve: the database answers and the live config is
	// the one on disk. An orchestrator routes on this, so a gateway that would
	// fail every request must not look ready.
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := s.db.Read.PingContext(ctx); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprintf(w, "database: %v\n", err)
			return
		}
		if err := s.store.LastError(); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprintf(w, "config: %v\n", err)
			return
		}
		fmt.Fprint(w, "ok\n")
	})
	mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		s.metrics.write(w, s.logw.Dropped(), s.logw.Written())
	})

	// Everything else goes to the admin API, which owns its own auth. Mounted
	// last and at the root so the three endpoints above win their exact paths:
	// an orchestrator and a Prometheus scrape read them, and a session in front
	// of either breaks it.
	mux.Handle("/", s.adm.Handler())
	return mux
}

// Run starts both listeners and blocks until ctx is cancelled, then drains.
func (s *Server) Run(ctx context.Context) error {
	// Derived so every goroutine this function starts is cancelled on any exit
	// path, not only on the caller's cancellation.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Workers get a context independent of the request lifecycle. Cancelling
	// them with the handlers would stop the log writer while requests were
	// still producing records, and the drain would race the producers.
	workerCtx, stopWorkers := context.WithCancel(context.Background())
	var workers sync.WaitGroup
	// Backstop for every exit path, including a listener that fails to bind
	// before the ordered shutdown below is ever reached. On the normal path the
	// explicit stop runs first and this is a no-op.
	defer func() {
		stopWorkers()
		workers.Wait()
		// Temporary OAuth redirect listeners hold a loopback port. Leaving one
		// bound after the gateway is gone is exactly the leak the operator
		// notices next time they run the vendor's own CLI.
		s.adm.Close()
	}()
	startWorker := func(name string, fn func(context.Context) error) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			if err := runWorker(workerCtx, name, fn, workerRestartDelay); err != nil {
				slog.Error("worker failed", "worker", name, "err", err)
			}
		}()
	}

	if err := s.persist.Restore(workerCtx); err != nil {
		// Not fatal: an unreadable health table costs a restart's worth of
		// accuracy, and refusing to serve over it would be worse.
		s.store.RecordError(fmt.Errorf("health rehydration: %w", err))
	}

	if lu, err := s.db.LoadLastUsed(workerCtx); err != nil {
		s.store.RecordError(fmt.Errorf("credential usage rehydration: %w", err))
	} else {
		s.breaker.RehydrateLastUsed(lu)
	}

	startWorker("credential usage", func(c context.Context) error {
		// Persisted purely for restart continuity: the in-memory map is
		// authoritative, so a missed write costs ordering accuracy across one
		// restart and nothing else.
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-c.Done():
				return s.db.SaveLastUsed(context.Background(), s.breaker.LastUsedSnapshot())
			case <-t.C:
				if err := s.db.SaveLastUsed(c, s.breaker.LastUsedSnapshot()); err != nil {
					slog.Error("saving credential usage failed", "err", err)
				}
			}
		}
	})

	startWorker("token refresh", s.refresher.Run)
	startWorker("log writer", s.logw.Run)
	startWorker("health persister", s.persist.Run)
	startWorker("rollup", func(c context.Context) error {
		return store.RunRollup(c, s.db, time.Hour)
	})
	startWorker("retention", func(c context.Context) error {
		return store.RunRetention(c, s.db, s.store, time.Hour)
	})
	// Both take workerCtx rather than ctx. Run already keeps worker lifetime
	// separate from request lifetime, and a sweep cancelled the instant
	// SIGTERM arrives would record a provider failure that was really a
	// shutdown — three of those mark its whole catalogue stale.
	if s.disc != nil {
		startWorker("discovery", s.disc.Run)
	}
	startWorker("models.dev sync", s.sync.Run)
	if s.freeSync != nil {
		startWorker("free catalogue sync", s.freeSync.Run)
	}
	if s.litellmSync != nil {
		startWorker("litellm price sync", s.litellmSync.Run)
	}

	startWorker("config watcher", func(c context.Context) error {
		// A watcher that cannot start leaves hot reload silently dead, so the
		// failure has to reach /healthz rather than being discarded.
		s.store.RecordError(s.store.Watch(c))
		return nil
	})

	cfg := s.store.Current()

	// The lifecycle context is handed to every request. It is deliberately not
	// derived from ctx: tying it there would kill in-flight streams the instant
	// SIGTERM arrives instead of letting them drain. It is cancelled only when
	// the drain deadline expires, which is the signal handlers need to emit a
	// terminal event.
	lc, cancelLC := context.WithCancel(context.Background())
	defer cancelLC()

	readTimeout, idleTimeout := listenerTimeouts(cfg.Policy.Timeout)
	proxy := &http.Server{
		Addr:    cfg.Server.ProxyListen,
		Handler: s.ProxyHandler(),
		// No WriteTimeout: it would kill long streams at a fixed age. Slowloris
		// protection comes from ReadHeaderTimeout; ReadTimeout bounds a client
		// that sends its body at a trickle.
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		IdleTimeout:       idleTimeout,
		BaseContext:       func(net.Listener) context.Context { return lc },
	}
	admin := &http.Server{
		Addr:              cfg.Server.AdminListen,
		Handler:           s.AdminHandler(),
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       minReadTimeout,
		IdleTimeout:       idleTimeout,
	}

	// Both listeners are bound before any goroutine starts. ListenAndServe would
	// bind inside the goroutine, which makes a bind failure asynchronous and
	// leaves Close racing a listener that has not been created yet — the
	// surviving server then binds *after* Close and leaks its port.
	proxyLn, err := net.Listen("tcp", cfg.Server.ProxyListen)
	if err != nil {
		return fmt.Errorf("proxy listen %s: %w", cfg.Server.ProxyListen, err)
	}
	adminLn, err := net.Listen("tcp", cfg.Server.AdminListen)
	if err != nil {
		_ = proxyLn.Close()
		return fmt.Errorf("admin listen %s: %w", cfg.Server.AdminListen, err)
	}

	errCh := make(chan error, 2)
	go func() { errCh <- ignoreClosed(proxy.Serve(proxyLn)) }()
	go func() { errCh <- ignoreClosed(admin.Serve(adminLn)) }()

	select {
	case err := <-errCh:
		// One server stopped serving. Close the other rather than leaking its
		// port, and wait for its goroutine to confirm the listener is released.
		_ = proxy.Close()
		_ = admin.Close()
		<-errCh
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

	// In-flight requests have finished, so nothing more will be produced.
	// Stopping the workers now drains the log channel and flushes health, in
	// the order master design §16 fixes.
	stopWorkers()
	workers.Wait()
	return shutdownErr
}

// Listener bounds. minReadTimeout covers max_body_bytes at modest bandwidth;
// a proxy whose policy.timeout.total is longer gets twice that instead, so a
// client is never cut off sooner than the gateway would cut off a provider.
const (
	readHeaderTimeout  = 10 * time.Second
	minReadTimeout     = 60 * time.Second
	listenerIdle       = 120 * time.Second
	workerRestartDelay = time.Second
)

func listenerTimeouts(t config.TimeoutConfig) (read, idle time.Duration) {
	read = minReadTimeout
	if d := 2 * t.Total; d > read {
		read = d
	}
	return read, listenerIdle
}

// runWorker runs fn until it returns or ctx ends, restarting it after a panic.
// A background job that dies with a stack trace takes its whole function —
// log writing, health persistence, discovery — down for the rest of the
// process lifetime, which is a worse failure than the one it panicked over.
func runWorker(ctx context.Context, name string, fn func(context.Context) error,
	restartDelay time.Duration) error {

	for {
		err, panicked := runOnce(name, fn, ctx)
		if !panicked {
			return err
		}
		if ctx.Err() != nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(restartDelay):
		}
	}
}

func runOnce(name string, fn func(context.Context) error, ctx context.Context) (err error, panicked bool) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("worker panicked", "worker", name, "panic", r, "stack", string(debug.Stack()))
			panicked = true
		}
	}()
	return fn(ctx), false
}

func ignoreClosed(err error) error {
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
