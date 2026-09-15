package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkrouter/internal/config"
	"github.com/darkraise/darkrouter/internal/store"
	"github.com/darkraise/darkrouter/internal/store/storetest"
)

// configBody is the shape GET /api/config returns: a flat map of every
// registry key, each value annotated with where it came from, whether changing
// it does anything, and what it holds.
type configBody struct {
	Valid    bool                 `json:"valid"`
	Warnings []string             `json:"warnings"`
	Values   map[string]string    `json:"values"`
	Fields   map[string]fieldMeta `json:"fields"`

	PendingRestart []string `json:"pending_restart"`
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

// The registry is the allowlist. A key it carries reaches the screen with no
// second edit, which is the whole reason the hand-written block tree went:
// nine catalogue keys never reached the console because nobody added them
// twice.
func TestConfigServesEveryRegistryKey(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, _ := login(t, s)
	var body struct {
		Values map[string]string `json:"values"`
		Fields map[string]struct {
			Source        string `json:"source"`
			HotReloadable bool   `json:"hot_reloadable"`
			Kind          string `json:"kind"`
			Env           string `json:"env"`
		} `json:"fields"`
	}
	w := do(t, s, cookie, "", "GET", "/api/config", "")
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	for _, key := range store.ConfigKeys() {
		if _, ok := body.Values[key]; !ok {
			t.Errorf("values is missing %s", key)
		}
		f, ok := body.Fields[key]
		if !ok {
			t.Errorf("fields is missing %s", key)
			continue
		}
		if f.Kind == "" {
			t.Errorf("%s carries no kind", key)
		}
	}
	// The keys that were invisible before this change.
	for _, key := range []string{
		"catalog.free_catalog_url", "catalog.litellm_sync",
		"catalog.seed_free_providers", "catalog.discovery.timeout",
		"catalog.discovery.concurrency",
	} {
		if _, ok := body.Values[key]; !ok {
			t.Errorf("%s is still not served", key)
		}
	}
}

// The bootstrap keys are shown so an operator can see what the gateway is
// listening on, and named with their variable so it is obvious why the screen
// will not change them.
func TestConfigNamesTheVariableThatOwnsABootstrapKey(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, _ := login(t, s)
	var body struct {
		Values map[string]string `json:"values"`
		Fields map[string]struct {
			Source string `json:"source"`
			Env    string `json:"env"`
		} `json:"fields"`
	}
	w := do(t, s, cookie, "", "GET", "/api/config", "")
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	listen, ok := body.Fields["server.proxy_listen"]
	if !ok {
		t.Fatal("server.proxy_listen is not served")
	}
	if listen.Source != "env" {
		t.Errorf("source = %q, want env", listen.Source)
	}
	if listen.Env != "DARKROUTER_PROXY_LISTEN" {
		t.Errorf("env = %q, want the variable name", listen.Env)
	}
	// A stored key carries no variable, and a client must not print one.
	if stored := body.Fields["policy.timeout.total"]; stored.Env != "" {
		t.Errorf("policy.timeout.total names %q as its variable", stored.Env)
	}
	// The row needs a value as well as a badge; metadata alone renders an
	// empty box on the settings screen.
	if body.Values["server.proxy_listen"] == "" {
		t.Error("server.proxy_listen carries no value")
	}
	if body.Values["server.admin_listen"] == "" {
		t.Error("server.admin_listen carries no value")
	}
}

// The registry is the allowlist, so the one key that must never be echoed is
// excluded by construction rather than by remembering to leave it out.
func TestConfigNeverServesTheProxyToken(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, _ := login(t, s)
	w := do(t, s, cookie, "", "GET", "/api/config", "")
	// The fixture's bootstrap really does put this token in the config, so a
	// handler that serialised the whole struct would print it here.
	if strings.Contains(w.Body.String(), "fixture-proxy-token") {
		t.Errorf("the response carries the token: %s", w.Body.String())
	}
	if strings.Contains(w.Body.String(), "proxy_token") {
		t.Errorf("the response names the token key: %s", w.Body.String())
	}
}

// A configured value has to survive the whole path -- stored as a row, read
// back by the loader, serialised by the registry -- and the defaults the test
// above reads would look identical if none of that ran.
func TestConfigServesAConfiguredValue(t *testing.T) {
	s, _ := testServerFullWithConfig(t, func(c *config.Config) {
		c.Server.PublicURL = "https://llm.example.test"
	})
	cookie, _ := login(t, s)
	var body struct {
		Values map[string]string `json:"values"`
	}
	w := do(t, s, cookie, "", "GET", "/api/config", "")
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if got := body.Values["server.public_url"]; got != "https://llm.example.test" {
		t.Errorf("public_url = %q, want the configured value", got)
	}
}

// The values are the registry's own serialisation, which is what the write
// path parses back. A screen that displayed one spelling and submitted another
// would round-trip wrong on every save.
func TestConfigServesValuesInTheirStoredSpelling(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, _ := login(t, s)
	var body struct {
		Values map[string]string `json:"values"`
	}
	w := do(t, s, cookie, "", "GET", "/api/config", "")
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if got := body.Values["policy.timeout.total"]; got != "10m0s" {
		t.Errorf("total = %q, want the Go duration spelling", got)
	}
	if got := body.Values["server.max_body_bytes"]; got != "33554432" {
		t.Errorf("max_body_bytes = %q, want the decimal count", got)
	}
	if got := body.Values["capture.bodies"]; got != "false" {
		t.Errorf("capture.bodies = %q, want a parseable bool", got)
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

func TestPutConfigWritesASettingAndItTakesEffect(t *testing.T) {
	s, db := testServerFull(t)
	cookie, token := login(t, s)
	w := do(t, s, cookie, token, "PUT", "/api/config",
		`{"set":{"log.retention":"96h"}}`)
	if w.Code != 200 {
		t.Fatalf("PUT = %d: %s", w.Code, w.Body.String())
	}
	if got := s.deps.Config.Current().Log.Retention; got != 96*time.Hour {
		t.Errorf("log.retention = %s, want 96h", got)
	}
	stored, err := store.StoredConfigKeys(t.Context(), db)
	if err != nil {
		t.Fatal(err)
	}
	if !stored["log.retention"] {
		t.Error("the row is not in the database")
	}
}

// Accepted, and the answer says so. The value belongs in the database whether
// or not this process can apply it; refusing it would leave the operator no
// way to set it at all.
func TestPutConfigAcceptsARestartOnlyFieldAndNamesIt(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	w := do(t, s, cookie, token, "PUT", "/api/config",
		`{"set":{"policy.timeout.connect":"5s"}}`)
	if w.Code != 200 {
		t.Fatalf("PUT = %d: %s", w.Code, w.Body.String())
	}
	var body struct {
		Valid           bool     `json:"valid"`
		RestartRequired []string `json:"restart_required"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.Valid {
		t.Fatalf("valid = false: %s", w.Body.String())
	}
	if len(body.RestartRequired) != 1 || body.RestartRequired[0] != "policy.timeout.connect" {
		t.Errorf("restart_required = %v", body.RestartRequired)
	}
}

// Never null: a client cannot tell a JSON null from a field an older build did
// not serve.
func TestPutConfigRestartRequiredIsAlwaysAnArray(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	w := do(t, s, cookie, token, "PUT", "/api/config",
		`{"set":{"log.retention":"96h"}}`)
	if !strings.Contains(w.Body.String(), `"restart_required":[]`) {
		t.Errorf("body = %s", w.Body.String())
	}
}

func TestPutConfigResetsAKeyToItsDefault(t *testing.T) {
	s, db := testServerFull(t)
	cookie, token := login(t, s)
	if w := do(t, s, cookie, token, "PUT", "/api/config",
		`{"set":{"log.retention":"96h"}}`); w.Code != 200 {
		t.Fatalf("PUT = %d: %s", w.Code, w.Body.String())
	}
	if w := do(t, s, cookie, token, "PUT", "/api/config",
		`{"reset":["log.retention"]}`); w.Code != 200 {
		t.Fatalf("PUT = %d: %s", w.Code, w.Body.String())
	}
	stored, err := store.StoredConfigKeys(t.Context(), db)
	if err != nil {
		t.Fatal(err)
	}
	if stored["log.retention"] {
		t.Error("the row survived a reset")
	}
	if got := s.deps.Config.Current().Log.Retention; got != 720*time.Hour {
		t.Errorf("log.retention = %s, want the compiled default", got)
	}
}

func TestPutConfigRefusesABootstrapKey(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	w := do(t, s, cookie, token, "PUT", "/api/config",
		`{"set":{"server.proxy_token":"sekrit"}}`)
	if w.Code != 400 {
		t.Fatalf("PUT = %d, want 400: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "DARKROUTER_PROXY_TOKEN") {
		t.Errorf("the refusal does not name the variable: %s", w.Body.String())
	}
}

func TestPutConfigRefusesAValueTheLoaderWouldReject(t *testing.T) {
	s, db := testServerFull(t)
	cookie, token := login(t, s)
	w := do(t, s, cookie, token, "PUT", "/api/config",
		`{"set":{"log.retention":"1h"}}`)
	if w.Code != 400 {
		t.Fatalf("PUT = %d, want 400: %s", w.Code, w.Body.String())
	}
	stored, err := store.StoredConfigKeys(t.Context(), db)
	if err != nil {
		t.Fatal(err)
	}
	if stored["log.retention"] {
		t.Error("a refused write left a row behind")
	}
}

// The one endpoint that must never echo credential material, on the path that
// now accepts writes for everything else.
func TestPutConfigNeverEchoesTheProxyToken(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	w := do(t, s, cookie, token, "PUT", "/api/config",
		`{"set":{"server.proxy_token":"sekrit"}}`)
	if strings.Contains(w.Body.String(), "sekrit") {
		t.Errorf("the response echoed the value: %s", w.Body.String())
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

// ReconcileConfig deletes a policy row equal to the compiled default so an
// upgraded install stops claiming the operator picked it. Reporting the whole
// policy block as database-owned would undo that on the read side: the console
// would name a chosen value behind every one of the seven keys.
func TestPolicySourceFollowsWhetherARowIsStored(t *testing.T) {
	s, _ := testServerFull(t)
	if got := getConfig(t, s).Fields["policy.timeout.connect"].Source; got != "default" {
		t.Errorf("policy.timeout.connect source = %q with no row stored, want default", got)
	}

	stored, db := testServerFullWithConfig(t, func(c *config.Config) {
		c.Policy.Timeout.Connect = 3 * time.Second
	})
	keys, err := store.StoredConfigKeys(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if !keys["policy.timeout.connect"] {
		t.Fatal("the fixture did not store policy.timeout.connect, so this proves nothing")
	}
	if got := getConfig(t, stored).Fields["policy.timeout.connect"].Source; got != "database" {
		t.Errorf("policy.timeout.connect source = %q with a row stored, want database", got)
	}
}

// /healthz and this endpoint answer the same question, and the settings banner
// reads this one. A reverted key left valid here told an operator their
// configuration was fine while the process ran a default they never chose.
func TestConfigIsNotValidWhenAKeyWasReverted(t *testing.T) {
	s, _ := testServerFullWithConfig(t, func(c *config.Config) { c.Capture.MaxBytes = -1 })
	body := getConfig(t, s)
	if body.Valid {
		t.Errorf("valid = true with a reverted key; warnings %v", body.Warnings)
	}
}

// Pending-restart is boot versus current. The consecutive-reload diff that
// lands in warnings is cleared by the next unrelated save, which leaves an
// operator running an old value with nothing on screen saying so.
func TestConfigReportsPendingRestartAcrossAnUnrelatedReload(t *testing.T) {
	s, db := testServerFull(t)
	if got := getConfig(t, s).PendingRestart; len(got) != 0 {
		t.Fatalf("pending_restart = %v at boot, want none", got)
	}

	storeSetting(t, db, "policy.timeout.connect", "3s")
	if err := s.deps.Config.Reload(); err != nil {
		t.Fatal(err)
	}
	if got := getConfig(t, s).PendingRestart; !slices.Contains(got, "policy.timeout.connect") {
		t.Fatalf("pending_restart = %v after a restart-only change, want policy.timeout.connect", got)
	}

	storeSetting(t, db, "log.retention", "720h")
	if err := s.deps.Config.Reload(); err != nil {
		t.Fatal(err)
	}
	if got := getConfig(t, s).PendingRestart; !slices.Contains(got, "policy.timeout.connect") {
		t.Fatalf("pending_restart = %v after an unrelated save, want the notice still standing", got)
	}
}

func storeSetting(t *testing.T, db *store.DB, key, value string) {
	t.Helper()
	if _, err := db.Write.ExecContext(context.Background(),
		`INSERT OR REPLACE INTO settings (key, value) VALUES (?, ?)`, key, value); err != nil {
		t.Fatal(err)
	}
}

// A committed write the gateway could not load is identified by
// routing_updated:false on every endpoint, so a client needs one check to know
// the change is stored but not serving.
func TestACommittedConfigWriteThatDidNotLoadSaysRoutingWasNotUpdated(t *testing.T) {
	for _, tc := range []struct{ path, body string }{
		{"/api/config", `{"set":{}}`},
		{"/api/aliases", `{}`},
		{"/api/policy", `{}`},
	} {
		t.Run(tc.path, func(t *testing.T) {
			s, _ := testServerFull(t)
			cookie, token := login(t, s)
			loads := 0
			cfg, err := config.NewStoreFrom(func() (*config.Config, error) {
				loads++
				if loads > 1 {
					return nil, errors.New("boom")
				}
				return s.deps.Config.Current(), nil
			})
			if err != nil {
				t.Fatal(err)
			}
			cfg.SetWriter(func(context.Context, config.Patch) ([]string, error) { return nil, nil })
			s.deps.Config = cfg

			w := do(t, s, cookie, token, "PUT", tc.path, tc.body)
			var body struct {
				Valid          *bool `json:"valid"`
				RoutingUpdated *bool `json:"routing_updated"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if w.Code != 200 || body.Valid == nil || *body.Valid {
				t.Fatalf("status = %d, body = %s; want 200 valid:false", w.Code, w.Body.String())
			}
			if body.RoutingUpdated == nil || *body.RoutingUpdated {
				t.Errorf("body = %s; want routing_updated:false", w.Body.String())
			}
		})
	}
}
