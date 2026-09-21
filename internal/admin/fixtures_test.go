package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/adapter/openaicompat"
	"github.com/darkraise/darkrouter/internal/catalog"
	"github.com/darkraise/darkrouter/internal/config"
	"github.com/darkraise/darkrouter/internal/exec"
	"github.com/darkraise/darkrouter/internal/health"
	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/provider"
	"github.com/darkraise/darkrouter/internal/provider/providertest"
	"github.com/darkraise/darkrouter/internal/store"
	"github.com/darkraise/darkrouter/internal/store/storetest"
)

// configStoreFor opens a store over a minimal config carrying aliases.
func configStoreFor(t *testing.T, aliases map[string][]string) *config.Store {
	t.Helper()
	return configStoreWith(t, aliases, nil)
}

// configStoreWith lets a test change a key the minimal config leaves on its
// default.
func configStoreWith(t *testing.T, aliases map[string][]string, tune func(*config.Config)) *config.Store {
	t.Helper()
	c := &config.Config{}
	config.ApplyDefaults(c)
	c.Server.ProxyListen, c.Server.AdminListen = ":0", ":0"
	c.Aliases = aliases
	if tune != nil {
		tune(c)
	}
	if err := config.Validate(c); err != nil {
		t.Fatal(err)
	}
	return config.NewStoreOf(c)
}

// storeOverDatabase builds the config store the way cmd/darkrouter does: over
// store.LoadConfig, with the alias overlay on top. A test that writes a setting
// through the API then reads it back off the snapshot is exercising the path
// the running gateway uses, which a store over a fixed Config cannot show.
//
// The tune describes a configuration, and the database is where one now lives,
// so every key it moved off its default is written as a row first.
func storeOverDatabase(t *testing.T, db *store.DB, aliases map[string][]string,
	tune func(*config.Config)) *config.Store {

	t.Helper()
	ctx := context.Background()

	want := &config.Config{}
	config.ApplyDefaults(want)
	if tune != nil {
		tune(want)
	}
	base := &config.Config{}
	config.ApplyDefaults(base)
	defaults := store.ConfigRowsFor(base)
	for k, v := range store.ConfigRowsFor(want) {
		if defaults[k] == v {
			continue
		}
		if _, err := db.Write.ExecContext(ctx,
			`INSERT OR REPLACE INTO settings (key, value) VALUES (?, ?)`, k, v); err != nil {
			t.Fatal(err)
		}
	}
	if aliases != nil {
		if err := db.PutAliases(ctx, aliases); err != nil {
			t.Fatal(err)
		}
	}

	// Ephemeral listeners, because nothing in this package binds a port.
	boot := config.BootstrapFrom(func(name string) (string, bool) {
		switch name {
		case "DARKROUTER_PROXY_LISTEN", "DARKROUTER_ADMIN_LISTEN":
			return ":0", true
		case "DARKROUTER_PROXY_TOKEN":
			// So a test asserting the token is never echoed is asserting
			// against a token that is actually in the served config. Without
			// it the process carries an empty token and the assertion checks
			// for a string that was never there.
			return "fixture-proxy-token", true
		}
		return "", false
	})
	cfg, err := config.NewStoreFrom(func() (*config.Config, error) {
		return store.LoadConfig(ctx, db, boot)
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg.SetOverlay(func(c *config.Config) error {
		return store.OverlayConfig(ctx, db, c)
	})
	cfg.SetWriter(func(ctx context.Context, p config.Patch) ([]string, error) {
		return store.WriteConfig(ctx, db, boot, p)
	})
	if err := cfg.Reload(); err != nil {
		t.Fatal(err)
	}
	cfg.MarkBoot()
	return cfg
}

// testServerFull is testServer with every collaborator the provider endpoints
// need: a keyring to encrypt with, the shipped presets, a SQL provider source to
// reload, a breaker, and a config store for the alias lookups.
func testServerFull(t *testing.T) (*Server, *store.DB) {
	t.Helper()
	return testServerFullWithAliases(t, nil)
}

func testServerFullWithAliases(t *testing.T, aliases map[string][]string) (*Server, *store.DB) {
	t.Helper()
	return testServerFullWith(t, aliases, nil)
}

// testServerFullWithConfig is testServerFull with one setting changed, for a
// test that turns a key off.
func testServerFullWithConfig(t *testing.T, tune func(*config.Config)) (*Server, *store.DB) {
	t.Helper()
	return testServerFullWith(t, nil, tune)
}

func testServerFullWith(t *testing.T, aliases map[string][]string, tune func(*config.Config)) (*Server, *store.DB) {
	t.Helper()
	db := storetest.Migrated(t)
	key, err := store.OpenKeyring(context.Background(), db, "master")
	if err != nil {
		t.Fatal(err)
	}
	cfg := storeOverDatabase(t, db, aliases, tune)
	src := provider.NewSQLSource(db, key)
	cat := catalog.NewStore(db, src)
	breaker := health.New(3, time.Minute)
	s, err := New(Deps{
		DB:     db,
		Config: cfg, Key: key, Presets: catalog.Embedded(),
		Src:     src,
		Breaker: breaker,
		Catalog: cat,
		// Trigger never blocks and SyncOnce is a single call, so both are
		// exercised without a running worker behind them.
		Disc: catalog.NewDiscoverer(db, src, cat, breaker, catalog.DiscoveryOptions{}),
		Sync: catalog.NewSyncer(db, src, cat, catalog.SyncOptions{}),
	})
	if err != nil {
		t.Fatal(err)
	}
	return s, db
}

// do performs an authenticated request against the admin mux, adding the CSRF
// header on mutating verbs so a test does not repeat six lines of setup.
func do(t *testing.T, s *Server, cookie *http.Cookie, token, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	return doSite(t, s, "same-origin", cookie, token, method, path, body)
}

// doSite is do with the Sec-Fetch-Site header spelled out, for a test that has
// to arrive from somewhere other than the console's own origin.
func doSite(t *testing.T, s *Server, site string, cookie *http.Cookie, token, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	// nil for the unauthenticated shorthands below: /api/auth/login and
	// /api/auth/status are reached without a session.
	if cookie != nil {
		r.AddCookie(cookie)
	}
	r.Header.Set("Sec-Fetch-Site", site)
	if method != "GET" {
		r.Header.Set(csrfHeader, token)
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}

// mustHash creates fixture credentials at minimum cost, like testHash.
// Password and API tests still exercise HashPassword at the production cost.
func mustHash(t *testing.T, password string) string {
	t.Helper()
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	return string(h)
}

// newServerWithSession returns a server whose console is claimed by one admin
// account, that account's id, and a raw session cookie value for it.
func newServerWithSession(t *testing.T) (*Server, string, string) {
	t.Helper()
	s, _ := testServer(t)
	const uid = "u1"
	if _, err := s.deps.DB.ClaimFirstUser(t.Context(), uid, "alice", mustHash(t, "correct-horse-battery")); err != nil {
		t.Fatal(err)
	}
	const cookie = "cookie-1"
	if err := s.deps.DB.CreateSession(t.Context(), cookie, uid, sessionTTL); err != nil {
		t.Fatal(err)
	}
	return s, uid, cookie
}

// seedSecondAccountWithSession adds a second, non-admin account holding the
// session cookie "other", so a test can show that one account's request never
// reaches another's rows.
//
// The row is inserted directly rather than through a store method: CreateUser
// arrives with the account-management endpoints, and until then ClaimFirstUser
// is the only writer — and it refuses once the console is claimed.
func seedSecondAccountWithSession(t *testing.T, s *Server) (string, string) {
	t.Helper()
	const uid = "u2"
	if _, err := s.deps.DB.Write.ExecContext(t.Context(),
		`INSERT INTO users (id, username, username_lc, password_hash, role, created_at)
		 VALUES (?, 'bob', 'bob', ?, 'member', 0)`, uid, mustHash(t, "correct-horse-battery")); err != nil {
		t.Fatal(err)
	}
	const cookie = "other"
	if err := s.deps.DB.CreateSession(t.Context(), cookie, uid, sessionTTL); err != nil {
		t.Fatal(err)
	}
	return uid, cookie
}

// The request shorthands. Each is a thin wrapper over do, so a test that only
// cares about one path and one caller does not spell out six lines of setup.

func postJSON(t *testing.T, s *Server, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	return do(t, s, nil, "", "POST", path, body)
}

func getJSON(t *testing.T, s *Server, path string) *httptest.ResponseRecorder {
	t.Helper()
	return do(t, s, nil, "", "GET", path, "")
}

func postJSONAs(t *testing.T, s *Server, path, body, cookie string) *httptest.ResponseRecorder {
	t.Helper()
	c := &http.Cookie{Name: sessionCookie, Value: cookie}
	return do(t, s, c, s.csrf.Token(cookie), "POST", path, body)
}

func getJSONAs(t *testing.T, s *Server, path, cookie string) *httptest.ResponseRecorder {
	t.Helper()
	c := &http.Cookie{Name: sessionCookie, Value: cookie}
	return do(t, s, c, s.csrf.Token(cookie), "GET", path, "")
}

func deleteAs(t *testing.T, s *Server, path, cookie string) *httptest.ResponseRecorder {
	t.Helper()
	c := &http.Cookie{Name: sessionCookie, Value: cookie}
	return do(t, s, c, s.csrf.Token(cookie), "DELETE", path, "")
}

// seedProviderWithKey creates a provider and one credential, returning the
// credential id — which the cooldown tests need to build breaker keys.
func seedProviderWithKey(t *testing.T, s *Server, cookie *http.Cookie, token, id, baseURL string) string {
	t.Helper()
	if w := do(t, s, cookie, token, "POST", "/api/providers",
		`{"id":"`+id+`","name":"`+id+`","kind":"openaicompat","base_url":"`+baseURL+`"}`); w.Code != http.StatusCreated {
		t.Fatalf("seed provider %s: %d %s", id, w.Code, w.Body.String())
	}
	w := do(t, s, cookie, token, "POST", "/api/providers/"+id+"/keys",
		`{"label":"primary","secret":"sk-seed-abcdef1234"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("seed credential for %s: %d %s", id, w.Code, w.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	return created.ID
}

// catalogFixture is the four-model catalog the catalog tests read: one model on
// two providers, one with known capabilities, one with guessed ones, and one
// serving embeddings.
func catalogFixture() *catalog.Store {
	c := &catalog.Store{}
	c.Set(catalog.NewSnapshot([]catalog.Model{
		{ProviderID: "a", ModelID: "shared-model", State: catalog.StateLive,
			Surfaces: []ir.Surface{ir.SurfaceLLM}, ContextWindow: 128000,
			Capabilities: catalog.Capabilities{Tools: true, Known: true}},
		{ProviderID: "b", ModelID: "shared-model", State: catalog.StateLive,
			Surfaces: []ir.Surface{ir.SurfaceLLM}, ContextWindow: 128000,
			Capabilities: catalog.Capabilities{Tools: true, Known: true}},
		{ProviderID: "a", ModelID: "known-model", State: catalog.StateLive,
			Surfaces:     []ir.Surface{ir.SurfaceLLM},
			Capabilities: catalog.Capabilities{Known: true}},
		{ProviderID: "c", ModelID: "guessed-model", State: catalog.StateLive,
			Surfaces:     []ir.Surface{ir.SurfaceLLM, ir.SurfaceEmbedding},
			Capabilities: catalog.Capabilities{Known: false}},
	}, []string{"a", "b", "c"}))
	return c
}

// testServerWithCatalog is testServerFull carrying catalogFixture.
func testServerWithCatalog(t *testing.T, aliases map[string][]string) (*Server, *store.DB) {
	t.Helper()
	s, db := testServerFullWithAliases(t, aliases)
	s.deps.Catalog = catalogFixture()
	return s, db
}

// testServerWithExecutor builds an admin server carrying a real exec.Executor
// over a one-provider config, so the playground exercises the gateway rather
// than a mock.
func testServerWithExecutor(t *testing.T, upstreamURL, model string) *Server {
	t.Helper()
	return testServerWithExecutorLog(t, upstreamURL, model, nil)
}

// testServerWithExecutorLog is testServerWithExecutor with a log sink, for a
// test that reads what the executor recorded about a request.
func testServerWithExecutorLog(t *testing.T, upstreamURL, model string, logger exec.Logger) *Server {
	t.Helper()
	db := storetest.Migrated(t)
	key, err := store.OpenKeyring(context.Background(), db, "master")
	if err != nil {
		t.Fatal(err)
	}
	cfg := configStoreWith(t, nil, nil)
	src := providertest.NewSource(providertest.Keyed("p", "openaicompat", upstreamURL, "sk", model))
	cat := &catalog.Store{}
	cat.Set(catalog.NewSnapshot([]catalog.Model{{
		ProviderID: "p", ModelID: model, State: catalog.StateLive,
		Surfaces: []ir.Surface{ir.SurfaceLLM, ir.SurfaceEmbedding},
	}}, []string{"p"}))

	ex := exec.New(cfg, src,
		map[string]adapter.Adapter{"openaicompat": openaicompat.New()},
		exec.Deps{Catalog: cat, Log: logger})

	s, err := New(Deps{
		DB: db, Config: cfg, Key: key,
		Presets: catalog.Embedded(), Catalog: cat,
		Breaker: health.New(3, time.Minute), Exec: ex,
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}
