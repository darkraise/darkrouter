package admin

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProxyTokenIsShownOnceAndNeverAgain(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)

	w := do(t, s, cookie, token, "POST", "/api/proxy-tokens", `{"name":"laptop"}`)
	if w.Code != 201 {
		t.Fatalf("create = %d: %s", w.Code, w.Body.String())
	}
	var created proxyTokenView
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Secret == "" {
		t.Fatal("creation did not return the secret")
	}

	list := do(t, s, cookie, token, "GET", "/api/proxy-tokens", "")
	if strings.Contains(list.Body.String(), created.Secret) {
		t.Error("the listing reproduced the secret")
	}
	var env struct {
		Tokens []proxyTokenView `json:"tokens"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	listed := env.Tokens
	if len(listed) != 1 || listed[0].Prefix == "" {
		t.Fatalf("listing = %+v", listed)
	}
	if listed[0].Secret != "" {
		t.Error("a listed token carries a secret field")
	}
}

func TestProxyTokenDeleteRevokes(t *testing.T) {
	s, db := testServerFull(t)
	cookie, token := login(t, s)
	w := do(t, s, cookie, token, "POST", "/api/proxy-tokens", `{"name":"laptop"}`)
	var created proxyTokenView
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	if w := do(t, s, cookie, token, "DELETE", "/api/proxy-tokens/"+created.ID, ""); w.Code != 204 {
		t.Fatalf("delete = %d: %s", w.Code, w.Body.String())
	}
	if ok, _ := db.ProxyTokenValid(t.Context(), created.Secret); ok {
		t.Error("a revoked token still validates")
	}
	if w := do(t, s, cookie, token, "DELETE", "/api/proxy-tokens/nosuch", ""); w.Code != 404 {
		t.Errorf("deleting an unknown id = %d, want 404", w.Code)
	}
}

func TestProxyTokenWritesNeedASession(t *testing.T) {
	s, _ := testServerFull(t)
	r := httptest.NewRequest("POST", "/api/proxy-tokens",
		strings.NewReader(`{"name":"x"}`))
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatalf("unauthenticated create = %d, want 401", w.Code)
	}
}

func TestPatchCredentialNeverEchoesTheSecret(t *testing.T) {
	// Phase 7 §4.1: no endpoint returns credential material. A PATCH that
	// echoed what it stored would be the easiest place to leak one.
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	keyID := seedProviderWithKey(t, s, cookie, token, "groq", "http://127.0.0.1:1")

	w := do(t, s, cookie, token, "PATCH", "/api/providers/groq/keys/"+keyID,
		`{"secret":"sk-brand-new-value"}`)
	if w.Code != 200 {
		t.Fatalf("patch = %d: %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "sk-brand-new-value") {
		t.Errorf("the response echoed the secret: %s", w.Body.String())
	}
}

func TestPatchCredentialTogglesEnabled(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	keyID := seedProviderWithKey(t, s, cookie, token, "groq", "http://127.0.0.1:1")

	if w := do(t, s, cookie, token, "PATCH", "/api/providers/groq/keys/"+keyID,
		`{"enabled":false}`); w.Code != 200 {
		t.Fatalf("disable = %d: %s", w.Code, w.Body.String())
	}
	if w := do(t, s, cookie, token, "PATCH", "/api/providers/groq/keys/nosuch",
		`{"enabled":false}`); w.Code != 404 {
		t.Errorf("patching an unknown credential = %d, want 404", w.Code)
	}
	if w := do(t, s, cookie, token, "PATCH", "/api/providers/groq/keys/"+keyID,
		`{}`); w.Code != 400 {
		t.Errorf("an empty patch = %d, want 400", w.Code)
	}
}
