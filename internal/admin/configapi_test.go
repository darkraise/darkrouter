package admin

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkrouter/internal/config"
	"github.com/darkraise/darkrouter/internal/store"
	"github.com/darkraise/darkrouter/internal/store/storetest"
)

// configBody is the shape GET /api/config returns: every block, each value
// annotated with where it came from and whether changing it does anything.
type configBody struct {
	Valid    bool                 `json:"valid"`
	Warnings []string             `json:"warnings"`
	Blocks   map[string]any       `json:"blocks"`
	Fields   map[string]fieldMeta `json:"fields"`
}

func getConfig(t *testing.T, s *Server) configBody {
	t.Helper()
	cookie, token := login(t, s)
	w := do(t, s, cookie, token, "GET", "/api/config", "")
	if w.Code != 200 {
		t.Fatalf("GET /api/config = %d: %s", w.Code, w.Body.String())
	}
	var body configBody
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v\n%s", err, w.Body.String())
	}
	return body
}

func TestConfigReturnsEveryBlock(t *testing.T) {
	// server was the only block served, so log, capture and catalog had no
	// data source at all behind the settings screen.
	s, _ := testServerFull(t)
	body := getConfig(t, s)
	for _, block := range []string{
		"server", "log", "capture", "catalog", "aliases", "policy",
	} {
		if _, ok := body.Blocks[block]; !ok {
			t.Errorf("block %q missing from the response", block)
		}
	}
	catalog, ok := body.Blocks["catalog"].(map[string]any)
	if !ok {
		t.Fatalf("catalog is %T, want an object", body.Blocks["catalog"])
	}
	if _, ok := catalog["discovery"]; !ok {
		t.Error("catalog.discovery missing; the settings screen reads it")
	}
}

// The Connect page reads server.public_url to decide whether to hand out a
// configured address or guess one from the page it was served on, so the block
// has to carry the key whether or not a value was set.
func TestConfigServesPublicURL(t *testing.T) {
	// The extra YAML is indented and lands directly after admin_listen, so it
	// continues the server mapping rather than opening a second one.
	s, _ := testServerFullWithConfig(t, func(c *config.Config) { c.Server.PublicURL = "https://api.example.com/dr" })
	server, ok := getConfig(t, s).Blocks["server"].(map[string]any)
	if !ok {
		t.Fatalf("server block is %T, want an object", getConfig(t, s).Blocks["server"])
	}
	if got := server["public_url"]; got != "https://api.example.com/dr" {
		t.Errorf("server.public_url = %v, want the configured address", got)
	}
}

func TestConfigServesAnEmptyPublicURLWhenUnset(t *testing.T) {
	s, _ := testServerFull(t)
	server := getConfig(t, s).Blocks["server"].(map[string]any)
	got, present := server["public_url"]
	if !present {
		t.Fatal("server.public_url missing; the Connect page cannot tell unset from a stale build")
	}
	if got != "" {
		t.Errorf("server.public_url = %v, want empty when nothing configured it", got)
	}
}

// Nothing in the process reads public_url, so a change to it must not be
// reported as needing a restart the way the listen addresses do.
func TestPublicURLIsHotReloadable(t *testing.T) {
	s, _ := testServerFull(t)
	f, ok := getConfig(t, s).Fields["server.public_url"]
	if !ok {
		t.Fatal("server.public_url missing from fields; the settings screen cannot show it")
	}
	if !f.HotReloadable {
		t.Error("server.public_url marked restart-only, but no listener depends on it")
	}
}

func TestConfigMarksRestartOnlyFieldsAsCold(t *testing.T) {
	s, _ := testServerFull(t)
	body := getConfig(t, s)
	for _, field := range []string{
		"server.proxy_listen",
		"policy.timeout.connect",
		"catalog.sync_interval",
		"catalog.discovery.interval",
	} {
		meta, ok := body.Fields[field]
		if !ok {
			t.Errorf("field %q is not annotated", field)
			continue
		}
		if meta.HotReloadable {
			t.Errorf("%q is restart-only but reports hot_reloadable", field)
		}
	}
	if meta, ok := body.Fields["log.retention"]; !ok || !meta.HotReloadable {
		t.Errorf("log.retention should be hot-reloadable, got %+v", meta)
	}
}

func TestConfigNamesTheSourceOfEachValue(t *testing.T) {
	s, _ := testServerFull(t)
	body := getConfig(t, s)

	// The listen addresses are read from the environment before the database
	// is open, so the settings screen cannot offer to change them.
	if got := body.Fields["server.proxy_listen"].Source; got != "environment" {
		t.Errorf("server.proxy_listen source = %q, want environment", got)
	}
	// Nothing stored it, so it is whatever applyDefaults chose -- reporting a
	// source the operator never chose would be a lie the console repeats.
	if got := body.Fields["capture.max_bytes"].Source; got != "default" {
		t.Errorf("capture.max_bytes source = %q, want default", got)
	}
	if got := body.Fields["aliases"].Source; got != "database" {
		t.Errorf("aliases source = %q, want database", got)
	}
}

// A stored value reported as a built-in default is worse than no annotation:
// the screen says "not set anywhere" about a row the loader read. Nothing but
// the database can answer which of the two a value is, because a stored row
// equal to the default parses identically to no row at all.
func TestConfigReportsAStoredKeyAsComingFromTheDatabase(t *testing.T) {
	s, db := testServerFullWithConfig(t, func(c *config.Config) {
		c.Log.Retention = 1000 * time.Hour
	})
	// The fixture wrote the row; assert that rather than trusting it, since
	// the whole test turns on the key really being stored.
	stored, err := store.StoredConfigKeys(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if !stored["log.retention"] {
		t.Fatal("the fixture did not store log.retention, so this proves nothing")
	}

	body := getConfig(t, s)
	if got := body.Fields["log.retention"].Source; got != "database" {
		t.Errorf("log.retention source = %q, want database; it is stored", got)
	}
	// Its neighbour in the same block is untouched, so the answer has to come
	// from the row rather than from the block the key sits in.
	if got := body.Fields["capture.retention"].Source; got != "default" {
		t.Errorf("capture.retention source = %q, want default; nothing stored it", got)
	}
}

func TestConfigNeverEchoesACredential(t *testing.T) {
	// Phase 7 §4.1: no endpoint returns credential material. proxy_token is a
	// shared secret and the config block is the obvious place to leak it.
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	w := do(t, s, cookie, token, "GET", "/api/config", "")
	if strings.Contains(w.Body.String(), "proxy_token") {
		t.Errorf("the config response names proxy_token:\n%s", w.Body.String())
	}
	if strings.Contains(w.Body.String(), "api_key") {
		t.Errorf("the config response names api_key:\n%s", w.Body.String())
	}
}

func TestPutConfigWritesAliasesAndTheyTakeEffect(t *testing.T) {
	s, db := testServerFull(t)
	cookie, token := login(t, s)
	seedProviderWithKey(t, s, cookie, token, "groq", "http://127.0.0.1:1")
	w := do(t, s, cookie, token, "PUT", "/api/config",
		`{"aliases":{"fast":["groq/llama"]}}`)
	if w.Code != 200 {
		t.Fatalf("PUT = %d: %s", w.Code, w.Body.String())
	}

	stored, err := db.Aliases(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(stored["fast"]) != 1 {
		t.Fatalf("aliases in the database = %v", stored)
	}
	// The point of the overlay: the next snapshot a request takes carries it.
	if got := s.deps.Config.Current().Aliases["fast"]; len(got) != 1 {
		t.Errorf("the live config does not carry the write: %v",
			s.deps.Config.Current().Aliases)
	}
}

func TestPutConfigRejectsAnUnknownProvider(t *testing.T) {
	// The same validation Load applies. Without it the database becomes a way
	// to store a configuration the file would have refused.
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	w := do(t, s, cookie, token, "PUT", "/api/config",
		`{"aliases":{"fast":["nosuch/model"]}}`)
	if w.Code != 400 {
		t.Fatalf("PUT = %d, want 400: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "nosuch") {
		t.Errorf("the error does not name the offending target: %s", w.Body.String())
	}
}

func TestPutConfigRefusesARestartOnlyField(t *testing.T) {
	// Refused, not accepted-with-a-warning. A file reload is an operator
	// editing a file the process watches; this is an API accepting a request
	// it can honour or cannot.
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	w := do(t, s, cookie, token, "PUT", "/api/config",
		`{"policy":{"timeout":{"connect":"5s"}}}`)
	if w.Code != 400 {
		t.Fatalf("PUT = %d, want 400: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "restart") {
		t.Errorf("the refusal does not say why: %s", w.Body.String())
	}
}

func TestPutConfigWritesAHotReloadablePolicyField(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	w := do(t, s, cookie, token, "PUT", "/api/config",
		`{"policy":{"retry":{"max_attempts":5}}}`)
	if w.Code != 200 {
		t.Fatalf("PUT = %d: %s", w.Code, w.Body.String())
	}
	if got := s.deps.Config.Current().Policy.Retry.MaxAttempts; got != 5 {
		t.Errorf("max_attempts = %d, want 5", got)
	}
}

func TestPutConfigNeedsASession(t *testing.T) {
	s, _ := testServerFull(t)
	r := httptest.NewRequest("PUT", "/api/config",
		strings.NewReader(`{"aliases":{}}`))
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatalf("unauthenticated PUT = %d, want 401", w.Code)
	}
}

func TestOverviewCarriesFailoverEdges(t *testing.T) {
	// The routing graph draws a return from the provider that refused to the
	// one that served. RecentFailovers names only where a request ended, so
	// without the pair the arcs cannot be drawn truthfully.
	s, db := testServerFull(t)
	seedFailover(t, db)

	cookie, token := login(t, s)
	w := do(t, s, cookie, token, "GET", "/api/overview", "")
	if w.Code != 200 {
		t.Fatalf("GET /api/overview = %d: %s", w.Code, w.Body.String())
	}
	var body struct {
		Edges []struct {
			From     string `json:"from_provider_id"`
			To       string `json:"to_provider_id"`
			Requests int64  `json:"requests"`
		} `json:"failover_edges"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Edges) != 1 {
		t.Fatalf("failover_edges = %+v, want one pair", body.Edges)
	}
	if body.Edges[0].From != "groq" || body.Edges[0].To != "together" {
		t.Errorf("edge = %+v, want groq -> together", body.Edges[0])
	}
}

func TestOverviewFailoverEdgesIsAnArray(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	w := do(t, s, cookie, token, "GET", "/api/overview", "")
	if !strings.Contains(w.Body.String(), `"failover_edges":[]`) {
		t.Errorf("an empty edge set did not serialize as []: %s", w.Body.String())
	}
}

// seedFailover writes one request that groq refused and together served.
func seedFailover(t *testing.T, db *store.DB) {
	t.Helper()
	storetest.WriteBatch(t, db, []*store.RequestRecord{{
		ID: "01FAILOVER", TS: time.Now(), Dialect: "openai", Surface: "llm",
		RequestedModel: "m", FinalProviderID: "together", FinalModel: "m",
		Status: "success",
		Attempts: []store.AttemptRecord{
			{Seq: 1, ProviderID: "groq", Model: "m", Outcome: "retryable_provider"},
			{Seq: 2, ProviderID: "together", Model: "m", Outcome: "success"},
		},
	}})
}
