// Package server wires the components into two listeners and owns shutdown.
package server

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/darkraise/darkrouter/internal/adapter"
	anthropicadapter "github.com/darkraise/darkrouter/internal/adapter/anthropic"
	geminiadapter "github.com/darkraise/darkrouter/internal/adapter/gemini"
	"github.com/darkraise/darkrouter/internal/adapter/openaicompat"
	"github.com/darkraise/darkrouter/internal/admin"
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
	breaker *health.Breaker
	persist *health.Persister

	cat  *catalog.Store
	disc *catalog.Discoverer
	sync *catalog.Syncer

	adm *admin.Server

	started  time.Time
	warnings []string
}

// Catalog exposes the live snapshot holder. The listing handlers read it, and
// phase 7's admin API will too.
func (s *Server) Catalog() *catalog.Store { return s.cat }

// Discoverer exposes the worker so a provider change can trigger an immediate
// probe. It is nil when discovery is disabled.
func (s *Server) Discoverer() *catalog.Discoverer { return s.disc }

// New wires the gateway. It loads the provider set eagerly so a bad credential
// fails startup rather than every request.
func New(cfgStore *config.Store, db *store.DB, key *crypto.Key, startupWarnings []string) (*Server, error) {
	cfg := cfgStore.Current()

	src := provider.NewSQLSource(db, key)
	if err := src.Reload(context.Background()); err != nil {
		return nil, fmt.Errorf("load providers: %w", err)
	}

	logw := store.NewLogWriter(db, store.LogOptions{})
	breaker := health.New(*cfg.Policy.Cooldown.TripAfter, cfg.Policy.Cooldown.Max)

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

	var disc *catalog.Discoverer
	if e := cfg.Catalog.Discovery.Enabled; e == nil || *e {
		disc = catalog.NewDiscoverer(db, src, cat, breaker, catalog.DiscoveryOptions{
			Interval:    cfg.Catalog.Discovery.Interval,
			Concurrency: cfg.Catalog.Discovery.Concurrency,
			Timeout:     cfg.Catalog.Discovery.Timeout,
		})
	}
	syncer := catalog.NewSyncer(db, src, cat, catalog.SyncOptions{
		URL:      cfg.Catalog.ModelsDevURL,
		Interval: cfg.Catalog.SyncInterval,
		Timeout:  cfg.Catalog.SyncTimeout,
	})

	ex := exec.New(cfgStore, src, map[string]adapter.Adapter{
		"openaicompat": openaicompat.New(),
		"anthropic":    anthropicadapter.New(),
		"gemini":       geminiadapter.New(),
	}, exec.Deps{
		Log: logw, Health: breaker, Fleet: breaker, Catalog: cat,
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
	adm, err := admin.New(admin.Deps{
		DB: db, PasswordHash: passwordHash,
		Config: cfgStore, Src: src, Key: key,
		Catalog: cat, Disc: disc, Breaker: breaker,
		Presets: catalog.Embedded(), Exec: ex,
		Warnings: startupWarnings,
	})
	if err != nil {
		return nil, fmt.Errorf("admin: %w", err)
	}

	return &Server{
		store: cfgStore, db: db, src: src, logw: logw, breaker: breaker,
		persist: health.NewPersister(breaker, db, 5*time.Second),
		cat:     cat, disc: disc, sync: syncer, adm: adm,
		ex:       ex,
		started:  time.Now(),
		warnings: startupWarnings,
	}, nil
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
		token := s.store.Current().Server.ProxyToken
		if token != "" && !constantTimeEqual(d.ProxyToken(r), token) {
			_ = d.WriteError(w, &ir.Error{
				Type: ir.ErrAuthentication, Message: "invalid proxy token",
			})
			return
		}
		h(w, r)
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

// handleModels lists configured models. Aliases would be listed first, but
// Phase 1 has none; Phase 6 replaces the backing with the catalog.
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
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		fmt.Fprintf(w,
			"# HELP darkrouter_log_records_dropped_total Request records discarded because the log channel was full.\n"+
				"# TYPE darkrouter_log_records_dropped_total counter\n"+
				"darkrouter_log_records_dropped_total %d\n"+
				"# HELP darkrouter_log_records_written_total Request records persisted.\n"+
				"# TYPE darkrouter_log_records_written_total counter\n"+
				"darkrouter_log_records_written_total %d\n",
			s.logw.Dropped(), s.logw.Written())
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
	}()
	startWorker := func(name string, fn func(context.Context) error) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			if err := fn(workerCtx); err != nil {
				log.Printf("%s: %v", name, err)
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
					log.Printf("credential usage: %v", err)
				}
			}
		}
	})

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

func ignoreClosed(err error) error {
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
