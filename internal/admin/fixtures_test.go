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
)

// configStoreFor writes a minimal config and opens a store over it. aliases is
// raw YAML appended under an aliases: block, or empty for none.
func configStoreFor(t *testing.T, aliases string) *config.Store {
	t.Helper()
	body := "server:\n  proxy_listen: \":0\"\n  admin_listen: \":0\"\n"
	if aliases != "" {
		body += "aliases:\n" + aliases
	}
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
	db := store.MigratedForTest(t)
	key, err := store.OpenKeyring(context.Background(), db, "master")
	if err != nil {
		t.Fatal(err)
	}
	cfg := configStoreFor(t, aliases)
	s, err := New(Deps{
		DB: db, PasswordHash: testHash(),
		Config: cfg, Key: key, Presets: catalog.Embedded(),
		Src:     provider.NewSQLSource(db, key),
		Breaker: health.New(3, time.Minute),
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
	db := store.MigratedForTest(t)
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
		Surfaces: []ir.Surface{ir.SurfaceLLM},
	}}, []string{"p"}))

	ex := exec.New(cfg, provider.NewYAMLSource(cfg),
		map[string]adapter.Adapter{"openaicompat": openaicompat.New()},
		exec.Deps{Catalog: cat})

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
