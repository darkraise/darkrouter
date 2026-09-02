package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkrouter/internal/store"
	"github.com/darkraise/darkrouter/internal/store/storetest"
)

func TestTraceKeyLabelIsTheCredentialLabel(t *testing.T) {
	s, db := testServerFull(t)
	ctx := context.Background()
	if err := db.CreateProvider(ctx, store.ProviderRow{ID: "b", Kind: "openaicompat", BaseURL: "https://x", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.AddCredential(ctx, s.deps.Key, store.Credential{ID: "k2", ProviderID: "b", Label: "primary", Secret: "s", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	storetest.SeedFailoverTrace(t, db, "01LABEL")
	cookie, token := login(t, s)
	w := do(t, s, cookie, token, "GET", "/api/requests/01LABEL", "")
	var body struct {
		Source   string `json:"source"`
		Path     string `json:"path"`
		Attempts []struct {
			KeyLabel string `json:"key_label"`
		} `json:"attempts"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Attempts[1].KeyLabel != "primary" {
		t.Errorf("attempt 2 key_label = %q, want primary", body.Attempts[1].KeyLabel)
	}
	if body.Attempts[0].KeyLabel != "k1" {
		t.Errorf("attempt 1 with a deleted key = %q, want the id", body.Attempts[0].KeyLabel)
	}
	if body.Source != "proxy" {
		t.Errorf("source = %q", body.Source)
	}
	if !strings.Contains(w.Body.String(), `"path":`) {
		t.Error("the trace carries no path")
	}
}

func TestListRowsCarryReasoningTokens(t *testing.T) {
	s, db := testServerFull(t)
	storetest.SeedFailoverTrace(t, db, "01REASON")
	cookie, token := login(t, s)
	w := do(t, s, cookie, token, "GET", "/api/requests", "")
	if !strings.Contains(w.Body.String(), `"reasoning_tokens":12`) {
		t.Errorf("list body lacks reasoning_tokens: %s", w.Body.String())
	}
}

func TestNextCursorAppearsOnlyOnAFullPage(t *testing.T) {
	s, db := testServerFull(t)
	rows := []*store.RequestRecord{}
	for i := 0; i < 3; i++ {
		rows = append(rows, &store.RequestRecord{
			ID: "01PAGE" + string(rune('A'+i)), TS: time.UnixMilli(int64(1700000000000 + i)),
			Dialect: "openai", Surface: "llm", RequestedModel: "m", Status: "success",
		})
	}
	storetest.WriteBatch(t, db, rows)
	cookie, token := login(t, s)
	full := do(t, s, cookie, token, "GET", "/api/requests?limit=2", "").Body.String()
	if !strings.Contains(full, `"next_cursor"`) {
		t.Errorf("a full page carries no cursor: %s", full)
	}
	short := do(t, s, cookie, token, "GET", "/api/requests?limit=5", "").Body.String()
	if strings.Contains(short, `"next_cursor"`) {
		t.Errorf("a short page carries a cursor: %s", short)
	}
}

func TestOverrideGetOmitsUnsetFields(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	_ = do(t, s, cookie, token, "POST", "/api/providers",
		`{"id":"p1","name":"P","kind":"openaicompat","base_url":"https://x/v1"}`)
	if w := do(t, s, cookie, token, "PUT", "/api/models/p1/m/override", `{"context_window":4096}`); w.Code != http.StatusOK {
		t.Fatalf("put = %d %s", w.Code, w.Body.String())
	}
	got := do(t, s, cookie, token, "GET", "/api/models/p1/m/override", "").Body.String()
	if strings.Contains(got, `"surfaces"`) || strings.Contains(got, `"capabilities"`) {
		t.Errorf("unset fields are present: %s", got)
	}
	if !strings.Contains(got, `"context_window":4096`) {
		t.Errorf("the set field is missing: %s", got)
	}
	if w := do(t, s, cookie, token, "PUT", "/api/models/p1/m/override", `{"surfaces":["telepathy"]}`); w.Code != http.StatusBadRequest {
		t.Errorf("an unknown surface = %d, want 400", w.Code)
	}
}

func TestPatchesReturnTheUpdatedResource(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	keyID := seedProviderWithKey(t, s, cookie, token, "p1", "https://x/v1")

	w := do(t, s, cookie, token, "PATCH", "/api/providers/p1", `{"name":"Renamed","priority":7}`)
	var prov providerView
	if err := json.Unmarshal(w.Body.Bytes(), &prov); err != nil || prov.Name != "Renamed" || prov.Priority != 7 || len(prov.Credentials) != 1 {
		t.Errorf("provider patch = %d %s (%v)", w.Code, w.Body.String(), err)
	}

	w = do(t, s, cookie, token, "PATCH", "/api/providers/p1/keys/"+keyID, `{"enabled":false}`)
	var cred credentialView
	if err := json.Unmarshal(w.Body.Bytes(), &cred); err != nil || cred.ID != keyID || cred.Enabled || cred.Masked == "" {
		t.Errorf("credential patch = %d %s", w.Code, w.Body.String())
	}

	w = do(t, s, cookie, token, "POST", "/api/playground/presets",
		`{"name":"n","dialect":"openai","model":"m","config":{}}`)
	var preset playgroundPresetView
	_ = json.Unmarshal(w.Body.Bytes(), &preset)
	w = do(t, s, cookie, token, "PATCH", "/api/playground/presets/"+preset.ID,
		`{"name":"n2","dialect":"openai","model":"m2","config":{"a":1}}`)
	if err := json.Unmarshal(w.Body.Bytes(), &preset); err != nil || preset.Name != "n2" || preset.Model != "m2" {
		t.Errorf("preset patch = %d %s", w.Code, w.Body.String())
	}

	w = do(t, s, cookie, token, "POST", "/api/playground/conversations",
		`{"title":"t","dialect":"openai","model":"m","config":{}}`)
	var conv playgroundConversationView
	_ = json.Unmarshal(w.Body.Bytes(), &conv)
	w = do(t, s, cookie, token, "PATCH", "/api/playground/conversations/"+conv.ID,
		`{"title":"t2","dialect":"openai","model":"m","config":{}}`)
	if err := json.Unmarshal(w.Body.Bytes(), &conv); err != nil || conv.Title != "t2" {
		t.Errorf("conversation patch = %d %s", w.Code, w.Body.String())
	}
}

func TestProviderValidation(t *testing.T) {
	s, _ := testServerFull(t)
	s.deps.Kinds = []string{"openaicompat", "anthropic"}
	cookie, token := login(t, s)
	for name, body := range map[string]string{
		"uppercase id":    `{"id":"Bad","kind":"openaicompat","base_url":"https://x/v1"}`,
		"id with a slash": `{"id":"a/b","kind":"openaicompat","base_url":"https://x/v1"}`,
		"id too long":     `{"id":"` + strings.Repeat("a", 65) + `","kind":"openaicompat","base_url":"https://x/v1"}`,
		"ftp base_url":    `{"id":"p","kind":"openaicompat","base_url":"ftp://x/v1"}`,
		"relative url":    `{"id":"p","kind":"openaicompat","base_url":"/v1"}`,
		"unknown kind":    `{"id":"p","kind":"telepathy","base_url":"https://x/v1"}`,
		"unknown style":   `{"id":"p","kind":"openaicompat","base_url":"https://x/v1","auth_style":"magic"}`,
		"priority high":   `{"id":"p","kind":"openaicompat","base_url":"https://x/v1","priority":1001}`,
		"priority low":    `{"id":"p","kind":"openaicompat","base_url":"https://x/v1","priority":-1}`,
		"unknown field":   `{"id":"p","kind":"openaicompat","base_url":"https://x/v1","colour":"red"}`,
	} {
		if w := do(t, s, cookie, token, "POST", "/api/providers", body); w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s, want 400", name, w.Code, w.Body.String())
		}
	}
	if w := do(t, s, cookie, token, "POST", "/api/providers",
		`{"id":"ok.p-1_x","kind":"anthropic","base_url":"https://x/v1","auth_style":"x-api-key","priority":1000}`); w.Code != http.StatusCreated {
		t.Errorf("a valid provider = %d %s", w.Code, w.Body.String())
	}
	if w := do(t, s, cookie, token, "PATCH", "/api/providers/ok.p-1_x", `{"base_url":"nope"}`); w.Code != http.StatusBadRequest {
		t.Errorf("patching in a bad url = %d, want 400", w.Code)
	}
	if w := do(t, s, cookie, token, "PATCH", "/api/providers/ok.p-1_x", `{}`); w.Code != http.StatusBadRequest {
		t.Errorf("an empty patch = %d, want 400", w.Code)
	}
	if w := do(t, s, cookie, token, "PATCH", "/api/providers/missing", `{"name":"x"}`); w.Code != http.StatusNotFound {
		t.Errorf("patching a missing provider = %d, want 404", w.Code)
	}
}

func TestCatalogSyncIsAccepted(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	w := do(t, s, cookie, token, "POST", "/api/catalog/sync", "")
	if w.Code != http.StatusAccepted || !strings.Contains(w.Body.String(), `"triggered":true`) {
		t.Errorf("sync = %d %s, want 202", w.Code, w.Body.String())
	}
}

func TestListEnvelopes(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	for path, key := range map[string]string{
		"/api/health/providers":         "providers",
		"/api/sessions":                 "sessions",
		"/api/proxy-tokens":             "tokens",
		"/api/playground/presets":       "presets",
		"/api/playground/conversations": "conversations",
	} {
		w := do(t, s, cookie, token, "GET", path, "")
		var body map[string]json.RawMessage
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Errorf("%s is not an object: %s", path, w.Body.String())
			continue
		}
		if raw, ok := body[key]; !ok || !strings.HasPrefix(string(raw), "[") {
			t.Errorf("%s lacks an array under %q: %s", path, key, w.Body.String())
		}
	}
}
