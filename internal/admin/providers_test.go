package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkrouter/internal/catalog"
	"github.com/darkraise/darkrouter/internal/store"
)

func TestAProviderCanBeCreatedListedAndDeleted(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)

	w := do(t, s, cookie, token, "POST", "/api/providers",
		`{"id":"p1","name":"P One","kind":"openaicompat",
		  "base_url":"https://x/v1","priority":7,"enabled":true}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", w.Code, w.Body.String())
	}

	w = do(t, s, cookie, token, "GET", "/api/providers", "")
	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d, body = %s", w.Code, w.Body.String())
	}
	var list struct {
		Providers []struct {
			ID       string `json:"id"`
			Priority int    `json:"priority"`
			Enabled  bool   `json:"enabled"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Providers) != 1 || list.Providers[0].ID != "p1" {
		t.Fatalf("providers = %+v", list.Providers)
	}

	w = do(t, s, cookie, token, "DELETE", "/api/providers/p1", "")
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, body = %s", w.Code, w.Body.String())
	}
	if w := do(t, s, cookie, token, "DELETE", "/api/providers/p1", ""); w.Code != http.StatusNotFound {
		t.Fatalf("second delete status = %d, want 404", w.Code)
	}
}

func TestCreatingAProviderWithADuplicateIDIsAConflict(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	body := `{"id":"p1","name":"P","kind":"openaicompat","base_url":"https://x/v1"}`

	if w := do(t, s, cookie, token, "POST", "/api/providers", body); w.Code != http.StatusCreated {
		t.Fatalf("first create = %d", w.Code)
	}
	w := do(t, s, cookie, token, "POST", "/api/providers", body)
	if w.Code != http.StatusConflict {
		t.Errorf("duplicate create = %d, want 409", w.Code)
	}
}

func TestCreatingAProviderFromAPresetFillsTheKindAndBaseURL(t *testing.T) {
	// Spec §4: create from preset OR raw kind+base_url. From a preset the
	// operator supplies an id and a key and nothing else, which is the whole
	// point of shipping presets.
	s, _ := testServerFull(t)
	cookie, token := login(t, s)

	w := do(t, s, cookie, token, "POST", "/api/providers", `{"id":"groq","preset":"groq"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	w = do(t, s, cookie, token, "GET", "/api/providers", "")
	var list struct {
		Providers []struct {
			Kind    string `json:"kind"`
			BaseURL string `json:"base_url"`
			Preset  string `json:"preset"`
		} `json:"providers"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	if len(list.Providers) != 1 {
		t.Fatalf("providers = %+v", list.Providers)
	}
	if list.Providers[0].Kind == "" || list.Providers[0].BaseURL == "" {
		t.Errorf("preset did not fill the row: %+v", list.Providers[0])
	}
	if list.Providers[0].Preset != "groq" {
		t.Errorf("preset = %q; the name must be recorded or nothing joins the catalog",
			list.Providers[0].Preset)
	}
}

func TestCreatingAProviderFromAnUnknownPresetIsRejected(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	w := do(t, s, cookie, token, "POST", "/api/providers",
		`{"id":"p1","preset":"not-a-real-preset"}`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestCreatingAProviderWithNeitherPresetNorKindIsRejected(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	w := do(t, s, cookie, token, "POST", "/api/providers", `{"id":"p1"}`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestEveryShippedEndpointShapeCanBeCreatedFromItsPreset(t *testing.T) {
	cases := map[string]string{
		// Region and project are endpoint properties: the URL is derived.
		"bedrock":          `{"id":"bedrock","preset":"bedrock","region":"us-east-1"}`,
		"vertex":           `{"id":"vertex","preset":"vertex","project":"p","location":"us-central1"}`,
		"vertex-anthropic": `{"id":"va","preset":"vertex-anthropic","project":"p","location":"us-east5"}`,
		// A local program served by its own transport.
		"auggie": `{"id":"auggie","preset":"auggie"}`,
		// The account identifier lands in the hostname.
		"snowflake": `{"id":"snowflake","preset":"snowflake"}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			s, _ := testServerFull(t)
			cookie, token := login(t, s)
			if w := do(t, s, cookie, token, "POST", "/api/providers", body); w.Code != http.StatusCreated {
				t.Fatalf("create: %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestAnAuggieProviderCanBePatched(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	// Written as an import leaves it, so the patch is the only thing on trial.
	if err := s.deps.DB.CreateProvider(context.Background(), store.ProviderRow{
		ID: "auggie", Name: "Auggie", Preset: "auggie", Kind: "openaicompat",
		BaseURL: "auggie://cli/v1", AuthStyle: "optional", Priority: 1, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	if w := do(t, s, cookie, token, "PATCH", "/api/providers/auggie", `{"priority":3}`); w.Code != http.StatusOK {
		t.Errorf("patch: %d %s", w.Code, w.Body.String())
	}
}

// A Vertex row written before project and location were required has no
// location. Turning it off or reordering it does not
// touch its endpoint, so the endpoint rule it predates must not refuse that.
func TestALegacyVertexProviderCanBePatchedOutsideItsEndpoint(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	if err := s.deps.DB.CreateProvider(context.Background(), store.ProviderRow{
		ID: "vx", Name: "Vertex", Preset: "vertex", Kind: "vertex",
		BaseURL: "https://us-central1-aiplatform.googleapis.com", AuthStyle: "gcp-sa",
		Priority: 1, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{`{"enabled":false}`, `{"priority":4}`, `{"name":"Old Vertex"}`} {
		if w := do(t, s, cookie, token, "PATCH", "/api/providers/vx", body); w.Code != http.StatusOK {
			t.Errorf("patch %s: %d %s", body, w.Code, w.Body.String())
		}
	}
	if w := do(t, s, cookie, token, "PATCH", "/api/providers/vx", `{"base_url":"nope"}`); w.Code != http.StatusBadRequest {
		t.Errorf("a patch to the endpoint itself is still checked: %d %s", w.Code, w.Body.String())
	}
}

func TestAPatchCannotPointAnEndpointAtAnotherHost(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	if err := s.deps.DB.CreateProvider(context.Background(), store.ProviderRow{
		ID: "vx", Name: "Vertex", Preset: "vertex", Kind: "vertex",
		AuthStyle: "gcp-sa", Project: "proj", Priority: 1, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{
		`{"id":"vy","preset":"vertex","project":"proj","location":"us-central1"}`,
		`{"id":"b","preset":"bedrock","region":"us-east-1"}`,
	} {
		if w := do(t, s, cookie, token, "POST", "/api/providers", body); w.Code != http.StatusCreated {
			t.Fatalf("create: %d %s", w.Code, w.Body.String())
		}
	}
	for _, tc := range []struct{ id, body string }{
		{"vx", `{"location":"evil.example/"}`},
		{"vy", `{"project":"proj#"}`},
		{"b", `{"region":"evil.example/"}`},
	} {
		if w := do(t, s, cookie, token, "PATCH", "/api/providers/"+tc.id, tc.body); w.Code != http.StatusBadRequest {
			t.Errorf("patch %s %s: status = %d, want 400: %s", tc.id, tc.body, w.Code, w.Body.String())
		}
	}
}

func TestALegacyVertexProviderCanBeGivenALocation(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	if err := s.deps.DB.CreateProvider(context.Background(), store.ProviderRow{
		ID: "vx", Name: "Vertex", Preset: "vertex", Kind: "vertex",
		BaseURL: "https://us-central1-aiplatform.googleapis.com", AuthStyle: "gcp-sa",
		Priority: 1, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	if w := do(t, s, cookie, token, "PATCH", "/api/providers/vx",
		`{"location":"us-central1"}`); w.Code != http.StatusBadRequest {
		t.Errorf("a location without a project is still no endpoint: %d %s", w.Code, w.Body.String())
	}
	if w := do(t, s, cookie, token, "PATCH", "/api/providers/vx",
		`{"project":"proj","location":"us-central1"}`); w.Code != http.StatusOK {
		t.Fatalf("patch: %d %s", w.Code, w.Body.String())
	}
	row, err := s.deps.DB.ProviderByID(context.Background(), "vx")
	if err != nil {
		t.Fatal(err)
	}
	if row.Project != "proj" || row.Location != "us-central1" {
		t.Errorf("project, location = %q, %q", row.Project, row.Location)
	}
	if w := do(t, s, cookie, token, "PATCH", "/api/providers/vx",
		`{"location":"us-central1"}`); w.Code != http.StatusOK {
		t.Errorf("restating the location: %d %s", w.Code, w.Body.String())
	}
}

func TestASetLocationCannotBeMoved(t *testing.T) {
	// Moving a location moves every catalogued model to another host, which
	// is a new provider rather than an edit to this one.
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	if w := do(t, s, cookie, token, "POST", "/api/providers",
		`{"id":"vx","preset":"vertex","project":"proj","location":"us-central1"}`); w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	if w := do(t, s, cookie, token, "PATCH", "/api/providers/vx",
		`{"location":"europe-west4"}`); w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400: %s", w.Code, w.Body.String())
	}
	row, err := s.deps.DB.ProviderByID(context.Background(), "vx")
	if err != nil {
		t.Fatal(err)
	}
	if row.Location != "us-central1" {
		t.Errorf("location = %q; a refused patch moved it", row.Location)
	}
}

func TestAnEndpointThatCannotBeReachedIsRejected(t *testing.T) {
	cases := map[string]string{
		"bedrock with no region":  `{"id":"b","preset":"bedrock"}`,
		"vertex with no project":  `{"id":"v","preset":"vertex","location":"us-central1"}`,
		"vertex with no location": `{"id":"v","preset":"vertex","project":"p"}`,
		"custom with no base url": `{"id":"c","kind":"openaicompat"}`,
		"an unserved scheme":      `{"id":"c","kind":"openaicompat","base_url":"ftp://x/v1"}`,
		"a broken templated host": `{"id":"c","kind":"openaicompat","base_url":"https://{account_id} x.example"}`,
		"an unknown placeholder":  `{"id":"c","kind":"openaicompat","base_url":"https://{tenant}.example"}`,
		// Vertex builds its host from the location and Bedrock from the
		// region, so either could send a credential to another host.
		"a vertex location naming a host": `{"id":"v","preset":"vertex","project":"p","location":"evil.example/"}`,
		"a vertex location with userinfo": `{"id":"v","preset":"vertex","project":"p","location":"x@evil.example"}`,
		"a vertex project with a slash":   `{"id":"v","preset":"vertex","project":"p/../q","location":"us-central1"}`,
		"a bedrock region naming a host":  `{"id":"b","preset":"bedrock","region":"evil.example/"}`,
		"a bedrock region with a dot":     `{"id":"b","preset":"bedrock","region":"us-east-1.evil.example"}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			s, _ := testServerFull(t)
			cookie, token := login(t, s)
			if w := do(t, s, cookie, token, "POST", "/api/providers", body); w.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400: %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestPatchingAProviderChangesOnlyWhatItNames(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	_ = do(t, s, cookie, token, "POST", "/api/providers",
		`{"id":"p1","name":"P","kind":"openaicompat","base_url":"https://x/v1",
		  "priority":7,"enabled":true}`)

	w := do(t, s, cookie, token, "PATCH", "/api/providers/p1", `{"enabled":false}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	w = do(t, s, cookie, token, "GET", "/api/providers", "")
	var list struct {
		Providers []struct {
			Priority int  `json:"priority"`
			Enabled  bool `json:"enabled"`
		} `json:"providers"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	if list.Providers[0].Enabled {
		t.Error("enabled did not change")
	}
	if list.Providers[0].Priority != 7 {
		t.Errorf("priority = %d; an unnamed field changed", list.Providers[0].Priority)
	}
}

func TestPatchingAnUnknownProviderIsANotFound(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	w := do(t, s, cookie, token, "PATCH", "/api/providers/nope", `{"enabled":false}`)
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestACredentialCanBeAddedAndDeleted(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	_ = do(t, s, cookie, token, "POST", "/api/providers",
		`{"id":"p1","name":"P","kind":"openaicompat","base_url":"https://x/v1"}`)

	w := do(t, s, cookie, token, "POST", "/api/providers/p1/keys",
		`{"label":"primary","secret":"sk-abcdef123456"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	if created.ID == "" {
		t.Fatal("the created credential has no id")
	}

	w = do(t, s, cookie, token, "GET", "/api/providers", "")
	var list struct {
		Providers []struct {
			Credentials []struct {
				ID     string `json:"id"`
				Label  string `json:"label"`
				Masked string `json:"masked"`
			} `json:"credentials"`
		} `json:"providers"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	creds := list.Providers[0].Credentials
	if len(creds) != 1 || creds[0].Label != "primary" {
		t.Fatalf("credentials = %+v", creds)
	}
	if !strings.HasSuffix(creds[0].Masked, "3456") || strings.Contains(creds[0].Masked, "abcdef") {
		t.Errorf("masked = %q; it must show a suffix and hide the rest", creds[0].Masked)
	}

	w = do(t, s, cookie, token, "DELETE", "/api/providers/p1/keys/"+created.ID, "")
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, body = %s", w.Code, w.Body.String())
	}
	if w := do(t, s, cookie, token, "DELETE", "/api/providers/p1/keys/"+created.ID, ""); w.Code != http.StatusNotFound {
		t.Fatalf("second delete status = %d, want 404", w.Code)
	}
}

func TestPresetsAreListedForTheCreateForm(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	w := do(t, s, cookie, token, "GET", "/api/presets", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var body struct {
		Presets []struct {
			ID       string   `json:"id"`
			Name     string   `json:"name"`
			Kind     string   `json:"kind"`
			BaseURL  string   `json:"base_url"`
			Surfaces []string `json:"surfaces"`
		} `json:"presets"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Presets) < 100 {
		t.Fatalf("got %d presets; the shipped catalog holds ~197", len(body.Presets))
	}
	var groq bool
	for _, p := range body.Presets {
		if p.ID == "groq" {
			groq = true
			if p.Kind == "" || p.BaseURL == "" || len(p.Surfaces) == 0 {
				t.Errorf("groq preset is incomplete: %+v", p)
			}
		}
	}
	if !groq {
		t.Error("groq is not in the preset list")
	}
}

// A provider's priority is the order routing attempts candidates in, and that
// order is baked into the catalog snapshot by catalog.Store.Rebuild. Reloading
// the provider source alone leaves the snapshot holding the old order, so the
// operator's change does not reach routing until some unrelated worker rebuilds
// — up to a full discovery interval later.
func TestPriorityChangeReachesRoutingImmediately(t *testing.T) {
	s, db := testServerFull(t)
	cookie, token := login(t, s)
	ctx := context.Background()

	cat := catalog.NewStore(db, s.deps.Src)
	s.deps.Catalog = cat

	seedProviderWithKey(t, s, cookie, token, "low", "http://low.invalid")
	seedProviderWithKey(t, s, cookie, token, "high", "http://high.invalid")
	for _, id := range []string{"low", "high"} {
		if err := db.RecordDiscoverySuccess(ctx, id,
			[]store.DiscoveredModel{{ModelID: "m", ContextWindow: 1000, MaxOutputTokens: 100}}, nil, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if err := cat.Rebuild(ctx); err != nil {
		t.Fatal(err)
	}

	// Equal priority ties break on id, so "high" leads before any change.
	if got := cat.Snapshot().Offering("m"); len(got) != 2 || got[0] != "high" {
		t.Fatalf("initial offering = %v, want high first", got)
	}

	if w := do(t, s, cookie, token, "PATCH", "/api/providers/low", `{"priority":50}`); w.Code != http.StatusOK {
		t.Fatalf("patch priority: %d %s", w.Code, w.Body.String())
	}

	if got := cat.Snapshot().Offering("m"); len(got) != 2 || got[0] != "low" {
		t.Errorf("offering after raising low's priority = %v, want low first: "+
			"the write reloaded the provider source but never rebuilt the catalog", got)
	}
}

func TestACredentialViewCarriesOAuthMetadata(t *testing.T) {
	s, db := testServerFull(t)
	cookie, token := login(t, s)
	seedProviderWithKey(t, s, cookie, token, "oa", "https://oa.example")

	// Written straight to the store: the create endpoint mints static keys,
	// and the shape under test is the one the refresh worker writes.
	expiry := int64(1790000000)
	if _, err := db.AddCredential(context.Background(), s.deps.Key, store.Credential{
		ProviderID: "oa", Label: "subscription", Kind: "oauth",
		Secret: "refresh-token-value", Scope: "read", Enabled: true,
		ExpiresAt: &expiry,
	}); err != nil {
		t.Fatal(err)
	}

	w := do(t, s, cookie, token, "GET", "/api/providers", "")
	var out struct {
		Providers []struct {
			ID          string `json:"id"`
			Credentials []struct {
				Kind      string `json:"kind"`
				Scope     string `json:"scope"`
				ExpiresAt *int64 `json:"expires_at"`
				Masked    string `json:"masked"`
			} `json:"credentials"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	var oauth *struct {
		Kind      string `json:"kind"`
		Scope     string `json:"scope"`
		ExpiresAt *int64 `json:"expires_at"`
		Masked    string `json:"masked"`
	}
	for i := range out.Providers {
		if out.Providers[i].ID != "oa" {
			continue
		}
		for j := range out.Providers[i].Credentials {
			if out.Providers[i].Credentials[j].Kind == "oauth" {
				oauth = &out.Providers[i].Credentials[j]
			}
		}
	}
	if oauth == nil {
		t.Fatalf("no oauth credential in the view: %s", w.Body.String())
	}
	if oauth.ExpiresAt == nil || *oauth.ExpiresAt != expiry {
		t.Fatalf("expiry missing: %+v", oauth)
	}
	if oauth.Scope != "read" {
		t.Fatalf("scope = %q", oauth.Scope)
	}
	if strings.Contains(oauth.Masked, "refresh-token") {
		t.Fatal("the secret leaked into the masked field")
	}
}

func TestAStaticKeyOmitsOAuthOnlyFields(t *testing.T) {
	// Omitted rather than zeroed: an expiry of 0 on a static key reads as
	// "expired in 1970", which is a different claim from "has no expiry".
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	seedProviderWithKey(t, s, cookie, token, "st", "https://st.example")

	w := do(t, s, cookie, token, "GET", "/api/providers", "")
	if strings.Contains(w.Body.String(), `"expires_at"`) {
		t.Fatalf("a static key should carry no expires_at: %s", w.Body.String())
	}

	// A whole-body substring check for "kind" would pass on providerView's own
	// top-level kind field regardless of the credential; scoped to the
	// credential's own JSON is what actually exercises credentialView.Kind.
	var out struct {
		Providers []struct {
			ID          string `json:"id"`
			Credentials []struct {
				Kind string `json:"kind"`
			} `json:"credentials"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, p := range out.Providers {
		if p.ID != "st" {
			continue
		}
		for _, c := range p.Credentials {
			found = true
			if c.Kind == "" {
				t.Fatalf("credential kind should always be present: %s", w.Body.String())
			}
		}
	}
	if !found {
		t.Fatalf("no credential in the view: %s", w.Body.String())
	}
}

// spyTrigger records the providers a handler asked to have swept.
type spyTrigger struct{ swept []string }

func (s *spyTrigger) Trigger(providerID string) { s.swept = append(s.swept, providerID) }

// withSpyTrigger swaps the server's discoverer for one that only records, so a
// test can see the side effect the handler is supposed to have.
func withSpyTrigger(s *Server) *spyTrigger {
	spy := &spyTrigger{}
	s.deps.Disc = spy
	return spy
}

func TestFirstCredentialTriggersDiscovery(t *testing.T) {
	// A provider with no credential cannot be swept at all: the discoverer
	// needs one of the provider's own keys to ask what it serves. The first
	// key is therefore the moment its models become discoverable, and waiting
	// a quarter of an hour to find that out is the whole complaint.
	s, db := testServerFull(t)
	ctx := context.Background()
	if err := db.CreateProvider(ctx, store.ProviderRow{
		ID: "groq", Kind: "openaicompat", BaseURL: "https://x.example",
	}); err != nil {
		t.Fatal(err)
	}
	spy := withSpyTrigger(s)
	cookie, token := login(t, s)

	res := do(t, s, cookie, token, "POST", "/api/providers/groq/keys",
		`{"label":"one","secret":"sk-aaa"}`)
	if res.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", res.Code, res.Body.String())
	}
	if len(spy.swept) != 1 || spy.swept[0] != "groq" {
		t.Errorf("swept %v, want one sweep of groq", spy.swept)
	}
}

func TestLaterCredentialsDoNotResweep(t *testing.T) {
	// A bulk import of twenty keys must not ask the provider to list its
	// models twenty times, against the rate limit the operator is on a free
	// tier to respect. The second key does not change what a provider lists.
	s, db := testServerFull(t)
	ctx := context.Background()
	if err := db.CreateProvider(ctx, store.ProviderRow{
		ID: "groq", Kind: "openaicompat", BaseURL: "https://x.example",
	}); err != nil {
		t.Fatal(err)
	}
	spy := withSpyTrigger(s)
	cookie, token := login(t, s)

	for _, secret := range []string{"sk-aaa", "sk-bbb", "sk-ccc"} {
		res := do(t, s, cookie, token, "POST", "/api/providers/groq/keys",
			`{"label":"k","secret":"`+secret+`"}`)
		if res.Code != http.StatusCreated {
			t.Fatalf("status = %d: %s", res.Code, res.Body.String())
		}
	}
	if len(spy.swept) != 1 {
		t.Errorf("swept %v, want exactly one sweep for three keys", spy.swept)
	}
}

// The opt-in has to reach the operator both ways: readable on the provider and
// settable without a second call. A field that serialized but ignored its patch
// would look like it worked.
func TestProviderAPICarriesTheUnsanctionedOptIn(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	if w := do(t, s, cookie, token, "POST", "/api/providers",
		`{"id":"p1","name":"P","kind":"openaicompat","base_url":"https://x/v1"}`); w.Code != http.StatusCreated {
		t.Fatalf("seed status = %d, body = %s", w.Code, w.Body.String())
	}

	// A patch naming only this field is a patch that names a field: the
	// emptiness guard has to know about it or the opt-in is unsettable alone.
	w := do(t, s, cookie, token, "PATCH", "/api/providers/p1",
		`{"allow_unsanctioned_free":true}`)
	if w.Code != http.StatusOK {
		t.Fatalf("patch status = %d, body = %s", w.Code, w.Body.String())
	}
	w = do(t, s, cookie, token, "GET", "/api/providers", "")
	if !strings.Contains(w.Body.String(), `"allow_unsanctioned_free":true`) {
		t.Errorf("opt-in absent from the provider response: %s", w.Body.String())
	}
}

func TestCreatingAProviderTakesTheUnsanctionedOptIn(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	if w := do(t, s, cookie, token, "POST", "/api/providers",
		`{"id":"p1","name":"P","kind":"openaicompat","base_url":"https://x/v1",
		  "allow_unsanctioned_free":true}`); w.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", w.Code, w.Body.String())
	}
	w := do(t, s, cookie, token, "GET", "/api/providers", "")
	if !strings.Contains(w.Body.String(), `"allow_unsanctioned_free":true`) {
		t.Errorf("opt-in not taken at creation: %s", w.Body.String())
	}
}
