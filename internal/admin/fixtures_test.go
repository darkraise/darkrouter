package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/adapter/openaicompat"
	"github.com/darkraise/darkrouter/internal/catalog"
	"github.com/darkraise/darkrouter/internal/config"
	"github.com/darkraise/darkrouter/internal/exec"
	"github.com/darkraise/darkrouter/internal/health"
	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/provider"
	"github.com/darkraise/darkrouter/internal/store"
	"github.com/darkraise/darkrouter/internal/store/storetest"
)

// configStoreFor writes a minimal config and opens a store over it. aliases is
// raw YAML appended under an aliases: block, or empty for none.
func configStoreFor(t *testing.T, aliases string) *config.Store {
	t.Helper()
	return configStoreWith(t, aliases, "")
}

// configStoreWith adds arbitrary top-level YAML after the aliases, for a test
// that needs a key the minimal document does not carry.
func configStoreWith(t *testing.T, aliases, extra string) *config.Store {
	t.Helper()
	body := "server:\n  proxy_listen: \":0\"\n  admin_listen: \":0\"\n"
	if aliases != "" {
		body += "aliases:\n" + aliases
	}
	body += extra
	path := filepath.Join(t.TempDir(), "darkrouter.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := config.NewStore(path, func(string) (string, bool) { return "sk", true })
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// testServerFull is testServer with every collaborator the provider endpoints
// need: a keyring to encrypt with, the shipped presets, a SQL provider source to
// reload, a breaker, and a config store for the alias lookups.
func testServerFull(t *testing.T) (*Server, *store.DB) {
	t.Helper()
	return testServerFullWithAliases(t, "")
}

func testServerFullWithAliases(t *testing.T, aliases string) (*Server, *store.DB) {
	t.Helper()
	return testServerFullWith(t, aliases, "")
}

// testServerFullWithConfig is testServerFull with extra top-level YAML in the
// config document, for a test that turns a key off.
func testServerFullWithConfig(t *testing.T, extra string) (*Server, *store.DB) {
	t.Helper()
	return testServerFullWith(t, "", extra)
}

func testServerFullWith(t *testing.T, aliases, extra string) (*Server, *store.DB) {
	t.Helper()
	db := storetest.Migrated(t)
	key, err := store.OpenKeyring(context.Background(), db, "master")
	if err != nil {
		t.Fatal(err)
	}
	cfg := configStoreWith(t, aliases, extra)
	// Mirrors cmd/darkrouter: aliases and policy are overlaid from SQLite, so
	// a test that writes through the API sees the same snapshot a request
	// would. Without it the write lands in the database and nowhere else.
	if _, err := store.ImportConfigOnce(context.Background(), db, cfg.Current()); err != nil {
		t.Fatal(err)
	}
	cfg.SetOverlay(func(c *config.Config) error {
		return store.OverlayConfig(context.Background(), db, c)
	})
	if err := cfg.Reload(); err != nil {
		t.Fatal(err)
	}
	src := provider.NewSQLSource(db, key)
	cat := catalog.NewStore(db, src)
	breaker := health.New(3, time.Minute)
	s, err := New(Deps{
		DB: db, PasswordHash: testHash(),
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
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.AddCookie(cookie)
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	if method != "GET" {
		r.Header.Set(csrfHeader, token)
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
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
func testServerWithCatalog(t *testing.T, aliases string) (*Server, *store.DB) {
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
	path := filepath.Join(t.TempDir(), "darkrouter.yaml")
	body := "server:\n  proxy_listen: \":0\"\n  admin_listen: \":0\"\nproviders:\n" +
		"  - id: p\n    kind: openaicompat\n    base_url: " + upstreamURL +
		"\n    api_key: sk\n    models: [" + model + "]\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.NewStore(path, func(string) (string, bool) { return "sk", true })
	if err != nil {
		t.Fatal(err)
	}
	cat := &catalog.Store{}
	cat.Set(catalog.NewSnapshot([]catalog.Model{{
		ProviderID: "p", ModelID: model, State: catalog.StateLive,
		Surfaces: []ir.Surface{ir.SurfaceLLM, ir.SurfaceEmbedding},
	}}, []string{"p"}))

	ex := exec.New(cfg, provider.NewYAMLSource(cfg),
		map[string]adapter.Adapter{"openaicompat": openaicompat.New()},
		exec.Deps{Catalog: cat, Log: logger})

	s, err := New(Deps{
		DB: db, PasswordHash: testHash(), Config: cfg, Key: key,
		Presets: catalog.Embedded(), Catalog: cat,
		Breaker: health.New(3, time.Minute), Exec: ex,
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}
