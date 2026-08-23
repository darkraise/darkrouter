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

	"github.com/darkraise/darkrouter/internal/catalog"
	"github.com/darkraise/darkrouter/internal/config"
	"github.com/darkraise/darkrouter/internal/health"
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
