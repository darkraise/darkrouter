package admin

import (
	"encoding/base64"
	"net/http"
	"strings"
	"testing"
)

// theSecret is deliberately shaped so a substring match cannot produce a false
// positive against ordinary JSON.
const theSecret = "sk-DARKROUTERLEAKCANARY-9f3a7c1e"

func TestNoEndpointReturnsCredentialMaterial(t *testing.T) {
	// Spec §4.1: never plaintext, never ciphertext, not for editing, not for
	// export. Written as one sweep rather than per handler because a
	// per-handler assertion protects the handler it was written for, and this
	// protects the one nobody has written yet.
	s, _ := testServerFull(t)
	cookie, token := login(t, s)

	if w := do(t, s, cookie, token, "POST", "/api/providers",
		`{"id":"p1","name":"P","kind":"openaicompat","base_url":"https://x/v1"}`); w.Code != http.StatusCreated {
		t.Fatalf("create provider = %d, body = %s", w.Code, w.Body.String())
	}
	if w := do(t, s, cookie, token, "POST", "/api/providers/p1/keys",
		`{"label":"primary","secret":"`+theSecret+`"}`); w.Code != http.StatusCreated {
		t.Fatalf("add credential = %d, body = %s", w.Code, w.Body.String())
	}

	b64 := base64.StdEncoding.EncodeToString([]byte(theSecret))
	// Every endpoint, including ones that do not exist yet: they 404 with a
	// body carrying no secret, so the list is written once and covers each new
	// handler the moment it lands rather than needing this file edited.
	for _, ep := range []struct{ method, path, body string }{
		{"GET", "/api/providers", ""},
		{"GET", "/api/presets", ""},
		{"GET", "/api/playground/presets", ""},
		{"GET", "/api/overview", ""},
		{"GET", "/api/models", ""},
		{"GET", "/api/requests", ""},
		{"GET", "/api/usage", ""},
		{"GET", "/api/config", ""},
		{"GET", "/api/auth/status", ""},
		{"POST", "/api/config/reload", ""},
		{"POST", "/api/providers/p1/test", ""},
	} {
		w := do(t, s, cookie, token, ep.method, ep.path, ep.body)
		got := w.Body.String()
		if strings.Contains(got, theSecret) {
			t.Errorf("%s %s returned the secret in plaintext:\n%s", ep.method, ep.path, got)
		}
		if strings.Contains(got, b64) {
			t.Errorf("%s %s returned the secret base64-encoded:\n%s", ep.method, ep.path, got)
		}
		// The masked form is the only thing that may carry any of it, and only
		// the last four characters.
		if strings.Contains(got, "DARKROUTERLEAKCANARY") {
			t.Errorf("%s %s returned more than the masked suffix:\n%s", ep.method, ep.path, got)
		}
	}
}

func TestTheMaskShowsOnlyASuffix(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	_ = do(t, s, cookie, token, "POST", "/api/providers",
		`{"id":"p1","name":"P","kind":"openaicompat","base_url":"https://x/v1"}`)
	_ = do(t, s, cookie, token, "POST", "/api/providers/p1/keys",
		`{"label":"primary","secret":"`+theSecret+`"}`)

	got := do(t, s, cookie, token, "GET", "/api/providers", "").Body.String()
	if !strings.Contains(got, theSecret[len(theSecret)-4:]) {
		t.Errorf("the masked suffix is missing; two keys cannot be told apart:\n%s", got)
	}
}

func TestAShortSecretIsMaskedEntirely(t *testing.T) {
	// Showing three of four characters is not a mask.
	if got := maskSecret("abcd"); strings.Contains(got, "abc") {
		t.Errorf("maskSecret(%q) = %q", "abcd", got)
	}
	if got := maskSecret(""); got != "****" {
		t.Errorf("maskSecret(\"\") = %q", got)
	}
}
