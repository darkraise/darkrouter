package admin

import (
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkrouter/internal/catalog"
)

func TestAliasWritesAreVisibleThroughBothSurfaces(t *testing.T) {
	// Two write paths that can disagree is the failure worth testing for: the
	// focused endpoint and /api/config share one store method and one
	// validation, so a write through either must reach both the table and the
	// live snapshot.
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	seedProviderWithKey(t, s, cookie, token, "groq", "http://127.0.0.1:1")

	if w := do(t, s, cookie, token, "PUT", "/api/aliases",
		`{"fast":["groq/llama"]}`); w.Code != 200 {
		t.Fatalf("PUT /api/aliases = %d: %s", w.Code, w.Body.String())
	}

	w := do(t, s, cookie, token, "GET", "/api/aliases", "")
	var got map[string][]string
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got["fast"]) != 1 {
		t.Errorf("GET /api/aliases = %v", got)
	}
	if live := s.deps.Config.Current().Aliases["fast"]; len(live) != 1 {
		t.Errorf("the live config does not carry it: %v", s.deps.Config.Current().Aliases)
	}
}

// putIfMatch is do("PUT", "/api/aliases", ...) with an If-Match header, which
// do has no way to set.
func putIfMatch(t *testing.T, s *Server, cookie *http.Cookie, token, etag, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest("PUT", "/api/aliases", strings.NewReader(body))
	r.AddCookie(cookie)
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	r.Header.Set(csrfHeader, token)
	r.Header.Set("Content-Type", "application/json")
	if etag != "" {
		r.Header.Set("If-Match", etag)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}

// A save built against a table another admin has since changed must be
// refused rather than silently replacing that admin's edit: PUT sent no
// version check at all, so a stale in-memory copy of the alias map always
// won the race regardless of who wrote last.
func TestPutAliasesRejectsAStaleIfMatch(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	seedProviderWithKey(t, s, cookie, token, "groq", "http://127.0.0.1:1")

	get := do(t, s, cookie, token, "GET", "/api/aliases", "")
	etag := get.Header().Get("ETag")
	if etag == "" {
		t.Fatal("GET /api/aliases did not set an ETag")
	}

	// Another admin's edit, using the same starting ETag, lands first.
	if w := putIfMatch(t, s, cookie, token, etag, `{"fast":["groq/a"]}`); w.Code != 200 {
		t.Fatalf("first PUT = %d: %s", w.Code, w.Body.String())
	}

	// This save still carries the ETag from before that write.
	w := putIfMatch(t, s, cookie, token, etag, `{"fast":["groq/b"],"slow":["groq/c"]}`)
	if w.Code != 409 {
		t.Fatalf("PUT with a stale If-Match = %d, want 409: %s", w.Code, w.Body.String())
	}

	stored, err := s.deps.DB.Aliases(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(stored["fast"]) != 1 || stored["fast"][0] != "groq/a" {
		t.Errorf("the stale write overwrote the other admin's edit: %v", stored)
	}
	if _, ok := stored["slow"]; ok {
		t.Error("the stale write's new alias was committed anyway")
	}
}

// A compressing reverse proxy weakens the ETag it forwards, and the browser
// echoes the weakened form back. Refusing it would 409 every save made through
// such a proxy, forever, with no reload able to fix it.
func TestPutAliasesAcceptsAWeakIfMatch(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	seedProviderWithKey(t, s, cookie, token, "groq", "http://127.0.0.1:1")

	etag := do(t, s, cookie, token, "GET", "/api/aliases", "").Header().Get("ETag")
	if etag == "" {
		t.Fatal("GET /api/aliases did not set an ETag")
	}
	if w := putIfMatch(t, s, cookie, token, "W/"+etag, `{"fast":["groq/a"]}`); w.Code != 200 {
		t.Fatalf("PUT with a weak If-Match = %d, want 200: %s", w.Code, w.Body.String())
	}

	// Still a pin, not a bypass: the weak form of a stale ETag is stale too.
	if w := putIfMatch(t, s, cookie, token, "W/"+etag, `{"fast":["groq/b"]}`); w.Code != 409 {
		t.Fatalf("PUT with a stale weak If-Match = %d, want 409: %s", w.Code, w.Body.String())
	}
}

// If-Match is a list: a client holding more than one representation may send
// every ETag it has, and the save is pinned if any of them is current.
func TestPutAliasesAcceptsAnIfMatchList(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	seedProviderWithKey(t, s, cookie, token, "groq", "http://127.0.0.1:1")

	stale := do(t, s, cookie, token, "GET", "/api/aliases", "").Header().Get("ETag")
	if w := putIfMatch(t, s, cookie, token, stale, `{"fast":["groq/a"]}`); w.Code != 200 {
		t.Fatalf("first PUT = %d: %s", w.Code, w.Body.String())
	}
	current := do(t, s, cookie, token, "GET", "/api/aliases", "").Header().Get("ETag")

	if w := putIfMatch(t, s, cookie, token, stale+`, W/`+current, `{"fast":["groq/b"]}`); w.Code != 200 {
		t.Fatalf("PUT with a list naming the current ETag = %d, want 200: %s", w.Code, w.Body.String())
	}
	current = do(t, s, cookie, token, "GET", "/api/aliases", "").Header().Get("ETag")

	r := httptest.NewRequest("PUT", "/api/aliases", strings.NewReader(`{"fast":["groq/c"]}`))
	r.AddCookie(cookie)
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	r.Header.Set(csrfHeader, token)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Add("If-Match", stale)
	r.Header.Add("If-Match", current)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("PUT with the current ETag on a second If-Match line = %d, want 200: %s",
			w.Code, w.Body.String())
	}

	if w := putIfMatch(t, s, cookie, token, stale+`, `+current, `{"fast":["groq/d"]}`); w.Code != 409 {
		t.Fatalf("PUT with a list of stale ETags = %d, want 409: %s", w.Code, w.Body.String())
	}
}

// If-Match: * asks only that the resource exist, which the alias table always
// does, so it pins nothing.
func TestPutAliasesTreatsAStarIfMatchAsNoPin(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	seedProviderWithKey(t, s, cookie, token, "groq", "http://127.0.0.1:1")
	if w := putIfMatch(t, s, cookie, token, "*", `{"fast":["groq/a"]}`); w.Code != 200 {
		t.Fatalf("PUT with If-Match: * = %d, want 200: %s", w.Code, w.Body.String())
	}
}

// The grammar makes If-Match either * or a list of tags, never both. A header
// that mixes them still names a revision, and reading the * as "no pin" would
// let a stale save through.
func TestPutAliasesKeepsThePinWhenAStarIsMixedIntoAList(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	seedProviderWithKey(t, s, cookie, token, "groq", "http://127.0.0.1:1")

	stale := do(t, s, cookie, token, "GET", "/api/aliases", "").Header().Get("ETag")
	if w := putIfMatch(t, s, cookie, token, stale, `{"fast":["groq/a"]}`); w.Code != 200 {
		t.Fatalf("first PUT = %d: %s", w.Code, w.Body.String())
	}

	if w := putIfMatch(t, s, cookie, token, stale+`, *`, `{"fast":["groq/b"]}`); w.Code != 409 {
		t.Errorf("PUT with a stale ETag and * in one list = %d, want 409: %s", w.Code, w.Body.String())
	}

	r := httptest.NewRequest("PUT", "/api/aliases", strings.NewReader(`{"fast":["groq/c"]}`))
	r.AddCookie(cookie)
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	r.Header.Set(csrfHeader, token)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Add("If-Match", "*")
	r.Header.Add("If-Match", stale)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 409 {
		t.Errorf("PUT with * and a stale ETag on separate lines = %d, want 409: %s", w.Code, w.Body.String())
	}

	stored, err := s.deps.DB.Aliases(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if got := stored["fast"]; len(got) != 1 || got[0] != "groq/a" {
		t.Errorf("a stale save went through: %v", stored)
	}
}

// The ETag GET hands out has to be computed from the same table PUT checks
// it against. A save whose rows committed but whose republish failed leaves
// the stored table ahead of the live snapshot; an ETag taken from the snapshot
// then never matches, and every guarded save 409s until something else
// manages a reload. The stored table is written directly here to produce
// exactly that divergence.
func TestAliasesETagDescribesTheStoredTable(t *testing.T) {
	s, db := testServerFull(t)
	cookie, token := login(t, s)
	seedProviderWithKey(t, s, cookie, token, "groq", "http://127.0.0.1:1")
	if err := db.PutAliases(t.Context(), map[string][]string{"stored": {"groq/a"}}); err != nil {
		t.Fatal(err)
	}
	if _, live := s.deps.Config.Current().Aliases["stored"]; live {
		t.Fatal("setup: the live snapshot already carries the stored write")
	}

	get := do(t, s, cookie, token, "GET", "/api/aliases", "")
	var body map[string][]string
	if err := json.Unmarshal(get.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	// The body is what the ETag names, so it has to be the stored table too.
	if len(body["stored"]) != 1 {
		t.Errorf("GET /api/aliases = %v, want the stored table", body)
	}
	if w := putIfMatch(t, s, cookie, token, get.Header().Get("ETag"), `{"fast":["groq/b"]}`); w.Code != 200 {
		t.Fatalf("PUT with the ETag GET just served = %d, want 200: %s", w.Code, w.Body.String())
	}
}

// A save carrying no If-Match at all -- a caller that never read the ETag --
// keeps working exactly as before: the check is opt-in, not a new
// requirement every caller of this endpoint must satisfy.
func TestPutAliasesWithNoIfMatchStillWrites(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	seedProviderWithKey(t, s, cookie, token, "groq", "http://127.0.0.1:1")
	if w := do(t, s, cookie, token, "PUT", "/api/aliases", `{"fast":["groq/a"]}`); w.Code != 200 {
		t.Fatalf("PUT = %d: %s", w.Code, w.Body.String())
	}
}

func TestAliasWriteRejectsAnUnknownProvider(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	w := do(t, s, cookie, token, "PUT", "/api/aliases", `{"fast":["nosuch/m"]}`)
	if w.Code != 400 {
		t.Fatalf("PUT = %d, want 400: %s", w.Code, w.Body.String())
	}
}

// An empty map means delete every alias, where nil would mean leave the table
// alone. The two shapes are the difference between an operator clearing their
// last chain and a save that quietly changes nothing.
func TestPutAliasesWithAnEmptyMapDeletesEveryAlias(t *testing.T) {
	s, db := testServerFull(t)
	cookie, token := login(t, s)
	seedProviderWithKey(t, s, cookie, token, "groq", "http://127.0.0.1:1")
	if w := do(t, s, cookie, token, "PUT", "/api/aliases",
		`{"fast":["groq/llama"]}`); w.Code != 200 {
		t.Fatalf("PUT = %d: %s", w.Code, w.Body.String())
	}
	if w := do(t, s, cookie, token, "PUT", "/api/aliases", `{}`); w.Code != 200 {
		t.Fatalf("PUT = %d: %s", w.Code, w.Body.String())
	}
	stored, err := db.Aliases(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 0 {
		t.Errorf("aliases = %v, want none", stored)
	}
	if got := s.deps.Config.Current().Aliases; len(got) != 0 {
		t.Errorf("the live config still carries %v", got)
	}
}

// A null body is the one shape that reaches the handler's nil normalisation:
// encoding/json decodes {} to a non-nil empty map, so only null arrives as nil,
// and nil means "leave the alias table alone" by the time it reaches the store.
func TestPutAliasesWithANullBodyDeletesEveryAlias(t *testing.T) {
	s, db := testServerFull(t)
	cookie, token := login(t, s)
	seedProviderWithKey(t, s, cookie, token, "groq", "http://127.0.0.1:1")
	if w := do(t, s, cookie, token, "PUT", "/api/aliases",
		`{"fast":["groq/llama"]}`); w.Code != 200 {
		t.Fatalf("PUT = %d: %s", w.Code, w.Body.String())
	}
	if w := do(t, s, cookie, token, "PUT", "/api/aliases", `null`); w.Code != 200 {
		t.Fatalf("PUT = %d: %s", w.Code, w.Body.String())
	}
	stored, err := db.Aliases(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 0 {
		t.Errorf("aliases = %v, want none", stored)
	}
}

func TestPolicyWriteCarriesFirstByteAndCooldownMax(t *testing.T) {
	// first_byte and cooldown.max are the two keys no other test writes, and
	// a key policyPatch forgets is silently dropped rather than refused.
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	if w := do(t, s, cookie, token, "PUT", "/api/policy",
		`{"cooldown":{"max":"7m"},"timeout":{"first_byte":"30s"}}`); w.Code != 200 {
		t.Fatalf("PUT /api/policy = %d: %s", w.Code, w.Body.String())
	}
	p := s.deps.Config.Current().Policy
	if p.Cooldown.Max != 7*time.Minute {
		t.Errorf("cooldown.max = %s, want 7m", p.Cooldown.Max)
	}
	if p.Timeout.FirstByte != 30*time.Second {
		t.Errorf("timeout.first_byte = %s, want 30s", p.Timeout.FirstByte)
	}
}

func TestPolicyWriteTakesEffect(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	if w := do(t, s, cookie, token, "PUT", "/api/policy",
		`{"retry":{"max_attempts":6}}`); w.Code != 200 {
		t.Fatalf("PUT /api/policy = %d: %s", w.Code, w.Body.String())
	}
	if got := s.deps.Config.Current().Policy.Retry.MaxAttempts; got != 6 {
		t.Errorf("max_attempts = %d, want 6", got)
	}
}

func TestModelOverrideWriteReadDelete(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	seedProviderWithKey(t, s, cookie, token, "groq", "http://127.0.0.1:1")

	path := "/api/models/groq/m/override"
	if w := do(t, s, cookie, token, "PUT", path,
		`{"context_window":128000,"surfaces":["llm"]}`); w.Code != 200 {
		t.Fatalf("PUT %s = %d: %s", path, w.Code, w.Body.String())
	}

	w := do(t, s, cookie, token, "GET", path, "")
	if w.Code != 200 {
		t.Fatalf("GET %s = %d: %s", path, w.Code, w.Body.String())
	}
	var got overrideBody
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.ContextWindow == nil || *got.ContextWindow != 128000 {
		t.Errorf("context_window = %v", got.ContextWindow)
	}

	if w := do(t, s, cookie, token, "DELETE", path, ""); w.Code != 204 {
		t.Fatalf("DELETE %s = %d: %s", path, w.Code, w.Body.String())
	}
	if w := do(t, s, cookie, token, "GET", path, ""); w.Code != 200 || strings.TrimSpace(w.Body.String()) != "{}" {
		t.Errorf("GET after delete = %d %q, want 200 {}", w.Code, w.Body.String())
	}
}

func TestModelOverrideGetReportsTheCatalogCapabilitiesPerProvider(t *testing.T) {
	// The editor starts from these when no capabilities are overridden. The
	// stored override is three plain bools, so a baseline read from another
	// provider, or defaulted to false, would be written as a correction.
	s, _ := testServerFull(t)
	cat := &catalog.Store{}
	cat.Set(catalog.NewSnapshot([]catalog.Model{
		{ProviderID: "groq", ModelID: "m", State: catalog.StateLive,
			Capabilities: catalog.Capabilities{Tools: true, Reasoning: true, Known: true}},
		{ProviderID: "nebius", ModelID: "m", State: catalog.StateLive,
			Capabilities: catalog.Capabilities{Vision: true}},
	}, []string{"groq", "nebius"}))
	s.deps.Catalog = cat
	cookie, token := login(t, s)

	type view struct {
		Capabilities        map[string]bool `json:"capabilities"`
		CatalogCapabilities map[string]bool `json:"catalog_capabilities"`
	}
	get := func(path string) view {
		t.Helper()
		w := do(t, s, cookie, token, "GET", path, "")
		if w.Code != 200 {
			t.Fatalf("GET %s = %d: %s", path, w.Code, w.Body.String())
		}
		var v view
		if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
			t.Fatal(err)
		}
		return v
	}

	groq := get("/api/models/groq/m/override")
	if groq.Capabilities != nil {
		t.Errorf("capabilities = %v, want absent with no override", groq.Capabilities)
	}
	want := map[string]bool{"tools": true, "vision": false, "reasoning": true}
	if !maps.Equal(groq.CatalogCapabilities, want) {
		t.Errorf("groq catalog_capabilities = %v, want %v", groq.CatalogCapabilities, want)
	}
	want = map[string]bool{"tools": false, "vision": true, "reasoning": false}
	if got := get("/api/models/nebius/m/override").CatalogCapabilities; !maps.Equal(got, want) {
		t.Errorf("nebius catalog_capabilities = %v, want %v", got, want)
	}
	if got := get("/api/models/groq/unknown/override").CatalogCapabilities; got != nil {
		t.Errorf("a model the catalog lacks reports catalog_capabilities %v", got)
	}
}

func TestModelOverrideForAnUnknownProviderIs404(t *testing.T) {
	// model_overrides cascades on providers, so an orphan row would be
	// accepted here and vanish later with nothing to explain it.
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	w := do(t, s, cookie, token, "PUT", "/api/models/nosuch/m/override",
		`{"context_window":1}`)
	if w.Code != 404 {
		t.Fatalf("PUT = %d, want 404: %s", w.Code, w.Body.String())
	}
}

func TestModelOverrideAcceptsASlashInTheModelID(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	seedProviderWithKey(t, s, cookie, token, "groq", "http://127.0.0.1:1")
	// Publisher-prefixed ids reach the handler as one encoded segment.
	path := "/api/models/groq/publisher%2Fmodel-a/override"
	if w := do(t, s, cookie, token, "PUT", path, `{"context_window":4096}`); w.Code != 200 {
		t.Fatalf("PUT %s = %d: %s", path, w.Code, w.Body.String())
	}
	w := do(t, s, cookie, token, "GET", path, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "4096") {
		t.Fatalf("GET %s = %d: %s", path, w.Code, w.Body.String())
	}
	rows, err := s.deps.DB.ModelOverrides(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ModelID != "publisher/model-a" {
		t.Fatalf("stored overrides = %+v, want one for publisher/model-a", rows)
	}
}
