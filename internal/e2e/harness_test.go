// Package e2e drives the assembled gateway.
//
// It exists because phase 8's credential strategies only compose at the top:
// internal/auth, the adapters, the router and the admin API each have unit
// tests, and none of them can show that a request entering the proxy port
// leaves it signed.
//
// Every upstream here is a fake. This environment has no AWS account, no GCP
// service account and no Claude subscription, so what these tests prove is that
// the wiring holds end to end — not that a real vendor accepts the payload.
package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkrouter/internal/admin"
	"github.com/darkraise/darkrouter/internal/catalog"
	"github.com/darkraise/darkrouter/internal/config"
	"github.com/darkraise/darkrouter/internal/crypto"
	"github.com/darkraise/darkrouter/internal/server"
	"github.com/darkraise/darkrouter/internal/store"
)

// gateway is an assembled server with its two handlers exposed.
type gateway struct {
	srv    *server.Server
	db     *store.DB
	key    *crypto.Key
	dbPath string

	cookie *http.Cookie
	csrf   string
}

const testPassword = "hunter2"

// passwordHash is memoized: bcrypt at cost 12 is a tenth of a second, and this
// package would otherwise pay it per test.
var passwordHash = sync.OnceValue(func() string {
	h, err := admin.HashPassword(testPassword)
	if err != nil {
		panic(err)
	}
	return h
})

func newGateway(t *testing.T) *gateway { return openGateway(t, "") }

// openGateway builds a server over dbPath, or a fresh temporary file when it is
// empty. Reopening the same path is how the restart test reaches state that
// only durability can carry.
func openGateway(t *testing.T, dbPath string) *gateway {
	t.Helper()
	return openGatewayOpts(t, dbPath)
}

func openGatewayOpts(t *testing.T, dbPath string, opts ...server.Option) *gateway {
	t.Helper()
	if dbPath == "" {
		dbPath = filepath.Join(t.TempDir(), "e2e.db")
	}
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	key, err := store.OpenKeyring(ctx, db, "e2e-master-key")
	if err != nil {
		t.Fatal(err)
	}

	cfgPath := filepath.Join(t.TempDir(), "darkrouter.yaml")
	// Discovery and the models.dev sync are switched off: neither is what
	// these tests exercise, and a phase 9 test that runs the server's worker
	// loop otherwise races its own seeding against a background rebuild, and
	// makes a live internet call on every run.
	cfg := "server:\n  proxy_listen: \":0\"\n  admin_listen: \":0\"\n" +
		"catalog:\n  discovery:\n    enabled: false\n  models_dev_url: \"http://127.0.0.1:1/\"\n"
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	cfgStore, err := config.NewStore(cfgPath, func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatal(err)
	}

	t.Setenv("DARKROUTER_ADMIN_PASSWORD_HASH", passwordHash())
	srv, err := server.New(cfgStore, db, key, nil, opts...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		srv.CloseAdmin()
		_ = db.Close()
	})

	g := &gateway{srv: srv, db: db, key: key, dbPath: dbPath}
	g.login(t)
	return g
}

func (g *gateway) login(t *testing.T) {
	t.Helper()
	r := httptest.NewRequest("POST", "/api/auth/login",
		strings.NewReader(`{"password":"`+testPassword+`"}`))
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	w := httptest.NewRecorder()
	g.srv.AdminHandler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("login: %d %s", w.Code, w.Body.String())
	}
	var body struct {
		CSRF string `json:"csrf_token"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == "darkrouter_session" {
			g.cookie = c
		}
	}
	if g.cookie == nil {
		t.Fatal("no session cookie")
	}
	g.csrf = body.CSRF
}

// admin performs an authenticated admin request.
func (g *gateway) admin(t *testing.T, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.AddCookie(g.cookie)
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	if method != "GET" {
		r.Header.Set("X-CSRF-Token", g.csrf)
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	g.srv.AdminHandler().ServeHTTP(w, r)
	return w
}

func (g *gateway) mustAdmin(t *testing.T, method, path, body string, want int) *httptest.ResponseRecorder {
	t.Helper()
	w := g.admin(t, method, path, body)
	if w.Code != want {
		t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
	}
	return w
}

// proxy sends a client request through the proxy port.
func (g *gateway) proxy(t *testing.T, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest("POST", path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	g.srv.ProxyHandler().ServeHTTP(w, r)
	return w
}

// seedModel writes a catalog row directly. Discovery is a background worker and
// these tests must not wait on one.
func (g *gateway) seedModel(t *testing.T, providerID, modelID, publisher string) {
	t.Helper()
	if err := g.db.RecordDiscoverySuccess(context.Background(), providerID,
		[]store.DiscoveredModel{{
			ModelID: modelID, Publisher: publisher,
			ContextWindow: 128000, MaxOutputTokens: 4096,
		}}, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := g.srv.Catalog().Rebuild(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// reloadProviders forces the provider source to re-read the database. A
// credential written directly — an OAuth token document, which the API's key
// endpoint cannot express — is otherwise invisible to routing, and the provider
// is skipped as having none.
func (g *gateway) reloadProviders(t *testing.T, providerID string) {
	t.Helper()
	// A PATCH is followed by a source reload inside the handler, which is the
	// only reload reachable from outside the package.
	g.mustAdmin(t, "PATCH", "/api/providers/"+providerID, `{"priority":5}`, http.StatusOK)
}

func jsonStr(t *testing.T, s string) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func chatBody(model string) string {
	return fmt.Sprintf(
		`{"model":%q,"messages":[{"role":"user","content":"hi"}],"max_tokens":16}`, model)
}

// oauthPresets is the shipped set with anthropic-oauth's token endpoint pointed
// at a fake. The preset set is embedded, so this is the only way an assembled
// server can be made to talk to one.
func oauthPresets(tokenURL string) catalog.Presets {
	out := catalog.Presets{}
	for id, p := range catalog.Embedded() {
		out[id] = p
	}
	base := out["anthropic-oauth"]
	base.OAuth = &catalog.OAuth{
		AuthorizeURL: "https://claude.ai/oauth/authorize",
		TokenURL:     tokenURL,
		ClientID:     "e2e-client",
		Scopes:       []string{"user:inference"},
		Redirect:     catalog.Redirect{Style: "manual"},
	}
	out["anthropic-oauth"] = base
	return out
}

// openGatewayWithPresets is openGateway with a preset override.
func openGatewayWithPresets(t *testing.T, dbPath string, presets catalog.Presets) *gateway {
	t.Helper()
	return openGatewayOpts(t, dbPath, server.WithPresets(presets))
}
