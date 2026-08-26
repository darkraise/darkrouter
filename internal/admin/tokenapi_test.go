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
	var listed []proxyTokenView
	if err := json.Unmarshal(list.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
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
